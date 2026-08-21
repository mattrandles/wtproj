package core

import (
	"strings"
	"testing"
	"time"
)

func TestStatusCatalogPreservesDefaultAndAdditionalOrder(t *testing.T) {
	catalog, err := NewStatusCatalog([]StatusDefinition{
		{Name: "needsReview", Category: StatusCategoryWaiting},
		{Name: "vendorBlocked", Category: StatusCategoryBlocked},
		{Name: "qaFailed", Category: StatusCategoryFailed},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	got := catalog.Statuses()
	want := []Status{
		StatusTodo, StatusInProgress, StatusPaused, StatusDone,
		"needsReview", "vendorBlocked", "qaFailed",
	}
	if len(got) != len(want) {
		t.Fatalf("catalog has %d statuses, want %d", len(got), len(want))
	}
	for index, definition := range got {
		if definition.Name != want[index] {
			t.Fatalf("catalog[%d] = %q, want %q", index, definition.Name, want[index])
		}
	}
	got[0].Name = "mutated"
	if catalog.Statuses()[0].Name != StatusTodo {
		t.Fatal("catalog definitions were mutable through accessor")
	}
}

func TestNewStatusCatalogRejectsInvalidAdditionalDefinitions(t *testing.T) {
	tests := []StatusDefinition{
		{Name: "NotLowerCamel", Category: StatusCategoryWaiting},
		{Name: "not-kebab", Category: StatusCategoryWaiting},
		{Name: "todo", Category: StatusCategoryWaiting},
		{Name: "custom", Category: StatusCategoryWaiting},
		{Name: "custom", Category: StatusCategoryBlocked},
		{Name: "all", Category: StatusCategoryFailed},
		{Name: "custom", Category: "done"},
	}
	for index, definition := range tests {
		if index == 4 {
			continue // paired with index 3 to test a duplicate.
		}
		name := "invalid"
		if index == 3 {
			name = "duplicate"
		}
		t.Run(name, func(t *testing.T) {
			additional := []StatusDefinition{definition}
			if index == 3 {
				additional = append(additional, definition)
			}
			if _, err := NewStatusCatalog(additional); err == nil {
				t.Fatalf("NewStatusCatalog(%#v) accepted invalid definitions", additional)
			}
		})
	}
}

func TestStatusCatalogLifecycleAndDependencySemantics(t *testing.T) {
	catalog, err := NewStatusCatalog([]StatusDefinition{
		{Name: "waitingForReview", Category: StatusCategoryWaiting},
		{Name: "externalBlocked", Category: StatusCategoryBlocked},
		{Name: "verificationFailed", Category: StatusCategoryFailed},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	created := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	started := created.Add(time.Minute)
	completed := started.Add(time.Minute)
	for _, test := range []struct {
		status                 Status
		startedAt, completedAt *time.Time
		wantErrContains        string
	}{
		{status: "waitingForReview", startedAt: &started, wantErrContains: ""},
		{status: "externalBlocked", wantErrContains: ""},
		{status: "verificationFailed", startedAt: &started, completedAt: &completed, wantErrContains: ""},
		{status: "waitingForReview", wantErrContains: "requires startedAt"},
		{status: "verificationFailed", startedAt: &started, wantErrContains: "requires startedAt and completedAt"},
	} {
		err := catalog.NormalizeLifecycle(test.status, test.startedAt, test.completedAt)
		if test.wantErrContains == "" && err != nil {
			t.Fatalf("NormalizeLifecycle(%s) error = %v", test.status, err)
		}
		if test.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), test.wantErrContains)) {
			t.Fatalf("NormalizeLifecycle(%s) error = %v, want %q", test.status, err, test.wantErrContains)
		}
	}
	if catalog.DependencyResolved(StatusDone) == false || catalog.DependencyResolved("verificationFailed") {
		t.Fatal("dependency resolution semantics are incorrect")
	}
}

func TestStatusCatalogNormalizeTaskStatusSupportsReopening(t *testing.T) {
	catalog, err := NewStatusCatalog([]StatusDefinition{
		{Name: "waitingForReview", Category: StatusCategoryWaiting},
		{Name: "externalBlocked", Category: StatusCategoryBlocked},
		{Name: "verificationFailed", Category: StatusCategoryFailed},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Minute)
	completed := now.Add(-time.Second)
	task := Task{StartedAt: &started, CompletedAt: &completed}
	if err := catalog.NormalizeTaskStatus(&task, "externalBlocked", now); err != nil {
		t.Fatalf("normalize blocked error = %v", err)
	}
	if task.StartedAt != nil || task.CompletedAt != nil {
		t.Fatal("blocked status retained lifecycle timestamps")
	}
	if err := catalog.NormalizeTaskStatus(&task, "waitingForReview", now); err != nil {
		t.Fatalf("normalize waiting error = %v", err)
	}
	if task.StartedAt == nil || !task.StartedAt.Equal(now) || task.CompletedAt != nil {
		t.Fatalf("waiting lifecycle = started %v completed %v", task.StartedAt, task.CompletedAt)
	}
	if err := catalog.NormalizeTaskStatus(&task, "verificationFailed", now); err != nil {
		t.Fatalf("normalize failed error = %v", err)
	}
	if task.StartedAt == nil || task.CompletedAt == nil {
		t.Fatal("failed status did not receive both lifecycle timestamps")
	}
}
