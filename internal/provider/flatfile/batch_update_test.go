package flatfile_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
)

func TestBatchUpdatePublishesFinalStateDependencyChangesAtomically(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "Dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}
	dependency, err = p.UpdateTaskStatus(dependency.ShortID, core.StatusInProgress, "Builder")
	if err != nil {
		t.Fatalf("start dependency error = %v", err)
	}
	target, err := p.CreateTask(core.CreateTaskInput{
		Title:        "Target",
		Dependencies: []string{dependency.ShortID},
	})
	if err != nil {
		t.Fatalf("CreateTask(target) error = %v", err)
	}

	result, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{
			ID:                target.ID,
			ShortID:           target.ShortID,
			ExpectedUpdatedAt: target.UpdatedAt,
			Title:             core.OptionalString{Set: true, Value: "  Started target  "},
			Status:            core.OptionalStatus{Set: true, Value: core.StatusInProgress},
			Dependencies:      core.OptionalStrings{Set: true, Value: []string{dependency.ShortID}},
		},
		{
			ID:                dependency.ID,
			ExpectedUpdatedAt: dependency.UpdatedAt,
			Status:            core.OptionalStatus{Set: true, Value: core.StatusDone},
		},
	}})
	if err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	if len(result.Updated) != 2 || len(result.Unchanged) != 0 {
		t.Fatalf("BatchUpdate() counts = updated %d unchanged %d, want 2/0", len(result.Updated), len(result.Unchanged))
	}
	if result.Updated[0].ID != target.ID || result.Updated[0].Status != core.StatusInProgress || result.Updated[0].Title != "Started target" {
		t.Fatalf("updated target = %#v", result.Updated[0])
	}
	if result.Updated[1].ID != dependency.ID || result.Updated[1].Status != core.StatusDone {
		t.Fatalf("updated dependency = %#v", result.Updated[1])
	}
	if result.Updated[0].Readiness.Blocked {
		t.Fatalf("target readiness uses pre-batch dependency state: %#v", result.Updated[0].Readiness)
	}
	if !result.Updated[0].UpdatedAt.After(target.UpdatedAt) || !result.Updated[1].UpdatedAt.After(dependency.UpdatedAt) {
		t.Fatal("changed tasks did not advance updatedAt")
	}
	if _, err := os.Stat(filepath.Join(root, "meta", "batch-update.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful batch journal remains: %v", err)
	}
}

func TestBatchUpdateEffectiveNoOpPreservesTaskBytesAndTimestamp(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Same", Description: "Value"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	path := filepath.Join(root, string(task.Status), task.ShortID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task before batch: %v", err)
	}

	result, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           task.ShortID,
		ExpectedUpdatedAt: task.UpdatedAt,
		Title:             core.OptionalString{Set: true, Value: "  Same  "},
		Status:            core.OptionalStatus{Set: true, Value: core.StatusTodo},
	}}})
	if err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	if len(result.Updated) != 0 || len(result.Unchanged) != 1 {
		t.Fatalf("BatchUpdate() counts = updated %d unchanged %d, want 0/1", len(result.Updated), len(result.Unchanged))
	}
	if !result.Unchanged[0].UpdatedAt.Equal(task.UpdatedAt) {
		t.Fatalf("no-op updatedAt = %v, want %v", result.Unchanged[0].UpdatedAt, task.UpdatedAt)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task after batch: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("effective no-op rewrote task bytes")
	}
	if _, err := os.Stat(filepath.Join(root, "meta", "batch-update.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no-op batch created a journal: %v", err)
	}
}

func TestBatchUpdateMixedChangedAndUnchangedRows(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	first, err := p.CreateTask(core.CreateTaskInput{Title: "First"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	second, err := p.CreateTask(core.CreateTaskInput{Title: "Second"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}
	third, err := p.CreateTask(core.CreateTaskInput{Title: "Third"})
	if err != nil {
		t.Fatalf("CreateTask(third) error = %v", err)
	}
	secondFile := filepath.Join(root, string(second.Status), second.ShortID+".json")
	before, err := os.ReadFile(secondFile)
	if err != nil {
		t.Fatalf("read unchanged task before batch: %v", err)
	}
	result, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Title: core.OptionalString{Set: true, Value: "First changed"}},
		{ShortID: second.ShortID, ExpectedUpdatedAt: second.UpdatedAt, Title: core.OptionalString{Set: true, Value: " Second "}},
		{ShortID: third.ShortID, ExpectedUpdatedAt: third.UpdatedAt, Description: core.OptionalString{Set: true, Value: "changed"}},
	}})
	if err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	if len(result.Updated) != 2 || len(result.Unchanged) != 1 || result.Unchanged[0].ID != second.ID {
		t.Fatalf("BatchUpdate() result = %#v, want 2 updated and second unchanged", result)
	}
	after, err := os.ReadFile(secondFile)
	if err != nil {
		t.Fatalf("read unchanged task after batch: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("mixed batch rewrote unchanged task")
	}
}

