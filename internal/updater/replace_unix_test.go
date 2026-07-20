//go:build !windows

package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceExecutableAtomicallyPublishesStage(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "wtp")
	source := filepath.Join(directory, ".wtp-update-test")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o751); err != nil {
		t.Fatalf("write source: %v", err)
	}

	scheduled, err := replaceExecutable(source, target)
	if err != nil {
		t.Fatalf("replaceExecutable() error = %v", err)
	}
	if scheduled {
		t.Fatal("Unix replacement was unexpectedly scheduled")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "new" {
		t.Fatalf("target after replacement = %q, error = %v", contents, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists after replacement: %v", err)
	}
}

func TestReplaceExecutableFailureLeavesTargetUntouched(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "wtp")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if _, err := replaceExecutable(filepath.Join(directory, "missing"), target); err == nil {
		t.Fatal("replaceExecutable() error = nil")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "old" {
		t.Fatalf("target after failed replacement = %q, error = %v", contents, err)
	}
}
