package core

import "testing"

func TestValidateDependenciesRejectsMissingDependency(t *testing.T) {
	tasks := []Task{
		{ID: "task-a", Dependencies: nil},
	}

	err := ValidateDependencies("", []string{"missing"}, tasks)
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