func TestBatchUpdateMaintainsMonotonicTimestampAfterClockRollback(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Future task"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	future := task.UpdatedAt.Add(24 * time.Hour)
	path := filepath.Join(root, string(task.Status), task.ShortID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task error = %v", err)
	}
	var stored core.Task
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	stored.UpdatedAt = future
	data, err = json.MarshalIndent(stored, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("rewrite future task error = %v", err)
	}
	result, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: task.ShortID, ExpectedUpdatedAt: future, Title: core.OptionalString{Set: true, Value: "after rollback"},
	}}})
	if err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	if !result.Updated[0].UpdatedAt.After(future) {
		t.Fatalf("updatedAt = %v, want after %v", result.Updated[0].UpdatedAt, future)
	}
}

func TestBatchUpdateSupportsCustomStatusCatalog(t *testing.T) {
	p := newCustomProvider(t)
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Review me"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	result, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: task.ShortID, ExpectedUpdatedAt: task.UpdatedAt,
		Status:   core.OptionalStatus{Set: true, Value: "waitingForReview"},
		Assignee: core.OptionalString{Set: true, Value: "Reviewer"},
	}}})
	if err != nil {
		t.Fatalf("BatchUpdate(custom status) error = %v", err)
	}
	if len(result.Updated) != 1 || result.Updated[0].Status != "waitingForReview" || result.Updated[0].StartedAt == nil || result.Updated[0].CompletedAt != nil {
		t.Fatalf("custom batch result = %#v", result)
	}
	got, err := p.GetTask(task.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if got.Status != "waitingForReview" || got.Assignee != "Reviewer" {
		t.Fatalf("stored custom task = %#v", got.Task)
	}
}

func TestBatchUpdateSupportsLegacyAndScopedShortIDs(t *testing.T) {
	root := t.TempDir()
	legacy, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("legacy provider error = %v", err)
	}
	legacyTask, err := legacy.CreateTask(core.CreateTaskInput{Title: "Legacy"})
	if err != nil {
		t.Fatalf("CreateTask(legacy) error = %v", err)
	}
	scoped, err := flatfile.New(root, core.NewBranchScope("batch-scoped"))
	if err != nil {
		t.Fatalf("scoped provider error = %v", err)
	}
	scopedTask, err := scoped.CreateTask(core.CreateTaskInput{Title: "Scoped"})
	if err != nil {
		t.Fatalf("CreateTask(scoped) error = %v", err)
	}
	legacyResult, err := legacy.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: legacyTask.ShortID, ExpectedUpdatedAt: legacyTask.UpdatedAt, Title: core.OptionalString{Set: true, Value: "Legacy changed"},
	}}})
	if err != nil || len(legacyResult.Updated) != 1 {
		t.Fatalf("legacy BatchUpdate() result=%#v error=%v", legacyResult, err)
	}
	scopedResult, err := scoped.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: scopedTask.ShortID, ExpectedUpdatedAt: scopedTask.UpdatedAt, Title: core.OptionalString{Set: true, Value: "Scoped changed"},
	}}})
	if err != nil || len(scopedResult.Updated) != 1 {
		t.Fatalf("scoped BatchUpdate() result=%#v error=%v", scopedResult, err)
	}
}

func TestBatchUpdateRejectsStaleTokenBeforePublishingAnyTask(t *testing.T) {
	root := t.TempDir()
	p, err := flatfile.New(root, nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	first, err := p.CreateTask(core.CreateTaskInput{Title: "First"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	second, err := p.CreateTask(core.CreateTaskInput{Title: "Second"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}
	firstPath := filepath.Join(root, string(first.Status), first.ShortID+".json")
	firstBefore, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("read first task: %v", err)
	}

	_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{
			ShortID:           first.ShortID,
			ExpectedUpdatedAt: first.UpdatedAt,
			Title:             core.OptionalString{Set: true, Value: "Changed first"},
		},
		{
			ShortID:           second.ShortID,
			ExpectedUpdatedAt: second.UpdatedAt.Add(-time.Second),
			Title:             core.OptionalString{Set: true, Value: "Changed second"},
		},
	}})
	if !errors.Is(err, provider.ErrStaleTask) {
		t.Fatalf("BatchUpdate() error = %v, want ErrStaleTask", err)
	}
	firstAfter, readErr := os.ReadFile(firstPath)
	if readErr != nil {
		t.Fatalf("read first task after rejection: %v", readErr)
	}
	if string(firstAfter) != string(firstBefore) {
		t.Fatal("stale batch changed a preceding valid row")
	}
}

