package flatfile_test

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
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
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

func TestCustomStatusDirectoriesPersistAndMove(t *testing.T) {
	catalog := testCustomStatusCatalog(t)
	root := t.TempDir()
	p, err := flatfile.NewWithCatalog(root, nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	for _, status := range []core.Status{"todo", "inProgress", "paused", "done", "waitingForReview", "vendorBlocked", "verificationFailed"} {
		if info, err := os.Stat(filepath.Join(root, string(status))); err != nil || !info.IsDir() {
			t.Fatalf("status directory %s missing: info=%v err=%v", status, info, err)
		}
	}

	task, err := p.CreateTask(core.CreateTaskInput{Title: "Custom status task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	waiting, err := p.UpdateTaskStatus(task.ShortID, "waitingForReview", "Reviewer")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(waitingForReview) error = %v", err)
	}
	if waiting.Status != "waitingForReview" || waiting.Assignee != "Reviewer" || waiting.StartedAt == nil || waiting.CompletedAt != nil {
		t.Fatalf("waiting task lifecycle = %#v", waiting.Task)
	}
	if _, err := os.Stat(filepath.Join(root, "waitingForReview", task.ShortID+".json")); err != nil {
		t.Fatalf("custom status file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "todo", task.ShortID+".json")); !os.IsNotExist(err) {
		t.Fatalf("todo source remains after custom move: %v", err)
	}

	reopened, err := flatfile.NewWithCatalog(root, nil, catalog)
	if err != nil {
		t.Fatalf("reopen with catalog error = %v", err)
	}
	got, err := reopened.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() after reopen error = %v", err)
	}
	if got.Status != waiting.Status || got.Assignee != waiting.Assignee {
		t.Fatalf("reopened custom task = %#v, want status %s and assignee %s", got.Task, waiting.Status, waiting.Assignee)
	}
}

func TestCustomStatusTransitionsNormalizeLifecycleAndReopenTerminalStates(t *testing.T) {
	p := newCustomProvider(t)
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Lifecycle task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	waiting, err := p.UpdateTaskStatus(task.ShortID, "waitingForReview", "Reviewer")
	if err != nil {
		t.Fatalf("to waiting error = %v", err)
	}
	failed, err := p.UpdateTaskStatus(task.ShortID, "verificationFailed", "Reviewer")
	if err != nil {
		t.Fatalf("to failed error = %v", err)
	}
	if failed.StartedAt == nil || failed.CompletedAt == nil || !failed.CompletedAt.After(*failed.StartedAt) && !failed.CompletedAt.Equal(*failed.StartedAt) {
		t.Fatalf("failed lifecycle = started %v completed %v", failed.StartedAt, failed.CompletedAt)
	}
	reopened, err := p.UpdateTaskStatus(task.ShortID, core.StatusTodo, "Reviewer")
	if err != nil {
		t.Fatalf("reopen failed task error = %v", err)
	}
	if reopened.StartedAt != nil || reopened.CompletedAt != nil {
		t.Fatalf("reopened todo retained lifecycle timestamps: %#v", reopened.Task)
	}
	done, err := p.UpdateTaskStatus(task.ShortID, core.StatusDone, "Reviewer")
	if err != nil {
		t.Fatalf("todo to done error = %v", err)
	}
	if done.StartedAt == nil || done.CompletedAt == nil {
		t.Fatalf("done lifecycle = %#v", done.Task)
	}
	waitingAgain, err := p.UpdateTaskStatus(task.ShortID, "waitingForReview", "Reviewer")
	if err != nil {
		t.Fatalf("reopen done task error = %v", err)
	}
	if waitingAgain.StartedAt == nil || waitingAgain.CompletedAt != nil {
		t.Fatalf("reopened waiting lifecycle = %#v", waitingAgain.Task)
	}
	if !waitingAgain.UpdatedAt.After(waiting.UpdatedAt) {
		t.Fatalf("status-plus-metadata update did not advance updatedAt: before=%v after=%v", waiting.UpdatedAt, waitingAgain.UpdatedAt)
	}
}

func TestFailedDependencyDoesNotResolveReadiness(t *testing.T) {
	p := newCustomProvider(t)
	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "Failed dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}
	target, err := p.CreateTask(core.CreateTaskInput{Title: "Dependent task", Dependencies: []string{dependency.ShortID}})
	if err != nil {
		t.Fatalf("CreateTask(target) error = %v", err)
	}
	if _, err := p.UpdateTaskStatus(dependency.ShortID, "verificationFailed", "Reviewer"); err != nil {
		t.Fatalf("fail dependency error = %v", err)
	}
	view, err := p.GetTask(target.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(target) error = %v", err)
	}
	if !view.Readiness.Blocked || view.Readiness.Claimable {
		t.Fatalf("dependent readiness after failure = %#v, want blocked and not claimable", view.Readiness)
	}
	if _, err := p.UpdateTaskStatus(target.ShortID, core.StatusInProgress, "Reviewer"); err == nil || !strings.Contains(err.Error(), "unresolved dependencies") {
		t.Fatalf("starting dependent task error = %v, want unresolved dependency error", err)
	}
}

func TestCustomNonSelectableStatusesExcludedFromReadyAndNext(t *testing.T) {
	p := newCustomProvider(t)
	for _, status := range []core.Status{"waitingForReview", "vendorBlocked", "verificationFailed"} {
		task, err := p.CreateTask(core.CreateTaskInput{Title: string(status)})
		if err != nil {
			t.Fatalf("CreateTask(%s) error = %v", status, err)
		}
		if _, err := p.UpdateTaskStatus(task.ShortID, status, "Reviewer"); err != nil {
			t.Fatalf("UpdateTaskStatus(%s) error = %v", status, err)
		}
	}
	if _, err := p.PeekNextTask("Reviewer"); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("PeekNextTask() error = %v, want no eligible task", err)
	}
	if _, err := p.GetNextTask("Reviewer"); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("GetNextTask() error = %v, want no eligible task", err)
	}
}

func TestCustomStatusMigrationAndRecoveryResidue(t *testing.T) {
	catalog := testCustomStatusCatalog(t)
	root := t.TempDir()
	p, err := flatfile.NewWithCatalog(root, nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Migrated custom task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	legacyPath := filepath.Join(root, "todo", task.ID+".json")
	data, err := os.ReadFile(filepath.Join(root, "todo", task.ShortID+".json"))
	if err != nil {
		t.Fatalf("read canonical task: %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatalf("write legacy task: %v", err)
	}
	if _, err := flatfile.NewWithCatalog(root, nil, catalog); err != nil {
		t.Fatalf("custom filename migration error = %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy custom-layout filename remains: %v", err)
	}

	updated, err := p.UpdateTaskStatus(task.ShortID, "waitingForReview", "Reviewer")
	if err != nil {
		t.Fatalf("move to custom status error = %v", err)
	}
	old := task.Task
	if err := writeTaskJSONForTest(root, old); err != nil {
		t.Fatalf("write custom recovery residue: %v", err)
	}
	listed, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() with custom residue error = %v", err)
	}
	if len(listed) != 1 || listed[0].Status != updated.Status {
		t.Fatalf("custom residue winner = %#v, want %s", listed, updated.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "todo", task.ShortID+".json")); !os.IsNotExist(err) {
		t.Fatalf("custom recovery residue remains: %v", err)
	}
}

func TestCustomTaskRejectedWhenStatusConfigurationIsStale(t *testing.T) {
	catalog := testCustomStatusCatalog(t)
	root := t.TempDir()
	p, err := flatfile.NewWithCatalog(root, nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Stale configuration task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := p.UpdateTaskStatus(task.ShortID, "waitingForReview", "Reviewer"); err != nil {
		t.Fatalf("custom status move error = %v", err)
	}
	if _, err := flatfile.New(root, nil); err == nil || !strings.Contains(err.Error(), "absent from active configuration") {
		t.Fatalf("New() with stale status configuration error = %v", err)
	}
}

func testCustomStatusCatalog(t *testing.T) core.StatusCatalog {
	t.Helper()
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "waitingForReview", Category: core.StatusCategoryWaiting},
		{Name: "vendorBlocked", Category: core.StatusCategoryBlocked},
		{Name: "verificationFailed", Category: core.StatusCategoryFailed},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	return catalog
}

func newCustomProvider(t *testing.T) provider.Provider {
	t.Helper()
	p, err := flatfile.NewWithCatalog(t.TempDir(), nil, testCustomStatusCatalog(t))
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	return p
}

func writeTaskJSONForTest(root string, task core.Task) error {
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, string(task.Status), task.ShortID+".json"), append(data, '\n'), 0o644)
}

