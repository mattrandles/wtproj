package flatfile

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestNewCopiesInvocationScope(t *testing.T) {
	scope := core.NewBranchScope("feature/runtime-scope")
	p, err := New(t.TempDir(), scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := p.InvocationScope(); got == nil || *got != *scope {
		t.Fatalf("InvocationScope() = %#v, want %#v", got, scope)
	}

	scope.Branch = "changed-after-construction"
	returned := p.InvocationScope()
	returned.Branch = "changed-after-read"
	if got := p.InvocationScope(); got == nil || got.Branch != "feature/runtime-scope" {
		t.Fatalf("InvocationScope() was mutable: %#v", got)
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

func TestReplaceHandoffsPublishesAtomicallyAndKeepsOtherScopes(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	otherTask := validTask("ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a", "wtp-0002", core.StatusTodo)
	if err := p.writeTask(otherTask); err != nil {
		t.Fatalf("writeTask(other) error = %v", err)
	}
	initial := []core.Handoff{
		{
			ID:        "00000000-0000-4000-8000-000000000001",
			Message:   "global context",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T10:00:00Z"),
		},
		{
			ID:        "00000000-0000-4000-8000-000000000002",
			TaskID:    task.ID,
			Message:   "old task context",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T11:00:00Z"),
		},
		{
			ID:        "00000000-0000-4000-8000-000000000003",
			TaskID:    otherTask.ID,
			Message:   "other task context",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T12:00:00Z"),
		},
	}
	if err := p.writeHandoffs(initial); err != nil {
		t.Fatalf("writeHandoffs(initial) error = %v", err)
	}
	before, err := os.ReadFile(p.handoffsPath())
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}

	p.fs.replace = func(_, _ string) error { return errors.New("injected handoff replace failure") }
	if _, err := p.WriteHandoff(provider.HandoffWriteRequest{
		Task:    task.ShortID,
		Message: "new task context",
		Replace: true,
	}); err == nil || !strings.Contains(err.Error(), "injected handoff replace failure") {
		t.Fatalf("WriteHandoff(replace with injected failure) error = %v", err)
	}
	afterFailure, err := os.ReadFile(p.handoffsPath())
	if err != nil {
		t.Fatalf("os.ReadFile(after failure) error = %v", err)
	}
	if string(afterFailure) != string(before) {
		t.Fatalf("handoff collection changed after failed replacement: got %q, want %q", afterFailure, before)
	}
	assertStoredHandoffs(t, p.handoffsPath(), initial)

	p.fs = defaultFileSystem()
	if _, err := p.WriteHandoff(provider.HandoffWriteRequest{
		Task:    task.ShortID,
		Message: "new task context",
		Replace: true,
	}); err != nil {
		t.Fatalf("WriteHandoff(successful replace) error = %v", err)
	}
	all, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes) error = %v", err)
	}
	if len(all.Handoffs) != 3 {
		t.Fatalf("handoff count after same-scope replacement = %d, want 3", len(all.Handoffs))
	}
	seenMessages := make(map[string]bool, len(all.Handoffs))
	for _, handoff := range all.Handoffs {
		seenMessages[handoff.Message] = true
	}
	for _, message := range []string{"global context", "new task context", "other task context"} {
		if !seenMessages[message] {
			t.Errorf("same-scope replacement lost %q", message)
		}
	}
	if seenMessages["old task context"] {
		t.Error("same-scope replacement retained old task context")
	}
}

func TestPurgeHandoffsPreservesStoreWhenReplaceFails(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	initial := []core.Handoff{
		{
			ID:        "00000000-0000-4000-8000-000000000071",
			Message:   "global context",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T10:00:00Z"),
		},
		{
			ID:        "00000000-0000-4000-8000-000000000072",
			TaskID:    task.ID,
			Message:   "task context",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T11:00:00Z"),
		},
	}
	if err := p.writeHandoffs(initial); err != nil {
		t.Fatalf("writeHandoffs(initial) error = %v", err)
	}
	path := p.handoffsPath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}

	p.fs.replace = func(_, _ string) error { return errors.New("injected purge replace failure") }
	result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true})
	if err == nil || !strings.Contains(err.Error(), "injected purge replace failure") {
		t.Fatalf("PurgeHandoffs(replace failure) result = %#v, error = %v", result, err)
	}
	afterFailure, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(after failure) error = %v", err)
	}
	if string(afterFailure) != string(before) {
		t.Fatalf("handoff collection changed after failed purge: got %q, want %q", afterFailure, before)
	}
	assertStoredHandoffs(t, path, initial)

	p.fs = defaultFileSystem()
	result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true})
	if err != nil {
		t.Fatalf("PurgeHandoffs(after restoring filesystem) error = %v", err)
	}
	if result.Purged != 1 {
		t.Fatalf("PurgeHandoffs(after restoring filesystem) purged = %d, want 1", result.Purged)
	}
	assertStoredHandoffs(t, path, []core.Handoff{initial[1]})
}