func TestBatchUpdateComparesStatusBeforeLifecycleValidation(t *testing.T) {
	p := newProvider(t)
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Original"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	result, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           task.ShortID,
		ExpectedUpdatedAt: task.UpdatedAt,
		Title:             core.OptionalString{Set: true, Value: "Changed"},
		Status:            core.OptionalStatus{Set: true, Value: core.StatusTodo},
	}}})
	if err != nil {
		t.Fatalf("BatchUpdate(same status plus title) error = %v", err)
	}
	if len(result.Updated) != 1 || result.Updated[0].Title != "Changed" || result.Updated[0].Status != core.StatusTodo {
		t.Fatalf("BatchUpdate(same status plus title) = %#v", result)
	}

	_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           task.ShortID,
		ExpectedUpdatedAt: result.Updated[0].UpdatedAt,
		Status:            core.OptionalStatus{Set: true, Value: core.StatusDone},
	}}})
	if err == nil || !strings.Contains(err.Error(), "invalid status transition from todo to done") {
		t.Fatalf("BatchUpdate(invalid lifecycle) error = %v", err)
	}
}

func TestBatchUpdateRejectsIdentifierConflictsAndDuplicates(t *testing.T) {
	p := newProvider(t)
	first, err := p.CreateTask(core.CreateTaskInput{Title: "First"})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	second, err := p.CreateTask(core.CreateTaskInput{Title: "Second"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}

	_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ID:                first.ID,
		ShortID:           second.ShortID,
		ExpectedUpdatedAt: first.UpdatedAt,
		Title:             core.OptionalString{Set: true, Value: "Mismatch"},
	}}})
	if err == nil || !strings.Contains(err.Error(), "identify different tasks") {
		t.Fatalf("mismatched identifiers error = %v", err)
	}

	_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{ID: first.ID, ExpectedUpdatedAt: first.UpdatedAt, Title: core.OptionalString{Set: true, Value: "One"}},
		{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Description: core.OptionalString{Set: true, Value: "Two"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicates task") {
		t.Fatalf("duplicate task error = %v", err)
	}
}

func TestBatchUpdateValidatesFieldsAndFinalDependencyGraph(t *testing.T) {
	tests := []struct {
		name  string
		patch func(first, second core.TaskView) []core.BatchTaskUpdateInput
		want  string
	}{
		{
			name: "required title",
			patch: func(first, _ core.TaskView) []core.BatchTaskUpdateInput {
				return []core.BatchTaskUpdateInput{{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Title: core.OptionalString{Set: true, Value: " "}}}
			},
			want: "task title is required",
		},
		{
			name: "absolute git repository",
			patch: func(first, _ core.TaskView) []core.BatchTaskUpdateInput {
				return []core.BatchTaskUpdateInput{{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, GitRepo: core.OptionalString{Set: true, Value: "relative/repo"}}}
			},
			want: "must be an absolute path",
		},
		{
			name: "priority value",
			patch: func(first, _ core.TaskView) []core.BatchTaskUpdateInput {
				return []core.BatchTaskUpdateInput{{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Priority: core.OptionalPriority{Set: true, Value: "critical"}}}
			},
			want: "invalid priority",
		},
		{
			name: "dependency exists",
			patch: func(first, _ core.TaskView) []core.BatchTaskUpdateInput {
				return []core.BatchTaskUpdateInput{{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Dependencies: core.OptionalStrings{Set: true, Value: []string{"wtp-9999"}}}}
			},
			want: "not found",
		},
		{
			name: "final cycle",
			patch: func(first, second core.TaskView) []core.BatchTaskUpdateInput {
				return []core.BatchTaskUpdateInput{
					{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Dependencies: core.OptionalStrings{Set: true, Value: []string{second.ShortID}}},
					{ShortID: second.ShortID, ExpectedUpdatedAt: second.UpdatedAt, Dependencies: core.OptionalStrings{Set: true, Value: []string{first.ID}}},
				}
			},
			want: "cyclic dependency detected",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := newProvider(t)
			first, err := p.CreateTask(core.CreateTaskInput{Title: "First"})
			if err != nil {
				t.Fatalf("CreateTask(first) error = %v", err)
			}
			second, err := p.CreateTask(core.CreateTaskInput{Title: "Second"})
			if err != nil {
				t.Fatalf("CreateTask(second) error = %v", err)
			}
			_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: test.patch(first, second)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BatchUpdate() error = %v, want %q", err, test.want)
			}
			stored, getErr := p.GetTask(first.ShortID, "")
			if getErr != nil {
				t.Fatalf("GetTask(first) error = %v", getErr)
			}
			if stored.Title != first.Title || !stored.UpdatedAt.Equal(first.UpdatedAt) || len(stored.Dependencies) != 0 {
				t.Fatalf("rejected batch changed first task: %#v", stored.Task)
			}
		})
	}
}