func TestCreateTaskReturnsReadinessAgainstExistingDependencies(t *testing.T) {
	p := newProvider(t)

	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "Unfinished dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}

	created, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Blocked task",
		Dependencies: []string{dependency.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(blocked task) error = %v", err)
	}

	if created.Readiness.Claimable {
		t.Fatal("created task is claimable despite an unfinished dependency")
	}
	if !created.Readiness.Blocked {
		t.Fatal("created task is not marked blocked")
	}
	if got, want := created.Readiness.BlockedReason, "unresolved dependencies: "+dependency.ShortID+" (Unfinished dependency)"; got != want {
		t.Fatalf("blocked reason = %q, want %q", got, want)
	}
	if got, want := created.Readiness.DependencyCount, 1; got != want {
		t.Fatalf("dependency count = %d, want %d", got, want)
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

func TestCreateTaskPersistsNormalizedGitAndWorktreeMetadata(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	gitRepo := filepath.Join(t.TempDir(), "repository")
	worktreeDir := filepath.Join(t.TempDir(), "worktree")

	created, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Git-aware task",
		GitRepo:      "  " + gitRepo + "  ",
		GitBranch:    "  feature/task-metadata  ",
		WorktreeName: "  task-metadata  ",
		WorktreeDir:  "  " + worktreeDir + "  ",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	stored, err := p.GetTask(created.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(%q) error = %v", created.ShortID, err)
	}
	if stored.GitRepo != gitRepo {
		t.Fatalf("gitRepo = %q, want %q", stored.GitRepo, gitRepo)
	}
	if stored.GitBranch != "feature/task-metadata" {
		t.Fatalf("gitBranch = %q, want feature/task-metadata", stored.GitBranch)
	}
	if stored.WorktreeName != "task-metadata" {
		t.Fatalf("worktreeName = %q, want task-metadata", stored.WorktreeName)
	}
	if stored.WorktreeDir != worktreeDir {
		t.Fatalf("worktreeDir = %q, want %q", stored.WorktreeDir, worktreeDir)
	}

	data, err := os.ReadFile(filepath.Join(root, string(core.StatusTodo), created.ShortID+".json"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var roundTripped core.Task
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if roundTripped.GitRepo != gitRepo ||
		roundTripped.GitBranch != "feature/task-metadata" ||
		roundTripped.WorktreeName != "task-metadata" ||
		roundTripped.WorktreeDir != worktreeDir {
		t.Fatalf("round-tripped metadata = %#v", roundTripped)
	}
}

func TestCreateTaskRejectsRelativeGitAndWorktreePaths(t *testing.T) {
	p := newProvider(t)

	if _, err := p.CreateTask(core.CreateTaskInput{
		Title:   "Relative repository",
		GitRepo: "relative/repository",
	}); err == nil || !strings.Contains(err.Error(), "gitRepo") {
		t.Fatalf("CreateTask(relative gitRepo) error = %v", err)
	}
	if _, err := p.CreateTask(core.CreateTaskInput{
		Title:       "Relative worktree",
		WorktreeDir: "relative/worktree",
	}); err == nil || !strings.Contains(err.Error(), "worktreeDir") {
		t.Fatalf("CreateTask(relative worktreeDir) error = %v", err)
	}
}

func TestLoadTaskWithoutGitAndWorktreeMetadata(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	legacyJSON := `{
		"id": "25c3806a-bd1b-424d-889b-29e5b06679b8",
		"shortId": "wtp-0001",
		"title": "Legacy flat-file task",
		"description": "",
		"status": "todo",
		"dependencies": [],
		"comments": [],
		"createdAt": "2026-03-24T14:10:04Z",
		"updatedAt": "2026-03-24T14:10:04Z",
		"startedAt": null,
		"completedAt": null
	}`
	path := filepath.Join(root, string(core.StatusTodo), "wtp-0001.json")
	if err := os.WriteFile(path, []byte(legacyJSON), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	got, err := p.GetTask("wtp-0001", "")
	if err != nil {
		t.Fatalf("GetTask() legacy JSON error = %v", err)
	}
	if got.GitRepo != "" || got.GitBranch != "" || got.WorktreeName != "" || got.WorktreeDir != "" {
		t.Fatalf("legacy task contains Git/worktree metadata: %#v", got)
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

func TestClaimsAttachOnlyTaskScopedHandoffsNewestFirst(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	startedTask, err := p.CreateTask(core.CreateTaskInput{Title: "Start with context"})
	if err != nil {
		t.Fatalf("CreateTask(started) error = %v", err)
	}
	nextTask, err := p.CreateTask(core.CreateTaskInput{Title: "Next with context"})
	if err != nil {
		t.Fatalf("CreateTask(next) error = %v", err)
	}
	otherTask, err := p.CreateTask(core.CreateTaskInput{Title: "Other task"})
	if err != nil {
		t.Fatalf("CreateTask(other) error = %v", err)
	}
	writeHandoffCollection(t, root, []core.Handoff{
		testHandoff(t, "00000000-0000-4000-8000-000000000001", startedTask.ID, "2026-08-09T11:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000002", "", "2026-08-09T14:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000003", otherTask.ID, "2026-08-09T15:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000004", startedTask.ID, "2026-08-09T16:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000005", nextTask.ID, "2026-08-09T17:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000006", startedTask.ID, "2026-08-09T13:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000007", nextTask.ID, "2026-08-09T12:00:00Z"),
	})

	started, err := p.UpdateTaskStatus(startedTask.ShortID, core.StatusInProgress, "Tony")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(start) error = %v", err)
	}
	assertClaimHandoffIDs(t, started.Handoffs, []string{
		"00000000-0000-4000-8000-000000000004",
		"00000000-0000-4000-8000-000000000006",
		"00000000-0000-4000-8000-000000000001",
	})

	shown, err := p.GetTask(startedTask.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	assertNoHandoffs(t, shown, "GetTask")

	listed, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	for _, view := range listed {
		assertNoHandoffs(t, view, "ListTasks")
	}

	peeked, err := p.PeekNextTask("Tony")
	if err != nil {
		t.Fatalf("PeekNextTask() error = %v", err)
	}
	if peeked.ID != nextTask.ID {
		t.Fatalf("PeekNextTask() = %s, want %s", peeked.ID, nextTask.ID)
	}
	assertNoHandoffs(t, peeked, "PeekNextTask")

	next, err := p.GetNextTask("Tony")
	if err != nil {
		t.Fatalf("GetNextTask() error = %v", err)
	}
	if next.ID != nextTask.ID {
		t.Fatalf("GetNextTask() = %s, want %s", next.ID, nextTask.ID)
	}
	assertClaimHandoffIDs(t, next.Handoffs, []string{
		"00000000-0000-4000-8000-000000000005",
		"00000000-0000-4000-8000-000000000007",
	})

	paused, err := p.UpdateTaskStatus(startedTask.ShortID, core.StatusPaused, "Tony")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(pause) error = %v", err)
	}
	assertNoHandoffs(t, paused, "UpdateTaskStatus(pause)")

	reclaimed, err := p.UpdateTaskStatus(startedTask.ShortID, core.StatusInProgress, "Tony")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(reclaim) error = %v", err)
	}
	assertClaimHandoffIDs(t, reclaimed.Handoffs, []string{
		"00000000-0000-4000-8000-000000000004",
		"00000000-0000-4000-8000-000000000006",
		"00000000-0000-4000-8000-000000000001",
	})

	done, err := p.UpdateTaskStatus(startedTask.ShortID, core.StatusDone, "Tony")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(done) error = %v", err)
	}
	assertNoHandoffs(t, done, "UpdateTaskStatus(done)")
}

func TestWriteHandoffsAppendsAndReplacesOnlySelectedScope(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	firstTask, err := p.CreateTask(core.CreateTaskInput{Title: "First task"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	secondTask, err := p.CreateTask(core.CreateTaskInput{Title: "Second task"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}

	globalFirst := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Author:  " Ada ",
		Message: " global first ",
	})
	if globalFirst.Handoff.TaskID != "" || globalFirst.ScopeCount != 1 {
		t.Fatalf("first global write = %#v, want global scope count 1", globalFirst)
	}

	taskFirst := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Task:    firstTask.ShortID,
		Message: "first task first",
	})
	if taskFirst.Handoff.TaskID != firstTask.ID || taskFirst.ScopeCount != 1 {
		t.Fatalf("first task write = %#v, want canonical task ID and scope count 1", taskFirst)
	}

	globalSecond := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Message: "global second",
	})
	if globalSecond.ScopeCount != 2 {
		t.Fatalf("appended global write scope count = %d, want 2", globalSecond.ScopeCount)
	}

	taskSecond := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Task:    firstTask.ShortID,
		Message: "first task second",
	})
	if taskSecond.ScopeCount != 2 {
		t.Fatalf("appended task write scope count = %d, want 2", taskSecond.ScopeCount)
	}

	secondTaskHandoff := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Task:    secondTask.ShortID,
		Message: "second task retained",
	})
	if secondTaskHandoff.ScopeCount != 1 {
		t.Fatalf("second task write scope count = %d, want 1", secondTaskHandoff.ScopeCount)
	}

	taskReplacement := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Task:    firstTask.ShortID,
		Message: "first task replacement",
		Replace: true,
	})
	if taskReplacement.Handoff.TaskID != firstTask.ID || taskReplacement.ScopeCount != 1 {
		t.Fatalf("task replacement = %#v, want canonical task ID and scope count 1", taskReplacement)
	}

	globalReplacement := writeProviderHandoff(t, p, provider.HandoffWriteRequest{
		Message: "global replacement",
		Replace: true,
	})
	if globalReplacement.Handoff.TaskID != "" || globalReplacement.ScopeCount != 1 {
		t.Fatalf("global replacement = %#v, want global scope count 1", globalReplacement)
	}

	all, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes) error = %v", err)
	}
	if got, want := len(all.Handoffs), 3; got != want {
		t.Fatalf("retained handoff count = %d, want %d", got, want)
	}
	retainedMessages := make(map[string]bool, len(all.Handoffs))
	for _, handoff := range all.Handoffs {
		retainedMessages[handoff.Message] = true
	}
	for _, message := range []string{"global replacement", "first task replacement", "second task retained"} {
		if !retainedMessages[message] {
			t.Errorf("retained handoffs missing %q", message)
		}
	}
	for _, message := range []string{"global first", "global second", "first task first", "first task second"} {
		if retainedMessages[message] {
			t.Errorf("replaced handoff %q was retained", message)
		}
	}
}

func TestWriteHandoffsConcurrentAppendsDoNotLoseRecords(t *testing.T) {
	const writerCount = 24
	root := t.TempDir()
	providers := make([]provider.Provider, writerCount)
	for index := range providers {
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New(%d) error = %v", index, err)
		}
		providers[index] = p
	}

	start := make(chan struct{})
	errs := make(chan error, writerCount)
	var writers sync.WaitGroup
	for index, p := range providers {
		writers.Add(1)
		go func(index int, p provider.Provider) {
			defer writers.Done()
			<-start
			_, err := p.WriteHandoff(provider.HandoffWriteRequest{
				Author:  fmt.Sprintf("writer-%02d", index),
				Message: fmt.Sprintf("concurrent append %02d", index),
			})
			errs <- err
		}(index, p)
	}
	close(start)
	writers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent WriteHandoff() error = %v", err)
		}
	}

	all, err := providers[0].ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes) error = %v", err)
	}
	if all.TotalMatching != writerCount || len(all.Handoffs) != writerCount {
		t.Fatalf("concurrent handoff count = %d/%d, want %d", len(all.Handoffs), all.TotalMatching, writerCount)
	}
	seenMessages := make(map[string]bool, writerCount)
	for _, handoff := range all.Handoffs {
		seenMessages[handoff.Message] = true
	}
	for index := 0; index < writerCount; index++ {
		message := fmt.Sprintf("concurrent append %02d", index)
		if !seenMessages[message] {
			t.Errorf("concurrent append %q was lost", message)
		}
	}
}

func TestListHandoffsFiltersOrdersAndReportsPagination(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	firstTask, err := p.CreateTask(core.CreateTaskInput{Title: "First task"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	secondTask, err := p.CreateTask(core.CreateTaskInput{Title: "Second task"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}
	writeHandoffCollection(t, root, []core.Handoff{
		testHandoff(t, "00000000-0000-4000-8000-000000000001", "", "2026-08-09T10:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000003", "", "2026-08-09T12:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000002", "", "2026-08-09T12:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000004", firstTask.ID, "2026-08-09T11:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000006", firstTask.ID, "2026-08-09T13:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000005", firstTask.ID, "2026-08-09T13:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000007", secondTask.ID, "2026-08-09T14:00:00Z"),
	})

	global, err := p.ListHandoffs(provider.HandoffFilter{})
	if err != nil {
		t.Fatalf("ListHandoffs(default global) error = %v", err)
	}
	assertHandoffList(t, global, []string{
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000001",
	}, 3, false, true)

	globalLimited, err := p.ListHandoffs(provider.HandoffFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListHandoffs(default global limit) error = %v", err)
	}
	assertHandoffList(t, globalLimited, []string{
		"00000000-0000-4000-8000-000000000002",
	}, 3, true, true)

	taskLimited, err := p.ListHandoffs(provider.HandoffFilter{Task: firstTask.ShortID, Limit: 1})
	if err != nil {
		t.Fatalf("ListHandoffs(task limit) error = %v", err)
	}
	assertHandoffList(t, taskLimited, []string{
		"00000000-0000-4000-8000-000000000005",
	}, 3, true, true)

	taskAll, err := p.ListHandoffs(provider.HandoffFilter{Task: firstTask.ShortID})
	if err != nil {
		t.Fatalf("ListHandoffs(task unlimited) error = %v", err)
	}
	assertHandoffList(t, taskAll, []string{
		"00000000-0000-4000-8000-000000000005",
		"00000000-0000-4000-8000-000000000006",
		"00000000-0000-4000-8000-000000000004",
	}, 3, false, true)

	allLimited, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true, Limit: 2})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes limit) error = %v", err)
	}
	assertHandoffList(t, allLimited, []string{
		"00000000-0000-4000-8000-000000000007",
		"00000000-0000-4000-8000-000000000005",
	}, 7, true, false)

	all, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes unlimited) error = %v", err)
	}
	assertHandoffList(t, all, []string{
		"00000000-0000-4000-8000-000000000007",
		"00000000-0000-4000-8000-000000000005",
		"00000000-0000-4000-8000-000000000006",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000004",
		"00000000-0000-4000-8000-000000000001",
	}, 7, false, false)
}

