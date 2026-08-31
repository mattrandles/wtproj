package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/stats"
)

func TestParseStatsArgsAcceptsChartBeforeFocusedSelectors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want statsQuery
	}{
		{name: "status", args: []string{"--chart", "status"}, want: statsQuery{attribute: stats.AttributeStatus, chart: true}},
		{name: "model", args: []string{"--chart", "model"}, want: statsQuery{attribute: stats.AttributeModel, chart: true}},
		{name: "filtered model", args: []string{"--chart", "done", "model"}, want: statsQuery{status: statusPointer(core.StatusDone), attribute: stats.AttributeModel, chart: true}},
		{name: "created", args: []string{"--chart", "created", "7d-0d"}, want: statsQuery{metric: stats.SeriesMetricCreated, rangeSpec: stats.RollingRange{StartDays: 7, EndDays: 0}, chart: true}},
		{name: "progressed", args: []string{"--chart", "progressed", "14d-7d"}, want: statsQuery{metric: stats.SeriesMetricProgressed, rangeSpec: stats.RollingRange{StartDays: 14, EndDays: 7}, chart: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseStatsArgs(test.args)
			if err != nil {
				t.Fatalf("parseStatsArgs() error = %v", err)
			}
			if got.status != nil && test.want.status != nil && *got.status != *test.want.status {
				t.Fatalf("status = %q, want %q", *got.status, *test.want.status)
			}
			if got.status == nil && test.want.status != nil || got.status != nil && test.want.status == nil {
				t.Fatalf("status = %#v, want %#v", got.status, test.want.status)
			}
			got.status, test.want.status = nil, nil
			if got != test.want {
				t.Fatalf("parseStatsArgs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRunStatsChartFocusedPreservesOrderAndStatusFilter(t *testing.T) {
	p := mixedStatsProvider()
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := runStatsAt(ctx, []string{"--chart", "done", "model"}, time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runStatsAt() error = %v", err)
	}
	want := "stats chart\nmetric: model\nstatus: done\ntotalTasks: 1\nbuckets:\n" +
		"zeta │ ████████████████████████████████ 1\n"
	if stdout.String() != want {
		t.Fatalf("chart output = %q, want %q", stdout.String(), want)
	}
	if p.lastFilter.Status == nil || *p.lastFilter.Status != core.StatusDone {
		t.Fatalf("task filter = %#v, want done", p.lastFilter)
	}
	if p.handoffCalls != 0 {
		t.Fatalf("handoff calls = %d, want 0", p.handoffCalls)
	}
}

func TestRunStatsChartRendersZeroDataWithoutBars(t *testing.T) {
	p := &statsTestProvider{}
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := runStatsAt(ctx, []string{"--chart", "status"}, time.Now().UTC()); err != nil {
		t.Fatalf("runStatsAt() error = %v", err)
	}
	for _, line := range []string{
		"stats chart\n",
		"metric: status\n",
		"totalTasks: 0\n",
		"todo │ 0\n",
		"inProgress │ 0\n",
		"paused │ 0\n",
		"done │ 0\n",
	} {
		if !strings.Contains(stdout.String(), line) {
			t.Fatalf("chart output missing %q in %q", line, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "█") {
		t.Fatalf("zero-data chart contains bars: %q", stdout.String())
	}
}

func TestRunStatsChartRendersSeriesRangeAndBucketsInOrder(t *testing.T) {
	asOf := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	p := &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{
		{Task: core.Task{ID: "old", CreatedAt: asOf.Add(-7 * 24 * time.Hour), UpdatedAt: asOf.Add(-7 * 24 * time.Hour)}},
		{Task: core.Task{ID: "new", CreatedAt: asOf.Add(-time.Hour), UpdatedAt: asOf.Add(-time.Hour)}},
	}}}
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := runStatsAt(ctx, []string{"--chart", "created", "7d-0d"}, asOf); err != nil {
		t.Fatalf("runStatsAt() error = %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"metric: created\n", "range: 7d-0d\n", "totalTasks: 2\n", "7d-6d │", "1d-0d │"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("series chart missing %q in %q", needle, output)
		}
	}
	if strings.Index(output, "7d-6d │") > strings.Index(output, "1d-0d │") {
		t.Fatalf("series buckets reordered: %q", output)
	}
	if p.handoffCalls != 0 {
		t.Fatalf("handoff calls = %d, want 0", p.handoffCalls)
	}
}

func TestRunStatsChartRejectsEveryConflict(t *testing.T) {
	tests := []struct {
		name string
		args []string
		json bool
		want string
	}{
		{name: "missing selector", args: []string{"--chart"}, want: "requires a focused selector"},
		{name: "overview status", args: []string{"--chart", "done"}, want: "requires a focused selector"},
		{name: "comments scalar", args: []string{"--chart", "comments"}, want: "does not support scalar attribute"},
		{name: "dependencies scalar", args: []string{"--chart", "dependencies"}, want: "does not support scalar attribute"},
		{name: "misplaced", args: []string{"model", "--chart"}, want: "must appear before selectors"},
		{name: "duplicate", args: []string{"--chart", "--chart", "model"}, want: "only once"},
		{name: "root json", args: []string{"--chart", "model"}, json: true, want: "cannot be combined with root --json"},
		{name: "missing series range", args: []string{"--chart", "created"}, want: "requires STARTd-ENDd"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &statsTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runStats(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: test.json}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runStats(%v) error = %v, want containing %q", test.args, err, test.want)
			}
			if p.listCalls != 0 {
				t.Fatalf("ListTasks calls = %d, want 0", p.listCalls)
			}
		})
	}
}