func TestCorruptHandoffStoresAreReportedWithoutChangingStore(t *testing.T) {
	valid := func(message string) core.Handoff {
		return core.Handoff{
			ID:        "00000000-0000-4000-8000-000000000001",
			Message:   message,
			CreatedAt: mustRFC3339Time(t, "2026-08-09T10:00:00Z"),
		}
	}
	duplicate := handoffFile{Handoffs: []core.Handoff{valid("first"), valid("duplicate")}}
	invalidTimestamp := valid("invalid timestamp")
	invalidTimestamp.CreatedAt = time.Time{}
	invalidMessage := valid(" ")
	invalidTaskID := valid("invalid task id")
	invalidTaskID.TaskID = "not-a-task-id"

	tests := []struct {
		name      string
		value     any
		raw       []byte
		wantError string
	}{
		{
			name:      "malformed JSON",
			raw:       []byte(`{"handoffs":[`),
			wantError: "corrupt handoff file",
		},
		{
			name:      "duplicate IDs",
			value:     duplicate,
			wantError: "duplicated",
		},
		{
			name:      "invalid timestamp value",
			value:     handoffFile{Handoffs: []core.Handoff{invalidTimestamp}},
			wantError: "createdAt is required",
		},
		{
			name:      "invalid message field",
			value:     handoffFile{Handoffs: []core.Handoff{invalidMessage}},
			wantError: "message is required",
		},
		{
			name:      "invalid task ID field",
			value:     handoffFile{Handoffs: []core.Handoff{invalidTaskID}},
			wantError: "canonical lowercase UUID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if test.raw != nil {
				if err := os.WriteFile(p.handoffsPath(), test.raw, 0o644); err != nil {
					t.Fatalf("os.WriteFile(handoffs) error = %v", err)
				}
			} else {
				writeJSONFile(t, p.handoffsPath(), test.value)
			}
			before, err := os.ReadFile(p.handoffsPath())
			if err != nil {
				t.Fatalf("os.ReadFile(before) error = %v", err)
			}

			_, err = p.WriteHandoff(provider.HandoffWriteRequest{Message: "must not be appended"})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("WriteHandoff() error = %v, want containing %q", err, test.wantError)
			}
			after, err := os.ReadFile(p.handoffsPath())
			if err != nil {
				t.Fatalf("os.ReadFile(after) error = %v", err)
			}
			if string(after) != string(before) {
				t.Fatalf("corrupt handoff store changed after rejected append: got %q, want %q", after, before)
			}
		})
	}
}

func TestWriteHandoffsRejectsInvalidCollectionsWithoutChangingExistingStore(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)
	initial := []core.Handoff{{
		ID:        "00000000-0000-4000-8000-000000000001",
		Message:   "existing context",
		CreatedAt: mustRFC3339Time(t, "2026-08-09T10:00:00Z"),
	}}
	if err := p.writeHandoffs(initial); err != nil {
		t.Fatalf("writeHandoffs(initial) error = %v", err)
	}
	before, err := os.ReadFile(p.handoffsPath())
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}

	invalidTimestamp := initial[0]
	invalidTimestamp.CreatedAt = time.Time{}
	invalidMessage := initial[0]
	invalidMessage.Message = " "
	tests := []struct {
		name      string
		handoffs  []core.Handoff
		wantError string
	}{
		{
			name: "duplicate IDs",
			handoffs: []core.Handoff{
				initial[0],
				{ID: initial[0].ID, Message: "duplicate", CreatedAt: initial[0].CreatedAt},
			},
			wantError: "duplicated",
		},
		{
			name:      "invalid timestamp",
			handoffs:  []core.Handoff{invalidTimestamp},
			wantError: "createdAt is required",
		},
		{
			name:      "invalid field value",
			handoffs:  []core.Handoff{invalidMessage},
			wantError: "message is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := p.writeHandoffs(test.handoffs); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("writeHandoffs() error = %v, want containing %q", err, test.wantError)
			}
			after, err := os.ReadFile(p.handoffsPath())
			if err != nil {
				t.Fatalf("os.ReadFile(after) error = %v", err)
			}
			if string(after) != string(before) {
				t.Fatalf("existing handoff store changed after rejected collection: got %q, want %q", after, before)
			}
		})
	}
}

