package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/stats"
)

func TestRunStatsFocusedStatusAndModelContracts(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		jsonOut  bool
		chart    bool
		wantText string
		want     stats.FocusedReport
	}{
		{
			name: "status plain",
			args: []string{"status"},
			wantText: "stats\nstatus: all\ntotalTasks: 4\nattribute: status\n" +
				"buckets:\n  todo: 1\n  inProgress: 1\n  paused: 0\n  done: 2\n",
			want: stats.FocusedReport{TotalTasks: 4, Attribute: stats.AttributeStatus, Buckets: bucketsPointer([]stats.Bucket{
				{Value: "todo", Count: 1}, {Value: "inProgress", Count: 1}, {Value: "paused", Count: 0}, {Value: "done", Count: 2},
			})},
		},
		{
			name:  "status chart",
			args:  []string{"--chart", "status"},
			chart: true,
			wantText: "stats chart\nmetric: status\ntotalTasks: 4\nbuckets:\n" +
				"      todo │ " + strings.Repeat("█", 16) + " 1\n" +
				"inProgress │ " + strings.Repeat("█", 16) + " 1\n" +
				"    paused │ 0\n" +
				"      done │ " + strings.Repeat("█", 32) + " 2\n",
		},
		{
			name:    "status JSON",
			args:    []string{"status"},
			jsonOut: true,
			want: stats.FocusedReport{TotalTasks: 4, Attribute: stats.AttributeStatus, Buckets: bucketsPointer([]stats.Bucket{
				{Value: "todo", Count: 1}, {Value: "inProgress", Count: 1}, {Value: "paused", Count: 0}, {Value: "done", Count: 2},
			})},
		},
		{
			name: "model plain",
			args: []string{"model"},
			wantText: "stats\nstatus: all\ntotalTasks: 4\nattribute: model\n" +
				"buckets:\n  (unset): 1\n  alpha: 2\n  zeta: 1\n",
			want: stats.FocusedReport{TotalTasks: 4, Attribute: stats.AttributeModel, Buckets: bucketsPointer([]stats.Bucket{
				{Value: "", Count: 1}, {Value: "alpha", Count: 2}, {Value: "zeta", Count: 1},
			})},
		},
		{
			name:  "model chart",
			args:  []string{"--chart", "model"},
			chart: true,
			wantText: "stats chart\nmetric: model\ntotalTasks: 4\nbuckets:\n" +
				"(unset) │ ████████████████ 1\n" +
				"  alpha │ ████████████████████████████████ 2\n" +
				"   zeta │ ████████████████ 1\n",
		},
		{
			name:    "model JSON",
			args:    []string{"model"},
			jsonOut: true,
			want: stats.FocusedReport{TotalTasks: 4, Attribute: stats.AttributeModel, Buckets: bucketsPointer([]stats.Bucket{
				{Value: "", Count: 1}, {Value: "alpha", Count: 2}, {Value: "zeta", Count: 1},
			})},
		},
		{
			name: "filtered model plain",
			args: []string{"done", "model"},
			wantText: "stats\nstatus: done\ntotalTasks: 2\nattribute: model\n" +
				"buckets:\n  alpha: 2\n",
			want: stats.FocusedReport{Status: "done", TotalTasks: 2, Attribute: stats.AttributeModel, Buckets: bucketsPointer([]stats.Bucket{{Value: "alpha", Count: 2}})},
		},
		{
			name:  "filtered model chart",
			args:  []string{"--chart", "done", "model"},
			chart: true,
			wantText: "stats chart\nmetric: model\nstatus: done\ntotalTasks: 2\nbuckets:\n" +
				"alpha │ ████████████████████████████████ 2\n",
		},
		{
			name:    "filtered model JSON",
			args:    []string{"done", "model"},
			jsonOut: true,
			want:    stats.FocusedReport{Status: "done", TotalTasks: 2, Attribute: stats.AttributeModel, Buckets: bucketsPointer([]stats.Bucket{{Value: "alpha", Count: 2}})},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := focusedStatsFixture()
			var stdout, stderr bytes.Buffer
			ctx := context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: test.jsonOut}
			if err := runStats(ctx, test.args); err != nil {
				t.Fatalf("runStats(%v) error = %v", test.args, err)
			}
			if test.chart {
				if got := stdout.String(); got != test.wantText {
					t.Fatalf("chart output = %q, want %q", got, test.wantText)
				}
			} else if test.jsonOut {
				assertFocusedCLIJSON(t, stdout.Bytes(), test.want)
			} else if got := stdout.String(); got != test.wantText {
				t.Fatalf("text output = %q, want %q", got, test.wantText)
			}
			if p.handoffCalls != 0 {
				t.Fatalf("focused query requested %d handoffs", p.handoffCalls)
			}
		})
	}
}

