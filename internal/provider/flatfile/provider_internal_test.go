package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
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

func TestWriteJSONAtomicPreservesExistingFileWhenReplaceFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task.json")
	original := []byte("original contents\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	fs := defaultFileSystem()
	fs.replace = func(_, target string) error {
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("target disappeared before replacement: %v", err)
		}
		if string(data) != string(original) {
			t.Fatalf("target changed before replacement: got %q, want %q", data, original)
		}
		return errors.New("injected replace failure")
	}

	err := writeJSONAtomicWithFileSystem(path, map[string]string{"title": "replacement"}, fs)
	if err == nil || !strings.Contains(err.Error(), "injected replace failure") {
		t.Fatalf("writeJSONAtomicWithFileSystem() error = %v, want injected failure", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("target after failed replacement = %q, want %q", data, original)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "task.json" {
		t.Fatalf("files after failed replacement = %v, want only task.json", entryNames(entries))
	}
}

func TestWriteIndexPreservesExistingIndexWhenReplaceFails(t *testing.T) {
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	before, err := p.readIndex()
	if err != nil {
		t.Fatalf("readIndex() before failure error = %v", err)
	}

	p.fs.replace = func(_, _ string) error { return errors.New("injected index replace failure") }
	if err := p.writeIndex(indexFile{Next: before.Next + 1}); err == nil || !strings.Contains(err.Error(), "injected index replace failure") {
		t.Fatalf("writeIndex() error = %v, want injected failure", err)
	}
	after, err := p.readIndex()
	if err != nil {
		t.Fatalf("readIndex() after failure error = %v", err)
	}
	if after != before {
		t.Fatalf("index after failed replacement = %#v, want %#v", after, before)
	}
}

func TestReplaceTaskPublishFailureKeepsOnlyOldCopy(t *testing.T) {
	p, original := newInternalProviderWithTask(t)
	updated := original
	updated.Status = core.StatusInProgress
	updated.UpdatedAt = original.UpdatedAt.Add(time.Second)
	updated.StartedAt = &updated.UpdatedAt

	p.fs.replace = func(_, _ string) error { return errors.New("injected publish failure") }
	if err := p.replaceTask(updated); err == nil || !strings.Contains(err.Error(), "injected publish failure") {
		t.Fatalf("replaceTask() error = %v, want injected failure", err)
	}

	assertStoredTask(t, filepath.Join(p.root, string(core.StatusTodo), original.ShortID+".json"), original)
	if _, err := os.Stat(filepath.Join(p.root, string(core.StatusInProgress), original.ShortID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new status copy exists after failed publish: %v", err)
	}
}

func TestReplaceTaskCleanupFailureKeepsDurableNewCopy(t *testing.T) {
	p, original := newInternalProviderWithTask(t)
	updated := original
	updated.Status = core.StatusInProgress
	updated.UpdatedAt = original.UpdatedAt.Add(time.Second)
	updated.StartedAt = &updated.UpdatedAt
	oldPath := filepath.Join(p.root, string(core.StatusTodo), original.ShortID+".json")

	realRemove := p.fs.remove
	p.fs.remove = func(path string) error {
		if path == oldPath {
			return errors.New("injected cleanup failure")
		}
		return realRemove(path)
	}
	if err := p.replaceTask(updated); err == nil || !strings.Contains(err.Error(), "injected cleanup failure") {
		t.Fatalf("replaceTask() error = %v, want injected failure", err)
	}

	assertStoredTask(t, oldPath, original)
	assertStoredTask(t, filepath.Join(p.root, string(core.StatusInProgress), original.ShortID+".json"), updated)

	tasks, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() with crash residue error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != updated.ID || tasks[0].Status != updated.Status {
		t.Fatalf("ListTasks() with crash residue = %#v, want one updated task", tasks)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("ListTasks() destructively removed old residue: %v", err)
	}
}

func TestReaderWaitsForStatusMoveCleanup(t *testing.T) {
	p, original := newInternalProviderWithTask(t)
	oldPath := filepath.Join(p.root, string(core.StatusTodo), original.ShortID+".json")
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	realRemove := p.fs.remove
	var once sync.Once
	p.fs.remove = func(path string) error {
		if path == oldPath {
			once.Do(func() { close(cleanupStarted) })
			<-allowCleanup
		}
		return realRemove(path)
	}

	moveResult := make(chan error, 1)
	go func() {
		_, err := p.UpdateTaskStatus(original.ShortID, core.StatusInProgress, "Codex")
		moveResult <- err
	}()
	select {
	case <-cleanupStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("status move did not reach cleanup")
	}

	readResult := make(chan struct {
		task core.TaskView
		err  error
	}, 1)
	go func() {
		task, err := p.GetTask(original.ShortID, "")
		readResult <- struct {
			task core.TaskView
			err  error
		}{task: task, err: err}
	}()

	select {
	case result := <-readResult:
		t.Fatalf("reader returned during status move: task=%#v err=%v", result.task, result.err)
	case <-time.After(150 * time.Millisecond):
	}
	close(allowCleanup)
	if err := <-moveResult; err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}
	result := <-readResult
	if result.err != nil {
		t.Fatalf("GetTask() error = %v", result.err)
	}
	if result.task.ID != original.ID || result.task.Status != core.StatusInProgress {
		t.Fatalf("GetTask() = %#v, want moved task", result.task)
	}
}

func TestMigrateTaskFilenamesRefusesConflictingTarget(t *testing.T) {
	root := t.TempDir()
	p, err := New(root)
	if err != nil {
		t.Fatalf("New() setup error = %v", err)
	}

	legacy := validTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
	conflict := validTask("ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a", "wtp-0001", core.StatusTodo)
	legacyPath := filepath.Join(p.root, string(core.StatusTodo), legacy.ID+".json")
	targetPath := filepath.Join(p.root, string(core.StatusTodo), legacy.ShortID+".json")
	writeJSONFile(t, legacyPath, legacy)
	writeJSONFile(t, targetPath, conflict)
	legacyBefore, _ := os.ReadFile(legacyPath)
	targetBefore, _ := os.ReadFile(targetPath)

	if _, err := New(root); err == nil || !strings.Contains(err.Error(), "refuse to overwrite conflicting task file") {
		t.Fatalf("New() migration error = %v, want conflict", err)
	}
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("legacy file missing after conflict: %v", err)
	}
	targetAfter, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("target file missing after conflict: %v", err)
	}
	if string(legacyAfter) != string(legacyBefore) || string(targetAfter) != string(targetBefore) {
		t.Fatal("conflicting migration changed an existing task file")
	}
}

