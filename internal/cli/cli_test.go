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
	"github.com/mattrandles/wtproj/internal/runtimecontext"
	"github.com/mattrandles/wtproj/internal/stats"
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

func TestRewriteLegacyArgsPausePreservesAgent(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{
		"--agent", "Jim",
		"--set-task-paused",
		"--task-id", "wtp-0003",
	})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs() error = %v", err)
	}

	want := []string{"task", "pause", "--agent", "Jim", "wtp-0003"}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs() args = %v, want %v", got.args, want)
	}
}

func TestRewriteLegacyArgsRequiresComment(t *testing.T) {
	_, err := rewriteLegacyArgs([]string{"--add-comment", "--task-id", "wtp-0003"})
	if err == nil {
		t.Fatal("expected missing comment error")
	}
}

func TestRewriteLegacyArgsCreatePreservesTaskMetadata(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{
		"--create-task",
		"--title", "New task",
		"--priority", "high",
		"--estimate", "m",
		"--lane", "backend",
		"--model", "gpt-5",
		"--git-repo", "/workspace/repo",
		"--git-branch", "feature/task-metadata",
		"--worktree-name", "task-metadata",
		"--worktree-dir", "/workspace/task-metadata",
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
		"--git-repo", "/workspace/repo",
		"--git-branch", "feature/task-metadata",
		"--worktree-name", "task-metadata",
		"--worktree-dir", "/workspace/task-metadata",
		"--depends-on", "wtp-0001",
		"--agent", "Jim",
	}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs args = %v, want %v", got.args, want)
	}
}

func TestRewriteLegacyArgsCreatePreservesExplicitEmptyContextOverride(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{
		"--create-task",
		"--title", "Legacy task",
		"--git-repo", "/explicit/repository",
		"--git-branch=",
		"--worktree-dir", "",
	})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs() error = %v", err)
	}

	want := []string{
		"task", "create",
		"--title", "Legacy task",
		"--git-repo", "/explicit/repository",
		"--git-branch=",
		"--worktree-dir=",
	}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewriteLegacyArgs() args = %v, want %v", got.args, want)
	}
}

func TestRunTaskCreatePassesTaskMetadataToProvider(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
	}

	err := runTaskCreate(ctx, []string{
		"--title", "Metadata-aware task",
		"--model", "gpt-5.2-codex",
		"--git-repo", "/workspace/repo",
		"--git-branch", "feature/task-metadata",
		"--worktree-name", "task-metadata",
		"--worktree-dir", "/workspace/task-metadata",
	})
	if err != nil {
		t.Fatalf("runTaskCreate() error = %v", err)
	}
	if provider.gotCreateInput.Model != "gpt-5.2-codex" {
		t.Fatalf("model input = %q, want %q", provider.gotCreateInput.Model, "gpt-5.2-codex")
	}
	if provider.gotCreateInput.GitRepo != "/workspace/repo" ||
		provider.gotCreateInput.GitBranch != "feature/task-metadata" ||
		provider.gotCreateInput.WorktreeName != "task-metadata" ||
		provider.gotCreateInput.WorktreeDir != "/workspace/task-metadata" {
		t.Fatalf("Git/worktree metadata = %#v", provider.gotCreateInput)
	}
	if provider.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", provider.createCalls)
	}
}

func TestRunTaskCreateDefaultsEachOmittedContextFieldIndependently(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		invocation: runtimecontext.Context{
			InGit:          true,
			RepositoryRoot: "/discovered/repository",
			WorktreeRoot:   "/discovered/worktree",
			Branch:         "discovered-branch",
			WorktreeName:   "discovered-worktree",
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}

	err := runTaskCreate(ctx, []string{
		"--title", "Partially overridden task",
		"--git-branch=",
		"--worktree-name", "explicit-worktree",
	})
	if err != nil {
		t.Fatalf("runTaskCreate() error = %v", err)
	}

	got := provider.gotCreateInput
	if got.GitRepo != "/discovered/repository" {
		t.Fatalf("gitRepo = %q, want discovered repository", got.GitRepo)
	}
	if got.GitBranch != "" {
		t.Fatalf("gitBranch = %q, want explicitly empty", got.GitBranch)
	}
	if got.WorktreeName != "explicit-worktree" {
		t.Fatalf("worktreeName = %q, want explicit-worktree", got.WorktreeName)
	}
	if got.WorktreeDir != "/discovered/worktree" {
		t.Fatalf("worktreeDir = %q, want discovered worktree", got.WorktreeDir)
	}
}

