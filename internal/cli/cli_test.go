package cli

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"wtp/internal/core"
	"wtp/internal/provider"
)

func TestRewriteLegacyArgsGetTask(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{"--json", "--agent", "Jim", "--get-task", "--task-id", "wtp-0003"})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs error = %v", err)
	}

	want := []string{"--json", "task", "get", "wtp-0003", "--agent", "Jim"}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs args = %v, want %v", got.args, want)
	}
}

func TestRewriteLegacyArgsListPreservesAgent(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{"--agent", "Jim", "--get-tasks", "--status", "todo"})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs error = %v", err)
	}

	want := []string{"task", "list", "--status", "todo", "--agent", "Jim"}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs args = %v, want %v", got.args, want)
	}
}

func TestRewriteLegacyArgsRejectsMultipleActions(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--get-next-task", "--get-tasks"})
	if err == nil {
		t.Fatal("expected multiple-action error")
	}
}

func TestRewriteLegacyArgsRequiresTaskID(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--set-task-done"})
	if err == nil {
		t.Fatal("expected missing task-id error")
	}
}

func TestRewriteLegacyArgsRequiresComment(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--add-comment", "--task-id", "wtp-0003"})
	if err == nil {
		t.Fatal("expected missing comment error")
	}
}

func TestRewriteLegacyArgsCreatePreservesSchedulingMetadata(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{
		"--create-task",
		"--title", "New task",
		"--priority", "high",
		"--estimate", "m",
		"--lane", "backend",
		"--model", "gpt-5",
		"--dependencies", "wtp-0001",
		"--agent", "Jim",
	})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs error = %v", err)
	}

	want := []string{
		"task", "create",
		"--title", "New task",
		"--priority", "high",
		"--estimate", "m",
		"--lane", "backend",
		"--model", "gpt-5",
		"--depends-on", "wtp-0001",
		"--agent", "Jim",
	}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs args = %v, want %v", got.args, want)
	}
}

func TestRunTaskCreatePassesSuggestedModelToProvider(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
	}

	err := runTaskCreate(ctx, []string{"--title", "Model-aware task", "--model", "gpt-5.2-codex"})
	if err != nil {
		t.Fatalf("runTaskCreate() error = %v", err)
	}
	if provider.gotCreateInput.Model != "gpt-5.2-codex" {
		t.Fatalf("model input = %q, want %q", provider.gotCreateInput.Model, "gpt-5.2-codex")
	}
	if provider.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", provider.createCalls)
	}
}

func TestRunTaskReadyPrintsNoEligibleWorkWithoutError(t *testing.T) {
	var stdout bytes.Buffer
	ctx := context{
		provider: readyTestProvider{peekErr: provider.ErrNoEligibleTask},
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
	}

	if err := runTaskReady(ctx, []string{"--agent", "Jim"}); err != nil {
		t.Fatalf("runTaskReady() error = %v", err)
	}
	if got := stdout.String(); got != "no eligible task found\n" {
		t.Fatalf("stdout = %q, want %q", got, "no eligible task found\n")
	}
}

func TestRunTaskReadyPrintsEmptyJSONBatchWithoutError(t *testing.T) {
	var stdout bytes.Buffer
	ctx := context{
		provider: readyTestProvider{peekManyErr: provider.ErrNoEligibleTask},
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
		jsonOut:  true,
	}

	if err := runTaskReady(ctx, []string{"--agent", "Jim", "--limit", "3"}); err != nil {
		t.Fatalf("runTaskReady() error = %v", err)
	}
	if got := stdout.String(); got != "[]\n" {
		t.Fatalf("stdout = %q, want %q", got, "[]\n")
	}
}

