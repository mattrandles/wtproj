package flatfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wtp/internal/core"
)

func TestWriteJSONAtomicOverwritesExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task.json")

	original := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Original title",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    mustRFC3339Time(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustRFC3339Time(t, "2026-03-24T14:10:04Z"),
	}
	if err := writeJSONAtomic(path, original); err != nil {
		t.Fatalf("writeJSONAtomic(original) error = %v", err)
	}

	updated := original
	updated.Title = "Updated title"
	if err := writeJSONAtomic(path, updated); err != nil {
		t.Fatalf("writeJSONAtomic(updated) error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	var got core.Task
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Title != updated.Title {
		t.Fatalf("stored title = %q, want %q", got.Title, updated.Title)
	}
}

func mustRFC3339Time(t *testing.T, value string) time.Time {
	t.Helper()

	outTime, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return outTime
}