func TestRunStatsFocusedCustomCatalogAndStatusFirstAmbiguity(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "model", Category: core.StatusCategoryWaiting},
		{Name: "created", Category: core.StatusCategoryFailed},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	p := &statsTestProvider{graphTestProvider: graphTestProvider{
		catalog: catalog,
		tasks:   []core.TaskView{{Task: core.Task{Status: core.StatusDone, Model: "alpha"}}},
	}}

	query, err := parseStatsArgs([]string{"model"}, catalog)
	if err != nil {
		t.Fatalf("parseStatsArgs(model) error = %v", err)
	}
	if query.status == nil || *query.status != "model" || !query.isOverview() {
		t.Fatalf("ambiguous model query = %#v, want custom status overview", query)
	}
	query, err = parseStatsArgs([]string{"model", "model"}, catalog)
	if err != nil {
		t.Fatalf("parseStatsArgs(model model) error = %v", err)
	}
	if query.status == nil || *query.status != "model" || query.attribute != stats.AttributeModel {
		t.Fatalf("ambiguous focused query = %#v, want custom status plus model", query)
	}
	if query, err = parseStatsArgs([]string{"created"}, catalog); err != nil || query.status == nil || *query.status != "created" {
		t.Fatalf("created status-first query = %#v, error %v", query, err)
	}
	if _, err := parseStatsArgs([]string{"created", "7d-0d"}, catalog); err == nil || !strings.Contains(err.Error(), "unknown stats attribute") {
		t.Fatalf("created series ambiguity error = %v, want status-first rejection", err)
	}

	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}
	if err := runStats(ctx, []string{"status"}); err != nil {
		t.Fatalf("custom status focused stats error = %v", err)
	}
	assertFocusedCLIJSON(t, stdout.Bytes(), stats.FocusedReport{TotalTasks: 1, Attribute: stats.AttributeStatus, Buckets: bucketsPointer([]stats.Bucket{
		{Value: "todo", Count: 0}, {Value: "inProgress", Count: 0}, {Value: "paused", Count: 0}, {Value: "done", Count: 1}, {Value: "model", Count: 0}, {Value: "created", Count: 0},
	})})
	if p.handoffCalls != 0 {
		t.Fatalf("custom focused query requested %d handoffs", p.handoffCalls)
	}

	stdout.Reset()
	if err := runStats(ctx, []string{"model", "model"}); err != nil {
		t.Fatalf("custom filtered model stats error = %v", err)
	}
	assertFocusedCLIJSON(t, stdout.Bytes(), stats.FocusedReport{Status: "model", TotalTasks: 0, Attribute: stats.AttributeModel, Buckets: bucketsPointer([]stats.Bucket{})})
	if p.lastFilter.Status == nil || *p.lastFilter.Status != "model" {
		t.Fatalf("custom filter = %#v, want model", p.lastFilter)
	}
}

func TestRunStatsFocusedLongLabelsArePreservedAndSorted(t *testing.T) {
	longLabel := "model-" + strings.Repeat("long-", 18) + "label"
	p := &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{
		{Task: core.Task{Status: core.StatusTodo, Model: "zeta"}},
		{Task: core.Task{Status: core.StatusTodo, Model: longLabel}},
		{Task: core.Task{Status: core.StatusTodo, Model: "alpha"}},
		{Task: core.Task{Status: core.StatusTodo, Model: longLabel}},
		{Task: core.Task{Status: core.StatusTodo, Model: ""}},
	}}}
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := runStats(ctx, []string{"model"}); err != nil {
		t.Fatalf("runStats(model) error = %v", err)
	}
	wantText := "stats\nstatus: all\ntotalTasks: 5\nattribute: model\nbuckets:\n" +
		"  (unset): 1\n  alpha: 1\n  " + longLabel + ": 2\n  zeta: 1\n"
	if stdout.String() != wantText {
		t.Fatalf("long-label text = %q, want %q", stdout.String(), wantText)
	}
	stdout.Reset()
	if err := runStats(ctx, []string{"--chart", "model"}); err != nil {
		t.Fatalf("runStats(--chart model) error = %v", err)
	}
	chart := stdout.String()
	for _, needle := range []string{longLabel + " │ ", "(unset) │ ", "alpha │", "zeta │"} {
		if !strings.Contains(chart, needle) {
			t.Fatalf("long-label chart missing %q in %q", needle, chart)
		}
	}
	if strings.Index(chart, "(unset)") > strings.Index(chart, "alpha") || strings.Index(chart, "alpha") > strings.Index(chart, longLabel) || strings.Index(chart, longLabel) > strings.Index(chart, "zeta") {
		t.Fatalf("long-label chart order = %q", chart)
	}
	if p.handoffCalls != 0 {
		t.Fatalf("long-label query requested %d handoffs", p.handoffCalls)
	}
}

