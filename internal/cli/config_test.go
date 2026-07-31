package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderForInvocationDefaultsToInvocationDirectoryOutsideGit(t *testing.T) {
	invocation := filepath.Join(t.TempDir(), "non-git invocation")
	if err := os.MkdirAll(invocation, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if _, err := providerForInvocation(invocation); err != nil {
		t.Fatalf("providerForInvocation() error = %v", err)
	}
	assertStorageInitialized(t, filepath.Join(invocation, ".wtp"))
}

func TestProviderForInvocationUsesOnlyRepositoryWorktreeRootConfig(t *testing.T) {
	requireGitForConfigTest(t)

	repository := filepath.Join(t.TempDir(), "repository")
	runConfigGit(t, filepath.Dir(repository), "init", repository)
	nested := filepath.Join(repository, "nested", "invocation")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	rootStorage := filepath.Join(repository, "root task storage")
	nestedStorage := filepath.Join(nested, "nested task storage")
	writeConfigFile(t, repository, `{"wtpDir":"root task storage"}`)
	writeConfigFile(t, nested, `{"wtpDir":"nested task storage"}`)

	for name, invocation := range map[string]string{
		"repository root":  repository,
		"nested directory": nested,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := providerForInvocation(invocation); err != nil {
				t.Fatalf("providerForInvocation() error = %v", err)
			}
			assertStorageInitialized(t, rootStorage)
		})
	}
	if _, err := os.Stat(nestedStorage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested config was loaded: stat %s error = %v", nestedStorage, err)
	}
}

func TestProviderForInvocationUsesLinkedWorktreeConfigAndAbsoluteStorage(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	mainRoot := filepath.Join(fixture, "main repository")
	linkedRoot := filepath.Join(fixture, "linked worktree")
	runConfigGit(t, fixture, "init", mainRoot)
	runConfigGit(t, mainRoot, "config", "user.name", "WTP Test")
	runConfigGit(t, mainRoot, "config", "user.email", "wtp-test@example.invalid")
	if err := os.WriteFile(filepath.Join(mainRoot, "tracked.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runConfigGit(t, mainRoot, "add", "tracked.txt")
	runConfigGit(t, mainRoot, "commit", "-m", "fixture")
	runConfigGit(t, mainRoot, "worktree", "add", "-b", "linked-config-test", linkedRoot)

	mainStorage := filepath.Join(fixture, "main storage")
	linkedStorage := filepath.Join(fixture, "absolute linked storage")
	writeConfigFile(t, mainRoot, `{"wtpDir":`+jsonString(mainStorage)+`}`)
	writeConfigFile(t, linkedRoot, `{"wtpDir":`+jsonString(linkedStorage)+`}`)
	linkedNested := filepath.Join(linkedRoot, "nested", "invocation")
	if err := os.MkdirAll(linkedNested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if _, err := providerForInvocation(linkedNested); err != nil {
		t.Fatalf("providerForInvocation() error = %v", err)
	}
	assertStorageInitialized(t, linkedStorage)
	if _, err := os.Stat(mainStorage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("main worktree config was loaded: stat %s error = %v", mainStorage, err)
	}
}

func TestProviderForInvocationReturnsActionableAnchorConfigAndStorageErrors(t *testing.T) {
	t.Run("invalid config", func(t *testing.T) {
		invocation := t.TempDir()
		path := filepath.Join(invocation, ".wtp.json")
		if err := os.WriteFile(path, []byte(`{"wtpDir":`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		_, err := providerForInvocation(invocation)
		if err == nil || !strings.Contains(err.Error(), "parse "+path) {
			t.Fatalf("providerForInvocation() error = %v", err)
		}
	})

	t.Run("invalid storage", func(t *testing.T) {
		invocation := t.TempDir()
		storage := filepath.Join(invocation, "not-a-directory")
		if err := os.WriteFile(storage, nil, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		writeConfigFile(t, invocation, `{"wtpDir":"not-a-directory"}`)

		_, err := providerForInvocation(invocation)
		if err == nil || !strings.Contains(err.Error(), "initialize flat-file storage at "+storage) {
			t.Fatalf("providerForInvocation() error = %v", err)
		}
	})
}

func writeConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".wtp.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func assertStorageInitialized(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "meta", "index.json")); err != nil {
		t.Fatalf("storage was not initialized at %s: %v", root, err)
	}
}

// assertEquivalentPath compares paths after resolving aliases such as Windows
// short names. Git may report a different spelling for the same directory
// than the path returned by t.TempDir.
func assertEquivalentPath(t *testing.T, got, want string) {
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

func requireGitForConfigTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func runConfigGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", dir}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func jsonString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
