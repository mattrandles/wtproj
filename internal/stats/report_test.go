package stats

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestAggregateBuildsDeterministicOverview(t *testing.T) {
	tasks := []core.TaskView{
		{Task: core.Task{ID: "00000000-0000-4000-8000-000000000001", Status: core.StatusDone, Model: "zeta", Lane: "cli", Priority: core.PriorityUrgent, Estimate: core.EstimateXL, Assignee: "Zed", Comments: []core.Comment{{}, {}}, Dependencies: []string{"a", "b"}}},
		{Task: core.Task{ID: "00000000-0000-4000-8000-000000000002", Status: core.StatusTodo, Model: "", Lane: "backend", Priority: core.PriorityLow, Estimate: core.EstimateXS, Assignee: "Amy", Comments: []core.Comment{{}}}},
		{Task: core.Task{ID: "00000000-0000-4000-8000-000000000003", Status: core.StatusPaused, Model: "alpha", Lane: "", Priority: "", Estimate: "", Assignee: "", Dependencies: []string{}}},
	}
	global := core.Handoff{ID: "00000000-0000-4000-8000-000000000011", Message: "global", CreatedAt: time.Unix(1, 0).UTC()}
	taskHandoff := core.Handoff{ID: "00000000-0000-4000-8000-000000000012", TaskID: tasks[0].ID, Message: "task", CreatedAt: time.Unix(2, 0).UTC()}
	otherHandoff := core.Handoff{ID: "00000000-0000-4000-8000-000000000013", TaskID: "00000000-0000-4000-8000-000000000099", Message: "other", CreatedAt: time.Unix(3, 0).UTC()}

	report, err := Aggregate(fakeProvider{tasks: tasks, handoffs: []core.Handoff{global, taskHandoff, otherHandoff}}, Options{})
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	if report.TotalTasks != 3 {
		t.Fatalf("TotalTasks = %d, want 3", report.TotalTasks)
	}
	wantStatuses := []Bucket{{Value: "todo", Count: 1}, {Value: "inProgress", Count: 0}, {Value: "paused", Count: 1}, {Value: "done", Count: 1}}
	if !reflect.DeepEqual(report.StatusCounts, wantStatuses) {
		t.Fatalf("StatusCounts = %#v, want %#v", report.StatusCounts, wantStatuses)
	}
	if !reflect.DeepEqual(report.Attributes.Model, []Bucket{{Value: "", Count: 1}, {Value: "alpha", Count: 1}, {Value: "zeta", Count: 1}}) {
		t.Fatalf("Model buckets = %#v", report.Attributes.Model)
	}
	if !reflect.DeepEqual(report.Attributes.Priority, []Bucket{{Value: "", Count: 1}, {Value: "low", Count: 1}, {Value: "urgent", Count: 1}}) {
		t.Fatalf("Priority buckets = %#v", report.Attributes.Priority)
	}
	if !reflect.DeepEqual(report.Attributes.Estimate, []Bucket{{Value: "", Count: 1}, {Value: "xs", Count: 1}, {Value: "xl", Count: 1}}) {
		t.Fatalf("Estimate buckets = %#v", report.Attributes.Estimate)
	}
	if report.Comments != (CommentMetrics{TasksWithComments: 2, TotalRecords: 3}) {
		t.Fatalf("Comments = %#v", report.Comments)
	}
	if report.Dependencies != (DependencyMetrics{TasksWithDependencies: 1, IndependentTasks: 2, DirectDependencyTotal: 2}) {
		t.Fatalf("Dependencies = %#v", report.Dependencies)
	}
	if report.Handoffs != (HandoffMetrics{Total: 3, AllStatusTotal: 3, Global: 1, TaskScoped: 2}) {
		t.Fatalf("Handoffs = %#v", report.Handoffs)
	}
}

func TestAggregateFilteredHandoffsIncludeGlobalAndSelectedTasks(t *testing.T) {
	done := core.StatusDone
	selected := core.TaskView{Task: core.Task{ID: "00000000-0000-4000-8000-000000000001", Status: core.StatusDone}}
	todo := core.TaskView{Task: core.Task{ID: "00000000-0000-4000-8000-000000000002", Status: core.StatusTodo}}
	handoffs := []core.Handoff{
		{ID: "00000000-0000-4000-8000-000000000011", Message: "global"},
		{ID: "00000000-0000-4000-8000-000000000012", TaskID: selected.ID, Message: "selected"},
		{ID: "00000000-0000-4000-8000-000000000013", TaskID: todo.ID, Message: "excluded"},
	}
	report, err := Aggregate(fakeProvider{tasks: []core.TaskView{selected, todo}, handoffs: handoffs}, Options{Status: &done})
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	if report.Status != string(core.StatusDone) || report.TotalTasks != 1 {
		t.Fatalf("filtered scope = status %q, tasks %d", report.Status, report.TotalTasks)
	}
	if report.Handoffs != (HandoffMetrics{Total: 2, AllStatusTotal: 3, Global: 1, TaskScoped: 1}) {
		t.Fatalf("filtered Handoffs = %#v", report.Handoffs)
	}
}

