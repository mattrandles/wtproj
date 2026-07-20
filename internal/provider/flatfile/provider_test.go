package flatfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	got, err := p.GetTask(second.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", second.ShortID, err)
	}

	if len(got.Dependencies) != 1 || got.Dependencies[0] != first.ID {
		t.Fatalf("resolved dependencies = %v, want [%s]", got.Dependencies, first.ID)
	}
}

func TestCreateTaskPersistsSchedulingMetadata(t *testing.T) {
	p := newProvider(t)

	task, err := p.CreateTask(core.CreateTaskInput{
		Title:       "Scheduled task",
		Priority:    core.PriorityHigh,
		Estimate:    core.EstimateM,
		Lane:        "backend",
		Model:       "gpt-5.2-codex",
		Description: "metadata coverage",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	got, err := p.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", task.ShortID, err)
	}
	if got.Priority != core.PriorityHigh {
		t.Fatalf("priority = %s, want %s", got.Priority, core.PriorityHigh)
	}
	if got.Estimate != core.EstimateM {
		t.Fatalf("estimate = %s, want %s", got.Estimate, core.EstimateM)
	}
	if got.Lane != "backend" {
		t.Fatalf("lane = %q, want backend", got.Lane)
	}
	if got.Model != "gpt-5.2-codex" {
		t.Fatalf("model = %q, want gpt-5.2-codex", got.Model)
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

func TestGetNextTaskPrefersHigherPriorityWithinStatusBucket(t *testing.T) {
	p := newProvider(t)

	low, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Low priority",
		Priority: core.PriorityLow,
	})
	if err != nil {
		t.Fatalf("CreateTask(low) error = %v", err)
	}
	high, err := p.CreateTask(core.CreateTaskInput{
		Title:    "High priority",
		Priority: core.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("CreateTask(high) error = %v", err)
	}

	next, err := p.GetNextTask("")
	if err != nil {
		t.Fatalf("GetNextTask(\"\") error = %v", err)
	}
	if next.ID != high.ID {
		t.Fatalf("GetNextTask(\"\") = %s, want %s", next.ID, high.ID)
	}

	storedLow, err := p.GetTask(low.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", low.ShortID, err)
	}
	if storedLow.Status != core.StatusTodo {
		t.Fatalf("low-priority task status = %s, want %s", storedLow.Status, core.StatusTodo)
	}
}

func TestPeekNextTaskUsesSameEligibilityWithoutClaiming(t *testing.T) {
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

	ready, err := p.PeekNextTask("Tony")
	if err != nil {
		t.Fatalf("PeekNextTask(Tony) error = %v", err)
	}
	if ready.ID != assignedTodo.ID {
		t.Fatalf("PeekNextTask(Tony) = %s, want %s", ready.ID, assignedTodo.ID)
	}
	if ready.Status != core.StatusTodo {
		t.Fatalf("PeekNextTask(Tony) status = %s, want %s", ready.Status, core.StatusTodo)
	}

	stored, err := p.GetTask(assignedTodo.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", assignedTodo.ShortID, err)
	}
	if stored.Status != core.StatusTodo {
		t.Fatalf("stored assigned status = %s, want %s", stored.Status, core.StatusTodo)
	}

	fallback, err := p.PeekNextTask("Bob")
	if err != nil {
		t.Fatalf("PeekNextTask(Bob) error = %v", err)
	}
	if fallback.ID != unassignedTodo.ID {
		t.Fatalf("PeekNextTask(Bob) = %s, want %s", fallback.ID, unassignedTodo.ID)
	}
}

func TestPeekNextTasksReturnsOrderedBatchWithoutClaiming(t *testing.T) {
	p := newProvider(t)

	assignedLow, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Assigned low",
		Assignee: "Tony",
		Priority: core.PriorityLow,
	})
	if err != nil {
		t.Fatalf("CreateTask(assignedLow) error = %v", err)
	}
	assignedHigh, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Assigned high",
		Assignee: "Tony",
		Priority: core.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("CreateTask(assignedHigh) error = %v", err)
	}
	unassigned, err := p.CreateTask(core.CreateTaskInput{Title: "Unassigned"})
	if err != nil {
		t.Fatalf("CreateTask(unassigned) error = %v", err)
	}
	if _, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Foreign assigned",
		Assignee: "Alice",
	}); err != nil {
		t.Fatalf("CreateTask(foreignAssigned) error = %v", err)
	}

	ready, err := p.PeekNextTasks("Tony", 3)
	if err != nil {
		t.Fatalf("PeekNextTasks(Tony, 3) error = %v", err)
	}
	if len(ready) != 3 {
		t.Fatalf("PeekNextTasks(Tony, 3) count = %d, want 3", len(ready))
	}
	if ready[0].ID != assignedHigh.ID {
		t.Fatalf("ready[0] = %s, want %s", ready[0].ID, assignedHigh.ID)
	}
	if ready[1].ID != assignedLow.ID {
		t.Fatalf("ready[1] = %s, want %s", ready[1].ID, assignedLow.ID)
	}
	if ready[2].ID != unassigned.ID {
		t.Fatalf("ready[2] = %s, want %s", ready[2].ID, unassigned.ID)
	}

	stored, err := p.GetTask(assignedHigh.ShortID, "Tony")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", assignedHigh.ShortID, err)
	}
	if stored.Status != core.StatusTodo {
		t.Fatalf("stored status = %s, want %s", stored.Status, core.StatusTodo)
	}
}

