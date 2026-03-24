package flatfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"wtp/internal/core"
	"wtp/internal/provider"
	"wtp/internal/provider/flatfile"
)

func TestCreateAndResolveByShortID(t *testing.T) {
	p := newProvider(t)

	first, err := p.CreateTask(core.CreateTaskInput{
		Title:       "First task",
		Description: "base task",
	})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}

	second, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Second task",
		Dependencies: []string{first.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}

	got, err := p.GetTask(second.ShortID)
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", second.ShortID, err)
	}

	if len(got.Dependencies) != 1 || got.Dependencies[0] != first.ID {
		t.Fatalf("resolved dependencies = %v, want [%s]", got.Dependencies, first.ID)
	}
}

func TestUpdateTaskStatusRejectsBlockedStart(t *testing.T) {
	p := newProvider(t)

	first, err := p.CreateTask(core.CreateTaskInput{Title: "First task"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	second, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Blocked task",
		Dependencies: []string{first.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}

	if _, err := p.UpdateTaskStatus(second.ShortID, core.StatusInProgress, "Agent"); err == nil {
		t.Fatal("expected blocked task start to fail")
	}
}

func TestGetNextTaskPrefersPausedThenAssignedThenUnassigned(t *testing.T) {
	p := newProvider(t)

	assignedTodo, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Assigned todo",
		Assignee: "Tony",
	})
	if err != nil {
		t.Fatalf("CreateTask(assignedTodo) error = %v", err)
	}
	unassignedTodo, err := p.CreateTask(core.CreateTaskInput{Title: "Unassigned todo"})
	if err != nil {
		t.Fatalf("CreateTask(unassignedTodo) error = %v", err)
	}
	pausedCandidate, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Paused candidate",
		Assignee: "Tony",
	})
	if err != nil {
		t.Fatalf("CreateTask(pausedCandidate) error = %v", err)
	}

	if _, err := p.UpdateTaskStatus(pausedCandidate.ShortID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("start pausedCandidate: %v", err)
	}
	if _, err := p.UpdateTaskStatus(pausedCandidate.ShortID, core.StatusPaused, "Tony"); err != nil {
		t.Fatalf("pause pausedCandidate: %v", err)
	}

	next, err := p.GetNextTask("Tony")
	if err != nil {
		t.Fatalf("GetNextTask(Tony) error = %v", err)
	}
	if next.ID != pausedCandidate.ID {
		t.Fatalf("GetNextTask(Tony) = %s, want %s", next.ID, pausedCandidate.ID)
	}
	if next.Status != core.StatusInProgress {
		t.Fatalf("GetNextTask(Tony) status = %s, want %s", next.Status, core.StatusInProgress)
	}
	if next.Assignee != "Tony" {
		t.Fatalf("GetNextTask(Tony) assignee = %q, want Tony", next.Assignee)
	}

	if _, err := p.UpdateTaskStatus(pausedCandidate.ShortID, core.StatusDone, "Tony"); err != nil {
		t.Fatalf("complete pausedCandidate: %v", err)
	}
	if _, err := p.UpdateTaskStatus(assignedTodo.ShortID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("start assignedTodo: %v", err)
	}

	fallback, err := p.GetNextTask("Tony")
	if err != nil {
		t.Fatalf("GetNextTask(Tony) fallback error = %v", err)
	}
	if fallback.ID != unassignedTodo.ID {
		t.Fatalf("GetNextTask(Tony) fallback = %s, want %s", fallback.ID, unassignedTodo.ID)
	}
	if fallback.Status != core.StatusInProgress {
		t.Fatalf("GetNextTask(Tony) fallback status = %s, want %s", fallback.Status, core.StatusInProgress)
	}
}