func TestRunStatsFocusedEmptyStoreKeepsEmptyBucketsAndNoHandoffs(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		json bool
	}{
		{name: "status text", args: []string{"status"}},
		{name: "status JSON", args: []string{"status"}, json: true},
		{name: "status chart", args: []string{"--chart", "status"}},
		{name: "model text", args: []string{"model"}},
		{name: "model JSON", args: []string{"model"}, json: true},
		{name: "model chart", args: []string{"--chart", "model"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &statsTestProvider{}
			var stdout bytes.Buffer
			if err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: test.json}, test.args); err != nil {
				t.Fatalf("runStats(%v) error = %v", test.args, err)
			}
			if test.json {
				var report stats.FocusedReport
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatalf("decode focused JSON: %v", err)
				}
				if report.TotalTasks != 0 || report.Buckets == nil {
					t.Fatalf("empty focused report = %#v", report)
				}
				wantBuckets := 0
				if report.Attribute == stats.AttributeStatus {
					wantBuckets = 4
				}
				if len(*report.Buckets) != wantBuckets {
					t.Fatalf("empty focused %s buckets = %#v, want length %d", report.Attribute, *report.Buckets, wantBuckets)
				}
			}
			if p.handoffCalls != 0 {
				t.Fatalf("empty focused query requested %d handoffs", p.handoffCalls)
			}
		})
	}
}

func TestRunStatsFocusedProviderFailureDoesNotWriteOrLoadHandoffs(t *testing.T) {
	p := &focusedStatsErrorProvider{statsTestProvider: &statsTestProvider{}, listTasksErr: errors.New("tasks unavailable")}
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{{"status"}, {"model"}, {"done", "model"}, {"--chart", "model"}} {
		stdout.Reset()
		err := runStats(context{provider: p, stdout: &stdout, stderr: &stderr}, args)
		if err == nil || !strings.Contains(err.Error(), "tasks unavailable") {
			t.Fatalf("runStats(%v) error = %v, want provider error", args, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("runStats(%v) wrote %q on provider error", args, stdout.String())
		}
	}
	if p.handoffCalls != 0 {
		t.Fatalf("provider failure requested %d handoffs", p.handoffCalls)
	}
}

func TestRunStatsFocusedRejectsMalformedAndExcessArgumentsBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown selector", args: []string{"bogus"}, want: "must be a status or attribute"},
		{name: "reversed status", args: []string{"model", "done"}, want: "must precede"},
		{name: "unknown focused attribute", args: []string{"done", "bogus"}, want: "unknown stats attribute"},
		{name: "excess focused arguments", args: []string{"done", "model", "extra"}, want: statsUsage},
		{name: "excess arbitrary arguments", args: []string{"status", "model", "extra", "more"}, want: statsUsage},
		{name: "status before series", args: []string{"done", "created", "7d-0d"}, want: "does not accept a status filter"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &statsTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runStats(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runStats(%v) error = %v, want containing %q", test.args, err, test.want)
			}
			if p.listCalls != 0 || p.handoffCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("invalid query changed provider/output: list=%d handoffs=%d stdout=%q", p.listCalls, p.handoffCalls, stdout.String())
			}
		})
	}
}