func TestRunTaskUpdatePassesMutableFieldsToProvider(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
	}

	err := runTaskUpdate(ctx, []string{"wtp-0028", "--depends-on", "wtp-0020", "--priority", "high", "--model", "o3", "--agent", "Tony"})
	if err != nil {
		t.Fatalf("runTaskUpdate() error = %v", err)
	}
	if provider.gotID != "wtp-0028" {
		t.Fatalf("updated id = %q, want %q", provider.gotID, "wtp-0028")
	}
	if !provider.gotInput.Dependencies.Set || provider.gotInput.Dependencies.Value != "wtp-0020" {
		t.Fatalf("dependencies input = %#v", provider.gotInput.Dependencies)
	}
	if !provider.gotInput.Priority.Set || provider.gotInput.Priority.Value != core.PriorityHigh {
		t.Fatalf("priority input = %#v", provider.gotInput.Priority)
	}
	if !provider.gotInput.Assignee.Set || provider.gotInput.Assignee.Value != "Tony" {
		t.Fatalf("assignee input = %#v", provider.gotInput.Assignee)
	}
	if !provider.gotInput.Model.Set || provider.gotInput.Model.Value != "o3" {
		t.Fatalf("model input = %#v", provider.gotInput.Model)
	}
}

func TestRunTaskUpdateCanClearSuggestedModel(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{provider: provider, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	if err := runTaskUpdate(ctx, []string{"wtp-0028", "--model="}); err != nil {
		t.Fatalf("runTaskUpdate() error = %v", err)
	}
	if !provider.gotInput.Model.Set || provider.gotInput.Model.Value != "" {
		t.Fatalf("model input = %#v, want explicitly empty", provider.gotInput.Model)
	}
}

func TestRunTaskAcceptsEditAlias(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
	}

	err := runTask(ctx, []string{"edit", "wtp-0028", "--title", "Renamed"})
	if err != nil {
		t.Fatalf("runTask(edit) error = %v", err)
	}
	if provider.gotID != "wtp-0028" {
		t.Fatalf("updated id = %q, want %q", provider.gotID, "wtp-0028")
	}
	if !provider.gotInput.Title.Set || provider.gotInput.Title.Value != "Renamed" {
		t.Fatalf("title input = %#v", provider.gotInput.Title)
	}
	if provider.updateCalls != 1 {
		t.Fatalf("updateCalls = %d, want 1", provider.updateCalls)
	}
}

func TestRunTaskAcceptsShowAlias(t *testing.T) {
	provider := &getTestProvider{}
	ctx := context{
		provider: provider,
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
	}

	err := runTask(ctx, []string{"show", "wtp-0028", "--agent", "Tony"})
	if err != nil {
		t.Fatalf("runTask(show) error = %v", err)
	}
	if provider.gotID != "wtp-0028" {
		t.Fatalf("got id = %q, want %q", provider.gotID, "wtp-0028")
	}
	if provider.gotAgent != "Tony" {
		t.Fatalf("got agent = %q, want %q", provider.gotAgent, "Tony")
	}
	if provider.getCalls != 1 {
		t.Fatalf("getCalls = %d, want 1", provider.getCalls)
	}
}

