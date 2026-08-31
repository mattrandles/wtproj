package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/stats"
)

func TestParseStatsArgsAcceptsAllGroupingSelectorsBeforePositionals(t *testing.T) {
	args := []string{
		"--chart",
		"--issue-id", "ISSUE-42",
		"--project=Apollo",
		"--milestone", "MVP",
		"--version=v1",
		"--feature-id", "FEAT-7",
		"--feature=Search",
		"done", "feature",
	}
	got, err := parseStatsArgs(args)
	if err != nil {
		t.Fatalf("parseStatsArgs() error = %v", err)
	}
	wantGrouping := core.GroupingFilter{
		IssueID: "ISSUE-42", Project: "Apollo", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-7", Feature: "Search",
	}
	if got.status == nil || *got.status != core.StatusDone || got.attribute != stats.AttributeFeature || !got.chart || got.grouping != wantGrouping {
		t.Fatalf("stats query = %#v, want done/feature/chart/grouping %#v", got, wantGrouping)
	}

	series, err := parseStatsArgs([]string{"--project", "Apollo", "created", "7d-0d"})
	if err != nil {
		t.Fatalf("parseStatsArgs(series) error = %v", err)
	}
	if series.metric != stats.SeriesMetricCreated || series.grouping != (core.GroupingFilter{Project: "Apollo"}) {
		t.Fatalf("series query = %#v, want created with project grouping", series)
	}
}

func TestParseStatsArgsPreservesStatusFirstAmbiguityWithGrouping(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "feature", Category: core.StatusCategoryWaiting},
		{Name: "created", Category: core.StatusCategoryFailed},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}

	query, err := parseStatsArgs([]string{"--project", "Apollo", "feature"}, catalog)
	if err != nil {
		t.Fatalf("status-first query error = %v", err)
	}
	if query.status == nil || *query.status != "feature" || !query.isOverview() {
		t.Fatalf("ambiguous query = %#v, want custom status overview", query)
	}

	query, err = parseStatsArgs([]string{"--project", "Apollo", "feature", "feature"}, catalog)
	if err != nil {
		t.Fatalf("focused status-first query error = %v", err)
	}
	if query.status == nil || *query.status != "feature" || query.attribute != stats.AttributeFeature {
		t.Fatalf("focused ambiguous query = %#v, want custom status plus feature", query)
	}

	if _, err := parseStatsArgs([]string{"--project", "Apollo", "created", "7d-0d"}, catalog); err == nil || !strings.Contains(err.Error(), "unknown stats attribute") {
		t.Fatalf("series ambiguity error = %v, want status-first rejection", err)
	}
}

func TestRunStatsGroupingUpdatesOverviewFocusedScalarAndSeries(t *testing.T) {
	asOf := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)

	t.Run("overview JSON and text expose dimensions", func(t *testing.T) {
		p := groupedStatsProvider(asOf)
		var stdout bytes.Buffer
		ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}
		if err := runStatsAt(ctx, []string{"--project", "Apollo"}, asOf); err != nil {
			t.Fatalf("runStatsAt(overview JSON) error = %v", err)
		}
		var report stats.Report
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode overview JSON: %v", err)
		}
		if report.TotalTasks != 2 || !reflect.DeepEqual(report.Attributes.Feature, []stats.Bucket{{Value: "", Count: 1}, {Value: "Search", Count: 1}}) {
			t.Fatalf("grouped overview = %#v, want two tasks and search/unset feature buckets", report)
		}
		if p.lastFilter.Grouping != (core.GroupingFilter{Project: "Apollo"}) {
			t.Fatalf("overview grouping filter = %#v", p.lastFilter.Grouping)
		}

		stdout.Reset()
		ctx.jsonOut = false
		if err := runStatsAt(ctx, []string{"--project", "Apollo"}, asOf); err != nil {
			t.Fatalf("runStatsAt(overview text) error = %v", err)
		}
		for _, needle := range []string{"issueId:", "project:", "milestone:", "version:", "featureId:", "feature:", "(unset): 1"} {
			if !strings.Contains(stdout.String(), needle) {
				t.Fatalf("grouped overview text missing %q: %s", needle, stdout.String())
			}
		}
	})

	t.Run("combined status and all grouping selectors filter focused scalar", func(t *testing.T) {
		p := groupedStatsProvider(asOf)
		var stdout bytes.Buffer
		ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}
		args := []string{
			"--issue-id", "issue-42", "--project", "apollo", "--milestone", "mvp", "--version", "v1",
			"--feature-id", "feat-7", "--feature", "search", "done", "comments",
		}
		if err := runStatsAt(ctx, args, asOf); err != nil {
			t.Fatalf("runStatsAt(combined scalar) error = %v", err)
		}
		var report stats.FocusedReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode focused JSON: %v", err)
		}
		if report.Status != "done" || report.TotalTasks != 1 || report.Comments == nil || report.Comments.TotalRecords != 1 {
			t.Fatalf("combined focused report = %#v, want one done task with one comment", report)
		}
		wantFilter := core.GroupingFilter{IssueID: "issue-42", Project: "apollo", Milestone: "mvp", Version: "v1", FeatureID: "feat-7", Feature: "search"}
		if p.lastFilter.Status == nil || *p.lastFilter.Status != core.StatusDone || p.lastFilter.Grouping != wantFilter {
			t.Fatalf("combined task filter = %#v, want done and %#v", p.lastFilter, wantFilter)
		}
	})

	t.Run("grouped series", func(t *testing.T) {
		p := groupedStatsProvider(asOf)
		var stdout bytes.Buffer
		ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}
		if err := runStatsAt(ctx, []string{"--project", "Apollo", "created", "3d-0d"}, asOf); err != nil {
			t.Fatalf("runStatsAt(series) error = %v", err)
		}
		var report stats.SeriesReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode series JSON: %v", err)
		}
		if report.TotalTasks != 2 || p.lastFilter.Grouping != (core.GroupingFilter{Project: "Apollo"}) {
			t.Fatalf("grouped series = %#v, filter %#v; want two tasks and project filter", report, p.lastFilter.Grouping)
		}
	})
}