func TestExportCanonicalCreatesExactSnapshotInExistingDirectory(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	exportDir := t.TempDir()
	currentPath := filepath.Join(exportDir, task.ID+".json")
	stalePath := filepath.Join(exportDir, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a.json")
	if err := os.WriteFile(currentPath, []byte("old snapshot\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(current) error = %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("stale snapshot\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(stale) error = %v", err)
	}

	if err := p.ExportCanonical(exportDir); err != nil {
		t.Fatalf("ExportCanonical() error = %v", err)
	}

	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	if got, want := entryNames(entries), []string{task.ID + ".json"}; !slices.Equal(got, want) {
		t.Fatalf("export entries = %v, want %v", got, want)
	}
	assertStoredTask(t, currentPath, task)
}

func TestExportCanonicalRejectsUnmanagedEntriesBeforeChangingSnapshot(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	exportDir := t.TempDir()
	currentPath := filepath.Join(exportDir, task.ID+".json")
	currentBefore := []byte("old snapshot\n")
	if err := os.WriteFile(currentPath, currentBefore, 0o644); err != nil {
		t.Fatalf("os.WriteFile(current) error = %v", err)
	}
	for _, name := range []string{"notes.txt", "another-dir"} {
		path := filepath.Join(exportDir, name)
		if strings.HasSuffix(name, "-dir") {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("os.Mkdir(%s) error = %v", name, err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", name, err)
		}
	}

	err := p.ExportCanonical(exportDir)
	if err == nil || !strings.Contains(err.Error(), "unmanaged entries: another-dir, notes.txt") {
		t.Fatalf("ExportCanonical() error = %v, want sorted unmanaged-entry error", err)
	}
	currentAfter, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("os.ReadFile(current) error = %v", readErr)
	}
	if string(currentAfter) != string(currentBefore) {
		t.Fatalf("current snapshot changed during rejected export: got %q, want %q", currentAfter, currentBefore)
	}
	if _, statErr := os.Stat(filepath.Join(exportDir, "notes.txt")); statErr != nil {
		t.Fatalf("unmanaged file changed during rejected export: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(exportDir, "another-dir")); statErr != nil {
		t.Fatalf("unmanaged directory changed during rejected export: %v", statErr)
	}
}

func TestExportCanonicalRejectsStorageOverlapWithoutChangingStorage(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	storageTaskPath := filepath.Join(p.root, string(task.Status), task.ShortID+".json")
	before, err := os.ReadFile(storageTaskPath)
	if err != nil {
		t.Fatalf("os.ReadFile(storage task) error = %v", err)
	}

	tests := []struct {
		name   string
		target string
	}{
		{name: "storage root", target: p.root},
		{name: "inside storage", target: filepath.Join(p.root, "export")},
		{name: "contains storage", target: filepath.Dir(p.root)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := p.ExportCanonical(test.target)
			if err == nil || !strings.Contains(err.Error(), "overlaps active storage") {
				t.Fatalf("ExportCanonical(%s) error = %v, want overlap error", test.target, err)
			}
		})
	}

	after, err := os.ReadFile(storageTaskPath)
	if err != nil {
		t.Fatalf("os.ReadFile(storage task after rejection) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("overlap rejection changed active task storage")
	}
	if _, err := os.Stat(filepath.Join(p.root, "export")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlapping child export path was created: %v", err)
	}
}

func TestExportCanonicalPublishFailurePreservesExistingFileAndStaleSnapshot(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	exportDir := t.TempDir()
	currentPath := filepath.Join(exportDir, task.ID+".json")
	stalePath := filepath.Join(exportDir, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a.json")
	currentBefore := []byte("old snapshot\n")
	if err := os.WriteFile(currentPath, currentBefore, 0o644); err != nil {
		t.Fatalf("os.WriteFile(current) error = %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("stale snapshot\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(stale) error = %v", err)
	}
	p.fs.replace = func(_, _ string) error { return errors.New("injected export replace failure") }

	err := p.ExportCanonical(exportDir)
	if err == nil || !strings.Contains(err.Error(), "injected export replace failure") {
		t.Fatalf("ExportCanonical() error = %v, want injected failure", err)
	}
	currentAfter, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("os.ReadFile(current) error = %v", readErr)
	}
	if string(currentAfter) != string(currentBefore) {
		t.Fatalf("current snapshot after failed replacement = %q, want %q", currentAfter, currentBefore)
	}
	if _, statErr := os.Stat(stalePath); statErr != nil {
		t.Fatalf("stale snapshot removed after failed publication: %v", statErr)
	}
}

func newInternalProviderWithTask(t *testing.T) (*Provider, core.Task) {
	t.Helper()
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	task := validTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
	if err := p.writeTask(task); err != nil {
		t.Fatalf("writeTask() error = %v", err)
	}
	return p, task
}

func validTask(id, shortID string, status core.Status) core.Task {
	created := time.Date(2026, time.March, 24, 14, 10, 4, 0, time.UTC)
	return core.Task{
		ID:           id,
		ShortID:      shortID,
		Title:        "Atomic task",
		Status:       status,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    created,
		UpdatedAt:    created,
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func assertStoredTask(t *testing.T, path string, want core.Task) {
	t.Helper()
	var got core.Task
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", path, err)
	}
	if got.ID != want.ID || got.ShortID != want.ShortID || got.Status != want.Status || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("stored task at %s = %#v, want %#v", path, got, want)
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func mustRFC3339Time(t *testing.T, value string) time.Time {
	t.Helper()

	outTime, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return outTime
}
