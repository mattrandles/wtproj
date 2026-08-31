package batchexport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/batchcsv"
	"github.com/mattrandles/wtproj/internal/batchjson"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestExportSelectionOrderingAndStatusFilter(t *testing.T) {
	first := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusTodo, 1)
	second := testTask("00000000-0000-4000-8000-000000000002", "wtp-0002", core.StatusDone, 2)
	third := testTask("00000000-0000-4000-8000-000000000003", "wtp-0003", core.StatusTodo, 3)
	provider := &exportTestProvider{tasks: []core.TaskView{{Task: third}, {Task: first}, {Task: second}}}

	var all bytes.Buffer
	result, err := Export(provider, Options{Destination: "-", Format: FormatJSON}, &all)
	if err != nil {
		t.Fatalf("export all: %v", err)
	}
	if result.Count != 3 || result.Format != FormatJSON || result.Destination != "-" {
		t.Fatalf("all result = %#v", result)
	}
	assertJSONIDs(t, all.Bytes(), []string{third.ID, first.ID, second.ID})
	if provider.lastFilter.Status != nil {
		t.Fatalf("all export unexpectedly filtered: %#v", provider.lastFilter)
	}

	status := string(core.StatusTodo)
	var filtered bytes.Buffer
	if _, err := Export(provider, Options{Destination: "-", Format: FormatJSON, Status: status}, &filtered); err != nil {
		t.Fatalf("export status: %v", err)
	}
	assertJSONIDs(t, filtered.Bytes(), []string{third.ID, first.ID})
	if len(provider.filters) < 3 || provider.filters[1].Status == nil || *provider.filters[1].Status != core.StatusTodo {
		t.Fatalf("status filter = %#v", provider.filters)
	}

	var explicit bytes.Buffer
	if _, err := Export(provider, Options{Destination: "-", Format: FormatJSON, TaskIDs: []string{second.ShortID, third.ID}}, &explicit); err != nil {
		t.Fatalf("export explicit IDs: %v", err)
	}
	assertJSONIDs(t, explicit.Bytes(), []string{second.ID, third.ID})
}

func TestExportRejectsSelectorConflictsDuplicatesAndUnknownStatus(t *testing.T) {
	task := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusTodo, 1)
	p := &exportTestProvider{tasks: []core.TaskView{{Task: task}}}
	tests := []struct {
		name    string
		options Options
		want    string
	}{
		{"status and task", Options{Destination: "-", Format: FormatJSON, Status: "todo", TaskIDs: []string{task.ShortID}}, "cannot be combined"},
		{"duplicate exact task", Options{Destination: "-", Format: FormatJSON, TaskIDs: []string{task.ShortID, task.ShortID}}, "duplicates"},
		{"duplicate through ID aliases", Options{Destination: "-", Format: FormatJSON, TaskIDs: []string{task.ID, task.ShortID}}, "duplicates"},
		{"unknown status", Options{Destination: "-", Format: FormatJSON, Status: "missingStatus"}, "invalid status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Export(p, test.options, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExportRendersCompleteEditableTaskAndDependencyShortIDs(t *testing.T) {
	dependency := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusDone, 1)
	task := testTask("00000000-0000-4000-8000-000000000002", "wtp-0002", core.StatusTodo, 2)
	task.Dependencies = []string{dependency.ID}
	task.Description = ""
	task.Priority = ""
	task.Estimate = ""
	task.Lane = ""
	task.Model = ""
	task.GitRepo = ""
	task.GitBranch = ""
	task.WorktreeName = ""
	task.WorktreeDir = ""
	task.Assignee = ""
	p := &exportTestProvider{tasks: []core.TaskView{{Task: dependency}, {Task: task}}}

	var output bytes.Buffer
	if _, err := Export(p, Options{Destination: "-", Format: FormatJSON}, &output); err != nil {
		t.Fatalf("export: %v", err)
	}
	rows, err := batchjson.Decode(output.Bytes())

	if err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(rows) != 2 || rows[1].Dependencies.Value[0] != dependency.ShortID {
		t.Fatalf("exported dependencies = %#v", rows)
	}
	row := rows[1]
	if row.ID != task.ID || row.ShortID != task.ShortID || !row.ExpectedUpdatedAt.Equal(task.UpdatedAt) {
		t.Fatalf("exported identity/token = %#v", row)
	}
	if !row.Title.Set || !row.Description.Set || !row.Status.Set || !row.Priority.Set || !row.Estimate.Set ||
		!row.Lane.Set || !row.Model.Set || !row.GitRepo.Set || !row.GitBranch.Set || !row.WorktreeName.Set ||
		!row.WorktreeDir.Set || !row.Assignee.Set || !row.Dependencies.Set {
		t.Fatalf("export did not include every mutable field: %#v", row)
	}

	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatalf("parse JSON export: %v", err)
	}
	encoded := string(output.Bytes())
	for _, excluded := range []string{"comments", "handoffs", "readiness", "createdAt", "startedAt", "completedAt"} {
		if strings.Contains(encoded, `"`+excluded+`"`) {
			t.Errorf("JSON export contains excluded field %q", excluded)
		}
	}
	if fields["version"] != float64(batchjson.Version) {
		t.Fatalf("JSON version = %#v", fields["version"])
	}
}

