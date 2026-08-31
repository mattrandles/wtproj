package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/stats"
)

func TestStatsSeriesCLIIntegrationRendersPersistedCreatedAndProgressedData(t *testing.T) {
	p, storageRoot, asOf := newCLISeriesFixture(t)
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	before := storageManifest(t, storageRoot)

	if err := runStatsAt(ctx, []string{"created", "7d-0d"}, asOf); err != nil {
		t.Fatalf("runStatsAt(created text) error = %v", err)
	}
	wantText := "stats\nattribute: created\ntotalTasks: 3\nrange:\n" +
		"  start: 2026-08-23T12:34:56.789Z\n  end: 2026-08-30T12:34:56.789Z\n" +
		"buckets:\n" +
		"  7d-6d:\n    start: 2026-08-23T12:34:56.789Z\n    end: 2026-08-24T12:34:56.789Z\n    count: 1\n" +
		"  6d-5d:\n    start: 2026-08-24T12:34:56.789Z\n    end: 2026-08-25T12:34:56.789Z\n    count: 1\n" +
		"  5d-4d:\n    start: 2026-08-25T12:34:56.789Z\n    end: 2026-08-26T12:34:56.789Z\n    count: 0\n" +
		"  4d-3d:\n    start: 2026-08-26T12:34:56.789Z\n    end: 2026-08-27T12:34:56.789Z\n    count: 0\n" +
		"  3d-2d:\n    start: 2026-08-27T12:34:56.789Z\n    end: 2026-08-28T12:34:56.789Z\n    count: 0\n" +
		"  2d-1d:\n    start: 2026-08-28T12:34:56.789Z\n    end: 2026-08-29T12:34:56.789Z\n    count: 0\n" +
		"  1d-0d:\n    start: 2026-08-29T12:34:56.789Z\n    end: 2026-08-30T12:34:56.789Z\n    count: 1\n"
	if got := ctx.stdout.(*bytes.Buffer).String(); got != wantText {
		t.Fatalf("created text = %q, want %q", got, wantText)
	}
	assertSeriesStorageUnchanged(t, storageRoot, before)

	ctx.stdout.(*bytes.Buffer).Reset()
	if err := runStatsAt(ctx, []string{"--chart", "progressed", "7d-0d"}, asOf); err != nil {
		t.Fatalf("runStatsAt(progressed chart) error = %v", err)
	}
	wantChart := "stats chart\nmetric: progressed\nrange: 7d-0d\ntotalTasks: 2\nbuckets:\n" +
		"7d-6d │ 0\n6d-5d │ 0\n5d-4d │ 0\n4d-3d │ 0\n3d-2d │ 0\n2d-1d │ 0\n1d-0d │ " + strings.Repeat("█", 32) + " 2\n"
	if got := ctx.stdout.(*bytes.Buffer).String(); got != wantChart {
		t.Fatalf("progressed chart = %q, want %q", got, wantChart)
	}
	assertSeriesStorageUnchanged(t, storageRoot, before)

	for _, test := range []struct {
		name   string
		args   []string
		metric stats.SeriesMetric
		rangeV stats.RollingRange
		counts []int
		total  int
	}{
		{name: "created fourteen to seven", args: []string{"created", "14d-7d"}, metric: stats.SeriesMetricCreated, rangeV: stats.RollingRange{StartDays: 14, EndDays: 7}, counts: []int{1, 0, 0, 0, 1, 0, 0}, total: 2},
		{name: "progressed fourteen to seven", args: []string{"progressed", "14d-7d"}, metric: stats.SeriesMetricProgressed, rangeV: stats.RollingRange{StartDays: 14, EndDays: 7}, counts: []int{1, 0, 0, 0, 1, 0, 0}, total: 2},
		{name: "created one to zero", args: []string{"created", "1d-0d"}, metric: stats.SeriesMetricCreated, rangeV: stats.RollingRange{StartDays: 1, EndDays: 0}, counts: []int{1}, total: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() { ctx.jsonOut = false }()
			ctx.stdout.(*bytes.Buffer).Reset()
			ctx.jsonOut = true
			if err := runStatsAt(ctx, test.args, asOf); err != nil {
				t.Fatalf("runStatsAt(%v) error = %v", test.args, err)
			}
			var got stats.SeriesReport
			if err := json.Unmarshal(ctx.stdout.(*bytes.Buffer).Bytes(), &got); err != nil {
				t.Fatalf("decode series JSON: %v", err)
			}
			want := expectedCLISeriesReport(test.metric, test.rangeV, asOf, test.counts, test.total)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("series JSON = %#v, want complete report %#v", got, want)
			}
			assertSeriesStorageUnchanged(t, storageRoot, before)
		})
	}
}