func TestRunTaskCreateDefaultsDetachedAndNonGitContexts(t *testing.T) {
	tests := []struct {
		name       string
		invocation runtimecontext.Context
		want       core.CreateTaskInput
	}{
		{
			name: "detached HEAD",
			invocation: runtimecontext.Context{
				InGit:          true,
				RepositoryRoot: "/repository",
				WorktreeRoot:   "/worktree",
				DetachedHEAD:   true,
				WorktreeName:   "detached-worktree",
			},
			want: core.CreateTaskInput{
				GitRepo:      "/repository",
				GitBranch:    "",
				WorktreeName: "detached-worktree",
				WorktreeDir:  "/worktree",
			},
		},
		{
			name:       "non-Git invocation",
			invocation: runtimecontext.Context{InvocationDir: "/outside-git"},
			want:       core.CreateTaskInput{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &updateTestProvider{}
			ctx := context{
				provider:   provider,
				invocation: test.invocation,
				stdout:     &bytes.Buffer{},
				stderr:     &bytes.Buffer{},
			}
			if err := runTaskCreate(ctx, []string{"--title", test.name}); err != nil {
				t.Fatalf("runTaskCreate() error = %v", err)
			}

			got := provider.gotCreateInput
			if got.GitRepo != test.want.GitRepo ||
				got.GitBranch != test.want.GitBranch ||
				got.WorktreeName != test.want.WorktreeName ||
				got.WorktreeDir != test.want.WorktreeDir {
				t.Fatalf("context metadata = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRunTaskCreateDefaultsLinkedWorktreeMetadata(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	mainRoot := filepath.Join(fixture, "main-repository")
	linkedRoot := filepath.Join(fixture, "linked-context")
	runConfigGit(t, fixture, "init", mainRoot)
	runConfigGit(t, mainRoot, "config", "user.name", "WTP Test")
	runConfigGit(t, mainRoot, "config", "user.email", "wtp-test@example.invalid")
	if err := os.WriteFile(filepath.Join(mainRoot, "tracked.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runConfigGit(t, mainRoot, "add", "tracked.txt")
	runConfigGit(t, mainRoot, "commit", "-m", "fixture")
	runConfigGit(t, mainRoot, "worktree", "add", "-b", "linked-context-branch", linkedRoot)

	nested := filepath.Join(linkedRoot, "nested", "invocation")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	p, invocation, err := providerAndContextForInvocation(nested)
	if err != nil {
		t.Fatalf("providerAndContextForInvocation() error = %v", err)
	}
	scoped, ok := p.(interface{ InvocationScope() *core.BranchScope })
	if !ok {
		t.Fatalf("providerAndContextForInvocation() provider %T does not expose its invocation scope", p)
	}
	if got, want := scoped.InvocationScope(), invocation.Scope(); got == nil || want == nil || *got != *want {
		t.Fatalf("provider invocation scope = %#v, want %#v", got, want)
	}
	ctx := context{
		provider:   p,
		invocation: invocation,
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
	}
	if err := runTaskCreate(ctx, []string{"--title", "Linked worktree task"}); err != nil {
		t.Fatalf("runTaskCreate() error = %v", err)
	}

	tasks, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	task := tasks[0]
	assertEquivalentPath(t, task.GitRepo, mainRoot)
	assertEquivalentPath(t, task.WorktreeDir, linkedRoot)
	if task.GitBranch != "linked-context-branch" || task.WorktreeName != "linked-context" {
		t.Fatalf("linked worktree metadata = %#v", task.Task)
	}
}

func TestLegacyCreateUsesTheSameContextDefaultsAndOverrides(t *testing.T) {
	legacy, err := rewriteLegacyArgs([]string{
		"--create-task",
		"--title", "Legacy context task",
		"--git-branch=",
		"--worktree-name", "legacy-explicit",
	})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs() error = %v", err)
	}

	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		invocation: runtimecontext.Context{
			InGit:          true,
			RepositoryRoot: "/discovered/repository",
			WorktreeRoot:   "/discovered/worktree",
			Branch:         "discovered-branch",
			WorktreeName:   "discovered-worktree",
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	if err := runTaskCreate(ctx, legacy.args[2:]); err != nil {
		t.Fatalf("runTaskCreate(rewritten legacy args) error = %v", err)
	}

	got := provider.gotCreateInput
	if got.GitRepo != "/discovered/repository" ||
		got.GitBranch != "" ||
		got.WorktreeName != "legacy-explicit" ||
		got.WorktreeDir != "/discovered/worktree" {
		t.Fatalf("legacy context metadata = %#v", got)
	}
}

func TestRunTaskCreatePrintsBlockedReadinessForUnfinishedDependency(t *testing.T) {
	p, err := flatfile.New(t.TempDir(), nil)
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

func TestRunTaskReadyRejectsNonPositiveLimits(t *testing.T) {
	for _, limit := range []string{"0", "-1"} {
		t.Run("limit="+limit, func(t *testing.T) {
			ctx := context{
				provider: readyTestProvider{},
				stdout:   &bytes.Buffer{},
				stderr:   &bytes.Buffer{},
			}
			err := runTaskReady(ctx, []string{"--limit", limit})
			if err == nil || !strings.Contains(err.Error(), "ready task limit must be greater than zero") {
				t.Fatalf("runTaskReady(limit=%s) error = %v, want invalid limit error", limit, err)
			}
		})
	}
}

func TestRunTaskUpdatePassesMutableFieldsToProvider(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		stdout:   &bytes.Buffer{},
		stderr:   &bytes.Buffer{},
	}

	err := runTaskUpdate(ctx, []string{
		"wtp-0028",
		"--depends-on", "wtp-0020",
		"--priority", "high",
		"--model", "o3",
		"--git-repo", "/workspace/repo",
		"--git-branch", "feature/task-metadata",
		"--worktree-name", "task-metadata",
		"--worktree-dir", "/workspace/task-metadata",
		"--agent", "Tony",
	})
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
	if !provider.gotInput.GitRepo.Set || provider.gotInput.GitRepo.Value != "/workspace/repo" ||
		!provider.gotInput.GitBranch.Set || provider.gotInput.GitBranch.Value != "feature/task-metadata" ||
		!provider.gotInput.WorktreeName.Set || provider.gotInput.WorktreeName.Value != "task-metadata" ||
		!provider.gotInput.WorktreeDir.Set || provider.gotInput.WorktreeDir.Value != "/workspace/task-metadata" {
		t.Fatalf("Git/worktree update input = %#v", provider.gotInput)
	}
}

func TestRunTaskUpdateCanClearMetadataFields(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		invocation: runtimecontext.Context{
			InGit:          true,
			RepositoryRoot: "/current/repository",
			WorktreeRoot:   "/current/worktree",
			Branch:         "current-branch",
			WorktreeName:   "current-worktree",
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}

	if err := runTaskUpdate(ctx, []string{"wtp-0028", "--model=", "--git-repo=", "--git-branch=", "--worktree-name=", "--worktree-dir="}); err != nil {
		t.Fatalf("runTaskUpdate() error = %v", err)
	}
	if !provider.gotInput.Model.Set || provider.gotInput.Model.Value != "" {
		t.Fatalf("model input = %#v, want explicitly empty", provider.gotInput.Model)
	}
	if !provider.gotInput.GitRepo.Set || provider.gotInput.GitRepo.Value != "" ||
		!provider.gotInput.GitBranch.Set || provider.gotInput.GitBranch.Value != "" ||
		!provider.gotInput.WorktreeName.Set || provider.gotInput.WorktreeName.Value != "" ||
		!provider.gotInput.WorktreeDir.Set || provider.gotInput.WorktreeDir.Value != "" {
		t.Fatalf("Git/worktree clear input = %#v", provider.gotInput)
	}
}

func TestRunTaskUpdateDoesNotRefreshContextMetadata(t *testing.T) {
	provider := &updateTestProvider{}
	ctx := context{
		provider: provider,
		invocation: runtimecontext.Context{
			InGit:          true,
			RepositoryRoot: "/current/repository",
			WorktreeRoot:   "/current/worktree",
			Branch:         "current-branch",
			WorktreeName:   "current-worktree",
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}

	if err := runTaskUpdate(ctx, []string{"wtp-0028", "--description", "preserve stored context"}); err != nil {
		t.Fatalf("runTaskUpdate() error = %v", err)
	}
	got := provider.gotInput
	if got.GitRepo.Set || got.GitBranch.Set || got.WorktreeName.Set || got.WorktreeDir.Set {
		t.Fatalf("update injected invocation context: %#v", got)
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

func TestRunHandoffWriteHumanOutputIncludesScopeCountAndPurgeCommand(t *testing.T) {
	taskID := "25c3806a-bd1b-424d-889b-29e5b06679b8"
	handoff := testCLIHandoff("00000000-0000-4000-8000-000000000001", taskID, "task context")
	p := &handoffWriteTestProvider{writeResult: provider.HandoffWriteResult{Handoff: handoff, ScopeCount: 2}}
	var stdout, stderr bytes.Buffer

	err := runHandoffWrite(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{"--message", " task context ", "--agent", " Tony ", "--task", "wtp-0028", "--replace"})
	if err != nil {
		t.Fatalf("runHandoffWrite() error = %v", err)
	}
	if got, want := p.writeRequest, (provider.HandoffWriteRequest{Task: "wtp-0028", Author: " Tony ", Message: " task context ", Replace: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("WriteHandoff request = %#v, want %#v", got, want)
	}
	for _, want := range []string{"scope: task " + taskID, "message: task context", "scopeCount: 2", "purge: wtp handoff purge --task " + taskID} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("write output missing %q: %q", want, stdout.String())
		}
	}
}

func TestRunHandoffWritePassesOptionalFieldsAndReplacement(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantRequest provider.HandoffWriteRequest
	}{
		{
			name: "append with optional agent and task",
			args: []string{"--message", "context", "--agent", "Ada", "--task", "wtp-0028"},
			wantRequest: provider.HandoffWriteRequest{
				Task:    "wtp-0028",
				Author:  "Ada",
				Message: "context",
			},
		},
		{
			name: "replace",
			args: []string{"--message", "context", "--replace"},
			wantRequest: provider.HandoffWriteRequest{
				Message: "context",
				Replace: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffWriteTestProvider{writeResult: provider.HandoffWriteResult{Handoff: testCLIHandoff("00000000-0000-4000-8000-000000000002", "", "context")}}
			var stdout, stderr bytes.Buffer
			if err := runHandoffWrite(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args); err != nil {
				t.Fatalf("runHandoffWrite() error = %v", err)
			}
			if got := p.writeRequest; !reflect.DeepEqual(got, test.wantRequest) {
				t.Fatalf("WriteHandoff request = %#v, want %#v", got, test.wantRequest)
			}
		})
	}
}

func TestRunHandoffWriteRequiresNonblankMessageAndRejectsUnexpectedArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "empty message", args: []string{"--message", ""}, wantErr: "handoff message is required"},
		{name: "whitespace message", args: []string{"--message", " \t\n "}, wantErr: "handoff message is required"},
		{name: "unexpected positional argument", args: []string{"--message", "context", "unexpected"}, wantErr: "usage: wtp handoff write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffWriteTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runHandoffWrite(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("runHandoffWrite() error = %v, want containing %q", err, test.wantErr)
			}
			if p.writeCalls != 0 {
				t.Fatalf("WriteHandoff calls = %d, want 0", p.writeCalls)
			}
		})
	}
}

func TestRunHandoffWritePropagatesTaskResolutionError(t *testing.T) {
	wantErr := errors.New(`task "wtp-9999" not found`)
	p := &handoffWriteTestProvider{writeErr: wantErr}
	var stdout, stderr bytes.Buffer

	err := runHandoffWrite(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{"--message", "context", "--task", "wtp-9999"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runHandoffWrite() error = %v, want %v", err, wantErr)
	}
	if got, want := p.writeRequest.Task, "wtp-9999"; got != want {
		t.Fatalf("WriteHandoff task = %q, want %q", got, want)
	}
}

func TestRunHandoffWritePropagatesProviderError(t *testing.T) {
	wantErr := errors.New("handoff storage unavailable")
	p := &handoffWriteTestProvider{writeErr: wantErr}
	var stdout, stderr bytes.Buffer

	err := runHandoffWrite(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{"--message", "context"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runHandoffWrite() error = %v, want %v", err, wantErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty output on provider error", stdout.String())
	}
}

func TestRunHandoffWriteJSONUsesProviderResult(t *testing.T) {
	handoff := testCLIHandoff("00000000-0000-4000-8000-000000000002", "25c3806a-bd1b-424d-889b-29e5b06679b8", "task context")
	p := &handoffWriteTestProvider{writeResult: provider.HandoffWriteResult{Handoff: handoff, ScopeCount: 1}}
	var stdout, stderr bytes.Buffer

	if err := runHandoffWrite(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{"--message", "task context", "--task", "wtp-0028"}); err != nil {
		t.Fatalf("runHandoffWrite() error = %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("JSON output is invalid: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("JSON output keys = %v, want exactly handoff and scopeCount", got)
	}
	for _, key := range []string{"handoff", "scopeCount"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON output missing %q: %v", key, got)
		}
	}
	var result provider.HandoffWriteResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode handoff write JSON: %v", err)
	}
	if !reflect.DeepEqual(result, p.writeResult) {
		t.Fatalf("JSON result = %#v, want %#v", result, p.writeResult)
	}
}

func TestRunHandoffGetPassesScopeAndLimitOptions(t *testing.T) {
	const taskID = "wtp-0028"
	tests := []struct {
		name       string
		args       []string
		wantFilter provider.HandoffFilter
	}{
		{name: "default newest global", wantFilter: provider.HandoffFilter{Limit: 1}},
		{name: "task scope", args: []string{"--task", taskID}, wantFilter: provider.HandoffFilter{Task: taskID, Limit: 1}},
		{name: "all scopes", args: []string{"--all-scopes"}, wantFilter: provider.HandoffFilter{AllScopes: true, Limit: 1}},
		{name: "positive limit", args: []string{"--limit", "3"}, wantFilter: provider.HandoffFilter{Limit: 3}},
		{name: "all matching records", args: []string{"--all"}, wantFilter: provider.HandoffFilter{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffTestProvider{listResult: provider.HandoffListResult{Handoffs: []core.Handoff{testCLIHandoff("00000000-0000-4000-8000-000000000003", "", "context")}}}
			var stdout, stderr bytes.Buffer
			if err := runHandoffGet(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args); err != nil {
				t.Fatalf("runHandoffGet() error = %v", err)
			}
			if got := p.listFilter; !reflect.DeepEqual(got, test.wantFilter) {
				t.Fatalf("ListHandoffs filter = %#v, want %#v", got, test.wantFilter)
			}
			if p.listCalls != 1 {
				t.Fatalf("ListHandoffs calls = %d, want 1", p.listCalls)
			}
		})
	}
}

func TestRunHandoffGetHumanRenderingAndFollowUpHints(t *testing.T) {
	const taskID = "wtp-0028"
	tests := []struct {
		name       string
		args       []string
		listResult provider.HandoffListResult
		wantOutput []string
	}{
		{
			name: "renders newest global and exposes hidden scopes",
			listResult: provider.HandoffListResult{
				Handoffs:             []core.Handoff{testCLIHandoff("00000000-0000-4000-8000-000000000003", "", "newest global")},
				TotalMatching:        3,
				HasMore:              true,
				OtherScopesAvailable: true,
			},
			wantOutput: []string{
				"00000000-0000-4000-8000-000000000003",
				"scope: global",
				"author: Tony",
				"message: newest global",
				"created: 2026-08-09T18:00:00Z",
				"more matching handoffs: wtp handoff get --all",
				"other scopes: wtp handoff get --all-scopes --all",
			},
		},
		{
			name: "task truncation points to task all",
			args: []string{"--task", taskID},
			listResult: provider.HandoffListResult{
				Handoffs:      []core.Handoff{testCLIHandoff("00000000-0000-4000-8000-000000000004", taskID, "task context")},
				TotalMatching: 2,
				HasMore:       true,
			},
			wantOutput: []string{
				"scope: task " + taskID,
				"message: task context",
				"more matching handoffs: wtp handoff get --task " + taskID + " --all",
			},
		},
		{
			name:       "no results",
			listResult: provider.HandoffListResult{Handoffs: []core.Handoff{}, TotalMatching: 0},
			wantOutput: []string{"no handoffs found"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffTestProvider{listResult: test.listResult}
			var stdout, stderr bytes.Buffer
			if err := runHandoffGet(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args); err != nil {
				t.Fatalf("runHandoffGet() error = %v", err)
			}
			for _, want := range test.wantOutput {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("get output missing %q: %q", want, stdout.String())
				}
			}
			if test.name == "no results" && stdout.String() != "no handoffs found\n" {
				t.Fatalf("no-result output = %q, want exact no-result message", stdout.String())
			}
		})
	}
}

func TestRunHandoffGetJSONIncludesListMetadata(t *testing.T) {
	want := provider.HandoffListResult{
		Handoffs: []core.Handoff{
			testCLIHandoff("00000000-0000-4000-8000-000000000005", "", "global context"),
			testCLIHandoff("00000000-0000-4000-8000-000000000006", "25c3806a-bd1b-424d-889b-29e5b06679b8", "task context"),
		},
		TotalMatching:        2,
		HasMore:              false,
		OtherScopesAvailable: true,
	}
	p := &handoffTestProvider{listResult: want}
	var stdout, stderr bytes.Buffer
	if err := runHandoffGet(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{"--all-scopes", "--all"}); err != nil {
		t.Fatalf("runHandoffGet() error = %v", err)
	}
	if got, expected := p.listFilter, (provider.HandoffFilter{AllScopes: true}); !reflect.DeepEqual(got, expected) {
		t.Fatalf("ListHandoffs filter = %#v, want %#v", got, expected)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
		t.Fatalf("JSON output is invalid: %v", err)
	}
	wantFields := []string{"handoffs", "totalMatching", "hasMore", "otherScopesAvailable"}
	if len(fields) != len(wantFields) {
		t.Fatalf("JSON output keys = %v, want exactly %v", fields, wantFields)
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Fatalf("JSON output missing %q: %v", field, fields)
		}
	}
	var got provider.HandoffListResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode handoff list JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON result = %#v, want %#v", got, want)
	}
}