func TestPeekNextTasksRejectsNonPositiveLimit(t *testing.T) {
	p := newProvider(t)

	if _, err := p.CreateTask(core.CreateTaskInput{Title: "Ready task"}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := p.PeekNextTasks("", 0); err == nil {
		t.Fatal("expected invalid limit error")
	}
}

func TestGetNextTaskDoesNotClaimForeignAssignedTaskForAgent(t *testing.T) {
	p := newProvider(t)

	foreignAssigned, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Foreign assigned",
		Assignee: "Alice",
	})
	if err != nil {
		t.Fatalf("CreateTask(foreignAssigned) error = %v", err)
	}
	if _, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Later foreign assigned",
		Assignee: "Alice",
	}); err != nil {
		t.Fatalf("CreateTask(later foreign assigned) error = %v", err)
	}

	if _, err := p.GetNextTask("Bob"); err == nil {
		t.Fatal("expected no eligible task for Bob")
	}

	stored, err := p.GetTask(foreignAssigned.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", foreignAssigned.ShortID, err)
	}
	if stored.Status != core.StatusTodo {
		t.Fatalf("foreign-assigned task status = %s, want %s", stored.Status, core.StatusTodo)
	}
	if stored.Assignee != "Alice" {
		t.Fatalf("foreign-assigned task assignee = %q, want Alice", stored.Assignee)
	}
}

func TestGetNextTaskWithoutAgentCanClaimAssignedTask(t *testing.T) {
	p := newProvider(t)

	assigned, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Assigned task",
		Assignee: "Alice",
	})
	if err != nil {
		t.Fatalf("CreateTask(assigned) error = %v", err)
	}

	next, err := p.GetNextTask("")
	if err != nil {
		t.Fatalf("GetNextTask(\"\") error = %v", err)
	}
	if next.ID != assigned.ID {
		t.Fatalf("GetNextTask(\"\") = %s, want %s", next.ID, assigned.ID)
	}
	if next.Assignee != "Alice" {
		t.Fatalf("GetNextTask(\"\") assignee = %q, want Alice", next.Assignee)
	}
}

func TestGetTaskExposesReadinessMetadata(t *testing.T) {
	p := newProvider(t)

	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "Dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}
	target, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Blocked task",
		Dependencies: []string{dependency.ShortID},
		Assignee:     "Alice",
	})
	if err != nil {
		t.Fatalf("CreateTask(target) error = %v", err)
	}
	if _, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Reverse dependency",
		Dependencies: []string{target.ShortID},
	}); err != nil {
		t.Fatalf("CreateTask(reverse dependency) error = %v", err)
	}

	got, err := p.GetTask(target.ShortID, "Bob")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", target.ShortID, err)
	}
	if !got.Readiness.Blocked {
		t.Fatal("expected task to be blocked")
	}
	if got.Readiness.BlockedReason == "" {
		t.Fatal("expected blocked reason")
	}
	if got.Readiness.DependencyCount != 1 {
		t.Fatalf("dependencyCount = %d, want 1", got.Readiness.DependencyCount)
	}
	if got.Readiness.ReverseDependencyCount != 1 {
		t.Fatalf("reverseDependencyCount = %d, want 1", got.Readiness.ReverseDependencyCount)
	}
	if got.Readiness.Claimable {
		t.Fatal("expected foreign-assigned blocked task to be unclaimable")
	}
}

