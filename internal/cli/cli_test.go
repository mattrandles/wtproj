package cli

import (
	"bytes"
	stdcontext "context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/buildinfo"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
	"github.com/mattrandles/wtproj/internal/updater"
)

func TestRunVersionTextUsesDevelopmentDefaultsOutsideProject(t *testing.T) {
	chdir(t, t.TempDir())
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if got, want := stdout.String(), "wtp dev\ncommit: none\nbuildDate: unknown\n"; got != want {
		t.Fatalf("version text = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunVersionJSONReportsEmbeddedReleaseMetadata(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate
	buildinfo.Version = "1.2.3"
	buildinfo.Commit = "abc1234"
	buildinfo.BuildDate = "2026-07-20T22:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version = originalVersion
		buildinfo.Commit = originalCommit
		buildinfo.BuildDate = originalBuildDate
	})
	chdir(t, t.TempDir())
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--json", "version"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(--json version) error = %v", err)
	}
	if got, want := stdout.String(), "{\n  \"version\": \"1.2.3\",\n  \"commit\": \"abc1234\",\n  \"buildDate\": \"2026-07-20T22:00:00Z\"\n}\n"; got != want {
		t.Fatalf("version JSON = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunSelfUpdatePrintsNoOpUpgradeAndScheduledResults(t *testing.T) {
	tests := []struct {
		name   string
		result updater.Result
		args   []string
		want   string
	}{
		{
			name:   "no-op",
			result: updater.Result{CurrentVersion: "1.2.3", LatestVersion: "1.2.3"},
			want:   "no update available (current 1.2.3, latest 1.2.3)\n",
		},
		{
			name:   "upgrade",
			result: updater.Result{CurrentVersion: "1.2.3", LatestVersion: "1.3.0", Path: "/opt/bin/wtp", Updated: true},
			want:   "updated wtp from 1.2.3 to 1.3.0 at /opt/bin/wtp\n",
		},
		{
			name:   "scheduled Windows replacement",
			result: updater.Result{CurrentVersion: "1.2.3", LatestVersion: "1.3.0", Path: `C:\\bin\\wtp.exe`, Scheduled: true},
			want:   "Windows will replace",
		},
		{
			name:   "JSON",
			result: updater.Result{CurrentVersion: "1.2.3", LatestVersion: "1.3.0", Path: "/usr/local/bin/wtp", Updated: true},
			args:   []string{"--json"},
			want:   `"updated": true`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			called := 0
			runner := func(_ stdcontext.Context, version string) (updater.Result, error) {
				called++
				if version != buildinfo.Version {
					t.Fatalf("runner version = %q, want %q", version, buildinfo.Version)
				}
				return test.result, nil
			}
			if err := runSelfUpdate(contextForTest(&stdout, &stderr), test.args, runner); err != nil {
				t.Fatalf("runSelfUpdate() error = %v", err)
			}
			if called != 1 {
				t.Fatalf("runner calls = %d, want 1", called)
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout = %q, want containing %q", stdout.String(), test.want)
			}
		})
	}
}

func TestRunSelfUpdateRejectsArgumentsBeforeCallingNetworkRunner(t *testing.T) {
	called := false
	runner := func(stdcontext.Context, string) (updater.Result, error) {
		called = true
		return updater.Result{}, nil
	}
	var stdout, stderr bytes.Buffer
	err := runSelfUpdate(contextForTest(&stdout, &stderr), []string{"unexpected"}, runner)
	if err == nil || !strings.Contains(err.Error(), "usage: wtp [--json] update") {
		t.Fatalf("runSelfUpdate() error = %v", err)
	}
	if called {
		t.Fatal("invalid update arguments called the updater")
	}
}

func TestRunInformationalCommandsDoNotInitializeFlatfileStorage(t *testing.T) {
	for _, command := range []string{"help", "schema"} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)

			var stdout, stderr bytes.Buffer
			if err := Run([]string{command}, &stdout, &stderr); err != nil {
				t.Fatalf("Run(%s) error = %v", command, err)
			}
			if _, err := os.Stat(filepath.Join(dir, ".wtp")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s created .wtp: stat error = %v", command, err)
			}
		})
	}
}

func TestRunSchemaDoesNotMigrateExistingFlatfileStorage(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	legacyTask := filepath.Join(dir, ".wtp", "todo", "legacy.json")
	if err := os.MkdirAll(filepath.Dir(legacyTask), 0o755); err != nil {
		t.Fatalf("create legacy task directory: %v", err)
	}
	if err := os.WriteFile(legacyTask, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy task: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"schema"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(schema) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".wtp", "meta", "index.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("schema initialized or migrated flatfile storage: stat error = %v", err)
	}
	if got, err := os.ReadFile(legacyTask); err != nil || string(got) != "legacy" {
		t.Fatalf("legacy task changed: got %q, error = %v", got, err)
	}
}

func TestRunInformationalCommandsRejectUnexpectedArgumentsWithoutStorageSideEffects(t *testing.T) {
	for _, command := range []string{"help", "schema"} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			var stdout, stderr bytes.Buffer

			err := Run([]string{command, "--unexpected"}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "usage: wtp "+command) {
				t.Fatalf("Run(%s --unexpected) error = %v", command, err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, ".wtp")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid %s initialized .wtp: stat error = %v", command, statErr)
			}
		})
	}
}

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