func TestRunGraphDefaultsToTodoAndPrintsDependencyTree(t *testing.T) {
	provider := graphTestProvider{tasks: []core.TaskView{
		graphTaskView("dep-1", "wtp-0001", "Dependency", core.StatusTodo, nil, "2026-04-21T10:00:00Z"),
		graphTaskView("task-2", "wtp-0002", "Todo task", core.StatusTodo, []string{"dep-1"}, "2026-04-21T10:05:00Z"),
		graphTaskView("done-3", "wtp-0003", "Done task", core.StatusDone, nil, "2026-04-21T10:10:00Z"),
	}}
	var stdout bytes.Buffer
	ctx := context{provider: provider, stdout: &stdout, stderr: &bytes.Buffer{}}

	if err := runGraph(ctx, nil); err != nil {
		t.Fatalf("runGraph() error = %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "wtp-0002 [todo] Todo task") {
		t.Fatalf("graph output missing todo root: %q", got)
	}
	if !strings.Contains(got, "\\-- wtp-0001 [todo] Dependency") {
		t.Fatalf("graph output missing dependency child: %q", got)
	}
	if strings.Contains(got, "wtp-0003 [done] Done task") {
		t.Fatalf("graph output unexpectedly included done task: %q", got)
	}
	if provider.lastFilter.Status != nil {
		t.Fatal("graph should request all tasks from provider and filter locally")
	}
}

func TestRunGraphSupportsAllStatusInJSON(t *testing.T) {
	provider := graphTestProvider{tasks: []core.TaskView{
		graphTaskView("dep-1", "wtp-0001", "Dependency", core.StatusDone, nil, "2026-04-21T10:00:00Z"),
		graphTaskView("task-2", "wtp-0002", "Todo task", core.StatusTodo, []string{"dep-1"}, "2026-04-21T10:05:00Z"),
	}}
	var stdout bytes.Buffer
	ctx := context{provider: provider, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}

	if err := runGraph(ctx, []string{"--status", "all"}); err != nil {
		t.Fatalf("runGraph(--status all) error = %v", err)
	}
	got := stdout.String()
	for _, needle := range []string{"\"shortId\": \"wtp-0002\"", "\"shortId\": \"wtp-0001\"", "\"status\": \"done\""} {
		if !strings.Contains(got, needle) {
			t.Fatalf("json graph output missing %q in %q", needle, got)
		}
	}
}

func TestRunGraphRejectsInvalidStatus(t *testing.T) {
	ctx := context{provider: graphTestProvider{}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := runGraph(ctx, []string{"--status", "blocked"}); err == nil {
		t.Fatal("expected invalid graph status error")
	}
}

func TestHelpMentionsShowUpdateEditSchemaAndModel(t *testing.T) {
	var stdout bytes.Buffer
	if err := help(&stdout); err != nil {
		t.Fatalf("help() error = %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"wtp task show", "wtp task update", "wtp task edit", "wtp graph", "wtp schema", "--model", "Usage Guide:"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("help output missing %q", needle)
		}
	}
}

func TestSchemaMentionsDependenciesIndexAndModel(t *testing.T) {
	var stdout bytes.Buffer
	if err := schema(&stdout); err != nil {
		t.Fatalf("schema() error = %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"dependencies are stored as canonical UUID strings", ".wtp/meta/index.json", "Task JSON schema:", `"model": "gpt-5"`, "model: optional free-form string", "--model"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("schema output missing %q", needle)
		}
	}
}

func TestPrintValueIncludesSuggestedModelInHumanAndJSONOutput(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	task := core.TaskView{Task: core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Model-aware task",
		Model:        "gpt-5.2-codex",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}}

	var human bytes.Buffer
	if err := printValue(context{stdout: &human}, task); err != nil {
		t.Fatalf("printValue(human) error = %v", err)
	}
	if !strings.Contains(human.String(), "model: gpt-5.2-codex") {
		t.Fatalf("human output missing model: %q", human.String())
	}

	var summary bytes.Buffer
	if err := printValue(context{stdout: &summary}, []core.TaskView{task}); err != nil {
		t.Fatalf("printValue(summary) error = %v", err)
	}
	if !strings.Contains(summary.String(), "\tgpt-5.2-codex\tModel-aware task") {
		t.Fatalf("summary output missing model: %q", summary.String())
	}

	var jsonOutput bytes.Buffer
	if err := printValue(context{stdout: &jsonOutput, jsonOut: true}, task); err != nil {
		t.Fatalf("printValue(JSON) error = %v", err)
	}
	if !strings.Contains(jsonOutput.String(), `"model": "gpt-5.2-codex"`) {
		t.Fatalf("JSON output missing model: %q", jsonOutput.String())
	}
}

type readyTestProvider struct {
	peekErr     error
	peekManyErr error
}

type getTestProvider struct {
	gotID    string
	gotAgent string
	getCalls int
}

type updateTestProvider struct {
	gotCreateInput core.CreateTaskInput
	gotID          string
	gotInput       core.UpdateTaskInput
	createCalls    int
	updateCalls    int
}

type graphTestProvider struct {
	tasks      []core.TaskView
	lastFilter provider.TaskFilter
	listCalls  int
}

func (p *updateTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p *updateTestProvider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *updateTestProvider) CreateTask(input core.CreateTaskInput) (core.TaskView, error) {
	p.gotCreateInput = input
	p.createCalls++
	now := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	return core.TaskView{Task: core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0028",
		Title:        input.Title,
		Model:        input.Model,
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}}, nil
}