func TestAggregateRejectsInvalidStatusAndProviderErrors(t *testing.T) {
	invalid := core.Status("unknown")
	if _, err := Aggregate(fakeProvider{}, Options{Status: &invalid}); err == nil {
		t.Fatal("Aggregate() accepted invalid status")
	}
	if _, err := Aggregate(fakeProvider{listTasksErr: errors.New("tasks unavailable")}, Options{}); err == nil {
		t.Fatal("Aggregate() ignored task provider error")
	}
	if _, err := Aggregate(fakeProvider{listHandoffsErr: errors.New("handoffs unavailable")}, Options{}); err == nil {
		t.Fatal("Aggregate() ignored handoff provider error")
	}
}

func TestAggregateUsesOrderedCatalogWithZeroBuckets(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "waitingForReview", Category: core.StatusCategoryWaiting},
		{Name: "blockedByReview", Category: core.StatusCategoryBlocked},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	waiting := core.Status("waitingForReview")
	report, err := Aggregate(fakeProvider{catalog: catalog, tasks: []core.TaskView{
		{Task: core.Task{ID: "task-1", Status: core.StatusTodo}},
		{Task: core.Task{ID: "task-2", Status: waiting}},
	}}, Options{})
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	want := []Bucket{
		{Value: "todo", Count: 1},
		{Value: "inProgress", Count: 0},
		{Value: "paused", Count: 0},
		{Value: "done", Count: 0},
		{Value: "waitingForReview", Count: 1},
		{Value: "blockedByReview", Count: 0},
	}
	if !reflect.DeepEqual(report.StatusCounts, want) {
		t.Fatalf("StatusCounts = %#v, want %#v", report.StatusCounts, want)
	}
}

type fakeProvider struct {
	catalog           core.StatusCatalog
	tasks             []core.TaskView
	handoffs          []core.Handoff
	listTasksErr      error
	listHandoffsErr   error
	listTasksCalls    *int
	listHandoffsCalls *int
}

func (p fakeProvider) StatusCatalog() core.StatusCatalog {
	if len(p.catalog.Statuses()) == 0 {
		return core.DefaultStatusCatalog()
	}
	return p.catalog
}

func (p fakeProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	if p.listTasksCalls != nil {
		*p.listTasksCalls = *p.listTasksCalls + 1
	}
	if p.listTasksErr != nil {
		return nil, p.listTasksErr
	}
	if filter.Status == nil {
		return p.tasks, nil
	}
	filtered := make([]core.TaskView, 0, len(p.tasks))
	for _, task := range p.tasks {
		if task.Status == *filter.Status {
			filtered = append(filtered, task)
		}
	}
	return filtered, nil
}

func (p fakeProvider) ListHandoffs(provider.HandoffFilter) (provider.HandoffListResult, error) {
	if p.listHandoffsCalls != nil {
		*p.listHandoffsCalls = *p.listHandoffsCalls + 1
	}
	return provider.HandoffListResult{Handoffs: p.handoffs}, p.listHandoffsErr
}

func (fakeProvider) WriteHandoff(provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	panic("unused")
}
func (fakeProvider) PurgeHandoffs(provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	panic("unused")
}
func (fakeProvider) GetTask(string, string) (core.TaskView, error)                  { panic("unused") }
func (fakeProvider) CreateTask(core.CreateTaskInput) (core.TaskView, error)         { panic("unused") }
func (fakeProvider) UpdateTask(string, core.UpdateTaskInput) (core.TaskView, error) { panic("unused") }
func (fakeProvider) BatchUpdate(provider.BatchUpdateRequest) (provider.BatchUpdateResult, error) {
	panic("unused")
}
func (fakeProvider) UpdateTaskStatus(string, core.Status, string) (core.TaskView, error) {
	panic("unused")
}
func (fakeProvider) AddComment(string, string, string) (core.TaskView, error) { panic("unused") }
func (fakeProvider) PeekNextTask(string) (core.TaskView, error)               { panic("unused") }
func (fakeProvider) PeekNextTasks(string, int) ([]core.TaskView, error)       { panic("unused") }
func (fakeProvider) GetNextTask(string) (core.TaskView, error)                { panic("unused") }
func (fakeProvider) ExportCanonical(string) error                             { panic("unused") }
