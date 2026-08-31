package stats

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestAggregateFocusedEmptyStoreLoadsTasksOnceWithoutHandoffs(t *testing.T) {
	spy := &focusedProviderSpy{fakeProvider: fakeProvider{
		listHandoffsErr: errors.New("handoffs must not be loaded"),
	}}

	report, err := AggregateFocused(spy, Options{}, AttributeModel)
	if err != nil {
		t.Fatalf("AggregateFocused() error = %v", err)
	}
	wantBuckets := []Bucket{}
	want := FocusedReport{
		TotalTasks: 0,
		Attribute:  AttributeModel,
		Buckets:    &wantBuckets,
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("AggregateFocused() = %#v, want %#v", report, want)
	}
	spy.assertCalls(t)
}

func TestAggregateFocusedStatusUsesCatalogOrderAndZeroBuckets(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "waitingForReview", Category: core.StatusCategoryWaiting},
		{Name: "blockedByReview", Category: core.StatusCategoryBlocked},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	spy := &focusedProviderSpy{fakeProvider: fakeProvider{
		catalog: catalog,
		tasks: []core.TaskView{
			{Task: core.Task{Status: core.Status("blockedByReview")}},
			{Task: core.Task{Status: core.StatusDone}},
		},
	}}

	report, err := AggregateFocused(spy, Options{}, AttributeStatus)
	if err != nil {
		t.Fatalf("AggregateFocused() error = %v", err)
	}
	wantBuckets := []Bucket{
		{Value: "todo", Count: 0},
		{Value: "inProgress", Count: 0},
		{Value: "paused", Count: 0},
		{Value: "done", Count: 1},
		{Value: "waitingForReview", Count: 0},
		{Value: "blockedByReview", Count: 1},
	}
	if report.Attribute != AttributeStatus || report.TotalTasks != 2 || report.Buckets == nil || !reflect.DeepEqual(*report.Buckets, wantBuckets) {
		t.Fatalf("focused status report = %#v, want total 2 and buckets %#v", report, wantBuckets)
	}
	spy.assertCalls(t)
}

func TestAggregateFocusedFilteredModelUsesProviderStatusFilterOnce(t *testing.T) {
	done := core.StatusDone
	spy := &focusedProviderSpy{fakeProvider: fakeProvider{tasks: []core.TaskView{
		{Task: core.Task{Status: core.StatusDone, Model: "zeta"}},
		{Task: core.Task{Status: core.StatusDone, Model: ""}},
		{Task: core.Task{Status: core.StatusDone, Model: "alpha"}},
		{Task: core.Task{Status: core.StatusTodo, Model: "excluded"}},
	}}}

	report, err := AggregateFocused(spy, Options{Status: &done}, AttributeModel)
	if err != nil {
		t.Fatalf("AggregateFocused() error = %v", err)
	}
	wantBuckets := []Bucket{
		{Value: "", Count: 1},
		{Value: "alpha", Count: 1},
		{Value: "zeta", Count: 1},
	}
	if report.Status != string(done) || report.TotalTasks != 3 || report.Buckets == nil || !reflect.DeepEqual(*report.Buckets, wantBuckets) {
		t.Fatalf("filtered model report = %#v, want status %q, total 3, buckets %#v", report, done, wantBuckets)
	}
	spy.assertCalls(t)
	if len(spy.filters) != 1 || spy.filters[0].Status == nil || *spy.filters[0].Status != done {
		t.Fatalf("ListTasks filters = %#v, want one filter for %q", spy.filters, done)
	}
}

func TestAggregateFocusedGroupingPreservesCaseSensitiveBuckets(t *testing.T) {
	grouping := core.GroupingFilter{IssueID: "issue-42", Project: "apollo", Milestone: "m1", Version: "v1", FeatureID: "feature-7", Feature: "search"}
	spy := &focusedProviderSpy{fakeProvider: fakeProvider{tasks: []core.TaskView{
		{Task: core.Task{IssueID: "ISSUE-42", Project: "Apollo", Milestone: "M1", Version: "V1", FeatureID: "FEATURE-7", Feature: "Search"}},
		{Task: core.Task{IssueID: "issue-42", Project: "APOLLO", Milestone: "m1", Version: "v1", FeatureID: "feature-7", Feature: "search"}},
		{Task: core.Task{IssueID: "ISSUE-42", Project: "Apollo", Milestone: "M1", Version: "V1", FeatureID: "FEATURE-7", Feature: "excluded"}},
	}}}

	report, err := AggregateFocused(spy, Options{Grouping: grouping}, AttributeFeature)
	if err != nil {
		t.Fatalf("AggregateFocused() error = %v", err)
	}
	want := []Bucket{{Value: "Search", Count: 1}, {Value: "search", Count: 1}}
	if report.TotalTasks != 2 || report.Buckets == nil || !reflect.DeepEqual(*report.Buckets, want) {
		t.Fatalf("focused grouped report = %#v, want %#v", report, want)
	}
	spy.assertCalls(t)
	if got, want := spy.filters[0].Grouping, grouping; got != want {
		t.Fatalf("ListTasks grouping = %#v, want %#v", got, want)
	}
}

func TestAggregateFocusedReturnsTaskProviderErrorWithoutLoadingHandoffs(t *testing.T) {
	spy := &focusedProviderSpy{fakeProvider: fakeProvider{
		listTasksErr:    errors.New("tasks unavailable"),
		listHandoffsErr: errors.New("handoffs must not be loaded"),
	}}

	_, err := AggregateFocused(spy, Options{}, AttributeComments)
	if err == nil || !strings.Contains(err.Error(), "tasks unavailable") {
		t.Fatalf("AggregateFocused() error = %v, want task provider error", err)
	}
	spy.assertCalls(t)
}

func TestAggregateFocusedIgnoresHandoffProviderErrors(t *testing.T) {
	spy := &focusedProviderSpy{fakeProvider: fakeProvider{
		tasks:           []core.TaskView{{Task: core.Task{Status: core.StatusTodo}}},
		listHandoffsErr: errors.New("handoffs unavailable"),
	}}

	if _, err := AggregateFocused(spy, Options{}, AttributeDependencies); err != nil {
		t.Fatalf("AggregateFocused() error = %v; focused aggregation must not load handoffs", err)
	}
	spy.assertCalls(t)
}

type focusedProviderSpy struct {
	fakeProvider
	filters           []provider.TaskFilter
	listTasksCalls    int
	listHandoffsCalls int
}

func (p *focusedProviderSpy) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	p.listTasksCalls++
	p.filters = append(p.filters, filter)
	return p.fakeProvider.ListTasks(filter)
}

func (p *focusedProviderSpy) ListHandoffs(filter provider.HandoffFilter) (provider.HandoffListResult, error) {
	p.listHandoffsCalls++
	return p.fakeProvider.ListHandoffs(filter)
}

func (p *focusedProviderSpy) assertCalls(t *testing.T) {
	t.Helper()
	if p.listTasksCalls != 1 || p.listHandoffsCalls != 0 {
		t.Fatalf("provider calls = ListTasks %d, ListHandoffs %d; want 1, 0", p.listTasksCalls, p.listHandoffsCalls)
	}
}
