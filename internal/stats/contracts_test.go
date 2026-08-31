package stats

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestExistingReportJSONFieldOrderContract(t *testing.T) {
	report := Report{
		Status:       "done",
		TotalTasks:   1,
		StatusCounts: []Bucket{{Value: "done", Count: 1}},
		Attributes: Attributes{
			Model:    []Bucket{},
			Lane:     []Bucket{},
			Priority: []Bucket{},
			Estimate: []Bucket{},
			Assignee: []Bucket{},
		},
		Comments:     CommentMetrics{TasksWithComments: 1, TotalRecords: 2},
		Dependencies: DependencyMetrics{TasksWithDependencies: 1, IndependentTasks: 0, DirectDependencyTotal: 3},
		Handoffs:     HandoffMetrics{Total: 4, AllStatusTotal: 5, Global: 1, TaskScoped: 3},
	}

	assertJSONContract(t, report, `{"status":"done","totalTasks":1,"statusCounts":[{"value":"done","count":1}],"attributes":{"model":[],"lane":[],"priority":[],"estimate":[],"assignee":[]},"comments":{"tasksWithComments":1,"totalRecords":2},"dependencies":{"tasksWithDependencies":1,"independentTasks":0,"directDependencyTotal":3},"handoffs":{"total":4,"allStatusTotal":5,"global":1,"taskScoped":3}}`)
}

