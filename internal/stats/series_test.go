package stats

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestParseRollingRangeRejectsInvalidBounds(t *testing.T) {
	for _, value := range []string{
		"",
		"7d",
		"7-0d",
		"7d-0",
		"7d-0d-extra",
		"7d-0d-1d",
		"-1d-0d",
		"7d--1d",
		"7.5d-0d",
		"7d-0.5d",
		"7d-7d",
		"0d-7d",
		"999999999999999999999999d-0d",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseRollingRange(value); err == nil {
				t.Fatalf("ParseRollingRange(%q) accepted invalid range", value)
			}
		})
	}
}

func TestParseRollingRangeCanonicalizesWholeDayBounds(t *testing.T) {
	rangeValue, err := ParseRollingRange("07d-00d")
	if err != nil {
		t.Fatalf("ParseRollingRange() error = %v", err)
	}
	if rangeValue != (RollingRange{StartDays: 7, EndDays: 0}) {
		t.Fatalf("ParseRollingRange() = %#v", rangeValue)
	}
}

func TestAggregateSeriesCreatedBuildsOrderedHalfOpenBuckets(t *testing.T) {
	asOf := time.Date(2026, 8, 30, 12, 0, 0, 500, time.FixedZone("BST", 2*60*60))
	start := asOf.UTC().Add(-7 * day)
	tasks := make([]core.TaskView, 0, 10)
	for index := 0; index < 7; index++ {
		timestamp := start.Add(time.Duration(index) * day)
		tasks = append(tasks, seriesTask(index, timestamp, timestamp))
	}
	// The end boundary belongs to no bucket, while the start boundary is
	// included in the oldest bucket.
	tasks = append(tasks,
		seriesTask(7, asOf.UTC(), asOf.UTC()),
		seriesTask(8, start.Add(-time.Nanosecond), start.Add(-time.Nanosecond)),
		seriesTask(9, asOf.UTC().Add(time.Hour), asOf.UTC().Add(time.Hour)),
	)

	report, err := AggregateSeries(fakeProvider{tasks: tasks, listHandoffsErr: errors.New("handoffs must not be loaded")}, SeriesOptions{
		Metric: SeriesMetricCreated,
		Range:  RollingRange{StartDays: 7, EndDays: 0},
		AsOf:   asOf,
	})
	if err != nil {
		t.Fatalf("AggregateSeries() error = %v", err)
	}
	wantCounts := []int{1, 1, 1, 1, 1, 1, 1}
	if report.TotalTasks != 7 || report.Range.Start != start || report.Range.End != asOf.UTC() {
		t.Fatalf("report range/total = %#v, want range [%v, %v) and total 7", report, start, asOf.UTC())
	}
	if len(report.Buckets) != 7 {
		t.Fatalf("bucket count = %d, want 7", len(report.Buckets))
	}
	for index, bucket := range report.Buckets {
		wantStart := start.Add(time.Duration(index) * day)
		want := TimeBucket{
			Label: (RollingRange{StartDays: 7 - index, EndDays: 6 - index}).String(),
			Start: wantStart,
			End:   wantStart.Add(day),
			Count: wantCounts[index],
		}
		if !reflect.DeepEqual(bucket, want) {
			t.Errorf("bucket %d = %#v, want %#v", index, bucket, want)
		}
	}
}

func TestAggregateSeriesProgressedUsesLatestUpdatedAtOnce(t *testing.T) {
	asOf := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	old := asOf.Add(-2 * day)
	latest := asOf.Add(-time.Hour)
	tasks := []core.TaskView{
		seriesTask(1, asOf.Add(-10*day), old),
		// A duplicate current view must still represent one task, located by
		// its latest update in the newest bucket.
		seriesTask(1, asOf.Add(-10*day), latest),
		seriesTask(2, asOf.Add(-time.Hour), asOf.Add(-time.Hour)),
		seriesTask(3, asOf.Add(-time.Hour), asOf),
	}

	report, err := AggregateSeries(fakeProvider{tasks: tasks}, SeriesOptions{
		Metric: SeriesMetricProgressed,
		Range:  RollingRange{StartDays: 3, EndDays: 1},
		AsOf:   asOf,
	})
	if err != nil {
		t.Fatalf("AggregateSeries() error = %v", err)
	}
	want := []TimeBucket{
		{Label: "3d-2d", Start: asOf.Add(-3 * day), End: asOf.Add(-2 * day), Count: 0},
		{Label: "2d-1d", Start: asOf.Add(-2 * day), End: asOf.Add(-day), Count: 0},
	}
	// The selected range ends at asOf-24h, so both latest updates are outside
	// it. This also verifies the nonzero end offset and end half-open bound.
	if !reflect.DeepEqual(report.Buckets, want) || report.TotalTasks != 0 {
		t.Fatalf("progressed report = %#v, want empty two-bucket range", report)
	}

	report, err = AggregateSeries(fakeProvider{tasks: tasks[:2]}, SeriesOptions{
		Metric: SeriesMetricProgressed,
		Range:  RollingRange{StartDays: 2, EndDays: 0},
		AsOf:   asOf,
	})
	if err != nil {
		t.Fatalf("AggregateSeries() second call error = %v", err)
	}
	if report.TotalTasks != 1 || report.Buckets[0].Count != 0 || report.Buckets[1].Count != 1 {
		t.Fatalf("progressed latest timestamp = %#v, want one newest-bucket task", report)
	}
}

func TestAggregateSeriesEmptyStoreAndProviderError(t *testing.T) {
	asOf := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	listTasksCalls := 0
	listHandoffsCalls := 0
	provider := focusedProviderSpy{
		fakeProvider: fakeProvider{
			listTasksCalls:    &listTasksCalls,
			listHandoffsCalls: &listHandoffsCalls,
			listHandoffsErr:   errors.New("handoffs must not be loaded"),
		},
	}
	report, err := AggregateSeries(&provider, SeriesOptions{
		Metric: SeriesMetricCreated,
		Range:  RollingRange{StartDays: 2, EndDays: 0},
		AsOf:   asOf,
	})
	if err != nil {
		t.Fatalf("AggregateSeries() empty error = %v", err)
	}
	if report.TotalTasks != 0 || len(report.Buckets) != 2 || report.Buckets[0].Count != 0 || report.Buckets[1].Count != 0 {
		t.Fatalf("empty series report = %#v", report)
	}
	if listTasksCalls != 1 || listHandoffsCalls != 0 {
		t.Fatalf("provider calls = tasks %d, handoffs %d; want 1, 0", listTasksCalls, listHandoffsCalls)
	}

	_, err = AggregateSeries(fakeProvider{listTasksErr: errors.New("tasks unavailable")}, SeriesOptions{
		Metric: SeriesMetricCreated,
		Range:  RollingRange{StartDays: 1, EndDays: 0},
		AsOf:   asOf,
	})
	if err == nil || !strings.Contains(err.Error(), "tasks unavailable") {
		t.Fatalf("AggregateSeries() error = %v, want provider error", err)
	}
}

func seriesTask(number int, createdAt, updatedAt time.Time) core.TaskView {
	return core.TaskView{Task: core.Task{
		ID:        "series-task-" + strconv.Itoa(number),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}}
}