func (p *updateTestProvider) UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error) {
	p.gotID = idOrShortID
	p.gotInput = input
	p.updateCalls++
	now := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	return core.TaskView{
		Task: core.Task{
			ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
			ShortID:      "wtp-0028",
			Title:        "Updated",
			Status:       core.StatusTodo,
			Dependencies: []string{},
			Comments:     []core.Comment{},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}, nil
}

func (p *updateTestProvider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *updateTestProvider) AddComment(idOrShortID, actor, message string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *updateTestProvider) PeekNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *updateTestProvider) PeekNextTasks(agent string, limit int) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p *updateTestProvider) GetNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *updateTestProvider) ExportCanonical(outDir string) error {
	return errors.New("unexpected call")
}

func (p graphTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	p.lastFilter = filter
	p.listCalls++
	return p.tasks, nil
}

func (p graphTestProvider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) CreateTask(input core.CreateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) AddComment(idOrShortID, actor, message string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) PeekNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) PeekNextTasks(agent string, limit int) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p graphTestProvider) GetNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p graphTestProvider) ExportCanonical(outDir string) error {
	return errors.New("unexpected call")
}

func graphTaskView(id, shortID, title string, status core.Status, dependencies []string, createdAt string) core.TaskView {
	parsed, _ := time.Parse(time.RFC3339, createdAt)
	return core.TaskView{
		Task: core.Task{
			ID:           id,
			ShortID:      shortID,
			Title:        title,
			Status:       status,
			Dependencies: dependencies,
			Comments:     []core.Comment{},
			CreatedAt:    parsed,
			UpdatedAt:    parsed,
		},
	}
}

func (p readyTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p readyTestProvider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p readyTestProvider) CreateTask(input core.CreateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p readyTestProvider) UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p readyTestProvider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p readyTestProvider) AddComment(idOrShortID, actor, message string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p readyTestProvider) PeekNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, p.peekErr
}

func (p readyTestProvider) PeekNextTasks(agent string, limit int) ([]core.TaskView, error) {
	return nil, p.peekManyErr
}

func (p readyTestProvider) GetNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p readyTestProvider) ExportCanonical(outDir string) error {
	return errors.New("unexpected call")
}

func (p *getTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p *getTestProvider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	p.gotID = idOrShortID
	p.gotAgent = agent
	p.getCalls++
	now := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	return core.TaskView{
		Task: core.Task{
			ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
			ShortID:      idOrShortID,
			Title:        "Shown",
			Status:       core.StatusTodo,
			Assignee:     agent,
			Dependencies: []string{},
			Comments:     []core.Comment{},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}, nil
}

func (p *getTestProvider) CreateTask(input core.CreateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *getTestProvider) UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *getTestProvider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *getTestProvider) AddComment(idOrShortID, actor, message string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *getTestProvider) PeekNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *getTestProvider) PeekNextTasks(agent string, limit int) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p *getTestProvider) GetNextTask(agent string) (core.TaskView, error) {
	return core.TaskView{}, errors.New("unexpected call")
}

func (p *getTestProvider) ExportCanonical(outDir string) error {
	return errors.New("unexpected call")
}