func TestRunHandoffGetRejectsConflictsAndNonpositiveLimitsWithUsage(t *testing.T) {
	const usage = "usage: wtp handoff get [--task <task-id> | --all-scopes] [--limit N | --all]"
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "task and all scopes", args: []string{"--task", "wtp-0028", "--all-scopes"}, wantErr: "handoff get accepts either --task or --all-scopes, not both"},
		{name: "limit and all", args: []string{"--limit", "2", "--all"}, wantErr: "handoff get accepts either --limit or --all, not both"},
		{name: "zero limit", args: []string{"--limit", "0"}, wantErr: "handoff limit must be greater than zero"},
		{name: "negative limit", args: []string{"--limit=-1"}, wantErr: "handoff limit must be greater than zero"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runHandoffGet(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) || !strings.Contains(err.Error(), usage) {
				t.Fatalf("runHandoffGet() error = %v, want %q and usage guidance", err, test.wantErr)
			}
			if p.listCalls != 0 {
				t.Fatalf("ListHandoffs calls = %d, want 0 for invalid options", p.listCalls)
			}
		})
	}
}

func TestRunHandoffPurgePassesEachSelector(t *testing.T) {
	const (
		handoffID = "00000000-0000-4000-8000-000000000004"
		taskID    = "wtp-0028"
	)
	tests := []struct {
		name        string
		args        []string
		wantRequest provider.HandoffPurgeRequest
	}{
		{
			name:        "id",
			args:        []string{"--id", handoffID},
			wantRequest: provider.HandoffPurgeRequest{ID: handoffID},
		},
		{
			name:        "global",
			args:        []string{"--global"},
			wantRequest: provider.HandoffPurgeRequest{Global: true},
		},
		{
			name:        "task",
			args:        []string{"--task", taskID},
			wantRequest: provider.HandoffPurgeRequest{Task: taskID},
		},
		{
			name:        "all scopes",
			args:        []string{"--all-scopes"},
			wantRequest: provider.HandoffPurgeRequest{AllScopes: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffTestProvider{}
			var stdout, stderr bytes.Buffer
			if err := runHandoffPurge(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args); err != nil {
				t.Fatalf("runHandoffPurge() error = %v", err)
			}
			if got := p.purgeRequest; !reflect.DeepEqual(got, test.wantRequest) {
				t.Fatalf("PurgeHandoffs request = %#v, want %#v", got, test.wantRequest)
			}
			if p.purgeCalls != 1 {
				t.Fatalf("PurgeHandoffs calls = %d, want 1", p.purgeCalls)
			}
		})
	}
}

func TestRunHandoffPurgeParsesCutoffsAndRendersHumanCount(t *testing.T) {
	wantBefore := time.Date(2026, time.August, 1, 10, 34, 56, 0, time.UTC)
	p := &handoffTestProvider{purgeResult: provider.HandoffPurgeResult{Purged: 4}}
	var stdout, stderr bytes.Buffer
	if err := runHandoffPurge(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{
		"--task", "wtp-0028", "--before", "2026-08-01T12:34:56+02:00",
	}); err != nil {
		t.Fatalf("runHandoffPurge(--before) error = %v", err)
	}
	if p.purgeRequest.Before == nil || !p.purgeRequest.Before.Equal(wantBefore) || p.purgeRequest.Before.Location() != time.UTC {
		t.Fatalf("PurgeHandoffs cutoff = %#v, want exact UTC %s", p.purgeRequest.Before, wantBefore)
	}
	if got, want := stdout.String(), "purged: 4\n"; got != want {
		t.Fatalf("purge output = %q, want %q", got, want)
	}

	p = &handoffTestProvider{}
	var olderStdout bytes.Buffer
	cutoffStart := time.Now().UTC()
	if err := runHandoffPurge(context{provider: p, stdout: &olderStdout, stderr: &bytes.Buffer{}}, []string{"--global", "--older-than", "90m"}); err != nil {
		t.Fatalf("runHandoffPurge(--older-than) error = %v", err)
	}
	cutoffEnd := time.Now().UTC()
	if p.purgeRequest.Before == nil {
		t.Fatalf("PurgeHandoffs request = %#v, want older-than cutoff", p.purgeRequest)
	}
	wantEarliest := cutoffStart.Add(-90 * time.Minute)
	wantLatest := cutoffEnd.Add(-90 * time.Minute)
	if got := *p.purgeRequest.Before; got.Before(wantEarliest) || got.After(wantLatest) || got.Location() != time.UTC {
		t.Fatalf("older-than cutoff = %s, want between %s and %s", got, wantEarliest, wantLatest)
	}
}

