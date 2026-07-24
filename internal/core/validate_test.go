package core

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateDependenciesRejectsMissingDependency(t *testing.T) {
	tasks := []Task{
		{ID: "task-a", Dependencies: nil},
	}

	err := ValidateDependencies("", []string{"missing"}, tasks)
	if err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestValidateDependenciesRejectsMissingDependencyInExistingGraph(t *testing.T) {
	tasks := []Task{
		{ID: "task-a", Dependencies: []string{"missing"}},
	}

	err := ValidateDependencies("", nil, tasks)
	if err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestValidateDependenciesRejectsCycle(t *testing.T) {
	tasks := []Task{
		{ID: "task-a", Dependencies: []string{"task-b"}},
		{ID: "task-b", Dependencies: []string{"task-c"}},
		{ID: "task-c", Dependencies: nil},
	}

	err := ValidateDependencies("task-c", []string{"task-a"}, tasks)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestValidateDependenciesRejectsCycleInExistingGraph(t *testing.T) {
	tasks := []Task{
		{ID: "task-a", Dependencies: []string{"task-b"}},
		{ID: "task-b", Dependencies: []string{"task-a"}},
	}

	err := ValidateDependencies("", nil, tasks)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestAllowedTransition(t *testing.T) {
	cases := []struct {
		name string
		from Status
		to   Status
		want bool
	}{
		{name: "todo to in progress", from: StatusTodo, to: StatusInProgress, want: true},
		{name: "todo to done", from: StatusTodo, to: StatusDone, want: false},
		{name: "in progress to paused", from: StatusInProgress, to: StatusPaused, want: true},
		{name: "paused to done", from: StatusPaused, to: StatusDone, want: true},
		{name: "done to todo", from: StatusDone, to: StatusTodo, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllowedTransition(tc.from, tc.to); got != tc.want {
				t.Fatalf("AllowedTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestParsePriorityRejectsInvalidValue(t *testing.T) {
	if _, err := ParsePriority("critical"); err == nil {
		t.Fatal("expected invalid priority error")
	}
}

func TestParseEstimateRejectsInvalidValue(t *testing.T) {
	if _, err := ParseEstimate("xxl"); err == nil {
		t.Fatal("expected invalid estimate error")
	}
}

func TestTaskValidateRejectsInvalidSchedulingMetadata(t *testing.T) {
	task := Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Invalid scheduling metadata",
		Priority:     Priority("critical"),
		Status:       StatusTodo,
		Dependencies: []string{},
		Comments:     []Comment{},
		CreatedAt:    mustValidationTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustValidationTime(t, "2026-03-24T14:10:04Z"),
	}

	if err := task.Validate(); err == nil {
		t.Fatal("expected task validation error")
	}
}

func TestTaskValidateRejectsWhitespaceSuggestedModel(t *testing.T) {
	task := Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Invalid model metadata",
		Model:        "   ",
		Status:       StatusTodo,
		Dependencies: []string{},
		Comments:     []Comment{},
		CreatedAt:    mustValidationTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustValidationTime(t, "2026-03-24T14:10:04Z"),
	}

	if err := task.Validate(); err == nil {
		t.Fatal("expected whitespace model validation error")
	}
}

func TestTaskValidateAcceptsFreeFormSuggestedModel(t *testing.T) {
	task := Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Free-form model metadata",
		Model:        "provider/custom-model:latest",
		Status:       StatusTodo,
		Dependencies: []string{},
		Comments:     []Comment{},
		CreatedAt:    mustValidationTime(t, "2026-03-24T14:10:04Z"),
		UpdatedAt:    mustValidationTime(t, "2026-03-24T14:10:04Z"),
	}

	if err := task.Validate(); err != nil {
		t.Fatalf("Task.Validate() error = %v", err)
	}
}

func TestTaskValidateAcceptsGitAndWorktreeMetadata(t *testing.T) {
	task := validValidationTask(t)
	task.GitRepo = filepath.Join(t.TempDir(), "repository")
	task.GitBranch = "feature/task-metadata"
	task.WorktreeName = "task-metadata"
	task.WorktreeDir = filepath.Join(t.TempDir(), "task-metadata")

	if err := task.Validate(); err != nil {
		t.Fatalf("Task.Validate() error = %v", err)
	}
}

func TestTaskValidateRejectsInvalidGitAndWorktreeMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
		want   string
	}{
		{name: "relative git repo", mutate: func(task *Task) { task.GitRepo = "relative/repository" }, want: "gitRepo"},
		{name: "blank git repo", mutate: func(task *Task) { task.GitRepo = "   " }, want: "gitRepo cannot be blank"},
		{name: "blank git branch", mutate: func(task *Task) { task.GitBranch = "   " }, want: "gitBranch cannot be blank"},
		{name: "blank worktree name", mutate: func(task *Task) { task.WorktreeName = "   " }, want: "worktreeName cannot be blank"},
		{name: "relative worktree dir", mutate: func(task *Task) { task.WorktreeDir = "relative/worktree" }, want: "worktreeDir"},
		{name: "blank worktree dir", mutate: func(task *Task) { task.WorktreeDir = "   " }, want: "worktreeDir cannot be blank"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validValidationTask(t)
			test.mutate(&task)
			err := task.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Task.Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestTaskJSONWithoutGitAndWorktreeMetadataRemainsValid(t *testing.T) {
	legacyJSON := `{
		"id": "25c3806a-bd1b-424d-889b-29e5b06679b8",
		"shortId": "wtp-0001",
		"title": "Legacy task",
		"status": "todo",
		"dependencies": [],
		"comments": [],
		"createdAt": "2026-03-24T14:10:04Z",
		"updatedAt": "2026-03-24T14:10:04Z",
		"startedAt": null,
		"completedAt": null
	}`

	var task Task
	if err := json.Unmarshal([]byte(legacyJSON), &task); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Task.Validate() legacy JSON error = %v", err)
	}
	if task.GitRepo != "" || task.GitBranch != "" || task.WorktreeName != "" || task.WorktreeDir != "" {
		t.Fatalf("legacy task contains Git/worktree metadata: %#v", task)
	}
}

func TestTaskValidateRejectsCanonicalInvariantViolations(t *testing.T) {
	created := mustValidationTime(t, "2026-03-24T14:10:04Z")
	updated := created.Add(2 * time.Minute)
	started := created.Add(time.Minute)
	completed := updated
	validComment := Comment{
		ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
		Author:    "Tony",
		Message:   "Implemented parser",
		CreatedAt: started,
	}

	tests := []struct {
		name   string
		mutate func(*Task)
		want   string
	}{
		{name: "task id format", mutate: func(task *Task) { task.ID = "not-a-uuid" }, want: "canonical lowercase UUID"},
		{name: "short id format", mutate: func(task *Task) { task.ShortID = "WTP-1" }, want: "must match wtp-NNNN"},
		{name: "dependency id format", mutate: func(task *Task) { task.Dependencies = []string{"missing"} }, want: "dependency 0"},
		{name: "comment id format", mutate: func(task *Task) { task.Comments[0].ID = "comment-1" }, want: "comment 0 id"},
		{name: "duplicate comment id", mutate: func(task *Task) { task.Comments = append(task.Comments, task.Comments[0]) }, want: "comment id 7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6 is duplicated"},
		{name: "blank comment author", mutate: func(task *Task) { task.Comments[0].Author = "   " }, want: "author cannot be blank"},
		{name: "blank comment message", mutate: func(task *Task) { task.Comments[0].Message = "   " }, want: "message is required"},
		{name: "missing comment timestamp", mutate: func(task *Task) { task.Comments[0].CreatedAt = time.Time{} }, want: "comment 0 createdAt is required"},
		{name: "comment before task", mutate: func(task *Task) { task.Comments[0].CreatedAt = created.Add(-time.Second) }, want: "between task createdAt and updatedAt"},
		{name: "updated before created", mutate: func(task *Task) { task.UpdatedAt = created.Add(-time.Second) }, want: "updatedAt cannot be before createdAt"},
		{name: "todo with started timestamp", mutate: func(task *Task) { task.StartedAt = &started }, want: "todo task cannot have"},
		{name: "in progress without started timestamp", mutate: func(task *Task) { task.Status = StatusInProgress }, want: "inProgress task requires startedAt"},
		{name: "paused with completed timestamp", mutate: func(task *Task) { task.Status = StatusPaused; task.StartedAt = &started; task.CompletedAt = &completed }, want: "paused task cannot have completedAt"},
		{name: "done without completed timestamp", mutate: func(task *Task) { task.Status = StatusDone; task.StartedAt = &started }, want: "done task requires startedAt and completedAt"},
		{name: "completion before start", mutate: func(task *Task) { task.Status = StatusDone; task.StartedAt = &completed; task.CompletedAt = &started }, want: "completedAt cannot be before startedAt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := Task{
				ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
				ShortID:      "wtp-0001",
				Title:        "Canonical task",
				Status:       StatusTodo,
				Dependencies: []string{},
				Comments:     []Comment{validComment},
				CreatedAt:    created,
				UpdatedAt:    updated,
			}
			test.mutate(&task)
			err := task.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Task.Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func validValidationTask(t *testing.T) Task {
	t.Helper()
	created := mustValidationTime(t, "2026-03-24T14:10:04Z")
	return Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Valid task",
		Status:       StatusTodo,
		Dependencies: []string{},
		Comments:     []Comment{},
		CreatedAt:    created,
		UpdatedAt:    created,
	}
}

func TestTaskValidateAcceptsLegacyAnonymousComment(t *testing.T) {
	created := mustValidationTime(t, "2026-03-24T14:10:04Z")
	task := Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Legacy comment",
		Status:       StatusTodo,
		Dependencies: []string{},
		Comments: []Comment{{
			ID:        "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
			Author:    "",
			Message:   "Created before agent attribution was required",
			CreatedAt: created,
		}},
		CreatedAt: created,
		UpdatedAt: created,
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Task.Validate() legacy anonymous comment error = %v", err)
	}
}

func mustValidationTime(t *testing.T, value string) time.Time {
	t.Helper()

	outTime, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return outTime
}