func TestStatusExportResolvesDependenciesOutsideSelectedStatus(t *testing.T) {
	dependency := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusDone, 1)
	task := testTask("00000000-0000-4000-8000-000000000002", "wtp-0002", core.StatusTodo, 2)
	task.Dependencies = []string{dependency.ID}
	p := &exportTestProvider{tasks: []core.TaskView{{Task: task}, {Task: dependency}}}
	var output bytes.Buffer
	if _, err := Export(p, Options{Destination: "-", Format: FormatJSON, Status: "todo"}, &output); err != nil {
		t.Fatalf("status export: %v", err)
	}
	rows, err := batchjson.Decode(output.Bytes())
	if err != nil {
		t.Fatalf("decode status export: %v", err)
	}
	if len(rows) != 1 || len(rows[0].Dependencies.Value) != 1 || rows[0].Dependencies.Value[0] != dependency.ShortID {
		t.Fatalf("status dependency rendering = %#v", rows)
	}
}

func TestExportUsesCSVClearSemanticsForEmptyMutableFields(t *testing.T) {
	task := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusTodo, 1)
	task.Description = ""
	task.Dependencies = nil
	p := &exportTestProvider{tasks: []core.TaskView{{Task: task}}}
	var output bytes.Buffer
	if _, err := Export(p, Options{Destination: "-", Format: FormatCSV}, &output); err != nil {
		t.Fatalf("export: %v", err)
	}
	rows, err := batchcsv.Decode(output.Bytes())
	if err != nil {
		t.Fatalf("decode CSV export: %v\n%s", err, output.String())
	}
	if !rows[0].Description.Set || rows[0].Description.Value != "" || !rows[0].Dependencies.Set || rows[0].Dependencies.Value != nil {
		t.Fatalf("CSV clear state = %#v", rows[0])
	}
}