func TestRunHandoffPurgeRejectsInvalidArgumentsBeforeProvider(t *testing.T) {
	const usage = "usage: wtp handoff purge (--id <handoff-id> | --global | --task <task-id> | --all-scopes) [--before RFC3339 | --older-than DURATION]"
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing selector", wantErr: "handoff purge requires exactly one selector"},
		{name: "multiple selectors", args: []string{"--global", "--all-scopes"}, wantErr: "handoff purge requires exactly one selector"},
		{name: "selector and task", args: []string{"--id", "00000000-0000-4000-8000-000000000004", "--task", "wtp-0028"}, wantErr: "handoff purge requires exactly one selector"},
		{name: "before and older-than", args: []string{"--global", "--before", "2026-08-01T00:00:00Z", "--older-than", "2h"}, wantErr: "handoff purge accepts either --before or --older-than, not both"},
		{name: "id and before", args: []string{"--id", "00000000-0000-4000-8000-000000000004", "--before", "2026-08-01T00:00:00Z"}, wantErr: "handoff purge --id cannot be combined with --before or --older-than"},
		{name: "id and older-than", args: []string{"--id", "00000000-0000-4000-8000-000000000004", "--older-than", "2h"}, wantErr: "handoff purge --id cannot be combined with --before or --older-than"},
		{name: "invalid before", args: []string{"--global", "--before", "not-a-time"}, wantErr: "handoff purge --before must be RFC3339"},
		{name: "zero older-than", args: []string{"--global", "--older-than", "0s"}, wantErr: "handoff purge --older-than must be a positive Go duration"},
		{name: "negative older-than", args: []string{"--global", "--older-than", "-1s"}, wantErr: "handoff purge --older-than must be a positive Go duration"},
		{name: "invalid older-than", args: []string{"--global", "--older-than", "not-a-duration"}, wantErr: "handoff purge --older-than must be a positive Go duration"},
		{name: "stray argument", args: []string{"--global", "stray"}, wantErr: usage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &handoffTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runHandoffPurge(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("runHandoffPurge() error = %v, want containing %q", err, test.wantErr)
			}
			if p.purgeCalls != 0 {
				t.Fatalf("PurgeHandoffs calls = %d, want 0 for invalid options", p.purgeCalls)
			}
		})
	}
}

func TestRunHandoffPurgeRendersZeroAndJSONCounts(t *testing.T) {
	p := &handoffTestProvider{purgeResult: provider.HandoffPurgeResult{Purged: 0}}
	var stdout, stderr bytes.Buffer
	if err := runHandoffPurge(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{"--all-scopes"}); err != nil {
		t.Fatalf("runHandoffPurge(zero matches) error = %v", err)
	}
	if got, want := stdout.String(), "purged: 0\n"; got != want {
		t.Fatalf("zero-match purge output = %q, want %q", got, want)
	}

	p = &handoffTestProvider{purgeResult: provider.HandoffPurgeResult{Purged: 4}}
	var jsonOutput bytes.Buffer
	if err := runHandoffPurge(context{provider: p, stdout: &jsonOutput, stderr: &bytes.Buffer{}, jsonOut: true}, []string{"--all-scopes"}); err != nil {
		t.Fatalf("runHandoffPurge(JSON) error = %v", err)
	}
	if got, want := jsonOutput.String(), "{\n  \"purged\": 4\n}\n"; got != want {
		t.Fatalf("purge JSON = %q, want %q", got, want)
	}
}

func TestRunHandoffPurgePropagatesProviderFailure(t *testing.T) {
	wantErr := errors.New("handoff storage unavailable")
	p := &handoffTestProvider{purgeErr: wantErr}
	var stdout, stderr bytes.Buffer

	err := runHandoffPurge(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{"--global"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runHandoffPurge() error = %v, want %v", err, wantErr)
	}
	if p.purgeCalls != 1 {
		t.Fatalf("PurgeHandoffs calls = %d, want 1", p.purgeCalls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty output on provider failure", stdout.String())
	}
}

func testCLIHandoff(id, taskID, message string) core.Handoff {
	return core.Handoff{
		ID:        id,
		TaskID:    taskID,
		Author:    "Tony",
		Message:   message,
		CreatedAt: time.Date(2026, time.August, 9, 18, 0, 0, 0, time.UTC),
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

func TestCustomStatusCLICreateUpdateSetStatusAndReopen(t *testing.T) {
	catalog := testCustomStatusCatalog(t)
	p, err := flatfile.NewWithCatalog(t.TempDir(), nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	if err := runTaskCreate(ctx, []string{"--title", "Reviewable", "--status", "waitingForReview"}); err != nil {
		t.Fatalf("runTaskCreate(custom) error = %v", err)
	}
	created := mustSingleTask(t, p, core.Status("waitingForReview"))
	if created.StartedAt == nil || created.CompletedAt != nil {
		t.Fatalf("custom waiting lifecycle = started %v completed %v", created.StartedAt, created.CompletedAt)
	}

	if err := runTaskUpdate(ctx, []string{created.ShortID, "--status", "blockedByReview", "--title", "Blocked review"}); err != nil {
		t.Fatalf("runTaskUpdate(custom) error = %v", err)
	}
	updated, err := p.GetTask(created.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(updated) error = %v", err)
	}
	if updated.Status != core.Status("blockedByReview") || updated.Title != "Blocked review" || updated.StartedAt != nil || updated.CompletedAt != nil {
		t.Fatalf("updated custom task = %#v", updated.Task)
	}

	if err := runTaskSetStatus(ctx, []string{created.ShortID, "inProgress", "--agent", "Reviewer"}); err != nil {
		t.Fatalf("runTaskSetStatus() error = %v", err)
	}
	if err := runTask(ctx, []string{"done", created.ShortID}); err != nil {
		t.Fatalf("done alias error = %v", err)
	}
	if err := runTask(ctx, []string{"set-status", created.ShortID, "waitingForReview"}); err != nil {
		t.Fatalf("reopen set-status error = %v", err)
	}
	reopened, err := p.GetTask(created.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(reopened) error = %v", err)
	}
	if reopened.Status != core.Status("waitingForReview") || reopened.StartedAt == nil || reopened.CompletedAt != nil {
		t.Fatalf("reopened task lifecycle = status %s started %v completed %v", reopened.Status, reopened.StartedAt, reopened.CompletedAt)
	}
}

func TestCustomStatusCLIRejectsUnconfiguredNames(t *testing.T) {
	catalog := testCustomStatusCatalog(t)
	p := &updateTestProvider{catalog: catalog}
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	for _, args := range [][]string{
		{"--title", "bad", "--status", "notConfigured"},
	} {
		if err := runTaskCreate(ctx, args); err == nil || !strings.Contains(err.Error(), `invalid status "notConfigured"`) {
			t.Fatalf("runTaskCreate(%v) error = %v", args, err)
		}
	}
	if p.createCalls != 0 {
		t.Fatalf("invalid create called provider %d times", p.createCalls)
	}
}

func TestCustomStatusCLIListGraphAndStatsFilters(t *testing.T) {
	catalog := testCustomStatusCatalog(t)
	custom := core.Status("waitingForReview")
	p := &statsTestProvider{graphTestProvider: graphTestProvider{
		catalog: catalog,
		tasks: []core.TaskView{
			{Task: core.Task{ID: "custom", ShortID: "wtp-0001", Title: "Custom", Status: custom}},
			{Task: core.Task{ID: "todo", ShortID: "wtp-0002", Title: "Todo", Status: core.StatusTodo}},
		},
	}}
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, jsonOut: true}
	if err := runTaskList(ctx, []string{"--status", string(custom)}); err != nil {
		t.Fatalf("runTaskList(custom) error = %v", err)
	}
	if p.lastFilter.Status == nil || *p.lastFilter.Status != custom {
		t.Fatalf("list filter = %#v, want %s", p.lastFilter, custom)
	}
	p.lastFilter = provider.TaskFilter{}
	ctx.stdout = &bytes.Buffer{}
	if err := runGraph(ctx, []string{"--status", string(custom)}); err != nil {
		t.Fatalf("runGraph(custom) error = %v", err)
	}
	if !strings.Contains(ctx.stdout.(*bytes.Buffer).String(), "wtp-0001") || strings.Contains(ctx.stdout.(*bytes.Buffer).String(), "wtp-0002") {
		t.Fatalf("custom graph output = %q", ctx.stdout.(*bytes.Buffer).String())
	}

	ctx.stdout = &bytes.Buffer{}
	if err := runStats(ctx, []string{string(custom)}); err != nil {
		t.Fatalf("runStats(custom) error = %v", err)
	}
	var report stats.Report
	if err := json.Unmarshal([]byte(ctx.stdout.(*bytes.Buffer).String()), &report); err != nil {
		t.Fatalf("decode custom stats: %v", err)
	}
	want := []stats.Bucket{{Value: "todo", Count: 0}, {Value: "inProgress", Count: 0}, {Value: "paused", Count: 0}, {Value: "done", Count: 0}, {Value: "waitingForReview", Count: 1}, {Value: "blockedByReview", Count: 0}}
	if !reflect.DeepEqual(report.StatusCounts, want) {
		t.Fatalf("custom statusCounts = %#v, want %#v", report.StatusCounts, want)
	}
}

func testCustomStatusCatalog(t *testing.T) core.StatusCatalog {
	t.Helper()
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "waitingForReview", Category: core.StatusCategoryWaiting},
		{Name: "blockedByReview", Category: core.StatusCategoryBlocked},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	return catalog
}

func mustSingleTask(t *testing.T, p provider.Provider, status core.Status) core.TaskView {
	t.Helper()
	tasks, err := p.ListTasks(provider.TaskFilter{Status: &status})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("ListTasks(%s) returned %d tasks, want 1", status, len(tasks))
	}
	return tasks[0]
}

func TestParseStatsArgsAcceptsOnlyDocumentedForms(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		status    *core.Status
		attribute string
		focused   bool
		wantErr   string
	}{
		{name: "overview", args: nil},
		{name: "status", args: []string{"done"}, status: statusPointer(core.StatusDone)},
		{name: "attribute", args: []string{"model"}, attribute: "model", focused: true},
		{name: "status and attribute", args: []string{"paused", "dependencies"}, status: statusPointer(core.StatusPaused), attribute: "dependencies", focused: true},
		{name: "reversed", args: []string{"model", "done"}, wantErr: "must precede"},
		{name: "invalid status", args: []string{"blocked"}, wantErr: "must be a status or attribute"},
		{name: "unknown attribute", args: []string{"done", "bogus"}, wantErr: "unknown stats attribute"},
		{name: "extra", args: []string{"done", "model", "extra"}, wantErr: statsUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, attribute, focused, err := parseStatsArgs(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseStatsArgs() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatsArgs() error = %v", err)
			}
			if !reflect.DeepEqual(status, test.status) || string(attribute) != test.attribute || focused != test.focused {
				t.Fatalf("parseStatsArgs() = (%v, %q, %t), want (%v, %q, %t)", status, attribute, focused, test.status, test.attribute, test.focused)
			}
		})
	}
}

