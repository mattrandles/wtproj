package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

// TestCLIProcess runs Run in a separate process so each invocation has its own
// working directory. This makes the tests exercise the same "." resolution as
// the installed binary without changing the test process's global directory.
func TestCLIProcess(t *testing.T) {
	if os.Getenv("WTP_CLI_PROCESS") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			if err := Run(os.Args[index+1:], os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	fmt.Fprintln(os.Stderr, "missing CLI argument separator")
	os.Exit(2)
}

func TestRunUsesDefaultAndSharedExternalStorageAcrossProjects(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	mainRoot := createIntegrationRepository(t, fixture, "main repository")
	mainNested := filepath.Join(mainRoot, "nested", "invocation")
	if err := os.MkdirAll(mainNested, 0o755); err != nil {
		t.Fatalf("MkdirAll(main nested) error = %v", err)
	}

	mainTask := runCLIJSONTask(t, mainNested, "--json", "task", "create", "--title", "default store task")
	assertEquivalentPath(t, mainTask.GitRepo, mainRoot)
	assertEquivalentPath(t, mainTask.WorktreeDir, mainRoot)
	assertStorageInitialized(t, filepath.Join(mainRoot, ".wtp"))

	linkedRoot := filepath.Join(fixture, "linked worktree")
	runConfigGit(t, mainRoot, "worktree", "add", "-b", "shared-linked", linkedRoot)
	linkedNested := filepath.Join(linkedRoot, "nested", "invocation")
	if err := os.MkdirAll(linkedNested, 0o755); err != nil {
		t.Fatalf("MkdirAll(linked nested) error = %v", err)
	}
	sharedStore := filepath.Join(fixture, "shared task store")
	writeConfigFile(t, linkedRoot, `{"wtpDir":"../shared task store"}`)

	secondRoot := createIntegrationRepository(t, fixture, "second repository")
	writeConfigFile(t, secondRoot, `{"wtpDir":`+jsonString(sharedStore)+`}`)

	linkedTask := runCLIJSONTask(t, linkedNested, "--json", "task", "create", "--title", "linked shared task")
	secondTask := runCLIJSONTask(t, secondRoot, "--json", "task", "create", "--title", "second shared task")
	assertStorageInitialized(t, sharedStore)

	assertEquivalentPath(t, linkedTask.GitRepo, mainRoot)
	assertEquivalentPath(t, linkedTask.WorktreeDir, linkedRoot)
	if linkedTask.GitBranch != "shared-linked" {
		t.Fatalf("linked task metadata = %#v", linkedTask.Task)
	}
	assertEquivalentPath(t, secondTask.GitRepo, secondRoot)
	assertEquivalentPath(t, secondTask.WorktreeDir, secondRoot)
	if secondTask.GitRepo == "" || secondTask.WorktreeDir == "" {
		t.Fatalf("second project metadata = %#v", secondTask.Task)
	}

	sharedTasks := runCLIJSONTasks(t, secondRoot, "--json", "task", "list")
	if len(sharedTasks) != 2 {
		t.Fatalf("shared store task count = %d, want 2", len(sharedTasks))
	}
	metadataByTitle := map[string]core.Task{}
	for _, task := range sharedTasks {
		metadataByTitle[task.Title] = task.Task
	}
	if got := metadataByTitle["linked shared task"]; got.GitRepo == "" || got.WorktreeDir == "" {
		t.Fatalf("shared linked task metadata = %#v", got)
	}
	assertEquivalentPath(t, metadataByTitle["linked shared task"].GitRepo, mainRoot)
	assertEquivalentPath(t, metadataByTitle["linked shared task"].WorktreeDir, linkedRoot)
	if got := metadataByTitle["second shared task"]; got.GitRepo == "" || got.WorktreeDir == "" {
		t.Fatalf("shared second task metadata = %#v", got)
	}
	assertEquivalentPath(t, metadataByTitle["second shared task"].GitRepo, secondRoot)
	assertEquivalentPath(t, metadataByTitle["second shared task"].WorktreeDir, secondRoot)
}

func TestRunPreservesAndClearsContextMetadataAndHandlesDetachedAndNonGit(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	detachedRoot := createIntegrationRepository(t, fixture, "detached repository")
	runConfigGit(t, detachedRoot, "checkout", "--detach")

	detached := runCLIJSONTask(t, detachedRoot, "--json", "task", "create", "--title", "detached task")
	assertEquivalentPath(t, detached.GitRepo, detachedRoot)
	assertEquivalentPath(t, detached.WorktreeDir, detachedRoot)
	if detached.GitBranch != "" {
		t.Fatalf("detached task metadata = %#v", detached.Task)
	}
	updated := runCLIJSONTask(t, detachedRoot, "--json", "task", "update", detached.ShortID, "--description", "preserve context")
	assertEquivalentPath(t, updated.GitRepo, detachedRoot)
	assertEquivalentPath(t, updated.WorktreeDir, detachedRoot)
	if updated.Description != "preserve context" {
		t.Fatalf("updated detached task = %#v", updated.Task)
	}
	cleared := runCLIJSONTask(t, detachedRoot, "--json", "task", "edit", detached.ShortID,
		"--git-repo=", "--git-branch=", "--worktree-name=", "--worktree-dir=")
	if cleared.GitRepo != "" || cleared.GitBranch != "" || cleared.WorktreeName != "" || cleared.WorktreeDir != "" {
		t.Fatalf("cleared task metadata = %#v", cleared.Task)
	}

	legacy := runCLIJSONTask(t, detachedRoot, "--json", "--create-task", "--title", "legacy detached task")
	assertEquivalentPath(t, legacy.GitRepo, detachedRoot)
	assertEquivalentPath(t, legacy.WorktreeDir, detachedRoot)
	if legacy.GitBranch != "" {
		t.Fatalf("legacy task metadata = %#v", legacy.Task)
	}

	nonGitRoot := filepath.Join(fixture, "non git directory")
	if err := os.MkdirAll(nonGitRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(non-Git root) error = %v", err)
	}
	nonGit := runCLIJSONTask(t, nonGitRoot, "--json", "task", "create", "--title", "non-Git default task")
	if nonGit.GitRepo != "" || nonGit.GitBranch != "" || nonGit.WorktreeName != "" || nonGit.WorktreeDir != "" {
		t.Fatalf("non-Git task metadata = %#v", nonGit.Task)
	}
	assertStorageInitialized(t, filepath.Join(nonGitRoot, ".wtp"))
}

func TestRunReportsInvalidInvocationConfig(t *testing.T) {
	invocation := t.TempDir()
	configPath := filepath.Join(invocation, ".wtp.json")
	if err := os.WriteFile(configPath, []byte(`{"wtpDir":`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	output, err := runCLIProcess(invocation, "task", "list")
	if err == nil {
		t.Fatal("Run() error = nil, want invalid configuration failure")
	}
	if !strings.Contains(output, "parse "+configPath) {
		t.Fatalf("Run() output = %q, want parse error naming %s", output, configPath)
	}
}

func TestRunReportsWTPDirInitializationFailureWithoutChangingTarget(t *testing.T) {
	invocation := filepath.Join(t.TempDir(), "config path with spaces")
	if err := os.MkdirAll(invocation, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	storagePath := filepath.Join(invocation, "storage file")
	if err := os.WriteFile(storagePath, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile(storage) error = %v", err)
	}
	writeConfigFile(t, invocation, `{"wtpDir":"storage file"}`)

	output, err := runCLIProcess(invocation, "task", "list")
	if err == nil {
		t.Fatal("Run() error = nil, want storage initialization failure")
	}
	if !strings.Contains(output, "initialize flat-file storage at "+storagePath) {
		t.Fatalf("Run() output = %q, want storage path", output)
	}
	if got, readErr := os.ReadFile(storagePath); readErr != nil || string(got) != "keep me" {
		t.Fatalf("storage target changed: contents=%q error=%v", got, readErr)
	}
}

func TestRunLegacyCommandsRoundTripAgainstPublicCLI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "legacy repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	created := runCLIJSONTask(t, root, "--json", "--create-task", "--title", "legacy task", "--agent", "legacy-agent")
	if created.Status != core.StatusTodo || created.Assignee != "legacy-agent" {
		t.Fatalf("legacy create = %#v", created.Task)
	}

	shown := runCLIJSONTask(t, root, "--json", "--get-task", "--task-id", created.ShortID, "--agent", "legacy-agent")
	if shown.ID != created.ID {
		t.Fatalf("legacy get ID = %q, want %q", shown.ID, created.ID)
	}
	listed := runCLIJSONTasks(t, root, "--json", "--get-tasks", "--status", "todo", "--agent", "legacy-agent")
	if len(listed) != 1 || listed[0].ShortID != created.ShortID {
		t.Fatalf("legacy list = %#v, want task %s", listed, created.ShortID)
	}

	commented := runCLIJSONTask(t, root, "--json", "--add-comment", "--task-id", created.ShortID, "--agent", "legacy-agent", "--comment", "legacy progress")
	if len(commented.Comments) != 1 || commented.Comments[0].Author != "legacy-agent" || commented.Comments[0].Message != "legacy progress" {
		t.Fatalf("legacy comment = %#v", commented.Comments)
	}

	started := runCLIJSONTask(t, root, "--json", "--set-task-in-progress", "--task-id", created.ShortID, "--agent", "legacy-agent")
	if started.Status != core.StatusInProgress || started.Assignee != "legacy-agent" {
		t.Fatalf("legacy start = %#v", started.Task)
	}
	paused := runCLIJSONTask(t, root, "--json", "--set-task-paused", "--task-id", created.ShortID, "--agent", "legacy-agent")
	if paused.Status != core.StatusPaused || paused.Assignee != "legacy-agent" {
		t.Fatalf("legacy pause = %#v", paused.Task)
	}
	claimed := runCLIJSONTask(t, root, "--json", "--get-next-task", "--agent", "legacy-agent")
	if claimed.Status != core.StatusInProgress || claimed.Assignee != "legacy-agent" {
		t.Fatalf("legacy next = %#v", claimed.Task)
	}
	done := runCLIJSONTask(t, root, "--json", "--set-task-done", "--task-id", created.ShortID, "--agent", "legacy-agent")
	if done.Status != core.StatusDone || done.Assignee != "legacy-agent" {
		t.Fatalf("legacy done = %#v", done.Task)
	}

	exportDir := filepath.Join(root, "legacy export")
	if output, err := runCLIProcess(root, "--export-tasks="+exportDir); err != nil {
		t.Fatalf("legacy export: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(exportDir, created.ID+".json")); err != nil {
		t.Fatalf("legacy export missing canonical task: %v", err)
	}
}

func TestRunPublicExportWithSpacesPreservesStorage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "export repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	created := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "exported task")
	before := storageManifest(t, filepath.Join(root, ".wtp"))
	exportDir := filepath.Join(root, "snapshot with spaces")
	output, err := runCLIProcess(root, "--json", "export", "--out", exportDir)
	if err != nil {
		t.Fatalf("public export: %v\n%s", err, output)
	}
	var response map[string]string
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("decode export response %q: %v", output, err)
	}
	if response["out"] != exportDir {
		t.Fatalf("export response out = %q, want %q", response["out"], exportDir)
	}
	if _, err := os.Stat(filepath.Join(exportDir, created.ID+".json")); err != nil {
		t.Fatalf("exported task missing: %v", err)
	}
	if got := storageManifest(t, filepath.Join(root, ".wtp")); got != before {
		t.Fatalf("export changed active storage:\nbefore %s\nafter %s", before, got)
	}
}

func TestRunChangingConfigDoesNotMoveExistingDefaultStorage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config migration repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runCLIJSONTask(t, root, "--json", "task", "create", "--title", "original storage task")
	defaultStorage := filepath.Join(root, ".wtp")
	before := storageManifest(t, defaultStorage)

	configuredStorage := filepath.Join(root, "configured task storage")
	writeConfigFile(t, root, `{"wtpDir":"configured task storage"}`)
	if tasks := runCLIJSONTasks(t, root, "--json", "task", "list"); len(tasks) != 0 {
		t.Fatalf("configured store unexpectedly loaded old tasks: %#v", tasks)
	}
	assertStorageInitialized(t, configuredStorage)
	if got := storageManifest(t, defaultStorage); got != before {
		t.Fatalf("changing config moved or changed default storage:\nbefore %s\nafter %s", before, got)
	}
}

func createIntegrationRepository(t *testing.T, parent, name string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	runConfigGit(t, parent, "init", root)
	runConfigGit(t, root, "config", "user.name", "WTP Test")
	runConfigGit(t, root, "config", "user.email", "wtp-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runConfigGit(t, root, "add", "tracked.txt")
	runConfigGit(t, root, "commit", "-m", "fixture")
	return root
}

func runCLIJSONTask(t *testing.T, dir string, args ...string) core.TaskView {
	t.Helper()
	output, err := runCLIProcess(dir, args...)
	if err != nil {
		t.Fatalf("wtp %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var task core.TaskView
	if err := json.Unmarshal([]byte(output), &task); err != nil {
		t.Fatalf("decode task output %q: %v", output, err)
	}
	return task
}

func runCLIJSONTasks(t *testing.T, dir string, args ...string) []core.TaskView {
	t.Helper()
	output, err := runCLIProcess(dir, args...)
	if err != nil {
		t.Fatalf("wtp %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var tasks []core.TaskView
	if err := json.Unmarshal([]byte(output), &tasks); err != nil {
		t.Fatalf("decode task list output %q: %v", output, err)
	}
	return tasks
}

func runCLIProcess(dir string, args ...string) (string, error) {
	commandArgs := append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)
	command := exec.Command(os.Args[0], commandArgs...)
	command.Dir = dir
	command.Env = append(os.Environ(), "WTP_CLI_PROCESS=1")
	output, err := command.CombinedOutput()
	return string(output), err
}

func storageManifest(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			paths = append(paths, relative+"\x00"+string(data))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk storage: %v", err)
	}
	slices.Sort(paths)
	return strings.Join(paths, "\x00")
}