func TestExistingFocusedReportJSONFieldOrderContracts(t *testing.T) {
	buckets := []Bucket{{Value: "alpha", Count: 2}}
	comments := CommentMetrics{TasksWithComments: 1, TotalRecords: 2}
	dependencies := DependencyMetrics{TasksWithDependencies: 1, IndependentTasks: 1, DirectDependencyTotal: 2}
	tests := []struct {
		name   string
		report FocusedReport
		want   string
	}{
		{
			name:   "categorical",
			report: FocusedReport{Status: "done", TotalTasks: 2, Attribute: AttributeModel, Buckets: &buckets},
			want:   `{"status":"done","totalTasks":2,"attribute":"model","buckets":[{"value":"alpha","count":2}]}`,
		},
		{
			name:   "comments",
			report: FocusedReport{TotalTasks: 2, Attribute: AttributeComments, Comments: &comments},
			want:   `{"totalTasks":2,"attribute":"comments","comments":{"tasksWithComments":1,"totalRecords":2}}`,
		},
		{
			name:   "dependencies",
			report: FocusedReport{TotalTasks: 2, Attribute: AttributeDependencies, Dependencies: &dependencies},
			want:   `{"totalTasks":2,"attribute":"dependencies","dependencies":{"tasksWithDependencies":1,"independentTasks":1,"directDependencyTotal":2}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertJSONContract(t, test.report, test.want)
		})
	}
}

func TestAggregateFocusedStatusUsesTasksWithoutHandoffs(t *testing.T) {
	listTasksCalls := 0
	listHandoffsCalls := 0
	p := fakeProvider{
		tasks: []core.TaskView{
			{Task: core.Task{Status: core.StatusTodo}},
			{Task: core.Task{Status: core.StatusDone}},
		},
		listTasksCalls:    &listTasksCalls,
		listHandoffsCalls: &listHandoffsCalls,
	}

	report, err := AggregateFocused(p, Options{}, AttributeStatus)
	if err != nil {
		t.Fatalf("AggregateFocused() error = %v", err)
	}
	want := FocusedReport{
		TotalTasks: 2,
		Attribute:  AttributeStatus,
		Buckets: &[]Bucket{
			{Value: "todo", Count: 1},
			{Value: "inProgress", Count: 0},
			{Value: "paused", Count: 0},
			{Value: "done", Count: 1},
		},
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("AggregateFocused() = %#v, want %#v", report, want)
	}
	if listTasksCalls != 1 || listHandoffsCalls != 0 {
		t.Fatalf("provider calls = ListTasks %d, ListHandoffs %d; want 1, 0", listTasksCalls, listHandoffsCalls)
	}
}

func TestAggregateFocusedStopsBeforeOverviewHandoffsOnTaskError(t *testing.T) {
	listHandoffsCalls := 0
	_, err := AggregateFocused(fakeProvider{
		listTasksErr:      errors.New("unavailable"),
		listHandoffsCalls: &listHandoffsCalls,
	}, Options{}, AttributeModel)
	if err == nil {
		t.Fatal("AggregateFocused() ignored ListTasks error")
	}
	if listHandoffsCalls != 0 {
		t.Fatalf("ListHandoffs calls = %d, want 0", listHandoffsCalls)
	}
}

func TestAggregateOverviewAddsHandoffsAfterCommonTaskAggregation(t *testing.T) {
	listTasksCalls := 0
	listHandoffsCalls := 0
	_, err := Aggregate(fakeProvider{
		listTasksCalls:    &listTasksCalls,
		listHandoffsCalls: &listHandoffsCalls,
	}, Options{})
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	if listTasksCalls != 1 || listHandoffsCalls != 1 {
		t.Fatalf("provider calls = ListTasks %d, ListHandoffs %d; want 1, 1", listTasksCalls, listHandoffsCalls)
	}
}

func TestSeriesContractsResolveExplicitAsOfAndMarshalExactBoundaries(t *testing.T) {
	asOf := time.Date(2026, time.August, 30, 4, 5, 6, 789, time.FixedZone("offset", 2*60*60))
	options := SeriesOptions{
		Metric: SeriesMetricCreated,
		Range:  RollingRange{StartDays: 2, EndDays: 1},
		AsOf:   asOf,
	}
	rangeValue, err := options.ResolveRange()
	if err != nil {
		t.Fatalf("ResolveRange() error = %v", err)
	}
	wantRange := TimeRange{
		Start: time.Date(2026, time.August, 28, 2, 5, 6, 789, time.UTC),
		End:   time.Date(2026, time.August, 29, 2, 5, 6, 789, time.UTC),
	}
	if rangeValue != wantRange {
		t.Fatalf("ResolveRange() = %#v, want %#v", rangeValue, wantRange)
	}

	report := SeriesReport{
		Attribute:  SeriesMetricCreated,
		TotalTasks: 1,
		Range:      rangeValue,
		Buckets: []TimeBucket{{
			Label: "2d-1d",
			Start: rangeValue.Start,
			End:   rangeValue.End,
			Count: 1,
		}},
	}
	assertJSONContract(t, report, `{"attribute":"created","totalTasks":1,"range":{"start":"2026-08-28T02:05:06.000000789Z","end":"2026-08-29T02:05:06.000000789Z"},"buckets":[{"label":"2d-1d","start":"2026-08-28T02:05:06.000000789Z","end":"2026-08-29T02:05:06.000000789Z","count":1}]}`)
}

func TestSeriesOptionsRejectInvalidMetricRangeAndAsOf(t *testing.T) {
	asOf := time.Date(2026, time.August, 30, 0, 0, 0, 0, time.UTC)
	tests := []SeriesOptions{
		{Metric: "unknown", Range: RollingRange{StartDays: 7}, AsOf: asOf},
		{Metric: SeriesMetricCreated, Range: RollingRange{StartDays: 0, EndDays: 0}, AsOf: asOf},
		{Metric: SeriesMetricProgressed, Range: RollingRange{StartDays: -1, EndDays: 0}, AsOf: asOf},
		{Metric: SeriesMetricProgressed, Range: RollingRange{StartDays: 7, EndDays: 0}},
	}
	for _, options := range tests {
		if _, err := options.ResolveRange(); err == nil {
			t.Fatalf("ResolveRange() accepted %#v", options)
		}
	}
}

func assertJSONContract(t *testing.T, value any, want string) {
	t.Helper()
	got, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