func TestStatsSeriesCLIIntegrationRejectsInvalidRangesWithoutReadingOrMutatingStorage(t *testing.T) {
	p, storageRoot, asOf := newCLISeriesFixture(t)
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	before := storageManifest(t, storageRoot)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "malformed", args: []string{"created", "7-0d"}, want: "must match STARTd-ENDd"},
		{name: "reversed", args: []string{"created", "0d-7d"}, want: "start must be greater than end"},
		{name: "equal", args: []string{"created", "7d-7d"}, want: "start must be greater than end"},
		{name: "overflowing", args: []string{"created", "999999999999d-0d"}, want: "overflows time duration"},
		{name: "missing range", args: []string{"created"}, want: "requires STARTd-ENDd"},
		{name: "extra range", args: []string{"created", "7d-0d", "extra"}, want: "usage: wtp stats"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx.stdout.(*bytes.Buffer).Reset()
			err := runStatsAt(ctx, test.args, asOf)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runStatsAt(%v) error = %v, want containing %q", test.args, err, test.want)
			}
			if got := ctx.stdout.(*bytes.Buffer).String(); got != "" {
				t.Fatalf("rejected range wrote stdout %q", got)
			}
			assertSeriesStorageUnchanged(t, storageRoot, before)
		})
	}
}

func newCLISeriesFixture(t *testing.T) (provider.Provider, string, time.Time) {
	t.Helper()
	root := t.TempDir()
	p, err := providerForInvocation(root)
	if err != nil {
		t.Fatalf("providerForInvocation() error = %v", err)
	}
	asOf := time.Date(2026, time.August, 30, 12, 34, 56, 789000000, time.UTC)
	tasks := cliSeriesTasks(asOf)
	for _, task := range tasks {
		writeCLISeriesJSON(t, filepath.Join(root, ".wtp", string(task.Status), task.ShortID+".json"), task)
	}
	handoffs := struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}{Handoffs: []core.Handoff{{
		ID:        "00000000-0000-4000-8000-000000000099",
		Message:   "stats integration handoff",
		CreatedAt: asOf,
	}}}
	writeCLISeriesJSON(t, filepath.Join(root, ".wtp", "handoffs.json"), handoffs)
	return p, filepath.Join(root, ".wtp"), asOf
}

func cliSeriesTasks(asOf time.Time) []core.Task {
	return []core.Task{
		cliSeriesTask(1, "exact seven-day start", asOf.Add(-7*24*time.Hour), asOf.Add(-24*time.Hour)),
		cliSeriesTask(2, "exact fourteen-day start", asOf.Add(-14*24*time.Hour), asOf.Add(-14*24*time.Hour)),
		cliSeriesTask(3, "fourteen-day middle", asOf.Add(-10*24*time.Hour), asOf.Add(-10*24*time.Hour)),
		cliSeriesTask(4, "different created and progressed buckets", asOf.Add(-6*24*time.Hour), asOf.Add(-24*time.Hour)),
		cliSeriesTask(5, "one-day start", asOf.Add(-24*time.Hour), asOf),
		cliSeriesTask(6, "exact range end", asOf, asOf),
		cliSeriesTask(7, "outside range", asOf.Add(-15*24*time.Hour), asOf.Add(-15*24*time.Hour)),
		cliSeriesTask(8, "future value", asOf.Add(24*time.Hour), asOf.Add(24*time.Hour)),
	}
}

func cliSeriesTask(number int, title string, createdAt, updatedAt time.Time) core.Task {
	return core.Task{
		ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", number), ShortID: fmt.Sprintf("wtp-%04d", number),
		Title: title, Status: core.StatusTodo, Dependencies: []string{}, Comments: []core.Comment{},
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func writeCLISeriesJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func expectedCLISeriesReport(metric stats.SeriesMetric, rangeV stats.RollingRange, asOf time.Time, counts []int, total int) stats.SeriesReport {
	rangeValue := stats.TimeRange{Start: asOf.Add(-time.Duration(rangeV.StartDays) * 24 * time.Hour), End: asOf.Add(-time.Duration(rangeV.EndDays) * 24 * time.Hour)}
	buckets := make([]stats.TimeBucket, len(counts))
	for index, count := range counts {
		start := rangeValue.Start.Add(time.Duration(index) * 24 * time.Hour)
		buckets[index] = stats.TimeBucket{Label: fmt.Sprintf("%dd-%dd", rangeV.StartDays-index, rangeV.StartDays-index-1), Start: start, End: start.Add(24 * time.Hour), Count: count}
	}
	return stats.SeriesReport{Attribute: metric, TotalTasks: total, Range: rangeValue, Buckets: buckets}
}

func assertSeriesStorageUnchanged(t *testing.T, root, before string) {
	t.Helper()
	if after := storageManifest(t, root); after != before {
		t.Fatalf("stats read changed flat-file storage:\nbefore %s\nafter %s", before, after)
	}
}