func TestRunStatsSupportsEveryValidInvocationForm(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		json       bool
		wantStatus *core.Status
		focused    bool
	}{
		{name: "overview", args: nil},
		{name: "status-only", args: []string{"done"}, json: true, wantStatus: statusPointer(core.StatusDone)},
		{name: "attribute-only", args: []string{"lane"}, focused: true},
		{name: "status-and-attribute", args: []string{"paused", "comments"}, json: true, wantStatus: statusPointer(core.StatusPaused), focused: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{{Task: core.Task{ID: "task", Status: core.StatusTodo, Lane: "cli"}}}}}
			var stdout, stderr bytes.Buffer
			ctx := context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: test.json}
			if err := runStats(ctx, test.args); err != nil {
				t.Fatalf("runStats(%v) error = %v", test.args, err)
			}
			if test.wantStatus == nil {
				if p.lastFilter.Status != nil {
					t.Fatalf("stats filter = %#v, want no status filter", p.lastFilter)
				}
			} else if p.lastFilter.Status == nil || *p.lastFilter.Status != *test.wantStatus {
				t.Fatalf("stats filter = %#v, want status %q", p.lastFilter, *test.wantStatus)
			}
			if test.focused && !strings.Contains(stdout.String(), `"attribute"`) && !strings.Contains(stdout.String(), "attribute:") {
				t.Fatalf("focused stats output = %q", stdout.String())
			}
		})
	}
}

func TestRunStatsRejectsInvalidArgumentsBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid status", args: []string{"blocked"}, want: "must be a status or attribute"},
		{name: "unknown attribute", args: []string{"todo", "bogus"}, want: "unknown stats attribute"},
		{name: "reversed selector", args: []string{"model", "todo"}, want: "must precede"},
		{name: "excess arguments", args: []string{"todo", "model", "extra"}, want: statsUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &statsTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runStats(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runStats(%v) error = %v, want containing %q", test.args, err, test.want)
			}
			if p.listCalls != 0 {
				t.Fatalf("ListTasks calls = %d, want 0 for invalid arguments", p.listCalls)
			}
		})
	}
}

func TestRunStatsEmptyStoreHumanAndJSON(t *testing.T) {
	wantStatusCounts := []stats.Bucket{
		{Value: "todo", Count: 0},
		{Value: "inProgress", Count: 0},
		{Value: "paused", Count: 0},
		{Value: "done", Count: 0},
	}
	want := stats.Report{
		StatusCounts: wantStatusCounts,
		Attributes: stats.Attributes{
			Model: []stats.Bucket{}, Lane: []stats.Bucket{}, Priority: []stats.Bucket{},
			Estimate: []stats.Bucket{}, Assignee: []stats.Bucket{},
		},
	}

	t.Run("human", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := runStats(context{provider: &statsTestProvider{}, stdout: &stdout, stderr: &bytes.Buffer{}}, nil); err != nil {
			t.Fatalf("runStats() error = %v", err)
		}
		wantText := `stats
status: all
totalTasks: 0
statusCounts:
  todo: 0
  inProgress: 0
  paused: 0
  done: 0
model:
lane:
priority:
estimate:
assignee:
comments.tasksWithComments: 0
comments.totalRecords: 0
dependencies.tasksWithDependencies: 0
dependencies.independentTasks: 0
dependencies.directDependencyTotal: 0
handoffs.total: 0
handoffs.allStatusTotal: 0
handoffs.global: 0
handoffs.taskScoped: 0
`
		if got := stdout.String(); got != wantText {
			t.Fatalf("empty human stats = %q, want %q", got, wantText)
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := runStats(context{provider: &statsTestProvider{}, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}, nil); err != nil {
			t.Fatalf("runStats(JSON) error = %v", err)
		}
		var got stats.Report
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("decode empty stats JSON: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("empty stats JSON = %#v, want %#v", got, want)
		}
		if strings.Contains(stdout.String(), `"status"`) {
			t.Fatalf("unfiltered empty stats JSON contains status: %s", stdout.String())
		}
	})
}

func TestRunStatsMixedStoreHumanAndJSON(t *testing.T) {
	p := mixedStatsProvider()
	var stdout bytes.Buffer
	if err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, nil); err != nil {
		t.Fatalf("runStats() error = %v", err)
	}
	textOutput := stdout.String()
	for _, needle := range []string{
		"status: all", "totalTasks: 4", "todo: 1", "inProgress: 1", "paused: 1", "done: 1",
		"  (unset): 1\n  alpha: 1\n  beta: 1\n  zeta: 1",           // model, lexical with unset first
		"priority:\n  low: 1\n  medium: 1\n  high: 1\n  urgent: 1", // canonical order
		"estimate:\n  (unset): 1\n  xs: 1\n  m: 1\n  xl: 1",        // canonical order
		"comments.tasksWithComments: 2", "comments.totalRecords: 3",
		"dependencies.tasksWithDependencies: 2", "dependencies.independentTasks: 2", "dependencies.directDependencyTotal: 3",
		"handoffs.total: 7", "handoffs.allStatusTotal: 7", "handoffs.global: 2", "handoffs.taskScoped: 5",
	} {
		if !strings.Contains(textOutput, needle) {
			t.Fatalf("mixed human stats missing %q in %q", needle, textOutput)
		}
	}

	stdout.Reset()
	if err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}, nil); err != nil {
		t.Fatalf("runStats(JSON) error = %v", err)
	}
	var got stats.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode mixed stats JSON: %v", err)
	}
	want := stats.Report{
		TotalTasks:   4,
		StatusCounts: []stats.Bucket{{Value: "todo", Count: 1}, {Value: "inProgress", Count: 1}, {Value: "paused", Count: 1}, {Value: "done", Count: 1}},
		Attributes: stats.Attributes{
			Model:    []stats.Bucket{{Value: "", Count: 1}, {Value: "alpha", Count: 1}, {Value: "beta", Count: 1}, {Value: "zeta", Count: 1}},
			Lane:     []stats.Bucket{{Value: "", Count: 1}, {Value: "alpha", Count: 1}, {Value: "beta", Count: 1}, {Value: "zeta", Count: 1}},
			Priority: []stats.Bucket{{Value: "low", Count: 1}, {Value: "medium", Count: 1}, {Value: "high", Count: 1}, {Value: "urgent", Count: 1}},
			Estimate: []stats.Bucket{{Value: "", Count: 1}, {Value: "xs", Count: 1}, {Value: "m", Count: 1}, {Value: "xl", Count: 1}},
			Assignee: []stats.Bucket{{Value: "", Count: 1}, {Value: "Amy", Count: 1}, {Value: "Bob", Count: 1}, {Value: "Zed", Count: 1}},
		},
		Comments:     stats.CommentMetrics{TasksWithComments: 2, TotalRecords: 3},
		Dependencies: stats.DependencyMetrics{TasksWithDependencies: 2, IndependentTasks: 2, DirectDependencyTotal: 3},
		Handoffs:     stats.HandoffMetrics{Total: 7, AllStatusTotal: 7, Global: 2, TaskScoped: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed stats JSON = %#v, want %#v", got, want)
	}
	if !p.lastHandoffFilter.AllScopes {
		t.Fatalf("stats handoff filter = %#v, want all scopes", p.lastHandoffFilter)
	}
}