func TestAddCommentPersists(t *testing.T) {
	p := newProvider(t)

	task, err := p.CreateTask(core.CreateTaskInput{Title: "Commentable"})
	if err != nil {
		t.Fatalf("CreateTask error = %v", err)
	}
	updated, err := p.AddComment(task.ShortID, "Tony", "Implemented parser")
	if err != nil {
		t.Fatalf("AddComment error = %v", err)
	}

	if len(updated.Comments) != 1 {
		t.Fatalf("comment count = %d, want 1", len(updated.Comments))
	}
	if updated.Comments[0].Author != "Tony" || updated.Comments[0].Message != "Implemented parser" {
		t.Fatalf("comment = %#v", updated.Comments[0])
	}
}

func TestCreateWritesShortIDFilename(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}

	task, err := p.CreateTask(core.CreateTaskInput{Title: "Filename check"})
	if err != nil {
		t.Fatalf("CreateTask error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json")); err != nil {
		t.Fatalf("Stat(shortID filename) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), task.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy uuid filename to be absent, got err=%v", err)
	}
}

func TestNewMigratesLegacyUUIDFilename(t *testing.T) {
	root := t.TempDir()
	if _, err := flatfile.New(root); err != nil {
		t.Fatalf("flatfile.New() setup error = %v", err)
	}

	task := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Legacy file",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
	}
	legacyPath := filepath.Join(root, string(core.StatusTodo), task.ID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatalf("write legacy task error = %v", err)
	}

	if _, err := flatfile.New(root); err != nil {
		t.Fatalf("flatfile.New() migration error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json")); err != nil {
		t.Fatalf("shortID filename missing after migration: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy path removed, got err=%v", err)
	}
}

func TestConcurrentCreateAllocatesUniqueShortIDs(t *testing.T) {
	p := newProvider(t)

	const count = 8
	shortIDs := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			task, err := p.CreateTask(core.CreateTaskInput{
				Title:       "Concurrent task",
				Description: "create in parallel",
			})
			if err != nil {
				errs <- err
				return
			}
			shortIDs <- task.ShortID
		}(i)
	}
	wg.Wait()
	close(errs)
	close(shortIDs)

	for err := range errs {
		if err != nil {
			t.Fatalf("CreateTask() concurrent error = %v", err)
		}
	}

	got := make([]string, 0, count)
	seen := map[string]struct{}{}
	for shortID := range shortIDs {
		if _, exists := seen[shortID]; exists {
			t.Fatalf("duplicate shortID allocated: %s", shortID)
		}
		seen[shortID] = struct{}{}
		got = append(got, shortID)
	}
	slices.Sort(got)
	want := []string{
		"wtp-0001",
		"wtp-0002",
		"wtp-0003",
		"wtp-0004",
		"wtp-0005",
		"wtp-0006",
		"wtp-0007",
		"wtp-0008",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("allocated shortIDs = %v, want %v", got, want)
	}
}

func TestConcurrentGetNextTaskClaimsOnlyOnce(t *testing.T) {
	p := newProvider(t)

	task, err := p.CreateTask(core.CreateTaskInput{
		Title:       "Claim once",
		Description: "single eligible task",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	type result struct {
		task core.Task
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := p.GetNextTask("Tony")
			results <- result{task: next, err: err}
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	failures := 0
	for result := range results {
		if result.err != nil {
			failures++
			continue
		}
		successes++
		if result.task.ID != task.ID {
			t.Fatalf("claimed task id = %s, want %s", result.task.ID, task.ID)
		}
		if result.task.Status != core.StatusInProgress {
			t.Fatalf("claimed task status = %s, want %s", result.task.Status, core.StatusInProgress)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("GetNextTask() successes=%d failures=%d, want 1 success and 1 failure", successes, failures)
	}
}

func newProvider(t *testing.T) provider.Provider {
	t.Helper()

	root := t.TempDir()
	p, err := flatfile.New(root)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	return p
}

func mustTime(t *testing.T, value string) (outTime time.Time) {
	t.Helper()

	outTime, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return outTime
}
