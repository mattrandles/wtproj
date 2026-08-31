package planning_test

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/planning"
)

func TestPlanningReportJSONContract(t *testing.T) {
	// An unset project, a version and an unset milestone freeze the required
	// three distinct child keys and empty-string representation at each level.
	const fixture = `{"totalItems":1,"statusCounts":[{"value":"toplan","count":0},{"value":"researched","count":0},{"value":"planned","count":1},{"value":"rejected","count":0}],"projects":[{"value":"","totalItems":1,"statusCounts":[{"value":"toplan","count":0},{"value":"researched","count":0},{"value":"planned","count":1},{"value":"rejected","count":0}],"versions":[{"value":"v2","totalItems":1,"statusCounts":[{"value":"toplan","count":0},{"value":"researched","count":0},{"value":"planned","count":1},{"value":"rejected","count":0}],"milestones":[{"value":"","totalItems":1,"statusCounts":[{"value":"toplan","count":0},{"value":"researched","count":0},{"value":"planned","count":1},{"value":"rejected","count":0}]}]}]}]}`
	var report planning.Report
	if err := json.Unmarshal([]byte(fixture), &report); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil || string(data) != fixture {
		t.Fatalf("planning hierarchy JSON = %s, error %v", data, err)
	}
	if len(report.Projects) != 1 || len(report.Projects[0].Versions) != 1 || len(report.Projects[0].Versions[0].Milestones) != 1 {
		t.Fatal("report hierarchy must remain project -> version -> milestone")
	}
	for _, counts := range []planning.Summary{report.Summary, report.Projects[0].Summary, report.Projects[0].Versions[0].Summary, report.Projects[0].Versions[0].Milestones[0].Summary} {
		if !reflect.DeepEqual(counts, report.Summary) {
			t.Fatal("every hierarchy level must use the same summary shape")
		}
	}
	const empty = `{"totalItems":0,"statusCounts":[{"value":"toplan","count":0},{"value":"researched","count":0},{"value":"planned","count":0},{"value":"rejected","count":0}],"projects":[]}`
	var emptyReport planning.Report
	if err := json.Unmarshal([]byte(empty), &emptyReport); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(emptyReport)
	if err != nil || string(data) != empty {
		t.Fatalf("empty report must retain zero buckets and []: %s, %v", data, err)
	}
}

func TestPlanningReportArchitectureBoundary(t *testing.T) {
	// Report aggregation may use the narrow planning provider, but cannot
	// import execution stats, renderers, or concrete storage as a shortcut.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"/internal/stats", "/internal/cli", "/internal/provider/flatfile", "/internal/provider/trello"} {
				if strings.Contains(path, forbidden) {
					t.Errorf("%s imports %s across the planning report boundary", entry.Name(), path)
				}
			}
		}
	}

	// Preserve the normative decisions that do not yet have runtime code.
	// Behavioral validation belongs to the dependent implementation tasks.
	path := filepath.Join("..", "..", "docs", "planning-lifecycle.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document := strings.Join(strings.Fields(string(data)), " ")
	for _, clause := range []string{
		"architecture contract v1", "wtp-0d6e4079-0066",
		"planning deletion, planning comments, planning batch editing/import/export, execution-graph expansion",
		"Non-flat-file support is excluded", "Same-state and every other direct move are errors",
		"startedAt: null", "completedAt: null", "Do not extend `provider.Provider`",
		"case-insensitive exact AND", "`featureId` is a stable key, `feature` a display name",
		"rejected -> researched/planned", "Never create a planning index or renumber IDs",
		"Graph nodes and edges have executable endpoints only",
		"task-scoped handoffs return or mutate executable records only",
		"including through executable vertices", "Preview is not a reservation",
		"never initializes directories/indexes, repairs files, updates timestamps, or creates/consumes journals",
		"Reusable deletion must atomically detach references from BOTH lifecycles",
		"batch-update -> reusable-update -> planning-promote",
		"Prepared promotion recovery restores every exact planning before snapshot",
		"Committed recovery publishes every exact todo after snapshot",
		"Planning export is flat under the managed `planning` directory",
		"Normal batch export remains planning-blind",
	} {
		if !strings.Contains(document, clause) {
			t.Errorf("normative architecture lost clause %q", clause)
		}
	}
}