func TestRunStatsFilteredScopesKeepFourStatusCountsAndRelevantHandoffs(t *testing.T) {
	statuses := []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusPaused, core.StatusDone}
	wantTasks := map[core.Status]int{core.StatusTodo: 1, core.StatusInProgress: 1, core.StatusPaused: 1, core.StatusDone: 1}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			p := mixedStatsProvider()
			var stdout bytes.Buffer
			if err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}, []string{string(status)}); err != nil {
				t.Fatalf("runStats(%q) error = %v", status, err)
			}
			var got stats.Report
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("decode filtered stats JSON: %v", err)
			}
			if got.Status != string(status) || got.TotalTasks != wantTasks[status] {
				t.Fatalf("filtered report = %#v, want status %q and one task", got, status)
			}
			for _, bucket := range got.StatusCounts {
				want := 0
				if bucket.Value == string(status) {
					want = 1
				}
				if bucket.Count != want {
					t.Fatalf("status bucket %q = %d, want %d", bucket.Value, bucket.Count, want)
				}
			}
			if got.Handoffs != (stats.HandoffMetrics{Total: 3, AllStatusTotal: 7, Global: 2, TaskScoped: 1}) {
				t.Fatalf("filtered handoffs = %#v, want selected task plus two global records versus seven total", got.Handoffs)
			}
		})
	}
}

func TestRunStatsFocusedAttributesRenderOnlyRequestedMetric(t *testing.T) {
	tests := []struct {
		name      string
		attribute string
		wantJSON  string
		wantText  string
	}{
		{name: "model", attribute: "model", wantJSON: "buckets", wantText: "buckets:"},
		{name: "lane", attribute: "lane", wantJSON: "buckets", wantText: "buckets:"},
		{name: "priority", attribute: "priority", wantJSON: "buckets", wantText: "buckets:"},
		{name: "estimate", attribute: "estimate", wantJSON: "buckets", wantText: "buckets:"},
		{name: "assignee", attribute: "assignee", wantJSON: "buckets", wantText: "buckets:"},
		{name: "comments", attribute: "comments", wantJSON: "comments", wantText: "comments.tasksWithComments:"},
		{name: "dependencies", attribute: "dependencies", wantJSON: "dependencies", wantText: "dependencies.directDependencyTotal:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := mixedStatsProvider()
			var stdout bytes.Buffer
			if err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}, []string{test.attribute}); err != nil {
				t.Fatalf("runStats(%q JSON) error = %v", test.attribute, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
				t.Fatalf("decode focused JSON: %v", err)
			}
			if _, ok := fields[test.wantJSON]; !ok {
				t.Fatalf("focused JSON missing %q: %s", test.wantJSON, stdout.String())
			}
			for _, field := range []string{"statusCounts", "attributes", "handoffs"} {
				if _, ok := fields[field]; ok {
					t.Fatalf("focused JSON unexpectedly contains %q: %s", field, stdout.String())
				}
			}

			stdout.Reset()
			if err := runStats(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, []string{test.attribute}); err != nil {
				t.Fatalf("runStats(%q) error = %v", test.attribute, err)
			}
			if !strings.Contains(stdout.String(), test.wantText) {
				t.Fatalf("focused human output missing %q: %s", test.wantText, stdout.String())
			}
			if strings.Contains(stdout.String(), "statusCounts:") || strings.Contains(stdout.String(), "handoffs:") {
				t.Fatalf("focused human output contains overview metrics: %s", stdout.String())
			}
		})
	}
}

func mixedStatsProvider() *statsTestProvider {
	return &statsTestProvider{
		graphTestProvider: graphTestProvider{tasks: []core.TaskView{
			{Task: core.Task{ID: "task-todo", Status: core.StatusTodo, Model: "", Lane: "alpha", Priority: core.PriorityLow, Estimate: core.EstimateXS, Assignee: "", Comments: []core.Comment{}, Dependencies: []string{}}},
			{Task: core.Task{ID: "task-progress", Status: core.StatusInProgress, Model: "beta", Lane: "beta", Priority: core.PriorityHigh, Estimate: core.EstimateM, Assignee: "Bob", Comments: []core.Comment{}, Dependencies: []string{"dependency-3"}}},
			{Task: core.Task{ID: "task-paused", Status: core.StatusPaused, Model: "alpha", Lane: "", Priority: core.PriorityMedium, Estimate: "", Assignee: "Amy", Comments: []core.Comment{{}}, Dependencies: []string{}}},
			{Task: core.Task{ID: "task-done", Status: core.StatusDone, Model: "zeta", Lane: "zeta", Priority: core.PriorityUrgent, Estimate: core.EstimateXL, Assignee: "Zed", Comments: []core.Comment{{}, {}}, Dependencies: []string{"dependency-1", "dependency-2"}}},
		}},
		handoffs: []core.Handoff{
			{ID: "global-1", Message: "global one"},
			{ID: "global-2", Message: "global two"},
			{ID: "task-todo-handoff", TaskID: "task-todo", Message: "todo"},
			{ID: "task-progress-handoff", TaskID: "task-progress", Message: "progress"},
			{ID: "task-paused-handoff", TaskID: "task-paused", Message: "paused"},
			{ID: "task-done-handoff", TaskID: "task-done", Message: "done"},
			{ID: "foreign-handoff", TaskID: "foreign-task", Message: "foreign"},
		},
	}
}

func TestRunStatsFocusedJSONPreservesUnsetBucketAndFiltersStatus(t *testing.T) {
	p := &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{
		{Task: core.Task{ID: "done", Status: core.StatusDone, Model: "", Comments: []core.Comment{{}}, Dependencies: []string{"dependency"}}},
		{Task: core.Task{ID: "todo", Status: core.StatusTodo, Model: "gpt-5", Dependencies: []string{}}},
	}}}
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}
	if err := runStats(ctx, []string{"done", "model"}); err != nil {
		t.Fatalf("runStats() error = %v", err)
	}
	for _, needle := range []string{`"status": "done"`, `"totalTasks": 1`, `"attribute": "model"`, `"buckets": [`, `"value": ""`, `"count": 1`} {
		if !strings.Contains(stdout.String(), needle) {
			t.Fatalf("stats JSON missing %q in %q", needle, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "statusCounts") || strings.Contains(stdout.String(), "comments") {
		t.Fatalf("focused stats JSON included overview fields: %q", stdout.String())
	}
	if p.lastFilter.Status == nil || *p.lastFilter.Status != core.StatusDone {
		t.Fatalf("stats task filter = %#v, want done", p.lastFilter)
	}
}

func TestRunStatsTextDisplaysUnsetAndSpecialMetrics(t *testing.T) {
	p := &statsTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{{Task: core.Task{ID: "task", Status: core.StatusTodo, Model: "", Comments: []core.Comment{{}}, Dependencies: []string{"dependency"}}}}}}
	var stdout bytes.Buffer
	ctx := context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := runStats(ctx, []string{"model"}); err != nil {
		t.Fatalf("runStats(model) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "(unset): 1") {
		t.Fatalf("text stats did not display unset bucket: %q", stdout.String())
	}
	stdout.Reset()
	if err := runStats(ctx, []string{"dependencies"}); err != nil {
		t.Fatalf("runStats(dependencies) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "dependencies.directDependencyTotal: 1") || strings.Contains(stdout.String(), "buckets:") {
		t.Fatalf("focused dependency output = %q", stdout.String())
	}
}

func statusPointer(status core.Status) *core.Status {
	return &status
}

func TestHelpMentionsTaskMetadataOptions(t *testing.T) {
	var stdout bytes.Buffer
	if err := help(&stdout); err != nil {
		t.Fatalf("help() error = %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"wtp task show", "wtp task update", "wtp update", "checksum-verified", "wtp task edit", "wtp graph", "wtp schema", "--model", "--git-repo", "--git-branch", "--worktree-name", "--worktree-dir", "current Git worktree root", "wtpDir selects storage", "Usage Guide:", "wtp handoff write", "wtp handoff get", "wtp handoff purge", "handoff write appends by default", "newest global record", "handoff purge requires exactly one", ".wtp/handoffs.json", "claiming never consumes", "other scopes", "scopeCount", "otherScopesAvailable", "legacy --export-tasks is an alias", "Task IDs and scoped storage:", "wtp-BBBBBBBB-NNNN", "main hashes to 0d6e4079", ".wtp/meta/index-<branchId>.json", "Detached HEAD and non-Git", "foreign task can", "branch object", "UUID-named task file", "export remains canonical"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("help output missing %q", needle)
		}
	}
	for _, needle := range []string{"wtp task set-status <task-id> STATUS", "additionalStatuses", "waiting, blocked, or failed", "task start, task pause, and task done remain aliases", "statusCounts includes every configured status in catalog order"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("help output missing configurable-status contract %q", needle)
		}
	}
}

