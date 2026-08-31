package batchimport

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestResolveFormatInferenceAndOverride(t *testing.T) {
	tests := []struct {
		name, source string
		explicit     Format
		want         Format
		wantErr      string
	}{
		{name: "csv extension", source: "tasks.csv", want: FormatCSV},
		{name: "json extension case insensitive", source: "tasks.JSON", want: FormatJSON},
		{name: "explicit overrides extension", source: "tasks.csv", explicit: FormatJSON, want: FormatJSON},
		{name: "stdin requires format", source: "-", wantErr: "required"},
		{name: "unknown requires format", source: "tasks.txt", wantErr: "required"},
		{name: "invalid explicit", source: "tasks.json", explicit: "yaml", wantErr: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveFormat(test.source, test.explicit)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("ResolveFormat() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ResolveFormat() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestImportReadsFileAndPreservesJSONPatchStates(t *testing.T) {
	task := importTask("00000000-0000-4000-8000-000000000001", "wtp-0001")
	data := `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","description":null,"priority":null,"dependencies":null}]}`
	directory := t.TempDir()
	path := filepath.Join(directory, "patch.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	p := &importTestProvider{catalog: core.DefaultStatusCatalog(), returned: provider.BatchUpdateResult{
		Updated: []core.TaskView{{Task: task}},
	}}
	result, err := Import(p, Options{Source: path})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.Updated) != 1 || p.calls != 1 || len(p.lastRequest.Tasks) != 1 {
		t.Fatalf("result/calls/request = %#v, %d, %#v", result, p.calls, p.lastRequest)
	}
	input := p.lastRequest.Tasks[0]
	if input.Title.Set || !input.Description.Set || input.Description.Value != "" || !input.Priority.Set || input.Priority.Value != "" || !input.Dependencies.Set || input.Dependencies.Value != nil {
		t.Fatalf("JSON patch states = %#v", input)
	}
}

func TestImportReadsCSVFromStdinAndExplicitFormatOverridesExtension(t *testing.T) {
	data := "id,updatedAt,title,description,priority,dependencies,_clear\n" +
		"00000000-0000-4000-8000-000000000001,2026-01-02T03:04:05Z,New title,,,wtp-0002,\"description,priority\"\n"
	p := &importTestProvider{catalog: core.DefaultStatusCatalog()}
	_, err := Import(p, Options{Source: "-", Format: FormatCSV}, strings.NewReader(data))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("BatchUpdate calls = %d, want 1", p.calls)
	}
	input := p.lastRequest.Tasks[0]
	if input.Title != (core.OptionalString{Set: true, Value: "New title"}) || !input.Description.Set || input.Description.Value != "" || !input.Priority.Set || input.Priority.Value != "" {
		t.Fatalf("CSV clear mapping = %#v", input)
	}
	if !input.Dependencies.Set || !reflect.DeepEqual(input.Dependencies.Value, []string{"wtp-0002"}) {
		t.Fatalf("CSV dependency mapping = %#v", input.Dependencies)
	}
}