func TestListHandoffsTreatsMissingFileAsEmpty(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "handoffs.json")); !os.IsNotExist(err) {
		t.Fatalf("handoffs.json exists before first handoff read: %v", err)
	}

	for _, filter := range []provider.HandoffFilter{{}, {AllScopes: true}} {
		result, err := p.ListHandoffs(filter)
		if err != nil {
			t.Fatalf("ListHandoffs(%+v) error = %v", filter, err)
		}
		if result.Handoffs == nil || len(result.Handoffs) != 0 {
			t.Fatalf("ListHandoffs(%+v) handoffs = %#v, want non-nil empty slice", filter, result.Handoffs)
		}
		if result.TotalMatching != 0 || result.HasMore || result.OtherScopesAvailable {
			t.Fatalf("ListHandoffs(%+v) metadata = %#v, want empty result", filter, result)
		}
	}
}

func writeProviderHandoff(t *testing.T, p provider.Provider, request provider.HandoffWriteRequest) provider.HandoffWriteResult {
	t.Helper()
	result, err := p.WriteHandoff(request)
	if err != nil {
		t.Fatalf("WriteHandoff(%+v) error = %v", request, err)
	}
	return result
}

func assertHandoffList(t *testing.T, got provider.HandoffListResult, wantIDs []string, wantTotal int, wantMore, wantOtherScopes bool) {
	t.Helper()
	ids := make([]string, len(got.Handoffs))
	for index, handoff := range got.Handoffs {
		ids[index] = handoff.ID
	}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("handoff IDs = %v, want %v", ids, wantIDs)
	}
	if got.TotalMatching != wantTotal {
		t.Fatalf("totalMatching = %d, want %d", got.TotalMatching, wantTotal)
	}
	if got.HasMore != wantMore {
		t.Fatalf("hasMore = %t, want %t", got.HasMore, wantMore)
	}
	if got.OtherScopesAvailable != wantOtherScopes {
		t.Fatalf("otherScopesAvailable = %t, want %t", got.OtherScopesAvailable, wantOtherScopes)
	}
}

func TestCorruptHandoffsPreventClaimsBeforeTaskMutation(t *testing.T) {
	for _, claim := range []struct {
		name  string
		apply func(provider.Provider, string) error
	}{
		{
			name: "start",
			apply: func(p provider.Provider, shortID string) error {
				_, err := p.UpdateTaskStatus(shortID, core.StatusInProgress, "Tony")
				return err
			},
		},
		{
			name: "next",
			apply: func(p provider.Provider, _ string) error {
				_, err := p.GetNextTask("Tony")
				return err
			},
		},
	} {
		t.Run(claim.name, func(t *testing.T) {
			root := t.TempDir()
			p, err := flatfile.New(root, nil)
			if err != nil {
				t.Fatalf("flatfile.New() error = %v", err)
			}
			task, err := p.CreateTask(core.CreateTaskInput{Title: "Claim should not mutate"})
			if err != nil {
				t.Fatalf("CreateTask() error = %v", err)
			}
			before, err := p.GetTask(task.ShortID, "")
			if err != nil {
				t.Fatalf("GetTask(before claim) error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "handoffs.json"), []byte("not valid JSON"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(handoffs) error = %v", err)
			}

			err = claim.apply(p, task.ShortID)
			if err == nil || !strings.Contains(err.Error(), "corrupt handoff file") {
				t.Fatalf("claim error = %v, want corrupt handoff file", err)
			}
			stored, err := p.GetTask(task.ShortID, "")
			if err != nil {
				t.Fatalf("GetTask() error = %v", err)
			}
			assertTaskLifecycleUnchanged(t, before.Task, stored.Task)
		})
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
	for _, limit := range []int{0, -1} {
		if _, err := p.PeekNextTasks("", limit); err == nil {
			t.Fatalf("PeekNextTasks(limit=%d) returned nil error, want invalid limit error", limit)
		}
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

func TestAddCommentKeepsTimestampsMonotonicAcrossClockRollback(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	created := mustTime(t, "2026-03-24T14:10:04Z")
	future := mustTime(t, "2099-03-24T14:10:04Z")
	task := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Future comment",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments: []core.Comment{{
			ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
			Author:    "Tony",
			Message:   "Written before the clock changed",
			CreatedAt: future,
		}},
		CreatedAt: created,
		UpdatedAt: future,
	}
	writeTaskFile(t, root, task)

	updated, err := p.AddComment(task.ShortID, "Tony", "Written after the clock changed")
	if err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}
	if len(updated.Comments) != 2 {
		t.Fatalf("comment count = %d, want 2", len(updated.Comments))
	}
	latest := updated.Comments[1].CreatedAt
	if !latest.After(future) {
		t.Fatalf("new comment createdAt = %v, want after previous timestamp %v", latest, future)
	}
	if !updated.UpdatedAt.Equal(latest) {
		t.Fatalf("updatedAt = %v, want new comment timestamp %v", updated.UpdatedAt, latest)
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

func TestUpdateTaskPreservesAndClearsGitAndWorktreeMetadata(t *testing.T) {
	p := newProvider(t)
	gitRepo := filepath.Join(t.TempDir(), "repository")
	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	task, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Git-aware task",
		GitRepo:      gitRepo,
		GitBranch:    "feature/original",
		WorktreeName: "original",
		WorktreeDir:  worktreeDir,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	preserved, err := p.UpdateTask(task.ShortID, core.UpdateTaskInput{
		Description: core.OptionalString{Set: true, Value: "metadata unchanged"},
	})
	if err != nil {
		t.Fatalf("UpdateTask(preserve) error = %v", err)
	}
	if preserved.GitRepo != gitRepo ||
		preserved.GitBranch != "feature/original" ||
		preserved.WorktreeName != "original" ||
		preserved.WorktreeDir != worktreeDir {
		t.Fatalf("preserved metadata = %#v", preserved)
	}

	updatedRepo := filepath.Join(t.TempDir(), "updated-repository")
	updated, err := p.UpdateTask(task.ShortID, core.UpdateTaskInput{
		GitRepo:      core.OptionalString{Set: true, Value: "  " + updatedRepo + "  "},
		GitBranch:    core.OptionalString{Set: true, Value: ""},
		WorktreeName: core.OptionalString{Set: true, Value: "  updated  "},
		WorktreeDir:  core.OptionalString{Set: true, Value: ""},
	})
	if err != nil {
		t.Fatalf("UpdateTask(change and clear) error = %v", err)
	}
	if updated.GitRepo != updatedRepo {
		t.Fatalf("gitRepo = %q, want %q", updated.GitRepo, updatedRepo)
	}
	if updated.GitBranch != "" {
		t.Fatalf("gitBranch = %q, want empty", updated.GitBranch)
	}
	if updated.WorktreeName != "updated" {
		t.Fatalf("worktreeName = %q, want updated", updated.WorktreeName)
	}
	if updated.WorktreeDir != "" {
		t.Fatalf("worktreeDir = %q, want empty", updated.WorktreeDir)
	}
}

func TestUpdateTaskCanClearSuggestedModel(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
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
	p, err := flatfile.New(root, nil)
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
	if _, err := flatfile.New(root, nil); err != nil {
		t.Fatalf("flatfile.New() setup error = %v", err)
	}

	task := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Legacy file",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments: []core.Comment{{
			ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
			Author:    "",
			Message:   "Legacy anonymous comment",
			CreatedAt: mustTime(t, "2026-03-24T14:10:04Z"),
		}},
		CreatedAt: mustTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt: mustTime(t, "2026-03-24T14:10:04Z"),
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

	p, err := flatfile.New(root, nil)
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

func TestNewMigratesScopedLegacyUUIDFilename(t *testing.T) {
	root := t.TempDir()
	if _, err := flatfile.New(root, nil); err != nil {
		t.Fatalf("flatfile.New() setup error = %v", err)
	}
	scope := core.NewBranchScope("feature/scoped-filename-migration")
	task := canonicalDiskTask(t, "35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-"+scope.BranchID+"-0001")
	legacyPath := filepath.Join(root, string(core.StatusTodo), task.ID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(legacyPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write legacy task error = %v", err)
	}

	p, err := flatfile.New(root, scope)
	if err != nil {
		t.Fatalf("flatfile.New() scoped migration error = %v", err)
	}
	scopedPath := filepath.Join(root, string(core.StatusTodo), task.ShortID+".json")
	if _, err := os.Stat(scopedPath); err != nil {
		t.Fatalf("scoped shortID filename missing after migration: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy UUID path removed, got err=%v", err)
	}
	got, err := p.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() for migrated scoped task error = %v", err)
	}
	if got.ID != task.ID || got.ShortID != task.ShortID {
		t.Fatalf("migrated task = %#v, want ID %s and short ID %s", got.Task, task.ID, task.ShortID)
	}
}

func TestScopedStatusMoveKeepsLegacyShortIDFilename(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/scoped-status-move")
	scoped, err := flatfile.New(root, scope)
	if err != nil {
		t.Fatalf("flatfile.New(scoped) error = %v", err)
	}
	scopedTask, err := scoped.CreateTask(core.CreateTaskInput{Title: "Scoped task"})
	if err != nil {
		t.Fatalf("CreateTask(scoped) error = %v", err)
	}
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New(legacy) error = %v", err)
	}
	legacyTask, err := legacy.CreateTask(core.CreateTaskInput{Title: "Legacy task"})
	if err != nil {
		t.Fatalf("CreateTask(legacy) error = %v", err)
	}

	if _, err := scoped.UpdateTaskStatus(scopedTask.ShortID, core.StatusInProgress, "Codex"); err != nil {
		t.Fatalf("UpdateTaskStatus(scoped) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(core.StatusInProgress), scopedTask.ShortID+".json")); err != nil {
		t.Fatalf("scoped target filename missing after status move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), scopedTask.ShortID+".json")); !os.IsNotExist(err) {
		t.Fatalf("scoped source filename remains after status move, err=%v", err)
	}
	legacyPath := filepath.Join(root, string(core.StatusTodo), legacyTask.ShortID+".json")
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("status move removed unrelated legacy filename: %v", err)
	}
}