func TestWriteIndexPreservesExistingIndexWhenReplaceFails(t *testing.T) {
	p, err := New(t.TempDir(), nil)
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

func TestCreateTaskAdvancesPastStaleScopedIndex(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/stale-index")
	p, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, title := range []string{"first", "second"} {
		if _, err := p.CreateTask(core.CreateTaskInput{Title: title}); err != nil {
			t.Fatalf("CreateTask(%q) error = %v", title, err)
		}
	}
	if err := p.writeIndex(indexFile{Branch: scope.Branch, Next: 1}); err != nil {
		t.Fatalf("writeIndex(stale) error = %v", err)
	}

	created, err := p.CreateTask(core.CreateTaskInput{Title: "after stale index"})
	if err != nil {
		t.Fatalf("CreateTask(after stale index) error = %v", err)
	}
	if got, want := created.ShortID, "wtp-"+scope.BranchID+"-0003"; got != want {
		t.Fatalf("created short ID = %q, want %q", got, want)
	}
	index, err := p.readIndex()
	if err != nil {
		t.Fatalf("readIndex() error = %v", err)
	}
	if index.Next != 4 {
		t.Fatalf("index after stale allocation = %#v, want next 4", index)
	}
}

func TestCreateTaskRebuildsMissingScopedIndexFromExistingTasks(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/missing-index")
	p, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, err := p.CreateTask(core.CreateTaskInput{Title: "existing scoped task"})
	if err != nil {
		t.Fatalf("CreateTask(existing) error = %v", err)
	}
	if err := os.Remove(p.indexPath()); err != nil {
		t.Fatalf("Remove(scoped index) error = %v", err)
	}

	reopened, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() after removing scoped index error = %v", err)
	}
	created, err := reopened.CreateTask(core.CreateTaskInput{Title: "after missing index"})
	if err != nil {
		t.Fatalf("CreateTask(after missing index) error = %v", err)
	}
	if got, want := created.ShortID, "wtp-"+scope.BranchID+"-0002"; got != want {
		t.Fatalf("created short ID = %q, want %q", got, want)
	}
	if first.ShortID == created.ShortID {
		t.Fatalf("reused existing short ID %q after rebuilding index", created.ShortID)
	}
	index, err := reopened.readIndex()
	if err != nil {
		t.Fatalf("readIndex() error = %v", err)
	}
	if index.Branch != scope.Branch || index.Next != 3 {
		t.Fatalf("rebuilt index = %#v, want branch %q next 3", index, scope.Branch)
	}
}

func TestNewRejectsExact32BitBranchIDCollisionFromIndexMetadata(t *testing.T) {
	root := t.TempDir()
	firstScope := core.NewBranchScope("feature/hash-collision-108549")
	secondScope := core.NewBranchScope("feature/hash-collision-143127")
	if firstScope.BranchID != secondScope.BranchID || firstScope.BranchID != "e398bd06" {
		t.Fatalf("collision fixture IDs = %q and %q, want both e398bd06", firstScope.BranchID, secondScope.BranchID)
	}
	if _, err := New(root, firstScope); err != nil {
		t.Fatalf("New(first scope) error = %v", err)
	}

	_, err := New(root, secondScope)
	if err == nil || !strings.Contains(err.Error(), "branch index token e398bd06 belongs to branch \"feature/hash-collision-108549\", not \"feature/hash-collision-143127\"") {
		t.Fatalf("New(colliding scope) error = %v, want metadata collision error", err)
	}
}

