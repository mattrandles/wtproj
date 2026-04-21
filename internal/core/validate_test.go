package core

import (
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

func mustValidationTime(t *testing.T, value string) time.Time {
	t.Helper()

	outTime, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return outTime
}