func TestRunStatsGroupingSupportsFocusedAndSeriesCharts(t *testing.T) {
	asOf := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)

	t.Run("focused unset bucket", func(t *testing.T) {
		p := groupedStatsProvider(asOf)
		var stdout bytes.Buffer
		ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
		if err := runStatsAt(ctx, []string{"--chart", "--project", "Apollo", "feature"}, asOf); err != nil {
			t.Fatalf("runStatsAt(focused chart) error = %v", err)
		}
		output := stdout.String()
		for _, needle := range []string{"metric: feature", "(unset) │", "Search │"} {
			if !strings.Contains(output, needle) {
				t.Fatalf("focused chart missing %q: %s", needle, output)
			}
		}
	})

	t.Run("series", func(t *testing.T) {
		p := groupedStatsProvider(asOf)
		var stdout bytes.Buffer
		ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
		if err := runStatsAt(ctx, []string{"--chart", "--project", "Apollo", "created", "3d-0d"}, asOf); err != nil {
			t.Fatalf("runStatsAt(series chart) error = %v", err)
		}
		if !strings.Contains(stdout.String(), "metric: created\n") || !strings.Contains(stdout.String(), "totalTasks: 2\n") {
			t.Fatalf("series chart = %q", stdout.String())
		}
		if p.lastFilter.Grouping != (core.GroupingFilter{Project: "Apollo"}) {
			t.Fatalf("series chart grouping filter = %#v", p.lastFilter.Grouping)
		}
	})
}

func TestParseStatsArgsRejectsGroupingSelectorsAfterPositionals(t *testing.T) {
	for _, args := range [][]string{
		{"done", "model", "--project", "Apollo"},
		{"--chart", "done", "--feature", "Search", "model"},
		{"--project", "Apollo", "--project", "Zeus", "model"},
		{"--project", "Apollo", "model", "--chart"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			p := &statsTestProvider{}
			var stdout bytes.Buffer
			err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, args)
			if err == nil {
				t.Fatalf("runStats(%v) unexpectedly succeeded", args)
			}
			if p.listCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("invalid grouping query changed provider/output: list=%d stdout=%q", p.listCalls, stdout.String())
			}
		})
	}
}

func groupedStatsProvider(asOf time.Time) *statsTestProvider {
	return &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{
		{Task: core.Task{
			ID: "done-match", Status: core.StatusDone, IssueID: "ISSUE-42", Project: "Apollo", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-7", Feature: "Search",
			Model: "gpt-5", Comments: []core.Comment{{}}, CreatedAt: asOf.Add(-24 * time.Hour), UpdatedAt: asOf.Add(-24 * time.Hour), Dependencies: []string{},
		}},
		{Task: core.Task{
			ID: "todo-unset", Status: core.StatusTodo, IssueID: "ISSUE-99", Project: "Apollo", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-9", Feature: "",
			CreatedAt: asOf.Add(-48 * time.Hour), UpdatedAt: asOf.Add(-48 * time.Hour), Dependencies: []string{},
		}},
		{Task: core.Task{
			ID: "done-outside", Status: core.StatusDone, IssueID: "ISSUE-42", Project: "Zeus", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-7", Feature: "Search",
			CreatedAt: asOf.Add(-24 * time.Hour), UpdatedAt: asOf.Add(-24 * time.Hour), Dependencies: []string{},
		}},
	}}}
}

var _ provider.Provider = (*statsTestProvider)(nil)