func TestConcurrentScopedCreatesAdvanceOneSharedIndex(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/concurrent-internal")
	providers := make([]*Provider, 2)
	for index := range providers {
		p, err := New(root, scope)
		if err != nil {
			t.Fatalf("New(%d) error = %v", index, err)
		}
		providers[index] = p
	}

	const count = 8
	shortIDs := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			created, err := providers[index%len(providers)].CreateTask(core.CreateTaskInput{Title: "concurrent scoped task"})
			if err != nil {
				errs <- err
				return
			}
			shortIDs <- created.ShortID
		}(index)
	}
	wg.Wait()
	close(errs)
	close(shortIDs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	got := make([]string, 0, count)
	for shortID := range shortIDs {
		got = append(got, shortID)
	}
	slices.Sort(got)
	want := make([]string, count)
	for index := range want {
		want[index] = fmt.Sprintf("wtp-%s-%04d", scope.BranchID, index+1)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("concurrent scoped short IDs = %v, want %v", got, want)
	}
}

func TestCreateTaskPublicationFailureDoesNotOverwriteExistingTask(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/publication-failure")
	p, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	existing, err := p.CreateTask(core.CreateTaskInput{Title: "existing task"})
	if err != nil {
		t.Fatalf("CreateTask(existing) error = %v", err)
	}
	if err := p.writeIndex(indexFile{Branch: scope.Branch, Next: 1}); err != nil {
		t.Fatalf("writeIndex(stale) error = %v", err)
	}
	existingPath := filepath.Join(p.statusDir(core.StatusTodo), existing.ShortID+".json")
	before, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("ReadFile(existing task) error = %v", err)
	}
	newShortID := "wtp-" + scope.BranchID + "-0002"
	newTaskPath := filepath.Join(p.statusDir(core.StatusTodo), newShortID+".json")
	realReplace := p.fs.replace
	p.fs.replace = func(source, target string) error {
		if target == newTaskPath {
			return errors.New("injected task publication failure")
		}
		return realReplace(source, target)
	}

	if _, err := p.CreateTask(core.CreateTaskInput{Title: "must not publish"}); err == nil || !strings.Contains(err.Error(), "injected task publication failure") {
		t.Fatalf("CreateTask(publication failure) error = %v", err)
	}
	after, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("ReadFile(existing task after failure) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("existing task file changed after failed publication: got %q, want %q", after, before)
	}
	if _, err := os.Stat(newTaskPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new task file after failed publication has err=%v, want not exist", err)
	}
	index, err := p.readIndex()
	if err != nil {
		t.Fatalf("readIndex() after failed publication error = %v", err)
	}
	if index.Next != 3 {
		t.Fatalf("index after failed publication = %#v, want next 3 (acceptable gap)", index)
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

func TestReplaceTaskScopedCleansExactShortIDAndUUIDResidue(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/scoped-cleanup")
	p, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	scoped := validTask("35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-"+scope.BranchID+"-0001", core.StatusTodo)
	legacy := validTask("ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a", "wtp-0001", core.StatusTodo)
	legacyPath := p.taskPath(core.StatusTodo, legacy.ShortID)
	if err := p.writeTask(legacy); err != nil {
		t.Fatalf("writeTask(legacy) error = %v", err)
	}
	uuidResiduePath := p.taskPath(core.StatusTodo, scoped.ID)
	writeJSONFile(t, uuidResiduePath, scoped)

	updated := scoped
	updated.Status = core.StatusInProgress
	updated.UpdatedAt = scoped.UpdatedAt.Add(time.Second)
	updated.StartedAt = &updated.UpdatedAt
	if err := p.replaceTask(updated); err != nil {
		t.Fatalf("replaceTask(scoped) error = %v", err)
	}

	assertStoredTask(t, p.taskPath(core.StatusInProgress, updated.ShortID), updated)
	if _, err := os.Stat(uuidResiduePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("UUID residue remains after scoped status move: %v", err)
	}
	if _, err := os.Stat(p.taskPath(core.StatusTodo, updated.ShortID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scoped short-ID source remains after status move: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("scoped status move removed unrelated legacy task: %v", err)
	}
}

func TestReplaceTaskScopedUUIDCleanupFailureLeavesValidResidue(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/scoped-interrupted-cleanup")
	p, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	scoped := validTask("45c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-"+scope.BranchID+"-0001", core.StatusTodo)
	uuidResiduePath := p.taskPath(core.StatusTodo, scoped.ID)
	writeJSONFile(t, uuidResiduePath, scoped)
	updated := scoped
	updated.Status = core.StatusInProgress
	updated.UpdatedAt = scoped.UpdatedAt.Add(time.Second)
	updated.StartedAt = &updated.UpdatedAt

	realRemove := p.fs.remove
	p.fs.remove = func(path string) error {
		if path == uuidResiduePath {
			return errors.New("injected scoped cleanup failure")
		}
		return realRemove(path)
	}
	if err := p.replaceTask(updated); err == nil || !strings.Contains(err.Error(), "injected scoped cleanup failure") {
		t.Fatalf("replaceTask() error = %v, want injected failure", err)
	}

	assertStoredTask(t, p.taskPath(core.StatusInProgress, updated.ShortID), updated)
	assertStoredTask(t, uuidResiduePath, scoped)
	tasks, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() with scoped UUID residue error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != updated.ID || tasks[0].ShortID != updated.ShortID || tasks[0].Status != updated.Status {
		t.Fatalf("ListTasks() with scoped UUID residue = %#v, want one updated task", tasks)
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
	p, err := New(root, nil)
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

	if _, err := New(root, nil); err == nil || !strings.Contains(err.Error(), "refuse to overwrite conflicting task file") {
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

func TestMigrateTaskFilenamesRefusesConflictingScopedTarget(t *testing.T) {
	root := t.TempDir()
	if _, err := New(root, nil); err != nil {
		t.Fatalf("New() setup error = %v", err)
	}
	scope := core.NewBranchScope("feature/scoped-conflicting-target")
	legacy := validTask("35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-"+scope.BranchID+"-0001", core.StatusTodo)
	conflict := validTask("45c3806a-bd1b-424d-889b-29e5b06679b8", legacy.ShortID, core.StatusTodo)
	legacyPath := filepath.Join(root, string(core.StatusTodo), legacy.ID+".json")
	targetPath := filepath.Join(root, string(core.StatusTodo), legacy.ShortID+".json")
	writeJSONFile(t, legacyPath, legacy)
	writeJSONFile(t, targetPath, conflict)
	legacyBefore, _ := os.ReadFile(legacyPath)
	targetBefore, _ := os.ReadFile(targetPath)

	if _, err := New(root, scope); err == nil || !strings.Contains(err.Error(), "refuse to overwrite conflicting task file") {
		t.Fatalf("New() scoped migration error = %v, want conflict", err)
	}
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("scoped legacy file missing after conflict: %v", err)
	}
	targetAfter, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("scoped target file missing after conflict: %v", err)
	}
	if string(legacyAfter) != string(legacyBefore) || string(targetAfter) != string(targetBefore) {
		t.Fatal("conflicting scoped migration changed an existing task file")
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
	if err := os.WriteFile(filepath.Join(exportDir, handoffsFilename), []byte("old handoffs\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(handoffs) error = %v", err)
	}

	if err := p.ExportCanonical(exportDir); err != nil {
		t.Fatalf("ExportCanonical() error = %v", err)
	}

	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	if got, want := entryNames(entries), []string{task.ID + ".json", handoffsFilename}; !slices.Equal(got, want) {
		t.Fatalf("export entries = %v, want %v", got, want)
	}
	assertStoredTask(t, currentPath, task)
	assertStoredHandoffs(t, filepath.Join(exportDir, handoffsFilename), []core.Handoff{})

	if err := p.ExportCanonical(exportDir); err != nil {
		t.Fatalf("repeat ExportCanonical() error = %v", err)
	}
	entries, err = os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("os.ReadDir() after repeat error = %v", err)
	}
	if got, want := entryNames(entries), []string{task.ID + ".json", handoffsFilename}; !slices.Equal(got, want) {
		t.Fatalf("repeat export entries = %v, want %v", got, want)
	}
	assertStoredTask(t, currentPath, task)
	assertStoredHandoffs(t, filepath.Join(exportDir, handoffsFilename), []core.Handoff{})
}

func TestExportCanonicalIncludesRetainedHandoffs(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	handoffs := []core.Handoff{
		{
			ID:        "00000000-0000-4000-8000-000000000001",
			TaskID:    task.ID,
			Author:    "Tony",
			Message:   "Resume the export work.",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T17:00:00Z"),
		},
		{
			ID:        "00000000-0000-4000-8000-000000000002",
			Author:    "Ada",
			Message:   "Global context.",
			CreatedAt: mustRFC3339Time(t, "2026-08-09T18:00:00Z"),
		},
	}
	if err := p.writeHandoffs(handoffs); err != nil {
		t.Fatalf("writeHandoffs() error = %v", err)
	}

	exportDir := t.TempDir()
	if err := p.ExportCanonical(exportDir); err != nil {
		t.Fatalf("ExportCanonical() error = %v", err)
	}

	assertStoredHandoffs(t, filepath.Join(exportDir, handoffsFilename), handoffs)
	if err := p.ExportCanonical(exportDir); err != nil {
		t.Fatalf("repeat ExportCanonical() error = %v", err)
	}
	assertStoredHandoffs(t, filepath.Join(exportDir, handoffsFilename), handoffs)
}

func TestExportCanonicalRejectsUnmanagedEntriesBeforeChangingSnapshot(t *testing.T) {
	p, task := newInternalProviderWithTask(t)
	exportDir := t.TempDir()
	currentPath := filepath.Join(exportDir, task.ID+".json")
	currentBefore := []byte("old snapshot\n")
	if err := os.WriteFile(currentPath, currentBefore, 0o644); err != nil {
		t.Fatalf("os.WriteFile(current) error = %v", err)
	}
	unmanagedNames := []string{
		"notes.txt",
		"another-dir",
		"task.json",
		"handoffs.json.bak",
		"00000000-0000-4000-8000-000000000099.txt",
		".keep",
	}
	for _, name := range unmanagedNames {
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
	wantUnmanaged := append([]string(nil), unmanagedNames...)
	slices.Sort(wantUnmanaged)
	wantError := "unmanaged entries: " + strings.Join(wantUnmanaged, ", ")
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("ExportCanonical() error = %v, want %s", err, wantError)
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

func TestExportCanonicalValidatesHandoffsBeforeChangingSnapshot(t *testing.T) {
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
	exportHandoffsPath := filepath.Join(exportDir, handoffsFilename)
	exportHandoffsBefore := []byte("old exported handoffs\n")
	if err := os.WriteFile(exportHandoffsPath, exportHandoffsBefore, 0o644); err != nil {
		t.Fatalf("os.WriteFile(export handoffs) error = %v", err)
	}
	if err := os.WriteFile(p.handoffsPath(), []byte("not valid JSON"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(handoffs) error = %v", err)
	}

	err := p.ExportCanonical(exportDir)
	if err == nil || !strings.Contains(err.Error(), "corrupt handoff file") {
		t.Fatalf("ExportCanonical() error = %v, want corrupt handoff file", err)
	}
	currentAfter, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("os.ReadFile(current) error = %v", readErr)
	}
	if string(currentAfter) != string(currentBefore) {
		t.Fatalf("current snapshot changed before handoff validation: got %q, want %q", currentAfter, currentBefore)
	}
	if _, statErr := os.Stat(stalePath); statErr != nil {
		t.Fatalf("stale snapshot removed before handoff validation: %v", statErr)
	}
	exportHandoffsAfter, readErr := os.ReadFile(exportHandoffsPath)
	if readErr != nil {
		t.Fatalf("os.ReadFile(export handoffs after rejection) error = %v", readErr)
	}
	if string(exportHandoffsAfter) != string(exportHandoffsBefore) {
		t.Fatalf("export handoffs changed before handoff validation: got %q, want %q", exportHandoffsAfter, exportHandoffsBefore)
	}
}

func newInternalProviderWithTask(t *testing.T) (*Provider, core.Task) {
	t.Helper()
	p, err := New(t.TempDir(), nil)
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

func assertStoredHandoffs(t *testing.T, path string, want []core.Handoff) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	var stored handoffFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", path, err)
	}
	if stored.Handoffs == nil {
		t.Fatalf("stored handoffs = nil, want empty array")
	}
	if !slices.EqualFunc(stored.Handoffs, want, func(got, expected core.Handoff) bool {
		return got == expected
	}) {
		t.Fatalf("stored handoffs = %#v, want %#v", stored.Handoffs, want)
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
