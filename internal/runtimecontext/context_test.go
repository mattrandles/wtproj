package runtimecontext

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestDiscoverMainAndLinkedWorktrees(t *testing.T) {
	requireGit(t)

	fixtureRoot := t.TempDir()
	mainRoot := filepath.Join(fixtureRoot, "main repository with spaces")
	runGit(t, fixtureRoot, "init", mainRoot)
	runGit(t, mainRoot, "config", "user.name", "WTP Test")
	runGit(t, mainRoot, "config", "user.email", "wtp-test@example.invalid")
	if err := os.WriteFile(filepath.Join(mainRoot, "tracked.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, mainRoot, "add", "tracked.txt")
	runGit(t, mainRoot, "commit", "-m", "fixture")
	mainBranch := runGitOutput(t, mainRoot, "symbolic-ref", "--short", "HEAD")

	mainNested := filepath.Join(mainRoot, "nested", "invocation")
	if err := os.MkdirAll(mainNested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	for name, invocation := range map[string]string{
		"repository root":  mainRoot,
		"nested directory": mainNested,
	} {
		t.Run(name, func(t *testing.T) {
			context, err := Discover(invocation)
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			assertPathEqual(t, context.InvocationDir, invocation)
			assertPathEqual(t, context.RepositoryRoot, mainRoot)
			assertPathEqual(t, context.WorktreeRoot, mainRoot)
			if !context.InGit || context.DetachedHEAD {
				t.Fatalf("Git state = InGit %t, DetachedHEAD %t; want true, false", context.InGit, context.DetachedHEAD)
			}
			if context.Branch != mainBranch {
				t.Fatalf("Branch = %q, want %q", context.Branch, mainBranch)
			}
			scope := context.Scope()
			if scope == nil || scope.Branch != mainBranch || scope.BranchID != core.BranchID(mainBranch) {
				t.Fatalf("Scope() = %#v, want branch %q with ID %s", scope, mainBranch, core.BranchID(mainBranch))
			}
			if context.WorktreeName != filepath.Base(mainRoot) {
				t.Fatalf("WorktreeName = %q, want %q", context.WorktreeName, filepath.Base(mainRoot))
			}
		})
	}

	linkedRoot := filepath.Join(fixtureRoot, "linked worktree with spaces")
	runGit(t, mainRoot, "worktree", "add", "-b", "Feature/ABC", linkedRoot)
	// A same-named tag makes `git symbolic-ref --short HEAD` report
	// heads/Feature/ABC. Discovery must still use the exact branch name so a
	// tag cannot change task allocation or automatic-selection scope.
	runGit(t, mainRoot, "tag", "Feature/ABC")
	linkedNested := filepath.Join(linkedRoot, "another", "nested directory")
	if err := os.MkdirAll(linkedNested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	context, err := Discover(linkedNested)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	assertPathEqual(t, context.InvocationDir, linkedNested)
	assertPathEqual(t, context.RepositoryRoot, mainRoot)
	assertPathEqual(t, context.WorktreeRoot, linkedRoot)
	if !context.InGit || context.DetachedHEAD {
		t.Fatalf("Git state = InGit %t, DetachedHEAD %t; want true, false", context.InGit, context.DetachedHEAD)
	}
	if context.Branch != "Feature/ABC" {
		t.Fatalf("Branch = %q, want Feature/ABC", context.Branch)
	}
	scope := context.Scope()
	if scope == nil || scope.Branch != "Feature/ABC" || scope.BranchID != "f718f729" {
		t.Fatalf("Scope() = %#v, want Feature/ABC scope", scope)
	}
	linkedGitDir := runGitOutput(t, linkedRoot, "rev-parse", "--path-format=absolute", "--git-dir")
	if context.WorktreeName != filepath.Base(linkedGitDir) {
		t.Fatalf("WorktreeName = %q, want stable Git name %q", context.WorktreeName, filepath.Base(linkedGitDir))
	}
}

func TestDiscoverDetachedHEAD(t *testing.T) {
	requireGit(t)

	root := filepath.Join(t.TempDir(), "detached repository")
	runGit(t, filepath.Dir(root), "init", root)
	runGit(t, root, "config", "user.name", "WTP Test")
	runGit(t, root, "config", "user.email", "wtp-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "fixture")
	runGit(t, root, "checkout", "--detach")

	context, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !context.InGit || !context.DetachedHEAD {
		t.Fatalf("Git state = InGit %t, DetachedHEAD %t; want true, true", context.InGit, context.DetachedHEAD)
	}
	if context.Branch != "" {
		t.Fatalf("Branch = %q, want empty in detached HEAD", context.Branch)
	}
	if scope := context.Scope(); scope != nil {
		t.Fatalf("Scope() = %#v, want nil in detached HEAD", scope)
	}
	assertPathEqual(t, context.RepositoryRoot, root)
	assertPathEqual(t, context.WorktreeRoot, root)
}

func TestDiscoverNonGitDirectory(t *testing.T) {
	invocation := filepath.Join(t.TempDir(), "not a repository", "nested")
	if err := os.MkdirAll(invocation, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	context, err := Discover(invocation)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	assertPathEqual(t, context.InvocationDir, invocation)
	if context.InGit || context.DetachedHEAD {
		t.Fatalf("Git state = InGit %t, DetachedHEAD %t; want false, false", context.InGit, context.DetachedHEAD)
	}
	if context.RepositoryRoot != "" || context.WorktreeRoot != "" || context.Branch != "" || context.WorktreeName != "" {
		t.Fatalf("non-Git context contains Git metadata: %#v", context)
	}
	if scope := context.Scope(); scope != nil {
		t.Fatalf("Scope() = %#v, want nil outside Git", scope)
	}
}

func TestDiscoverRejectsNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Discover(path); err == nil {
		t.Fatal("Discover() error = nil, want non-directory error")
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", dir}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", dir}, args...)
	output, err := exec.Command("git", commandArgs...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(trimLineEnding(output))
}

func assertPathEqual(t *testing.T, got, want string) {
	t.Helper()
	gotPath, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", got, err)
	}
	wantPath, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", want, err)
	}
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