func TestListTasksUsesAgentContextForClaimability(t *testing.T) {
	p := newProvider(t)

	if _, err := p.CreateTask(core.CreateTaskInput{
		Title:    "Alice task",
		Assignee: "Alice",
	}); err != nil {
		t.Fatalf("CreateTask(Alice task) error = %v", err)
	}
	unassigned, err := p.CreateTask(core.CreateTaskInput{Title: "Unassigned task"})
	if err != nil {
		t.Fatalf("CreateTask(Unassigned task) error = %v", err)
	}

	tasks, err := p.ListTasks(provider.TaskFilter{Agent: "Bob"})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}

	for _, task := range tasks {
		switch task.ID {
		case unassigned.ID:
			if !task.Readiness.Claimable {
				t.Fatal("expected unassigned task to be claimable for Bob")
			}
		default:
			if task.Assignee == "Alice" && task.Readiness.Claimable {
				t.Fatal("expected Alice-assigned task to be unclaimable for Bob")
			}
		}
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

func TestUpdateTaskPersistsDependenciesAndMetadata(t *testing.T) {
	p := newProvider(t)

	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "Dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Updatable", Description: "old"})
	if err != nil {
		t.Fatalf("CreateTask(task) error = %v", err)
	}

	updated, err := p.UpdateTask(task.ShortID, core.UpdateTaskInput{
		Description:  core.OptionalString{Set: true, Value: "new description"},
		Priority:     core.OptionalPriority{Set: true, Value: core.PriorityHigh},
		Model:        core.OptionalString{Set: true, Value: "o3"},
		Dependencies: core.OptionalString{Set: true, Value: dependency.ShortID},
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if updated.Description != "new description" {
		t.Fatalf("description = %q, want %q", updated.Description, "new description")
	}
	if updated.Priority != core.PriorityHigh {
		t.Fatalf("priority = %s, want %s", updated.Priority, core.PriorityHigh)
	}
	if updated.Model != "o3" {
		t.Fatalf("model = %q, want o3", updated.Model)
	}
	if len(updated.Dependencies) != 1 || updated.Dependencies[0] != dependency.ID {
		t.Fatalf("dependencies = %v, want [%s]", updated.Dependencies, dependency.ID)
	}

	stored, err := p.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", task.ShortID, err)
	}
	if stored.Description != "new description" {
		t.Fatalf("stored description = %q, want %q", stored.Description, "new description")
	}
	if stored.Model != "o3" {
		t.Fatalf("stored model = %q, want o3", stored.Model)
	}
	if len(stored.Dependencies) != 1 || stored.Dependencies[0] != dependency.ID {
		t.Fatalf("stored dependencies = %v, want [%s]", stored.Dependencies, dependency.ID)
	}
}

func TestUpdateTaskCanClearSuggestedModel(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Model-aware", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	createdData, err := os.ReadFile(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(created task) error = %v", err)
	}
	if !strings.Contains(string(createdData), `"model": "gpt-5"`) {
		t.Fatalf("created task JSON missing model: %s", createdData)
	}

	updated, err := p.UpdateTask(task.ShortID, core.UpdateTaskInput{
		Model: core.OptionalString{Set: true, Value: ""},
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if updated.Model != "" {
		t.Fatalf("model = %q, want empty", updated.Model)
	}

	data, err := os.ReadFile(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), `"model"`) {
		t.Fatalf("cleared model should be omitted from JSON: %s", data)
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

	p, err := flatfile.New(root)
	if err != nil {
		t.Fatalf("flatfile.New() migration error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json")); err != nil {
		t.Fatalf("shortID filename missing after migration: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy path removed, got err=%v", err)
	}
	got, err := p.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() for pre-model task error = %v", err)
	}
	if got.Model != "" {
		t.Fatalf("pre-model task model = %q, want empty", got.Model)
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
		task core.TaskView
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

func TestLoadTasksRejectsMissingDependencyOnDisk(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}

	task := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Broken dependency",
		Status:       core.StatusTodo,
		Dependencies: []string{"missing-task"},
		Comments:     []core.Comment{},
		CreatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
	}
	writeTaskFile(t, root, task)

	_, err = p.ListTasks(provider.TaskFilter{})
	if err == nil {
		t.Fatal("expected load to fail for missing dependency")
	}
	if !contains(err.Error(), `dependency "missing-task" does not exist`) {
		t.Fatalf("ListTasks() error = %v", err)
	}
}

func TestLoadTasksRejectsCyclicDependencyGraphOnDisk(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}

	first := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "First",
		Status:       core.StatusTodo,
		Dependencies: []string{"ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a"},
		Comments:     []core.Comment{},
		CreatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
	}
	second := core.Task{
		ID:           "ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a",
		ShortID:      "wtp-0002",
		Title:        "Second",
		Status:       core.StatusTodo,
		Dependencies: []string{first.ID},
		Comments:     []core.Comment{},
		CreatedAt:    mustTime(t, "2026-03-24T14:10:05Z"),
		UpdatedAt:    mustTime(t, "2026-03-24T14:10:05Z"),
	}
	writeTaskFile(t, root, first)
	writeTaskFile(t, root, second)

	_, err = p.ListTasks(provider.TaskFilter{})
	if err == nil {
		t.Fatal("expected load to fail for cyclic dependency graph")
	}
	if !contains(err.Error(), "cyclic dependency detected") {
		t.Fatalf("ListTasks() error = %v", err)
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

func writeTaskFile(t *testing.T, root string, task core.Task) {
	t.Helper()

	path := filepath.Join(root, string(task.Status), task.ShortID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