func TestSchemaDocumentsTaskAndHandoffContracts(t *testing.T) {
	var stdout bytes.Buffer
	if err := schema(&stdout); err != nil {
		t.Fatalf("schema() error = %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"dependencies are stored as canonical UUID strings", ".wtp/meta/index.json", "Task JSON schema:", `"model": "gpt-5"`, `"gitRepo": "/workspace/repo"`, "Configuration and discovery:", "linked worktrees use that worktree's configuration", "model: optional free-form string", "gitRepo: optional absolute path", "worktreeDir: optional absolute path", "--git-branch=", "--model", "Task UUIDs and short IDs must be unique", "todo has no lifecycle timestamps", "comments created without an agent remain valid", ".wtp/handoffs.json", `"handoffs": [`, "Handoff field semantics:", "handoff write appends by default", "--task and --all-scopes conflict", "A cutoff is exclusive", "Handoff reads and task claims are non-consuming", "task start and task next attach", "Handoff JSON response shapes:", `"scopeCount": 1`, `"otherScopesAvailable": false`, `"purged": 1`, "missing .wtp/handoffs.json", "Legacy task compatibility:", "--export-tasks=<directory>", "Short IDs, branch scopes, and allocation indexes:", "{\"branch\":\"<exact branch name>\",\"next\":<positive integer>}", "SHA-256", "first four digest bytes", "wtp-0d6e4079-0001.json", "task ready and task next select current-scope", "Foreign tasks are not automatically claimable", "task start <task-id>", "Filename compatibility migration:", "canonical task UUID>.json", "conflicting files are rejected before migration", "export directory contains exactly one canonical UUID-named", "scoped short-ID filenames and allocation indexes are not exported", "preserve unknown future fields", ".wtp/meta/wtp.lock", "tolerate gaps", "Canonical export is unchanged"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("schema output missing %q", needle)
		}
	}
	for _, needle := range []string{"additionalStatuses", "waiting requires startedAt", "Only done resolves dependencies", "blocked also has no lifecycle timestamps"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("schema output missing configurable-status contract %q", needle)
		}
	}
}

func TestPrintValueIncludesTaskMetadataInHumanAndJSONOutput(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	task := core.TaskView{Task: core.Task{
		ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
		ShortID:      "wtp-0001",
		Title:        "Model-aware task",
		Model:        "gpt-5.2-codex",
		GitRepo:      "/workspace/repo",
		GitBranch:    "feature/task-metadata",
		WorktreeName: "task-metadata",
		WorktreeDir:  "/workspace/task-metadata",
		Status:       core.StatusTodo,
		Dependencies: []string{},
		Comments:     []core.Comment{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}, Handoffs: []core.Handoff{testCLIHandoff("00000000-0000-4000-8000-000000000006", "25c3806a-bd1b-424d-889b-29e5b06679b8", "Retained claim context")}}

	var human bytes.Buffer
	if err := printValue(context{stdout: &human}, task); err != nil {
		t.Fatalf("printValue(human) error = %v", err)
	}
	for _, needle := range []string{
		"model: gpt-5.2-codex",
		"gitRepo: /workspace/repo",
		"gitBranch: feature/task-metadata",
		"worktreeName: task-metadata",
		"worktreeDir: /workspace/task-metadata",
		"handoffs:",
		"Retained claim context",
	} {
		if !strings.Contains(human.String(), needle) {
			t.Fatalf("human output missing %q: %q", needle, human.String())
		}
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
	for _, needle := range []string{
		`"model": "gpt-5.2-codex"`,
		`"gitRepo": "/workspace/repo"`,
		`"gitBranch": "feature/task-metadata"`,
		`"worktreeName": "task-metadata"`,
		`"worktreeDir": "/workspace/task-metadata"`,
		`"handoffs": [`,
		`"message": "Retained claim context"`,
	} {
		if !strings.Contains(jsonOutput.String(), needle) {
			t.Fatalf("JSON output missing %q: %q", needle, jsonOutput.String())
		}
	}
}

func TestTaskClaimCommandsRenderHandoffsInHumanOutput(t *testing.T) {
	task := testCLIClaimTask([]core.Handoff{
		testCLIHandoff("00000000-0000-4000-8000-000000000007", "25c3806a-bd1b-424d-889b-29e5b06679b8", "retained claim context"),
	})
	want := strings.Join([]string{
		"wtp-0028 (25c3806a-bd1b-424d-889b-29e5b06679b8)",
		"title: Claim output",
		"status: inProgress",
		"priority: high",
		"assignee: Tony",
		"claimable: no",
		"blocked: no",
		"dependencyCount: 0",
		"reverseDependencyCount: 0",
		"created: 2026-08-09T17:00:00Z",
		"updated: 2026-08-09T18:00:00Z",
		"handoffs:",
		"- 00000000-0000-4000-8000-000000000007",
		"  scope: task 25c3806a-bd1b-424d-889b-29e5b06679b8",
		"  author: Tony",
		"  created: 2026-08-09T18:00:00Z",
		"  message: retained claim context",
	}, "\n") + "\n"

	tests := []struct {
		name string
		run  func(context, *claimOutputTestProvider) error
	}{
		{
			name: "task start",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskTransition(ctx, "start", core.StatusInProgress, []string{"wtp-0028", "--agent", "Tony"})
			},
		},
		{
			name: "task next",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskNext(ctx, []string{"--agent", "Tony"})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &claimOutputTestProvider{task: task}
			var stdout, stderr bytes.Buffer
			if err := test.run(context{provider: p, stdout: &stdout, stderr: &stderr}, p); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if got := stdout.String(); got != want {
				t.Fatalf("%s output = %q, want %q", test.name, got, want)
			}
			if test.name == "task start" && p.gotActor != "Tony" {
				t.Fatalf("task start agent forwarding = %q, want Tony", p.gotActor)
			}
			if test.name == "task next" && p.nextAgent != "Tony" {
				t.Fatalf("task next agent forwarding = %q, want Tony", p.nextAgent)
			}
		})
	}
}

func TestTaskClaimCommandsKeepNoHandoffHumanOutputByteCompatible(t *testing.T) {
	task := testCLIClaimTask(nil)
	want := strings.Join([]string{
		"wtp-0028 (25c3806a-bd1b-424d-889b-29e5b06679b8)",
		"title: Claim output",
		"status: inProgress",
		"priority: high",
		"assignee: Tony",
		"claimable: no",
		"blocked: no",
		"dependencyCount: 0",
		"reverseDependencyCount: 0",
		"created: 2026-08-09T17:00:00Z",
		"updated: 2026-08-09T18:00:00Z",
	}, "\n") + "\n"

	for _, test := range []struct {
		name string
		run  func(context, *claimOutputTestProvider) error
	}{
		{
			name: "task start",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskTransition(ctx, "start", core.StatusInProgress, []string{"wtp-0028", "--agent", "Tony"})
			},
		},
		{
			name: "task next",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskNext(ctx, []string{"--agent", "Tony"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &claimOutputTestProvider{task: task}
			var stdout, stderr bytes.Buffer
			if err := test.run(context{provider: p, stdout: &stdout, stderr: &stderr}, p); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if got := stdout.String(); got != want {
				t.Fatalf("%s output = %q, want %q", test.name, got, want)
			}
			if strings.Contains(stdout.String(), "handoffs:") {
				t.Fatalf("%s unexpectedly rendered a handoffs section: %q", test.name, stdout.String())
			}
		})
	}
}

func TestTaskClaimCommandsExposeHandoffsAdditivelyInJSON(t *testing.T) {
	task := testCLIClaimTask([]core.Handoff{
		testCLIHandoff("00000000-0000-4000-8000-000000000008", "25c3806a-bd1b-424d-889b-29e5b06679b8", "JSON claim context"),
	})
	for _, test := range []struct {
		name string
		run  func(context, *claimOutputTestProvider) error
	}{
		{
			name: "task start",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskTransition(ctx, "start", core.StatusInProgress, []string{"wtp-0028", "--agent", "Tony"})
			},
		},
		{
			name: "task next",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskNext(ctx, []string{"--agent", "Tony"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &claimOutputTestProvider{task: task}
			var stdout, stderr bytes.Buffer
			if err := test.run(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, p); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
				t.Fatalf("%s JSON error = %v", test.name, err)
			}
			if _, wrapped := fields["task"]; wrapped {
				t.Fatalf("%s JSON unexpectedly wrapped task fields: %s", test.name, stdout.String())
			}
			for _, field := range []string{"id", "shortId", "title", "status", "readiness", "handoffs"} {
				if _, present := fields[field]; !present {
					t.Fatalf("%s JSON missing additive task field %q: %s", test.name, field, stdout.String())
				}
			}
			var got core.TaskView
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("%s task JSON decode error = %v", test.name, err)
			}
			if !reflect.DeepEqual(got, task) {
				t.Fatalf("%s decoded task = %#v, want %#v", test.name, got, task)
			}
		})
	}
}