func TestLegacyIndexContinuationAndNamedBranchPreservesLegacyTasks(t *testing.T) {
	root := t.TempDir()
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New(legacy) error = %v", err)
	}

	existing, err := legacy.CreateTask(core.CreateTaskInput{Title: "Existing legacy task"})
	if err != nil {
		t.Fatalf("CreateTask(existing legacy) error = %v", err)
	}
	if existing.ShortID != "wtp-0001" {
		t.Fatalf("existing legacy short ID = %q, want wtp-0001", existing.ShortID)
	}
	legacyIndexPath := filepath.Join(root, "meta", "index.json")
	legacyIndexBefore, err := os.ReadFile(legacyIndexPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy index before scope) error = %v", err)
	}
	legacyTaskPath := filepath.Join(root, string(core.StatusTodo), existing.ShortID+".json")
	legacyTaskBefore, err := os.ReadFile(legacyTaskPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy task before scope) error = %v", err)
	}

	scope := core.NewBranchScope("feature/legacy-compatibility")
	named, err := flatfile.New(root, scope)
	if err != nil {
		t.Fatalf("flatfile.New(named branch) error = %v", err)
	}
	legacyIndexAfterOpen, err := os.ReadFile(legacyIndexPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy index after scope open) error = %v", err)
	}
	if string(legacyIndexAfterOpen) != string(legacyIndexBefore) {
		t.Fatalf("legacy index changed while opening named branch: got %q, want %q", legacyIndexAfterOpen, legacyIndexBefore)
	}
	legacyTaskAfterOpen, err := os.ReadFile(legacyTaskPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy task after scope open) error = %v", err)
	}
	if string(legacyTaskAfterOpen) != string(legacyTaskBefore) {
		t.Fatalf("legacy task changed while opening named branch: got %q, want %q", legacyTaskAfterOpen, legacyTaskBefore)
	}

	read, err := named.GetTask(existing.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(legacy from named branch) error = %v", err)
	}
	if read.ShortID != existing.ShortID {
		t.Fatalf("named branch read short ID = %q, want %q", read.ShortID, existing.ShortID)
	}
	updated, err := named.UpdateTask(existing.ShortID, core.UpdateTaskInput{
		Description: core.OptionalString{Set: true, Value: "updated by named branch"},
	})
	if err != nil {
		t.Fatalf("UpdateTask(legacy from named branch) error = %v", err)
	}
	if updated.ShortID != existing.ShortID || updated.Description != "updated by named branch" {
		t.Fatalf("named branch legacy update = %#v, want unchanged short ID and updated description", updated.Task)
	}
	commented, err := named.AddComment(existing.ShortID, "branch-agent", "legacy task remains addressable")
	if err != nil {
		t.Fatalf("AddComment(legacy from named branch) error = %v", err)
	}
	if commented.ShortID != existing.ShortID || len(commented.Comments) != 1 {
		t.Fatalf("named branch legacy comment result = %#v, want existing ID and one comment", commented.Task)
	}

	branchTask, err := named.CreateTask(core.CreateTaskInput{Title: "Named branch task"})
	if err != nil {
		t.Fatalf("CreateTask(named branch) error = %v", err)
	}
	if want := "wtp-" + scope.BranchID + "-0001"; branchTask.ShortID != want {
		t.Fatalf("named branch short ID = %q, want %q", branchTask.ShortID, want)
	}
	legacyIndexAfterBranchCreate, err := os.ReadFile(legacyIndexPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy index after branch create) error = %v", err)
	}
	if string(legacyIndexAfterBranchCreate) != string(legacyIndexBefore) {
		t.Fatalf("legacy index changed during named branch create: got %q, want %q", legacyIndexAfterBranchCreate, legacyIndexBefore)
	}

	continued, err := legacy.CreateTask(core.CreateTaskInput{Title: "Continued legacy task"})
	if err != nil {
		t.Fatalf("CreateTask(continued legacy) error = %v", err)
	}
	if continued.ShortID != "wtp-0002" {
		t.Fatalf("continued legacy short ID = %q, want wtp-0002", continued.ShortID)
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

func TestNamedBranchScopesAllocateIndependentShortIDsAndIndexes(t *testing.T) {
	root := t.TempDir()
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New(legacy) error = %v", err)
	}
	legacyIndexPath := filepath.Join(root, "meta", "index.json")
	legacyIndex, err := os.ReadFile(legacyIndexPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy index) error = %v", err)
	}
	if _, err := legacy.CreateTask(core.CreateTaskInput{Title: "Existing legacy task"}); err != nil {
		t.Fatalf("CreateTask(legacy) error = %v", err)
	}
	legacyIndex, err = os.ReadFile(legacyIndexPath)
	if err != nil {
		t.Fatalf("ReadFile(updated legacy index) error = %v", err)
	}

	firstScope := core.NewBranchScope("feature/first")
	secondScope := core.NewBranchScope("feature/second")
	first, err := flatfile.New(root, firstScope)
	if err != nil {
		t.Fatalf("flatfile.New(first scope) error = %v", err)
	}
	second, err := flatfile.New(root, secondScope)
	if err != nil {
		t.Fatalf("flatfile.New(second scope) error = %v", err)
	}

	firstOne, err := first.CreateTask(core.CreateTaskInput{Title: "First branch task one"})
	if err != nil {
		t.Fatalf("CreateTask(first one) error = %v", err)
	}
	secondOne, err := second.CreateTask(core.CreateTaskInput{Title: "Second branch task one"})
	if err != nil {
		t.Fatalf("CreateTask(second one) error = %v", err)
	}
	firstTwo, err := first.CreateTask(core.CreateTaskInput{Title: "First branch task two"})
	if err != nil {
		t.Fatalf("CreateTask(first two) error = %v", err)
	}

	if got, want := firstOne.ShortID, "wtp-"+firstScope.BranchID+"-0001"; got != want {
		t.Fatalf("first task short ID = %q, want %q", got, want)
	}
	if got, want := firstTwo.ShortID, "wtp-"+firstScope.BranchID+"-0002"; got != want {
		t.Fatalf("second first-scope task short ID = %q, want %q", got, want)
	}
	if got, want := secondOne.ShortID, "wtp-"+secondScope.BranchID+"-0001"; got != want {
		t.Fatalf("second-scope task short ID = %q, want %q", got, want)
	}
	for _, task := range []core.TaskView{firstOne, firstTwo, secondOne} {
		if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json")); err != nil {
			t.Fatalf("Stat(%s filename) error = %v", task.ShortID, err)
		}
	}

	assertScopedIndex := func(scope *core.BranchScope, next int) {
		t.Helper()
		path := filepath.Join(root, "meta", "index-"+scope.BranchID+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		var index struct {
			Branch string `json:"branch"`
			Next   int    `json:"next"`
		}
		if err := json.Unmarshal(data, &index); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", path, err)
		}
		if index.Branch != scope.Branch || index.Next != next {
			t.Fatalf("index %s = %#v, want branch %q next %d", path, index, scope.Branch, next)
		}
	}
	assertScopedIndex(firstScope, 3)
	assertScopedIndex(secondScope, 2)

	afterLegacyIndex, err := os.ReadFile(legacyIndexPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy index after scoped creates) error = %v", err)
	}
	if string(afterLegacyIndex) != string(legacyIndex) {
		t.Fatalf("legacy index changed during scoped creates: got %q, want %q", afterLegacyIndex, legacyIndex)
	}
}