func TestResolveFormatInferenceAndOverride(t *testing.T) {
	tests := []struct {
		name        string
		destination string
		explicit    Format
		want        Format
		wantErr     string
	}{
		{"csv extension", "tasks.csv", "", FormatCSV, ""},
		{"json extension case insensitive", "tasks.JSON", "", FormatJSON, ""},
		{"explicit overrides extension", "tasks.csv", FormatJSON, FormatJSON, ""},
		{"stdout needs explicit", "-", "", "", "required"},
		{"unknown needs explicit", "tasks.txt", "", "", "required"},
		{"invalid explicit", "tasks.json", "yaml", "", "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveFormat(test.destination, test.explicit)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ResolveFormat() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestExportAtomicallyPreservesExistingFileOnReplacementFailure(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "tasks.json")
	original := []byte("original bytes\n")
	if err := os.WriteFile(destination, original, 0o644); err != nil {
		t.Fatalf("write original: %v", err)
	}
	oldReplace := atomicReplace
	oldSync := atomicSyncDirectory
	defer func() { atomicReplace, atomicSyncDirectory = oldReplace, oldSync }()
	var temporaryPath string
	atomicReplace = func(source, target string) error {
		temporaryPath = source
		if filepath.Dir(source) != filepath.Dir(target) {
			t.Fatalf("temporary file directory = %s, target directory = %s", filepath.Dir(source), filepath.Dir(target))
		}
		return errors.New("injected replacement failure")
	}
	atomicSyncDirectory = func(string) error { return nil }

	task := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusTodo, 1)
	_, err := Export(&exportTestProvider{tasks: []core.TaskView{{Task: task}}}, Options{Destination: destination}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "injected replacement failure") {
		t.Fatalf("error = %v", err)
	}
	if temporaryPath == "" {
		t.Fatal("replacement hook was not called")
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatalf("read original after failed replacement: %v", readErr)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("destination after failed replacement = %q, want %q", got, original)
	}
	if _, statErr := os.Stat(temporaryPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary file cleanup error = %v", statErr)
	}
}

func TestExportStdoutEmitsCodecBytesWithoutMetadata(t *testing.T) {
	task := testTask("00000000-0000-4000-8000-000000000001", "wtp-0001", core.StatusTodo, 1)
	p := &exportTestProvider{tasks: []core.TaskView{{Task: task}}}
	var output bytes.Buffer
	result, err := Export(p, Options{Destination: "-", Format: FormatCSV}, &output)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	want, err := batchcsv.Encode([]core.BatchTaskUpdateInput{expectedInput(task, nil)})
	if err != nil {
		t.Fatalf("encode expected: %v", err)
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("stdout bytes differ\n got %q\nwant %q", output.Bytes(), want)
	}
	if result.Count != 1 || result.Destination != "-" {
		t.Fatalf("result = %#v", result)
	}
}

func assertJSONIDs(t *testing.T, data []byte, want []string) {
	t.Helper()
	rows, err := batchjson.Decode(data)
	if err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	got := make([]string, len(rows))
	for index, row := range rows {
		got[index] = row.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs = %v, want %v", got, want)
	}
}

func testTask(id, shortID string, status core.Status, day int) core.Task {
	stamp := time.Date(2026, time.January, day, 0, 0, 0, 0, time.UTC)
	return core.Task{
		ID: id, ShortID: shortID, Title: "Task " + shortID, Description: "Description " + shortID,
		Status: status, Priority: core.PriorityHigh, Estimate: core.EstimateM, Lane: "lane",
		Model: "model", GitRepo: "/repo", GitBranch: "main", WorktreeName: "worktree",
		WorktreeDir: "/worktree", Assignee: "Ada", Dependencies: []string{}, Comments: []core.Comment{
			{Message: "excluded"},
		}, CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour), StartedAt: pointer(stamp.Add(2 * time.Hour)),
		CompletedAt: pointer(stamp.Add(3 * time.Hour)),
	}
}

func pointer(value time.Time) *time.Time { return &value }

func expectedInput(task core.Task, dependencyShortIDs []string) core.BatchTaskUpdateInput {
	if dependencyShortIDs == nil {
		dependencyShortIDs = task.Dependencies
	}
	return core.BatchTaskUpdateInput{
		ID: task.ID, ShortID: task.ShortID, ExpectedUpdatedAt: task.UpdatedAt,
		Title: core.OptionalString{Set: true, Value: task.Title}, Description: core.OptionalString{Set: true, Value: task.Description},
		Status: core.OptionalStatus{Set: true, Value: task.Status}, Priority: core.OptionalPriority{Set: true, Value: task.Priority},
		Estimate: core.OptionalEstimate{Set: true, Value: task.Estimate}, Lane: core.OptionalString{Set: true, Value: task.Lane},
		Model: core.OptionalString{Set: true, Value: task.Model}, GitRepo: core.OptionalString{Set: true, Value: task.GitRepo},
		GitBranch: core.OptionalString{Set: true, Value: task.GitBranch}, WorktreeName: core.OptionalString{Set: true, Value: task.WorktreeName},
		WorktreeDir: core.OptionalString{Set: true, Value: task.WorktreeDir}, Assignee: core.OptionalString{Set: true, Value: task.Assignee},
		Dependencies: core.OptionalStrings{Set: true, Value: dependencyShortIDs},
	}
}

type exportTestProvider struct {
	provider.Provider
	tasks      []core.TaskView
	lastFilter provider.TaskFilter
	filters    []provider.TaskFilter
}

func (p *exportTestProvider) StatusCatalog() core.StatusCatalog { return core.DefaultStatusCatalog() }

func (p *exportTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	p.lastFilter = filter
	p.filters = append(p.filters, filter)
	if filter.Status == nil {
		return append([]core.TaskView(nil), p.tasks...), nil
	}
	filtered := make([]core.TaskView, 0, len(p.tasks))
	for _, task := range p.tasks {
		if task.Status == *filter.Status {
			filtered = append(filtered, task)
		}
	}
	return filtered, nil
}