func TestRunStatsLegacyOverviewAndFocusedShapesRemainStable(t *testing.T) {
	p := &statsTestProvider{}
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := runStats(ctx, nil); err != nil {
		t.Fatalf("runStats(overview) error = %v", err)
	}
	wantOverview := "stats\nstatus: all\ntotalTasks: 0\nstatusCounts:\n  todo: 0\n  inProgress: 0\n  paused: 0\n  done: 0\nmodel:\nlane:\npriority:\nestimate:\nassignee:\n" +
		"comments.tasksWithComments: 0\ncomments.totalRecords: 0\ndependencies.tasksWithDependencies: 0\ndependencies.independentTasks: 0\ndependencies.directDependencyTotal: 0\nhandoffs.total: 0\nhandoffs.allStatusTotal: 0\nhandoffs.global: 0\nhandoffs.taskScoped: 0\n"
	if stdout.String() != wantOverview {
		t.Fatalf("legacy overview = %q, want %q", stdout.String(), wantOverview)
	}
	stdout.Reset()
	if err := runStats(ctx, []string{"model"}); err != nil {
		t.Fatalf("runStats(legacy model) error = %v", err)
	}
	if want := "stats\nstatus: all\ntotalTasks: 0\nattribute: model\nbuckets:\n"; stdout.String() != want {
		t.Fatalf("legacy focused text = %q, want %q", stdout.String(), want)
	}

	stdout.Reset()
	if err := runStats(ctx, []string{"done"}); err != nil {
		t.Fatalf("runStats(legacy filtered overview) error = %v", err)
	}
	wantFiltered := "stats\nstatus: done\ntotalTasks: 0\nstatusCounts:\n  todo: 0\n  inProgress: 0\n  paused: 0\n  done: 0\nmodel:\nlane:\npriority:\nestimate:\nassignee:\n" +
		"comments.tasksWithComments: 0\ncomments.totalRecords: 0\ndependencies.tasksWithDependencies: 0\ndependencies.independentTasks: 0\ndependencies.directDependencyTotal: 0\nhandoffs.total: 0\nhandoffs.allStatusTotal: 0\nhandoffs.global: 0\nhandoffs.taskScoped: 0\n"
	if stdout.String() != wantFiltered {
		t.Fatalf("legacy filtered overview = %q, want %q", stdout.String(), wantFiltered)
	}

	stdout.Reset()
	ctx.jsonOut = true
	if err := runStats(ctx, []string{"done"}); err != nil {
		t.Fatalf("runStats(legacy status JSON) error = %v", err)
	}
	var overviewFields map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &overviewFields); err != nil {
		t.Fatalf("decode legacy overview JSON: %v", err)
	}
	for _, field := range []string{"status", "totalTasks", "statusCounts", "attributes", "comments", "dependencies", "handoffs"} {
		if _, ok := overviewFields[field]; !ok {
			t.Fatalf("legacy overview JSON missing %q: %s", field, stdout.String())
		}
	}
	if _, ok := overviewFields["attribute"]; ok {
		t.Fatalf("legacy overview JSON unexpectedly focused: %s", stdout.String())
	}
	if p.handoffCalls != 3 {
		t.Fatalf("legacy overview handoff calls = %d, want 3", p.handoffCalls)
	}
}

func focusedStatsFixture() *statsTestProvider {
	return &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{
		{Task: core.Task{Status: core.StatusTodo, Model: ""}},
		{Task: core.Task{Status: core.StatusInProgress, Model: "zeta"}},
		{Task: core.Task{Status: core.StatusDone, Model: "alpha"}},
		{Task: core.Task{Status: core.StatusDone, Model: "alpha"}},
	}}}
}

func bucketsPointer(buckets []stats.Bucket) *[]stats.Bucket { return &buckets }

func assertFocusedCLIJSON(t *testing.T, data []byte, want stats.FocusedReport) {
	t.Helper()
	var got stats.FocusedReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode focused JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("focused JSON = %#v, want %#v", got, want)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode focused JSON fields: %v", err)
	}
	for _, field := range []string{"statusCounts", "attributes", "comments", "dependencies", "handoffs"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("focused JSON unexpectedly contains overview field %q: %s", field, data)
		}
	}
}

type focusedStatsErrorProvider struct {
	*statsTestProvider
	listTasksErr error
}

func (p *focusedStatsErrorProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	p.listCalls++
	p.lastFilter = filter
	return nil, p.listTasksErr
}

var _ provider.Provider = (*focusedStatsErrorProvider)(nil)