func TestImportNormalizesConfiguredStatusAndEnums(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{{Name: "waitingReview", Category: core.StatusCategoryWaiting}})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	data := `{"version":1,"tasks":[{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","status":"waitingReview","priority":" HIGH ","estimate":" M "}]}`
	p := &importTestProvider{catalog: catalog}
	if _, err := Import(p, Options{Source: "-", Format: FormatJSON}, strings.NewReader(data)); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	input := p.lastRequest.Tasks[0]
	if input.Status.Value != "waitingReview" || input.Priority.Value != core.PriorityHigh || input.Estimate.Value != core.EstimateM {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestImportAggregatesMappingDiagnosticsAndDoesNotCallProvider(t *testing.T) {
	data := `{"version":1,"tasks":[` +
		`{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","status":"missing"},` +
		`{"shortId":"wtp-0002","updatedAt":"2026-01-02T03:04:05Z","priority":"critical","estimate":"xxl"}` +
		`]}`
	p := &importTestProvider{catalog: core.DefaultStatusCatalog()}
	_, err := Import(p, Options{Source: "-", Format: FormatJSON}, strings.NewReader(data))
	if err == nil {
		t.Fatal("Import() unexpectedly succeeded")
	}
	var diagnostics *DiagnosticsError
	if !errors.As(err, &diagnostics) {
		t.Fatalf("error type = %T, want *DiagnosticsError: %v", err, err)
	}
	want := []Diagnostic{
		{Row: 1, Task: "wtp-0001", Field: "status", Message: `invalid status "missing"`},
		{Row: 2, Task: "wtp-0002", Field: "priority", Message: `invalid priority "critical"`},
		{Row: 2, Task: "wtp-0002", Field: "estimate", Message: `invalid estimate "xxl"`},
	}
	if !reflect.DeepEqual(diagnostics.Diagnostics, want) {
		t.Fatalf("diagnostics = %#v, want %#v", diagnostics.Diagnostics, want)
	}
	if p.calls != 0 {
		t.Fatalf("BatchUpdate calls = %d after invalid mapping", p.calls)
	}
	if !strings.Contains(err.Error(), "row 1 task wtp-0001 field status") || !strings.Contains(err.Error(), "row 2 task wtp-0002 field estimate") {
		t.Fatalf("diagnostic error = %v", err)
	}
}

func TestImportAggregatesIndependentCodecRowErrors(t *testing.T) {
	data := `{"version":1,"tasks":[` +
		`{"shortId":"wtp-0001","title":"missing token"},` +
		`{"updatedAt":"not-a-timestamp","title":"missing identifier"}` +
		`]}`
	p := &importTestProvider{catalog: core.DefaultStatusCatalog()}
	_, err := Import(p, Options{Source: "-", Format: FormatJSON}, strings.NewReader(data))
	if err == nil {
		t.Fatal("Import() unexpectedly succeeded")
	}
	message := err.Error()
	if !strings.Contains(message, "task row 1: updatedAt is required") || !strings.Contains(message, "task row 2: id or shortId is required") {
		t.Fatalf("aggregated codec error = %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("BatchUpdate calls = %d after decode errors", p.calls)
	}
}

func TestImportRejectsMalformedPathsBeforeReading(t *testing.T) {
	p := &importTestProvider{catalog: core.DefaultStatusCatalog()}
	for _, options := range []Options{
		{Source: filepath.Join(t.TempDir(), "missing.json")},
		{Source: filepath.Join(t.TempDir(), "directory.json")},
	} {
		if options.Source != "" && strings.HasSuffix(options.Source, "directory.json") {
			if err := os.Mkdir(options.Source, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}
		_, err := Import(p, options)
		if err == nil || !strings.Contains(err.Error(), "read batch import input") {
			t.Errorf("Import(%q) error = %v", options.Source, err)
		}
	}
	if p.calls != 0 {
		t.Fatalf("BatchUpdate calls = %d", p.calls)
	}
}

func TestImportReturnsProviderViewsInInputOrder(t *testing.T) {
	first := importTask("00000000-0000-4000-8000-000000000001", "wtp-0001")
	second := importTask("00000000-0000-4000-8000-000000000002", "wtp-0002")
	data := `{"version":1,"tasks":[` +
		`{"shortId":"wtp-0001","updatedAt":"2026-01-02T03:04:05Z","title":"first"},` +
		`{"shortId":"wtp-0002","updatedAt":"2026-01-02T03:04:05Z","title":"second"}` + `]}`
	p := &importTestProvider{catalog: core.DefaultStatusCatalog(), returned: provider.BatchUpdateResult{
		Updated:   []core.TaskView{{Task: second}, {Task: first}},
		Unchanged: []core.TaskView{{Task: second}, {Task: first}},
	}}
	result, err := Import(p, Options{Source: "-", Format: FormatJSON}, strings.NewReader(data))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	for _, views := range [][]core.TaskView{result.Updated, result.Unchanged} {
		if len(views) != 2 || views[0].ShortID != "wtp-0001" || views[1].ShortID != "wtp-0002" {
			t.Fatalf("ordered views = %#v", views)
		}
	}
}

func importTask(id, shortID string) core.Task {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return core.Task{ID: id, ShortID: shortID, Title: shortID, Status: core.StatusTodo, CreatedAt: now, UpdatedAt: now}
}

type importTestProvider struct {
	catalog     core.StatusCatalog
	returned    provider.BatchUpdateResult
	lastRequest provider.BatchUpdateRequest
	calls       int
}

func (p *importTestProvider) StatusCatalog() core.StatusCatalog { return p.catalog }
func (p *importTestProvider) ListTasks(provider.TaskFilter) ([]core.TaskView, error) {
	return nil, nil
}
func (p *importTestProvider) WriteHandoff(provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	return provider.HandoffWriteResult{}, nil
}
func (p *importTestProvider) ListHandoffs(provider.HandoffFilter) (provider.HandoffListResult, error) {
	return provider.HandoffListResult{}, nil
}
func (p *importTestProvider) PurgeHandoffs(provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	return provider.HandoffPurgeResult{}, nil
}
func (p *importTestProvider) GetTask(string, string) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) CreateTask(core.CreateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) UpdateTask(string, core.UpdateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) BatchUpdate(request provider.BatchUpdateRequest) (provider.BatchUpdateResult, error) {
	p.calls++
	p.lastRequest = request
	return p.returned, nil
}
func (p *importTestProvider) UpdateTaskStatus(string, core.Status, string) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) AddComment(string, string, string) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) PeekNextTask(string) (core.TaskView, error) { return core.TaskView{}, nil }
func (p *importTestProvider) PeekNextTasks(string, int) ([]core.TaskView, error) {
	return nil, nil
}
func (p *importTestProvider) GetNextTask(string) (core.TaskView, error) { return core.TaskView{}, nil }
func (p *importTestProvider) PeekNextTaskWithFilter(provider.SelectionFilter) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) PeekNextTasksWithFilter(provider.SelectionFilter, int) ([]core.TaskView, error) {
	return nil, nil
}
func (p *importTestProvider) GetNextTaskWithFilter(provider.SelectionFilter) (core.TaskView, error) {
	return core.TaskView{}, nil
}
func (p *importTestProvider) ExportCanonical(string) error { return nil }

var _ provider.Provider = (*importTestProvider)(nil)