func TestRunTaskCreatePrintsBlockedReadinessForUnfinishedDependency(t *testing.T) {
	p, err := flatfile.New(t.TempDir())
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "Unfinished dependency"})
	if err != nil {
		t.Fatalf("CreateTask(dependency) error = %v", err)
	}

	var stdout bytes.Buffer
	ctx := context{
		provider: p,
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
		jsonOut:  true,
	}
	if err := runTaskCreate(ctx, []string{"--title", "Blocked task", "--depends-on", dependency.ShortID}); err != nil {
		t.Fatalf("runTaskCreate() error = %v", err)
	}

	var created core.TaskView
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal task creation output: %v", err)
	}
	if created.Readiness.Claimable {
		t.Fatal("created task output is claimable despite an unfinished dependency")
	}
	if !created.Readiness.Blocked {
		t.Fatal("created task output is not marked blocked")
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

func TestTaskTargetCommandsSupportEqualsOptionForms(t *testing.T) {
	getProvider := &getTestProvider{}
	getCtx := context{provider: getProvider, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := runTaskGet(getCtx, []string{"--agent=Tony", "wtp-0028"}, "show"); err != nil {
		t.Fatalf("runTaskGet() error = %v", err)
	}
	if getProvider.gotAgent != "Tony" {
		t.Fatalf("get agent = %q, want Tony", getProvider.gotAgent)
	}

	transitionProvider := &updateTestProvider{}
	transitionCtx := context{provider: transitionProvider, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := runTaskTransition(transitionCtx, "done", core.StatusDone, []string{"wtp-0028", "--agent=Tony"}); err != nil {
		t.Fatalf("runTaskTransition() error = %v", err)
	}
	if transitionProvider.gotActor != "Tony" || transitionProvider.gotStatus != core.StatusDone {
		t.Fatalf("transition = actor %q, status %q", transitionProvider.gotActor, transitionProvider.gotStatus)
	}

	commentProvider := &updateTestProvider{}
	commentCtx := context{provider: commentProvider, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := runTaskComment(commentCtx, []string{"wtp-0028", "--agent=Tony", "--message=ready"}); err != nil {
		t.Fatalf("runTaskComment() error = %v", err)
	}
	if commentProvider.gotActor != "Tony" || commentProvider.gotComment != "ready" {
		t.Fatalf("comment = actor %q, message %q", commentProvider.gotActor, commentProvider.gotComment)
	}
}

func TestTaskTargetCommandsRejectUnknownAndMissingOptions(t *testing.T) {
	tests := []struct {
		name    string
		run     func() error
		wantErr string
	}{
		{
			name: "show unknown option",
			run: func() error {
				return runTaskGet(context{provider: &getTestProvider{}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{"wtp-0028", "--unknown", "value"}, "show")
			},
			wantErr: "unknown option \"--unknown\"",
		},
		{
			name: "get missing agent",
			run: func() error {
				return runTaskGet(context{provider: &getTestProvider{}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{"wtp-0028", "--agent"}, "get")
			},
			wantErr: "option \"--agent\" requires a value",
		},
		{
			name: "transition unknown option",
			run: func() error {
				return runTaskTransition(context{provider: &updateTestProvider{}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, "start", core.StatusInProgress, []string{"wtp-0028", "--unknown", "value"})
			},
			wantErr: "unknown option \"--unknown\"",
		},
		{
			name: "comment missing message",
			run: func() error {
				return runTaskComment(context{provider: &updateTestProvider{}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{"wtp-0028", "--message"})
			},
			wantErr: "option \"--message\" requires a value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
		})
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

func TestHelpMentionsShowTaskUpdateSelfUpdateEditSchemaAndModel(t *testing.T) {
	var stdout bytes.Buffer
	if err := help(&stdout); err != nil {
		t.Fatalf("help() error = %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"wtp task show", "wtp task update", "wtp update", "checksum-verified", "wtp task edit", "wtp graph", "wtp schema", "--model", "Usage Guide:"} {
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
	for _, needle := range []string{"dependencies are stored as canonical UUID strings", ".wtp/meta/index.json", "Task JSON schema:", `"model": "gpt-5"`, "model: optional free-form string", "--model", "Task UUIDs and short IDs must be unique", "todo has no lifecycle timestamps", "comments created without an agent remain valid"} {
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
	gotStatus      core.Status
	gotActor       string
	gotComment     string
	createCalls    int
	updateCalls    int
	statusCalls    int
	commentCalls   int
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
	p.gotID = idOrShortID
	p.gotStatus = target
	p.gotActor = actor
	p.statusCalls++
	return p.UpdateTask(idOrShortID, core.UpdateTaskInput{})
}

func (p *updateTestProvider) AddComment(idOrShortID, actor, message string) (core.TaskView, error) {
	p.gotID = idOrShortID
	p.gotActor = actor
	p.gotComment = message
	p.commentCalls++
	return p.UpdateTask(idOrShortID, core.UpdateTaskInput{})
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

func chdir(t *testing.T, dir string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("change working directory to %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory to %s: %v", original, err)
		}
	})
}

func contextForTest(stdout, stderr *bytes.Buffer) context {
	return context{stdout: stdout, stderr: stderr}
}