func TestConcurrentCreateInNamedBranchScopeAllocatesUniqueShortIDs(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/concurrent")
	providers := make([]provider.Provider, 2)
	for i := range providers {
		p, err := flatfile.New(root, scope)
		if err != nil {
			t.Fatalf("flatfile.New(%d) error = %v", i, err)
		}
		providers[i] = p
	}

	const count = 8
	shortIDs := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			task, err := providers[index%len(providers)].CreateTask(core.CreateTaskInput{Title: "Concurrent scoped task"})
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
	for shortID := range shortIDs {
		got = append(got, shortID)
	}
	slices.Sort(got)
	want := make([]string, count)
	for i := range want {
		want[i] = fmt.Sprintf("wtp-%s-%04d", scope.BranchID, i+1)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("allocated scoped short IDs = %v, want %v", got, want)
	}

	data, err := os.ReadFile(filepath.Join(root, "meta", "index-"+scope.BranchID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(scoped index) error = %v", err)
	}
	var index struct {
		Branch string `json:"branch"`
		Next   int    `json:"next"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("Unmarshal(scoped index) error = %v", err)
	}
	if index.Branch != scope.Branch || index.Next != count+1 {
		t.Fatalf("scoped index = %#v, want branch %q next %d", index, scope.Branch, count+1)
	}
}

func TestNamedBranchScopeRejectsIndexTokenCollision(t *testing.T) {
	root := t.TempDir()
	firstScope := core.NewBranchScope("feature/first")
	if _, err := flatfile.New(root, firstScope); err != nil {
		t.Fatalf("flatfile.New(first scope) error = %v", err)
	}

	collidingScope := &core.BranchScope{
		Branch:   "feature/other",
		BranchID: firstScope.BranchID,
	}
	_, err := flatfile.New(root, collidingScope)
	if err == nil || !strings.Contains(err.Error(), "belongs to branch \"feature/first\", not \"feature/other\"") {
		t.Fatalf("flatfile.New(colliding scope) error = %v, want actionable branch collision", err)
	}
}

func TestAutomaticSelectionUsesBranchScopeTiers(t *testing.T) {
	t.Run("peek and batch exclude foreign scopes", func(t *testing.T) {
		fixture := newBranchSelectionFixture(t)

		peeked, err := fixture.current.PeekNextTask("Tony")
		if err != nil {
			t.Fatalf("PeekNextTask() error = %v", err)
		}
		if peeked.ID != fixture.currentTask.ID {
			t.Fatalf("PeekNextTask() = %s, want current-scope task %s", peeked.ID, fixture.currentTask.ID)
		}

		batch, err := fixture.current.PeekNextTasks("Tony", 3)
		if err != nil {
			t.Fatalf("PeekNextTasks() error = %v", err)
		}
		if got, want := taskViewIDs(batch), []string{fixture.currentTask.ID, fixture.legacyTask.ID}; !slices.Equal(got, want) {
			t.Fatalf("PeekNextTasks() IDs = %v, want %v", got, want)
		}
	})

	t.Run("get claims the current scope before a better legacy task", func(t *testing.T) {
		fixture := newBranchSelectionFixture(t)

		claimed, err := fixture.current.GetNextTask("Tony")
		if err != nil {
			t.Fatalf("GetNextTask() error = %v", err)
		}
		if claimed.ID != fixture.currentTask.ID {
			t.Fatalf("GetNextTask() = %s, want current-scope task %s", claimed.ID, fixture.currentTask.ID)
		}
		if claimed.Status != core.StatusInProgress || claimed.Assignee != "Tony" {
			t.Fatalf("claimed task = status %s assignee %q, want inProgress for Tony", claimed.Status, claimed.Assignee)
		}
	})
}

func TestAutomaticSelectionScopeControlsReadinessClaimability(t *testing.T) {
	fixture := newBranchSelectionFixture(t)

	for _, test := range []struct {
		name      string
		provider  *flatfile.Provider
		task      core.TaskView
		claimable bool
	}{
		{name: "current task on current branch", provider: fixture.current, task: fixture.currentTask, claimable: true},
		{name: "legacy task on current branch", provider: fixture.current, task: fixture.legacyTask, claimable: true},
		{name: "foreign task on current branch", provider: fixture.current, task: fixture.foreignTask, claimable: false},
		{name: "current task without branch", provider: fixture.legacy, task: fixture.currentTask, claimable: false},
		{name: "foreign task without branch", provider: fixture.legacy, task: fixture.foreignTask, claimable: false},
		{name: "legacy task without branch", provider: fixture.legacy, task: fixture.legacyTask, claimable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.provider.GetTask(test.task.ShortID, "Tony")
			if err != nil {
				t.Fatalf("GetTask() error = %v", err)
			}
			if got.Readiness.Claimable != test.claimable {
				t.Fatalf("Readiness.Claimable = %t, want %t", got.Readiness.Claimable, test.claimable)
			}
		})
	}

	peeked, err := fixture.legacy.PeekNextTask("Tony")
	if err != nil {
		t.Fatalf("PeekNextTask() without branch scope error = %v", err)
	}
	if peeked.ID != fixture.legacyTask.ID {
		t.Fatalf("PeekNextTask() without branch scope = %s, want legacy task %s", peeked.ID, fixture.legacyTask.ID)
	}
}

func TestScopedDependenciesGateReadinessAndBatchSelection(t *testing.T) {
	root := t.TempDir()
	current, err := flatfile.New(root, core.NewBranchScope("feature/current"))
	if err != nil {
		t.Fatalf("flatfile.New(current) error = %v", err)
	}
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New(legacy) error = %v", err)
	}
	foreign, err := flatfile.New(root, core.NewBranchScope("feature/foreign"))
	if err != nil {
		t.Fatalf("flatfile.New(foreign) error = %v", err)
	}

	foreignDependency, err := foreign.CreateTask(core.CreateTaskInput{
		Title:    "Foreign dependency",
		Assignee: "Tony",
	})
	if err != nil {
		t.Fatalf("CreateTask(foreign dependency) error = %v", err)
	}
	foreignReady, err := foreign.CreateTask(core.CreateTaskInput{
		Title:    "Foreign ready task",
		Assignee: "Tony",
		Priority: core.PriorityUrgent,
	})
	if err != nil {
		t.Fatalf("CreateTask(foreign ready task) error = %v", err)
	}
	currentCandidate, err := current.CreateTask(core.CreateTaskInput{
		Title:        "Current candidate",
		Assignee:     "Tony",
		Priority:     core.PriorityLow,
		Dependencies: []string{foreignDependency.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(current candidate) error = %v", err)
	}
	legacyCandidate, err := legacy.CreateTask(core.CreateTaskInput{
		Title:        "Legacy candidate",
		Assignee:     "Tony",
		Priority:     core.PriorityUrgent,
		Dependencies: []string{foreignDependency.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(legacy candidate) error = %v", err)
	}

	for _, test := range []struct {
		name string
		p    *flatfile.Provider
		task core.TaskView
	}{
		{name: "current candidate", p: current, task: currentCandidate},
		{name: "legacy candidate", p: legacy, task: legacyCandidate},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.p.GetTask(test.task.ShortID, "Tony")
			if err != nil {
				t.Fatalf("GetTask() error = %v", err)
			}
			if !got.Readiness.Blocked || got.Readiness.Claimable {
				t.Fatalf("readiness = %#v, want blocked and unclaimable", got.Readiness)
			}
			if got.Readiness.DependencyCount != 1 {
				t.Fatalf("dependencyCount = %d, want 1", got.Readiness.DependencyCount)
			}
			wantReason := "unresolved dependencies: " + foreignDependency.ShortID + " (Foreign dependency)"
			if got.Readiness.BlockedReason != wantReason {
				t.Fatalf("blockedReason = %q, want %q", got.Readiness.BlockedReason, wantReason)
			}

			filtered, err := test.p.GetTask(test.task.ShortID, "Alice")
			if err != nil {
				t.Fatalf("GetTask(Alice) error = %v", err)
			}
			if filtered.Readiness.Claimable {
				t.Fatal("agent-filtered readiness is claimable for Alice, want false")
			}
		})
	}

	dependencyView, err := current.GetTask(foreignDependency.ID, "Tony")
	if err != nil {
		t.Fatalf("GetTask(foreign dependency) error = %v", err)
	}
	if got, want := dependencyView.Readiness.ReverseDependencyCount, 2; got != want {
		t.Fatalf("foreign dependency reverseDependencyCount = %d, want %d", got, want)
	}

	if _, err := current.PeekNextTasks("Tony", 3); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("PeekNextTasks() with only foreign eligible work error = %v, want no eligible task", err)
	}
	if _, err := current.PeekNextTask("Tony"); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("PeekNextTask() with only foreign eligible work error = %v, want no eligible task", err)
	}

	storedCurrent := readStoredTask(t, root, currentCandidate)
	storedLegacy := readStoredTask(t, root, legacyCandidate)
	for _, stored := range []core.Task{storedCurrent, storedLegacy} {
		if !slices.Equal(stored.Dependencies, []string{foreignDependency.ID}) {
			t.Fatalf("stored dependencies for %s = %v, want canonical UUID [%s]", stored.ShortID, stored.Dependencies, foreignDependency.ID)
		}
	}

	if _, err := current.UpdateTaskStatus(foreignDependency.ID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("UpdateTaskStatus(foreign dependency, start) error = %v", err)
	}
	if _, err := current.UpdateTaskStatus(foreignDependency.ID, core.StatusDone, "Tony"); err != nil {
		t.Fatalf("UpdateTaskStatus(foreign dependency, done) error = %v", err)
	}

	for _, test := range []struct {
		name string
		p    *flatfile.Provider
		task core.TaskView
	}{
		{name: "current candidate", p: current, task: currentCandidate},
		{name: "legacy candidate", p: legacy, task: legacyCandidate},
	} {
		t.Run(test.name+" after foreign completion", func(t *testing.T) {
			got, err := test.p.GetTask(test.task.ShortID, "Tony")
			if err != nil {
				t.Fatalf("GetTask() error = %v", err)
			}
			if got.Readiness.Blocked || !got.Readiness.Claimable {
				t.Fatalf("readiness = %#v, want unblocked and claimable", got.Readiness)
			}
			filtered, err := test.p.GetTask(test.task.ShortID, "Alice")
			if err != nil {
				t.Fatalf("GetTask(Alice) error = %v", err)
			}
			if filtered.Readiness.Claimable {
				t.Fatal("agent-filtered readiness is claimable for Alice, want false")
			}
		})
	}

	batch, err := current.PeekNextTasks("Tony", 3)
	if err != nil {
		t.Fatalf("PeekNextTasks(Tony, 3) after foreign completion error = %v", err)
	}
	if got, want := taskViewIDs(batch), []string{currentCandidate.ID, legacyCandidate.ID}; !slices.Equal(got, want) {
		t.Fatalf("PeekNextTasks(Tony, 3) IDs = %v, want current then legacy only %v", got, want)
	}
	for _, task := range batch {
		if task.ID == foreignReady.ID {
			t.Fatal("batch returned foreign task, but foreign work must never be padded into readiness")
		}
		if !task.Readiness.Claimable {
			t.Fatalf("batch task %s is not claimable for Tony", task.ShortID)
		}
	}
}

func TestForeignScopedTasksRemainExplicitlyAddressableByShortIDAndUUID(t *testing.T) {
	root := t.TempDir()
	current, err := flatfile.New(root, core.NewBranchScope("feature/current"))
	if err != nil {
		t.Fatalf("flatfile.New(current) error = %v", err)
	}
	foreign, err := flatfile.New(root, core.NewBranchScope("feature/foreign"))
	if err != nil {
		t.Fatalf("flatfile.New(foreign) error = %v", err)
	}

	dependency, err := foreign.CreateTask(core.CreateTaskInput{Title: "Foreign dependency", Assignee: "Tony"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}
	target, err := foreign.CreateTask(core.CreateTaskInput{
		Title:        "Foreign explicit target",
		Description:  "original description",
		Assignee:     "Tony",
		Dependencies: []string{dependency.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(target) error = %v", err)
	}

	listed, err := current.ListTasks(provider.TaskFilter{Agent: "Tony"})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListTasks() count = %d, want 2 foreign tasks", len(listed))
	}
	shown, err := current.GetTask(target.ShortID, "Tony")
	if err != nil {
		t.Fatalf("GetTask(short ID) error = %v", err)
	}
	if shown.ID != target.ID || shown.Readiness.Claimable || !shown.Readiness.Blocked {
		t.Fatalf("foreign blocked readiness = %#v, want claimable false and blocked true", shown.Readiness)
	}
	if _, err := current.PeekNextTask("Tony"); !errors.Is(err, provider.ErrNoEligibleTask) {
		t.Fatalf("PeekNextTask() error = %v, want no eligible task", err)
	}

	if _, err := current.UpdateTaskStatus(target.ShortID, core.StatusInProgress, "Tony"); err == nil || !strings.Contains(err.Error(), "unresolved dependencies") {
		t.Fatalf("blocked foreign start error = %v, want unresolved dependency error", err)
	}
	if _, err := current.UpdateTaskStatus(dependency.ID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("start dependency by UUID error = %v", err)
	}
	if _, err := current.UpdateTaskStatus(dependency.ShortID, core.StatusDone, "Tony"); err != nil {
		t.Fatalf("complete dependency by short ID error = %v", err)
	}

	updated, err := current.UpdateTask(target.ID, core.UpdateTaskInput{
		Description: core.OptionalString{Set: true, Value: "updated from the current branch"},
	})
	if err != nil {
		t.Fatalf("UpdateTask(UUID) error = %v", err)
	}
	if updated.Description != "updated from the current branch" {
		t.Fatalf("updated description = %q, want current-branch update", updated.Description)
	}
	commented, err := current.AddComment(target.ShortID, "Tony", "commented from the current branch")
	if err != nil {
		t.Fatalf("AddComment(short ID) error = %v", err)
	}
	if len(commented.Comments) != 1 || commented.Comments[0].Message != "commented from the current branch" {
		t.Fatalf("foreign comments = %#v, want one explicit comment", commented.Comments)
	}

	started, err := current.UpdateTaskStatus(target.ID, core.StatusInProgress, "Tony")
	if err != nil {
		t.Fatalf("start foreign task by UUID error = %v", err)
	}
	if started.Status != core.StatusInProgress || started.Readiness.Claimable {
		t.Fatalf("started foreign task = status %s readiness %#v, want inProgress and unclaimable", started.Status, started.Readiness)
	}
	paused, err := current.UpdateTaskStatus(target.ShortID, core.StatusPaused, "Tony")
	if err != nil {
		t.Fatalf("pause foreign task by short ID error = %v", err)
	}
	if paused.Status != core.StatusPaused || paused.Readiness.Claimable {
		t.Fatalf("paused foreign task = status %s readiness %#v, want paused and unclaimable", paused.Status, paused.Readiness)
	}
	restarted, err := current.UpdateTaskStatus(target.ShortID, core.StatusInProgress, "Tony")
	if err != nil {
		t.Fatalf("restart foreign task by short ID error = %v", err)
	}
	if restarted.Status != core.StatusInProgress {
		t.Fatalf("restarted foreign task status = %s, want inProgress", restarted.Status)
	}
	done, err := current.UpdateTaskStatus(target.ID, core.StatusDone, "Tony")
	if err != nil {
		t.Fatalf("complete foreign task by UUID error = %v", err)
	}
	if done.Status != core.StatusDone || len(done.Comments) != 1 {
		t.Fatalf("completed foreign task = %#v, want done with comment", done.Task)
	}

	for _, identifier := range []string{"wtp-9999", "00000000-0000-4000-8000-000000000099"} {
		if _, err := current.GetTask(identifier, "Tony"); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("GetTask(%q) error = %v, want unknown identifier error", identifier, err)
		}
	}
}

func TestAutomaticSelectionPreservesOrderWithinBranchTier(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/current")
	current, err := flatfile.New(root, scope)
	if err != nil {
		t.Fatalf("flatfile.New(current) error = %v", err)
	}
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New(legacy) error = %v", err)
	}

	assignedLow, err := current.CreateTask(core.CreateTaskInput{Title: "Current assigned low", Assignee: "Tony", Priority: core.PriorityLow})
	if err != nil {
		t.Fatalf("CreateTask(assigned low) error = %v", err)
	}
	assignedHigh, err := current.CreateTask(core.CreateTaskInput{Title: "Current assigned high", Assignee: "Tony", Priority: core.PriorityHigh})
	if err != nil {
		t.Fatalf("CreateTask(assigned high) error = %v", err)
	}
	assignedPaused, err := current.CreateTask(core.CreateTaskInput{Title: "Current assigned paused", Assignee: "Tony", Priority: core.PriorityLow})
	if err != nil {
		t.Fatalf("CreateTask(assigned paused) error = %v", err)
	}
	if _, err := current.UpdateTaskStatus(assignedPaused.ShortID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("start assigned paused: %v", err)
	}
	if _, err := current.UpdateTaskStatus(assignedPaused.ShortID, core.StatusPaused, "Tony"); err != nil {
		t.Fatalf("pause assigned paused: %v", err)
	}
	unassignedPaused, err := current.CreateTask(core.CreateTaskInput{Title: "Current unassigned paused", Priority: core.PriorityUrgent})
	if err != nil {
		t.Fatalf("CreateTask(unassigned paused) error = %v", err)
	}
	if _, err := current.UpdateTaskStatus(unassignedPaused.ShortID, core.StatusInProgress, ""); err != nil {
		t.Fatalf("start unassigned paused: %v", err)
	}
	if _, err := current.UpdateTaskStatus(unassignedPaused.ShortID, core.StatusPaused, ""); err != nil {
		t.Fatalf("pause unassigned paused: %v", err)
	}
	legacyPaused, err := legacy.CreateTask(core.CreateTaskInput{Title: "Legacy assigned urgent", Assignee: "Tony", Priority: core.PriorityUrgent})
	if err != nil {
		t.Fatalf("CreateTask(legacy paused) error = %v", err)
	}
	if _, err := legacy.UpdateTaskStatus(legacyPaused.ShortID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("start legacy paused: %v", err)
	}
	if _, err := legacy.UpdateTaskStatus(legacyPaused.ShortID, core.StatusPaused, "Tony"); err != nil {
		t.Fatalf("pause legacy paused: %v", err)
	}

	got, err := current.PeekNextTasks("Tony", 5)
	if err != nil {
		t.Fatalf("PeekNextTasks() error = %v", err)
	}
	want := []string{assignedPaused.ID, assignedHigh.ID, assignedLow.ID, unassignedPaused.ID, legacyPaused.ID}
	if ids := taskViewIDs(got); !slices.Equal(ids, want) {
		t.Fatalf("PeekNextTasks() IDs = %v, want %v", ids, want)
	}
}

type branchSelectionFixture struct {
	current     *flatfile.Provider
	legacy      *flatfile.Provider
	currentTask core.TaskView
	legacyTask  core.TaskView
	foreignTask core.TaskView
}

func newBranchSelectionFixture(t *testing.T) branchSelectionFixture {
	t.Helper()
	root := t.TempDir()
	current, err := flatfile.New(root, core.NewBranchScope("feature/current"))
	if err != nil {
		t.Fatalf("flatfile.New(current) error = %v", err)
	}
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New(legacy) error = %v", err)
	}
	foreign, err := flatfile.New(root, core.NewBranchScope("feature/foreign"))
	if err != nil {
		t.Fatalf("flatfile.New(foreign) error = %v", err)
	}

	currentTask, err := current.CreateTask(core.CreateTaskInput{Title: "Current unassigned low", Priority: core.PriorityLow})
	if err != nil {
		t.Fatalf("CreateTask(current) error = %v", err)
	}
	legacyTask, err := legacy.CreateTask(core.CreateTaskInput{Title: "Legacy assigned urgent", Assignee: "Tony", Priority: core.PriorityUrgent})
	if err != nil {
		t.Fatalf("CreateTask(legacy) error = %v", err)
	}
	if _, err := legacy.UpdateTaskStatus(legacyTask.ShortID, core.StatusInProgress, "Tony"); err != nil {
		t.Fatalf("start legacy: %v", err)
	}
	if _, err := legacy.UpdateTaskStatus(legacyTask.ShortID, core.StatusPaused, "Tony"); err != nil {
		t.Fatalf("pause legacy: %v", err)
	}
	foreignTask, err := foreign.CreateTask(core.CreateTaskInput{Title: "Foreign assigned urgent", Assignee: "Tony", Priority: core.PriorityUrgent})
	if err != nil {
		t.Fatalf("CreateTask(foreign) error = %v", err)
	}

	return branchSelectionFixture{
		current:     current,
		legacy:      legacy,
		currentTask: currentTask,
		legacyTask:  legacyTask,
		foreignTask: foreignTask,
	}
}

func taskViewIDs(tasks []core.TaskView) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func readStoredTask(t *testing.T, root string, task core.TaskView) core.Task {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", task.ShortID, err)
	}
	var stored core.Task
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", task.ShortID, err)
	}
	return stored
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
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}

	task := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Broken dependency",
		Status:       core.StatusTodo,
		Dependencies: []string{"7f13f5e2-6d9d-4630-84e1-7aef10c637e4"},
		Comments:     []core.Comment{},
		CreatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustTime(t, "2026-03-24T14:10:04Z"),
	}
	writeTaskFile(t, root, task)

	_, err = p.ListTasks(provider.TaskFilter{})
	if err == nil {
		t.Fatal("expected load to fail for missing dependency")
	}
	if !contains(err.Error(), `dependency "7f13f5e2-6d9d-4630-84e1-7aef10c637e4" does not exist`) {
		t.Fatalf("ListTasks() error = %v", err)
	}
}

func TestLoadTasksRejectsCyclicDependencyGraphOnDisk(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
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

func TestLoadTasksRejectsCanonicalCorruption(t *testing.T) {
	created := mustTime(t, "2026-03-24T14:10:04Z")
	commentTime := created.Add(time.Minute)
	validComment := core.Comment{
		ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
		Author:    "Tony",
		Message:   "Implemented parser",
		CreatedAt: commentTime,
	}
	tests := []struct {
		name   string
		mutate func(*core.Task)
		want   string
	}{
		{name: "invalid task UUID", mutate: func(task *core.Task) { task.ID = "task-1" }, want: "canonical lowercase UUID"},
		{name: "invalid short ID", mutate: func(task *core.Task) { task.ShortID = "wtp-1" }, want: "must match wtp-NNNN"},
		{name: "invalid comment", mutate: func(task *core.Task) { task.Comments[0].Message = "" }, want: "comment 0 message is required"},
		{name: "comment before created", mutate: func(task *core.Task) { task.Comments[0].CreatedAt = created.Add(-time.Second) }, want: "between task createdAt and updatedAt"},
		{name: "status timestamp mismatch", mutate: func(task *core.Task) { task.Status = core.StatusDone }, want: "done task requires startedAt and completedAt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			p, err := flatfile.New(root, nil)
			if err != nil {
				t.Fatalf("flatfile.New() error = %v", err)
			}
			task := core.Task{
				ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
				ShortID:      "wtp-0001",
				Title:        "Corrupt task",
				Status:       core.StatusTodo,
				Dependencies: []string{},
				Comments:     []core.Comment{validComment},
				CreatedAt:    created,
				UpdatedAt:    commentTime,
			}
			test.mutate(&task)
			writeTaskFile(t, root, task)

			_, err = p.ListTasks(provider.TaskFilter{})
			if err == nil || !contains(err.Error(), test.want) {
				t.Fatalf("ListTasks() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNewRepairsUpdatedAtBehindCommentAndRecordsAuditComment(t *testing.T) {
	root := t.TempDir()
	if _, err := flatfile.New(root, nil); err != nil {
		t.Fatalf("flatfile.New() setup error = %v", err)
	}
	created := mustTime(t, "2026-03-24T14:10:04Z")
	commentTime := created.Add(time.Minute)
	task := core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Manually edited task",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments: []core.Comment{{
			ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
			Author:    "Tony",
			Message:   "Manually added without updating the task",
			CreatedAt: commentTime,
		}},
		CreatedAt: created,
		UpdatedAt: created,
	}
	writeTaskFile(t, root, task)

	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() repair error = %v", err)
	}
	got, err := p.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(got.Comments) != 2 {
		t.Fatalf("comments = %#v, want original and repair audit", got.Comments)
	}
	if got.Comments[0].Message != task.Comments[0].Message {
		t.Fatalf("original comment changed: got %#v, want %#v", got.Comments[0], task.Comments[0])
	}
	repair := got.Comments[1]
	if repair.Author != "wtp" || !strings.Contains(repair.Message, "automatically repaired task metadata") {
		t.Fatalf("repair audit comment = %#v", repair)
	}
	if !got.UpdatedAt.Equal(repair.CreatedAt) || !got.UpdatedAt.After(commentTime) {
		t.Fatalf("repaired timestamps: updatedAt=%v repair=%v original comment=%v", got.UpdatedAt, repair.CreatedAt, commentTime)
	}
	if err := got.Task.Validate(); err != nil {
		t.Fatalf("repaired task is invalid: %v", err)
	}
}

func TestListTasksRepairsUpdatedAtBeforeCreatedAt(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task := canonicalDiskTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001")
	task.UpdatedAt = task.CreatedAt.Add(-time.Second)
	writeTaskFile(t, root, task)

	tasks, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() repair error = %v", err)
	}
	if len(tasks) != 1 || len(tasks[0].Comments) != 1 || tasks[0].Comments[0].Author != "wtp" {
		t.Fatalf("repaired tasks = %#v", tasks)
	}
	if !tasks[0].UpdatedAt.After(tasks[0].CreatedAt) {
		t.Fatalf("updatedAt = %v, want after createdAt %v", tasks[0].UpdatedAt, tasks[0].CreatedAt)
	}
}

func TestLoadTasksRejectsFilenameOutsideShortIDAndUUIDForms(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task := canonicalDiskTask(t, "45c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-abcdef12-0001")
	invalidPath := filepath.Join(root, string(task.Status), "wtp-0001.json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(invalidPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write invalid filename task error = %v", err)
	}

	_, err = p.ListTasks(provider.TaskFilter{})
	if err == nil || !strings.Contains(err.Error(), "must use shortId filename "+task.ShortID+".json") {
		t.Fatalf("ListTasks() error = %v, want filename validation error", err)
	}
}

func TestLoadTasksRejectsDuplicateCanonicalTaskID(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	first := canonicalDiskTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001")
	writeTaskFile(t, root, first)
	duplicatePath := filepath.Join(root, string(first.Status), first.ID+".json")
	duplicateData, err := json.MarshalIndent(first, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(duplicatePath, append(duplicateData, '\n'), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err = p.ListTasks(provider.TaskFilter{})
	if err == nil || !contains(err.Error(), "duplicate canonical task id") {
		t.Fatalf("ListTasks() duplicate canonical ID error = %v", err)
	}
}

func TestLoadTasksRejectsDuplicateShortID(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	first := canonicalDiskTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001")
	second := canonicalDiskTask(t, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a", first.ShortID)
	second.Status = core.StatusInProgress
	second.UpdatedAt = second.CreatedAt.Add(time.Second)
	second.StartedAt = &second.UpdatedAt
	writeTaskFile(t, root, first)
	writeTaskFile(t, root, second)

	_, err = p.ListTasks(provider.TaskFilter{})
	if err == nil || !contains(err.Error(), "is used by both") {
		t.Fatalf("ListTasks() duplicate short ID error = %v", err)
	}
}

func TestNewDoesNotMigrateInvalidLegacyTask(t *testing.T) {
	root := t.TempDir()
	if _, err := flatfile.New(root, nil); err != nil {
		t.Fatalf("flatfile.New() setup error = %v", err)
	}
	valid := canonicalDiskTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001")
	validLegacyPath := filepath.Join(root, string(core.StatusTodo), valid.ID+".json")
	validData, err := json.MarshalIndent(valid, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(valid) error = %v", err)
	}
	if err := os.WriteFile(validLegacyPath, append(validData, '\n'), 0o644); err != nil {
		t.Fatalf("os.WriteFile(valid legacy) error = %v", err)
	}
	task := canonicalDiskTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "invalid-short-id")
	legacyPath := filepath.Join(root, string(core.StatusTodo), task.ID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(legacyPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	if _, err := flatfile.New(root, nil); err == nil || !contains(err.Error(), "invalid task file") {
		t.Fatalf("flatfile.New() invalid legacy error = %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("invalid legacy file was moved or removed: %v", err)
	}
	if _, err := os.Stat(validLegacyPath); err != nil {
		t.Fatalf("valid legacy file was moved before all migrations were validated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), valid.ShortID+".json")); !os.IsNotExist(err) {
		t.Fatalf("valid migration ran before later corruption was detected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(core.StatusTodo), task.ShortID+".json")); !os.IsNotExist(err) {
		t.Fatalf("invalid migration target exists: %v", err)
	}
}

func newProvider(t *testing.T) provider.Provider {
	t.Helper()

	root := t.TempDir()
	p, err := flatfile.New(root, nil)
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

func TestPurgeHandoffsByScopeAndCutoff(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	firstTask, err := p.CreateTask(core.CreateTaskInput{Title: "First task"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	secondTask, err := p.CreateTask(core.CreateTaskInput{Title: "Second task"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}
	cutoff := mustTime(t, "2026-08-09T11:00:00Z")
	writeHandoffCollection(t, root, []core.Handoff{
		testHandoff(t, "00000000-0000-4000-8000-000000000001", "", "2026-08-09T10:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000002", "", "2026-08-09T11:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000003", firstTask.ID, "2026-08-09T10:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000004", firstTask.ID, "2026-08-09T12:00:00Z"),
		testHandoff(t, "00000000-0000-4000-8000-000000000005", secondTask.ID, "2026-08-09T10:00:00Z"),
	})

	result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true, Before: &cutoff})
	if err != nil {
		t.Fatalf("PurgeHandoffs(global cutoff) error = %v", err)
	}
	if result.Purged != 1 {
		t.Fatalf("PurgeHandoffs(global cutoff) purged = %d, want 1", result.Purged)
	}
	assertHandoffIDs(t, p, []string{
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000004",
		"00000000-0000-4000-8000-000000000005",
	})

	result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{Task: firstTask.ShortID})
	if err != nil {
		t.Fatalf("PurgeHandoffs(task) error = %v", err)
	}
	if result.Purged != 2 {
		t.Fatalf("PurgeHandoffs(task) purged = %d, want 2", result.Purged)
	}
	assertHandoffIDs(t, p, []string{
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000005",
	})

	result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{AllScopes: true})
	if err != nil {
		t.Fatalf("PurgeHandoffs(all scopes) error = %v", err)
	}
	if result.Purged != 2 {
		t.Fatalf("PurgeHandoffs(all scopes) purged = %d, want 2", result.Purged)
	}
	assertHandoffIDs(t, p, []string{})
}

func TestAllScopeReadsAndIDPurgesRecoverOrphanTaskHandoffs(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	const (
		globalID     = "00000000-0000-4000-8000-000000000001"
		orphanID     = "00000000-0000-4000-8000-000000000002"
		orphanTaskID = "ec58fe90-eb0f-4c4c-b2bf-4f1178a56c8a"
	)
	writeHandoffCollection(t, root, []core.Handoff{
		testHandoff(t, globalID, "", "2026-08-09T10:00:00Z"),
		testHandoff(t, orphanID, orphanTaskID, "2026-08-09T11:00:00Z"),
	})

	all, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes) error = %v", err)
	}
	if len(all.Handoffs) != 2 {
		t.Fatalf("all-scope handoffs = %#v, want global and orphan records", all.Handoffs)
	}
	var foundOrphan bool
	for _, handoff := range all.Handoffs {
		if handoff.ID == orphanID && handoff.TaskID == orphanTaskID {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Fatalf("all-scope read did not expose orphan task handoff %q", orphanID)
	}

	purged, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: orphanID})
	if err != nil {
		t.Fatalf("PurgeHandoffs(orphan ID) error = %v", err)
	}
	if purged.Purged != 1 {
		t.Fatalf("PurgeHandoffs(orphan ID) purged = %d, want 1", purged.Purged)
	}
	remaining, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs(all scopes after purge) error = %v", err)
	}
	if len(remaining.Handoffs) != 1 || remaining.Handoffs[0].ID != globalID {
		t.Fatalf("handoffs after orphan purge = %#v, want only global record", remaining.Handoffs)
	}
}

func TestPurgeHandoffsByIDAndEmptyScopes(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Scoped task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	const id = "00000000-0000-4000-8000-000000000001"
	writeHandoffCollection(t, root, []core.Handoff{
		testHandoff(t, id, task.ID, "2026-08-09T11:00:00Z"),
	})

	cutoff := mustTime(t, "2026-08-09T11:00:00Z")
	result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: id, Before: &cutoff})
	if err != nil {
		t.Fatalf("PurgeHandoffs(id at cutoff) error = %v", err)
	}
	if result.Purged != 0 {
		t.Fatalf("PurgeHandoffs(id at cutoff) purged = %d, want 0", result.Purged)
	}

	result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true})
	if err != nil {
		t.Fatalf("PurgeHandoffs(empty global scope) error = %v", err)
	}
	if result.Purged != 0 {
		t.Fatalf("PurgeHandoffs(empty global scope) purged = %d, want 0", result.Purged)
	}

	if _, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: "00000000-0000-4000-8000-000000000099"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("PurgeHandoffs(missing id) error = %v, want not found", err)
	}
	assertHandoffIDs(t, p, []string{id})

	cutoff = mustTime(t, "2026-08-09T12:00:00Z")
	result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: id, Before: &cutoff})
	if err != nil {
		t.Fatalf("PurgeHandoffs(id before cutoff) error = %v", err)
	}
	if result.Purged != 1 {
		t.Fatalf("PurgeHandoffs(id before cutoff) purged = %d, want 1", result.Purged)
	}
	assertHandoffIDs(t, p, []string{})
}

func TestPurgeHandoffsRejectsInvalidSelectors(t *testing.T) {
	p := newProvider(t)
	tests := []provider.HandoffPurgeRequest{
		{},
		{Global: true, AllScopes: true},
		{ID: "00000000-0000-4000-8000-000000000001", Global: true},
		{ID: "not-a-uuid"},
		{Task: "missing-task"},
	}
	for _, request := range tests {
		if _, err := p.PurgeHandoffs(request); err == nil {
			t.Fatalf("PurgeHandoffs(%+v) error = nil, want error", request)
		}
	}
}

func TestPurgeHandoffsCoversSelectorsCutoffsAndPreservation(t *testing.T) {
	cutoff := "2026-08-09T11:00:00Z"

	t.Run("exact ID", func(t *testing.T) {
		root := t.TempDir()
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New() error = %v", err)
		}
		const (
			oldID   = "00000000-0000-4000-8000-000000000001"
			atID    = "00000000-0000-4000-8000-000000000002"
			otherID = "00000000-0000-4000-8000-000000000003"
		)
		writeHandoffCollection(t, root, []core.Handoff{
			testHandoff(t, oldID, "", "2026-08-09T10:00:00Z"),
			testHandoff(t, atID, "", cutoff),
			testHandoff(t, otherID, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", "2026-08-09T10:00:00Z"),
		})

		before := mustTime(t, cutoff)
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: oldID, Before: &before})
		if err != nil {
			t.Fatalf("PurgeHandoffs(old ID before cutoff) error = %v", err)
		}
		if result.Purged != 1 {
			t.Fatalf("PurgeHandoffs(old ID before cutoff) purged = %d, want 1", result.Purged)
		}
		assertHandoffIDs(t, p, []string{atID, otherID})

		result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: atID, Before: &before})
		if err != nil {
			t.Fatalf("PurgeHandoffs(ID at cutoff) error = %v", err)
		}
		if result.Purged != 0 {
			t.Fatalf("PurgeHandoffs(ID at cutoff) purged = %d, want 0", result.Purged)
		}
		assertHandoffIDs(t, p, []string{atID, otherID})

		result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{ID: atID})
		if err != nil {
			t.Fatalf("PurgeHandoffs(ID without cutoff) error = %v", err)
		}
		if result.Purged != 1 {
			t.Fatalf("PurgeHandoffs(ID without cutoff) purged = %d, want 1", result.Purged)
		}
		assertHandoffIDs(t, p, []string{otherID})
	})

	t.Run("global scope", func(t *testing.T) {
		root := t.TempDir()
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New() error = %v", err)
		}
		const (
			oldGlobal  = "00000000-0000-4000-8000-000000000011"
			atGlobal   = "00000000-0000-4000-8000-000000000012"
			newGlobal  = "00000000-0000-4000-8000-000000000013"
			taskScoped = "00000000-0000-4000-8000-000000000014"
		)
		writeHandoffCollection(t, root, []core.Handoff{
			testHandoff(t, oldGlobal, "", "2026-08-09T10:00:00Z"),
			testHandoff(t, atGlobal, "", cutoff),
			testHandoff(t, newGlobal, "", "2026-08-09T12:00:00Z"),
			testHandoff(t, taskScoped, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", "2026-08-09T10:00:00Z"),
		})

		before := mustTime(t, cutoff)
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true, Before: &before})
		if err != nil {
			t.Fatalf("PurgeHandoffs(global before cutoff) error = %v", err)
		}
		if result.Purged != 1 {
			t.Fatalf("PurgeHandoffs(global before cutoff) purged = %d, want 1", result.Purged)
		}
		assertHandoffIDs(t, p, []string{atGlobal, newGlobal, taskScoped})

		result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true})
		if err != nil {
			t.Fatalf("PurgeHandoffs(global without cutoff) error = %v", err)
		}
		if result.Purged != 2 {
			t.Fatalf("PurgeHandoffs(global without cutoff) purged = %d, want 2", result.Purged)
		}
		assertHandoffIDs(t, p, []string{taskScoped})
	})

	t.Run("resolved task scope", func(t *testing.T) {
		root := t.TempDir()
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New() error = %v", err)
		}
		task, err := p.CreateTask(core.CreateTaskInput{Title: "Target task"})
		if err != nil {
			t.Fatalf("CreateTask(target) error = %v", err)
		}
		const (
			oldTask   = "00000000-0000-4000-8000-000000000021"
			atTask    = "00000000-0000-4000-8000-000000000022"
			newTask   = "00000000-0000-4000-8000-000000000023"
			global    = "00000000-0000-4000-8000-000000000024"
			otherTask = "00000000-0000-4000-8000-000000000025"
		)
		writeHandoffCollection(t, root, []core.Handoff{
			testHandoff(t, oldTask, task.ID, "2026-08-09T10:00:00Z"),
			testHandoff(t, atTask, task.ID, cutoff),
			testHandoff(t, newTask, task.ID, "2026-08-09T12:00:00Z"),
			testHandoff(t, global, "", "2026-08-09T10:00:00Z"),
			testHandoff(t, otherTask, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", "2026-08-09T10:00:00Z"),
		})

		before := mustTime(t, cutoff)
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Task: task.ShortID, Before: &before})
		if err != nil {
			t.Fatalf("PurgeHandoffs(task short ID before cutoff) error = %v", err)
		}
		if result.Purged != 1 {
			t.Fatalf("PurgeHandoffs(task short ID before cutoff) purged = %d, want 1", result.Purged)
		}
		assertHandoffIDs(t, p, []string{atTask, newTask, global, otherTask})

		result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{Task: task.ID})
		if err != nil {
			t.Fatalf("PurgeHandoffs(task canonical ID without cutoff) error = %v", err)
		}
		if result.Purged != 2 {
			t.Fatalf("PurgeHandoffs(task canonical ID without cutoff) purged = %d, want 2", result.Purged)
		}
		assertHandoffIDs(t, p, []string{global, otherTask})
	})

	t.Run("all scopes", func(t *testing.T) {
		root := t.TempDir()
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New() error = %v", err)
		}
		const (
			oldGlobal = "00000000-0000-4000-8000-000000000031"
			atGlobal  = "00000000-0000-4000-8000-000000000032"
			oldTask   = "00000000-0000-4000-8000-000000000033"
			atTask    = "00000000-0000-4000-8000-000000000034"
			newTask   = "00000000-0000-4000-8000-000000000035"
		)
		writeHandoffCollection(t, root, []core.Handoff{
			testHandoff(t, oldGlobal, "", "2026-08-09T10:00:00Z"),
			testHandoff(t, atGlobal, "", cutoff),
			testHandoff(t, oldTask, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", "2026-08-09T10:00:00Z"),
			testHandoff(t, atTask, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", cutoff),
			testHandoff(t, newTask, "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", "2026-08-09T12:00:00Z"),
		})

		before := mustTime(t, cutoff)
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{AllScopes: true, Before: &before})
		if err != nil {
			t.Fatalf("PurgeHandoffs(all scopes before cutoff) error = %v", err)
		}
		if result.Purged != 2 {
			t.Fatalf("PurgeHandoffs(all scopes before cutoff) purged = %d, want 2", result.Purged)
		}
		assertHandoffIDs(t, p, []string{atGlobal, atTask, newTask})

		result, err = p.PurgeHandoffs(provider.HandoffPurgeRequest{AllScopes: true})
		if err != nil {
			t.Fatalf("PurgeHandoffs(all scopes without cutoff) error = %v", err)
		}
		if result.Purged != 3 {
			t.Fatalf("PurgeHandoffs(all scopes without cutoff) purged = %d, want 3", result.Purged)
		}
		assertHandoffIDs(t, p, []string{})
	})
}

func TestPurgeHandoffsReturnsZeroForEmptyScopes(t *testing.T) {
	t.Run("global scope with only task records", func(t *testing.T) {
		root := t.TempDir()
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New() error = %v", err)
		}
		writeHandoffCollection(t, root, []core.Handoff{
			testHandoff(t, "00000000-0000-4000-8000-000000000041", "ec58fe90-eb0f-4c4c-b2bf-4f1178a56a8a", "2026-08-09T10:00:00Z"),
		})
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true})
		if err != nil {
			t.Fatalf("PurgeHandoffs(empty global scope) error = %v", err)
		}
		if result.Purged != 0 {
			t.Fatalf("PurgeHandoffs(empty global scope) purged = %d, want 0", result.Purged)
		}
		assertHandoffIDs(t, p, []string{"00000000-0000-4000-8000-000000000041"})
	})

	t.Run("task scope with only global records", func(t *testing.T) {
		root := t.TempDir()
		p, err := flatfile.New(root, nil)
		if err != nil {
			t.Fatalf("flatfile.New() error = %v", err)
		}
		task, err := p.CreateTask(core.CreateTaskInput{Title: "Empty target scope"})
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		const globalID = "00000000-0000-4000-8000-000000000042"
		writeHandoffCollection(t, root, []core.Handoff{testHandoff(t, globalID, "", "2026-08-09T10:00:00Z")})
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Task: task.ShortID})
		if err != nil {
			t.Fatalf("PurgeHandoffs(empty task scope) error = %v", err)
		}
		if result.Purged != 0 {
			t.Fatalf("PurgeHandoffs(empty task scope) purged = %d, want 0", result.Purged)
		}
		assertHandoffIDs(t, p, []string{globalID})
	})

	t.Run("all scopes with no records", func(t *testing.T) {
		p := newProvider(t)
		result, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{AllScopes: true})
		if err != nil {
			t.Fatalf("PurgeHandoffs(empty all scopes) error = %v", err)
		}
		if result.Purged != 0 {
			t.Fatalf("PurgeHandoffs(empty all scopes) purged = %d, want 0", result.Purged)
		}
		assertHandoffIDs(t, p, []string{})
	})
}

func TestPurgeHandoffsRejectsInvalidOrMissingIDsWithoutChangingStore(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	const existingID = "00000000-0000-4000-8000-000000000051"
	writeHandoffCollection(t, root, []core.Handoff{testHandoff(t, existingID, "", "2026-08-09T10:00:00Z")})
	path := filepath.Join(root, "handoffs.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}

	for _, request := range []provider.HandoffPurgeRequest{
		{ID: "not-a-uuid"},
		{ID: "00000000-0000-4000-8000-000000000099"},
		{},
	} {
		_, err := p.PurgeHandoffs(request)
		if err == nil {
			t.Fatalf("PurgeHandoffs(%+v) error = nil, want validation/not-found error", request)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("os.ReadFile(after %+v) error = %v", request, readErr)
		}
		if string(after) != string(before) {
			t.Fatalf("store changed after rejected PurgeHandoffs(%+v): got %q, want %q", request, after, before)
		}
	}
}

func TestPurgeHandoffsPreservesStoreOnValidationFailure(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	path := filepath.Join(root, "handoffs.json")
	invalid := core.Handoff{
		ID:        "00000000-0000-4000-8000-000000000061",
		Message:   "invalid timestamp",
		CreatedAt: time.Time{},
	}
	writeHandoffCollection(t, root, []core.Handoff{invalid})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}

	if _, err := p.PurgeHandoffs(provider.HandoffPurgeRequest{Global: true}); err == nil || !strings.Contains(err.Error(), "createdAt is required") {
		t.Fatalf("PurgeHandoffs(invalid collection) error = %v, want validation error", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(after) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("store changed after validation failure: got %q, want %q", after, before)
	}
}

func testHandoff(t *testing.T, id, taskID, createdAt string) core.Handoff {
	t.Helper()
	return core.Handoff{
		ID:        id,
		TaskID:    taskID,
		Message:   "Retained context",
		CreatedAt: mustTime(t, createdAt),
	}
}

func writeHandoffCollection(t *testing.T, root string, handoffs []core.Handoff) {
	t.Helper()
	data, err := json.MarshalIndent(struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}{Handoffs: handoffs}, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(handoffs) error = %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, "handoffs.json"), data, 0o644); err != nil {
		t.Fatalf("os.WriteFile(handoffs) error = %v", err)
	}
}

func assertHandoffIDs(t *testing.T, p provider.Provider, want []string) {
	t.Helper()
	got, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		t.Fatalf("ListHandoffs() error = %v", err)
	}
	ids := make([]string, len(got.Handoffs))
	for index, handoff := range got.Handoffs {
		ids[index] = handoff.ID
	}
	slices.Sort(ids)
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("handoff IDs = %v, want %v", ids, want)
	}
}

func assertClaimHandoffIDs(t *testing.T, handoffs []core.Handoff, want []string) {
	t.Helper()
	got := make([]string, len(handoffs))
	for index, handoff := range handoffs {
		got[index] = handoff.ID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("claim handoff IDs = %v, want %v", got, want)
	}
}

func assertNoHandoffs(t *testing.T, view core.TaskView, operation string) {
	t.Helper()
	if len(view.Handoffs) != 0 {
		t.Fatalf("%s attached handoffs = %#v, want none", operation, view.Handoffs)
	}
}

func assertTaskLifecycleUnchanged(t *testing.T, before, after core.Task) {
	t.Helper()
	if after.Status != before.Status || after.Assignee != before.Assignee {
		t.Fatalf("task status or assignee changed after failed claim: before=%#v after=%#v", before, after)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("task creation or update timestamp changed after failed claim: before=%#v after=%#v", before, after)
	}
	if !equalOptionalTime(after.StartedAt, before.StartedAt) || !equalOptionalTime(after.CompletedAt, before.CompletedAt) {
		t.Fatalf("task lifecycle timestamp changed after failed claim: before=%#v after=%#v", before, after)
	}
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
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

func canonicalDiskTask(t *testing.T, id, shortID string) core.Task {
	t.Helper()
	created := mustTime(t, "2026-03-24T14:10:04Z")
	return core.Task{
		ID:           id,
		ShortID:      shortID,
		Title:        "Canonical task",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    created,
		UpdatedAt:    created,
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
