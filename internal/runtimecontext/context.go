// Package runtimecontext discovers facts about the directory from which wtp
// was invoked. Discovery is intentionally independent of configuration and
// provider setup so that callers can decide how to use the resulting context.
package runtimecontext

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mattrandles/wtproj/internal/core"
)

// Context describes the invocation directory and its enclosing Git worktree.
//
// RepositoryRoot is the primary worktree root shared by all linked worktrees.
// WorktreeRoot is the root of the worktree containing InvocationDir. Branch is
// empty when DetachedHEAD is true. All Git-specific fields remain empty for a
// non-Git invocation.
type Context struct {
	InvocationDir  string
	InGit          bool
	RepositoryRoot string
	WorktreeRoot   string
	Branch         string
	DetachedHEAD   bool
	WorktreeName   string
}

// Scope returns the task scope for the actual invocation context. Detached
// HEAD and non-Git invocations intentionally return nil so they keep the
// legacy/global task namespace.
func (c Context) Scope() *core.BranchScope {
	if !c.InGit || c.DetachedHEAD {
		return nil
	}
	return core.NewBranchScope(c.Branch)
}

// Discover resolves invocationDir and discovers its enclosing Git context.
// A non-Git directory and a detached HEAD are valid contexts, not errors.
func Discover(invocationDir string) (Context, error) {
	if invocationDir == "" {
		invocationDir = "."
	}

	absoluteDir, err := filepath.Abs(invocationDir)
	if err != nil {
		return Context{}, fmt.Errorf("resolve invocation directory: %w", err)
	}
	absoluteDir = filepath.Clean(absoluteDir)

	info, err := os.Stat(absoluteDir)
	if err != nil {
		return Context{}, fmt.Errorf("inspect invocation directory %s: %w", absoluteDir, err)
	}
	if !info.IsDir() {
		return Context{}, fmt.Errorf("invocation path %s is not a directory", absoluteDir)
	}

	result := Context{InvocationDir: absoluteDir}
	inside, err := gitOutput(absoluteDir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return result, nil
		}
		return Context{}, fmt.Errorf("detect Git context: %w", err)
	}
	if string(inside) != "true" {
		return result, nil
	}

	worktreeRoot, err := requiredGitPath(absoluteDir, "resolve worktree root", "rev-parse", "--show-toplevel")
	if err != nil {
		return Context{}, err
	}
	commonDir, err := requiredGitPath(absoluteDir, "resolve common Git directory", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Context{}, err
	}
	gitDir, err := requiredGitPath(absoluteDir, "resolve worktree Git directory", "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return Context{}, err
	}
	repositoryRoot, err := primaryWorktreeRoot(absoluteDir)
	if err != nil {
		return Context{}, err
	}

	result.InGit = true
	result.RepositoryRoot = repositoryRoot
	result.WorktreeRoot = worktreeRoot
	result.WorktreeName = worktreeName(worktreeRoot, commonDir, gitDir)

	branch, err := gitOutput(absoluteDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		result.Branch = string(branch)
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		result.DetachedHEAD = true
		return result, nil
	}
	return Context{}, fmt.Errorf("resolve current Git branch: %w", err)
}

func primaryWorktreeRoot(dir string) (string, error) {
	output, err := gitOutput(dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}

	const prefix = "worktree "
	field, _, found := bytes.Cut(output, []byte{0})
	if !found || !bytes.HasPrefix(field, []byte(prefix)) || len(field) == len(prefix) {
		return "", errors.New("resolve repository root: unexpected output from git worktree list")
	}
	return filepath.Clean(string(field[len(prefix):])), nil
}

func worktreeName(worktreeRoot, commonDir, gitDir string) string {
	if filepath.Clean(commonDir) == filepath.Clean(gitDir) {
		return filepath.Base(worktreeRoot)
	}

	// Linked worktrees have a stable administrative name under the common
	// directory, even if their checkout directory is subsequently moved.
	relative, err := filepath.Rel(commonDir, gitDir)
	if err == nil && filepath.Dir(relative) == "worktrees" {
		return filepath.Base(relative)
	}
	return filepath.Base(worktreeRoot)
}

func requiredGitPath(dir, operation string, args ...string) (string, error) {
	output, err := gitOutput(dir, args...)
	if err != nil {
		return "", fmt.Errorf("%s: %w", operation, err)
	}
	if len(output) == 0 {
		return "", fmt.Errorf("%s: Git returned an empty path", operation)
	}
	return filepath.Clean(string(output)), nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", dir}, args...)
	output, err := exec.Command("git", commandArgs...).Output()
	if err != nil {
		return nil, err
	}
	return trimLineEnding(output), nil
}

func trimLineEnding(value []byte) []byte {
	value = bytes.TrimSuffix(value, []byte{'\n'})
	return bytes.TrimSuffix(value, []byte{'\r'})
}