func TestTaskClaimCommandsOmitEmptyHandoffsFromJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(context, *claimOutputTestProvider) error
	}{
		{
			name: "task start",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskTransition(ctx, "start", core.StatusInProgress, []string{"wtp-0028"})
			},
		},
		{
			name: "task next",
			run: func(ctx context, p *claimOutputTestProvider) error {
				return runTaskNext(ctx, nil)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &claimOutputTestProvider{task: testCLIClaimTask(nil)}
			var stdout, stderr bytes.Buffer
			if err := test.run(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, p); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
				t.Fatalf("%s JSON error = %v", test.name, err)
			}
			if _, present := fields["handoffs"]; present {
				t.Fatalf("%s JSON gained handoffs without records: %s", test.name, stdout.String())
			}
		})
	}
}

func TestRegularTaskShowAndListDoNotAttachRetainedHandoffs(t *testing.T) {
	p, err := flatfile.New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("flatfile.New() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Ordinary read"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := p.WriteHandoff(provider.HandoffWriteRequest{Task: task.ShortID, Author: "Tony", Message: "claim-only context"}); err != nil {
		t.Fatalf("WriteHandoff() error = %v", err)
	}

	for _, test := range []struct {
		name string
		run  func(context) error
	}{
		{
			name: "show human",
			run: func(ctx context) error {
				return runTaskGet(ctx, []string{task.ShortID}, "show")
			},
		},
		{
			name: "list human",
			run: func(ctx context) error {
				return runTaskList(ctx, nil)
			},
		},
		{
			name: "show JSON",
			run: func(ctx context) error {
				return runTaskGet(ctx, []string{task.ShortID}, "show")
			},
		},
		{
			name: "list JSON",
			run: func(ctx context) error {
				return runTaskList(ctx, nil)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			jsonOut := strings.HasSuffix(test.name, "JSON")
			if err := test.run(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: jsonOut}); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if strings.Contains(stdout.String(), "claim-only context") || strings.Contains(stdout.String(), "handoffs:") {
				t.Fatalf("%s unexpectedly attached retained handoff: %q", test.name, stdout.String())
			}
			if jsonOut {
				var value json.RawMessage
				if err := json.Unmarshal(stdout.Bytes(), &value); err != nil {
					t.Fatalf("%s JSON error = %v", test.name, err)
				}
				if bytes.Contains(value, []byte(`"handoffs"`)) {
					t.Fatalf("%s JSON unexpectedly contains handoffs: %s", test.name, stdout.String())
				}
			}
		})
	}
}

func testCLIClaimTask(handoffs []core.Handoff) core.TaskView {
	return core.TaskView{
		Task: core.Task{
			ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
			ShortID:      "wtp-0028",
			Title:        "Claim output",
			Priority:     core.PriorityHigh,
			Status:       core.StatusInProgress,
			Assignee:     "Tony",
			Dependencies: []string{},
			Comments:     []core.Comment{},
			CreatedAt:    time.Date(2026, time.August, 9, 17, 0, 0, 0, time.UTC),
			UpdatedAt:    time.Date(2026, time.August, 9, 18, 0, 0, 0, time.UTC),
		},
		Handoffs: handoffs,
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
	catalog        core.StatusCatalog
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

type claimOutputTestProvider struct {
	updateTestProvider
	task      core.TaskView
	nextAgent string
}

func (p *claimOutputTestProvider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
	p.gotID = idOrShortID
	p.gotStatus = target
	p.gotActor = actor
	p.statusCalls++
	return p.task, nil
}

func (p *claimOutputTestProvider) GetNextTask(agent string) (core.TaskView, error) {
	p.nextAgent = agent
	return p.task, nil
}

type handoffTestProvider struct {
	updateTestProvider
	writeRequest provider.HandoffWriteRequest
	writeResult  provider.HandoffWriteResult
	listFilter   provider.HandoffFilter
	listResult   provider.HandoffListResult
	listCalls    int
	purgeRequest provider.HandoffPurgeRequest
	purgeResult  provider.HandoffPurgeResult
	purgeErr     error
	purgeCalls   int
}

func (p *handoffTestProvider) WriteHandoff(request provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	p.writeRequest = request
	return p.writeResult, nil
}

func (p *handoffTestProvider) ListHandoffs(filter provider.HandoffFilter) (provider.HandoffListResult, error) {
	p.listCalls++
	p.listFilter = filter
	return p.listResult, nil
}

func (p *handoffTestProvider) PurgeHandoffs(request provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	p.purgeCalls++
	p.purgeRequest = request
	return p.purgeResult, p.purgeErr
}

type handoffWriteTestProvider struct {
	updateTestProvider
	writeRequest provider.HandoffWriteRequest
	writeResult  provider.HandoffWriteResult
	writeErr     error
	writeCalls   int
}

func (p *handoffWriteTestProvider) WriteHandoff(request provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	p.writeCalls++
	p.writeRequest = request
	return p.writeResult, p.writeErr
}

type graphTestProvider struct {
	catalog    core.StatusCatalog
	tasks      []core.TaskView
	lastFilter provider.TaskFilter
	listCalls  int
}

type statsTestProvider struct {
	graphTestProvider
	handoffs          []core.Handoff
	lastHandoffFilter provider.HandoffFilter
}

func (p *updateTestProvider) StatusCatalog() core.StatusCatalog {
	if len(p.catalog.Statuses()) == 0 {
		return core.DefaultStatusCatalog()
	}
	return p.catalog
}

func (p graphTestProvider) StatusCatalog() core.StatusCatalog {
	if len(p.catalog.Statuses()) == 0 {
		return core.DefaultStatusCatalog()
	}
	return p.catalog
}

func (readyTestProvider) StatusCatalog() core.StatusCatalog { return core.DefaultStatusCatalog() }
func (getTestProvider) StatusCatalog() core.StatusCatalog   { return core.DefaultStatusCatalog() }

func (p *statsTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	p.lastFilter = filter
	p.listCalls++
	if filter.Status == nil {
		return p.tasks, nil
	}
	filtered := make([]core.TaskView, 0, len(p.tasks))
	for _, task := range p.tasks {
		if task.Status == *filter.Status {
			filtered = append(filtered, task)
		}
	}
	return filtered, nil
}

func (p *statsTestProvider) ListHandoffs(filter provider.HandoffFilter) (provider.HandoffListResult, error) {
	p.lastHandoffFilter = filter
	return provider.HandoffListResult{Handoffs: p.handoffs}, nil
}

func (p *updateTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	return nil, errors.New("unexpected call")
}

func (p *updateTestProvider) WriteHandoff(provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	return provider.HandoffWriteResult{}, errors.New("unexpected call")
}

func (p *updateTestProvider) ListHandoffs(provider.HandoffFilter) (provider.HandoffListResult, error) {
	return provider.HandoffListResult{}, errors.New("unexpected call")
}

func (p *updateTestProvider) PurgeHandoffs(provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	return provider.HandoffPurgeResult{}, errors.New("unexpected call")
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

func (p graphTestProvider) WriteHandoff(provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	return provider.HandoffWriteResult{}, errors.New("unexpected call")
}

func (p graphTestProvider) ListHandoffs(provider.HandoffFilter) (provider.HandoffListResult, error) {
	return provider.HandoffListResult{}, errors.New("unexpected call")
}

func (p graphTestProvider) PurgeHandoffs(provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	return provider.HandoffPurgeResult{}, errors.New("unexpected call")
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

func (p readyTestProvider) WriteHandoff(provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	return provider.HandoffWriteResult{}, errors.New("unexpected call")
}

func (p readyTestProvider) ListHandoffs(provider.HandoffFilter) (provider.HandoffListResult, error) {
	return provider.HandoffListResult{}, errors.New("unexpected call")
}

func (p readyTestProvider) PurgeHandoffs(provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	return provider.HandoffPurgeResult{}, errors.New("unexpected call")
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

func (p *getTestProvider) WriteHandoff(provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	return provider.HandoffWriteResult{}, errors.New("unexpected call")
}

func (p *getTestProvider) ListHandoffs(provider.HandoffFilter) (provider.HandoffListResult, error) {
	return provider.HandoffListResult{}, errors.New("unexpected call")
}

func (p *getTestProvider) PurgeHandoffs(provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	return provider.HandoffPurgeResult{}, errors.New("unexpected call")
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
