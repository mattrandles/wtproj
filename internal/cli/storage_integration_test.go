package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
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

func TestRunSharedStoreHandoffRoundTripAcrossCLIProcesses(t *testing.T) {
	fixture := t.TempDir()
	firstRoot := filepath.Join(fixture, "first project")
	secondRoot := filepath.Join(fixture, "second project")
	sharedStore := filepath.Join(fixture, "shared handoff store")
	for _, root := range []string{firstRoot, secondRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", root, err)
		}
		writeConfigFile(t, root, `{"wtpDir":`+jsonString(sharedStore)+`}`)
	}

	task := runCLIJSONTask(t, firstRoot, "--json", "task", "create", "--title", "shared task")
	global := runCLIJSONHandoff(t, firstRoot, "--json", "handoff", "write", "--agent", "Alice", "--message", "shared global context")
	taskScoped := runCLIJSONHandoff(t, firstRoot, "--json", "handoff", "write", "--task", task.ShortID, "--agent", "Bob", "--message", "shared task context")
	if global.Handoff.TaskID != "" {
		t.Fatalf("global handoff task ID = %q, want empty", global.Handoff.TaskID)
	}
	if taskScoped.Handoff.TaskID != task.ID {
		t.Fatalf("task handoff task ID = %q, want %q", taskScoped.Handoff.TaskID, task.ID)
	}
	assertStorageInitialized(t, sharedStore)

	fromSecondProject := runCLIJSONHandoffs(t, secondRoot, "--json", "handoff", "get", "--all-scopes", "--all")
	if fromSecondProject.TotalMatching != 2 || len(fromSecondProject.Handoffs) != 2 {
		t.Fatalf("shared handoffs from second project = %#v, want two records", fromSecondProject)
	}
	assertHandoffMessages(t, fromSecondProject.Handoffs, []string{"shared global context", "shared task context"})

	taskOnly := runCLIJSONHandoffs(t, secondRoot, "--json", "handoff", "get", "--task", task.ShortID, "--all")
	if taskOnly.TotalMatching != 1 || len(taskOnly.Handoffs) != 1 || taskOnly.Handoffs[0].ID != taskScoped.Handoff.ID {
		t.Fatalf("shared task handoffs from second project = %#v, want task handoff %s", taskOnly, taskScoped.Handoff.ID)
	}

	claimed := runCLIJSONTask(t, secondRoot, "--json", "task", "start", task.ShortID, "--agent", "Carol")
	if claimed.Status != core.StatusInProgress || claimed.Assignee != "Carol" {
		t.Fatalf("shared task claim = %#v, want inProgress assigned to Carol", claimed.Task)
	}
	if len(claimed.Handoffs) != 1 || claimed.Handoffs[0].ID != taskScoped.Handoff.ID {
		t.Fatalf("claimed shared task handoffs = %#v, want task handoff %s", claimed.Handoffs, taskScoped.Handoff.ID)
	}

	purged := runCLIJSONHandoffPurge(t, secondRoot, "--json", "handoff", "purge", "--global")
	if purged.Purged != 1 {
		t.Fatalf("shared global purge count = %d, want 1", purged.Purged)
	}
	remaining := runCLIJSONHandoffs(t, firstRoot, "--json", "handoff", "get", "--all-scopes", "--all")
	if remaining.TotalMatching != 1 || len(remaining.Handoffs) != 1 || remaining.Handoffs[0].ID != taskScoped.Handoff.ID {
		t.Fatalf("shared handoffs after purge from first project = %#v, want task handoff %s", remaining, taskScoped.Handoff.ID)
	}

	exportDir := filepath.Join(secondRoot, "shared handoff export")
	if output, err := runCLIProcess(secondRoot, "--json", "export", "--out", exportDir); err != nil {
		t.Fatalf("shared handoff export: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(exportDir, task.ID+".json")); err != nil {
		t.Fatalf("shared handoff export missing task: %v", err)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{taskScoped.Handoff})
}

func TestRunLegacyStoreWithoutHandoffsReadsCleanlyThroughCLIProcess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "legacy repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	initial := runCLIJSONHandoffs(t, root, "--json", "handoff", "get", "--all-scopes", "--all")
	if initial.TotalMatching != 0 || len(initial.Handoffs) != 0 {
		t.Fatalf("empty legacy handoff read = %#v, want no records", initial)
	}
	handoffsPath := filepath.Join(root, ".wtp", "handoffs.json")
	if _, err := os.Stat(handoffsPath); !os.IsNotExist(err) {
		t.Fatalf("legacy handoff read created %s: %v", handoffsPath, err)
	}

	task := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "legacy task")
	listed := runCLIJSONTasks(t, root, "--json", "task", "list")
	if len(listed) != 1 || listed[0].ID != task.ID {
		t.Fatalf("legacy task list = %#v, want task %s", listed, task.ID)
	}
	claimed := runCLIJSONTask(t, root, "--json", "task", "start", task.ShortID, "--agent", "Legacy")
	if claimed.Status != core.StatusInProgress || len(claimed.Handoffs) != 0 {
		t.Fatalf("legacy task claim = %#v, want no attached handoffs", claimed)
	}

	exportDir := filepath.Join(root, "legacy export")
	if output, err := runCLIProcess(root, "--json", "export", "--out", exportDir); err != nil {
		t.Fatalf("legacy export: %v\n%s", err, output)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})
}

func TestRunLegacyAllocationContextsAndNamedBranchPreservesLegacyIDs(t *testing.T) {
	requireGitForConfigTest(t)
	fixture := t.TempDir()

	t.Run("detached HEAD continues global allocation", func(t *testing.T) {
		root := createIntegrationRepository(t, fixture, "detached allocation repository")
		runConfigGit(t, root, "checkout", "--detach")

		first := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "detached first")
		second := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "detached second")
		if first.ShortID != "wtp-0001" || second.ShortID != "wtp-0002" {
			t.Fatalf("detached short IDs = %q, %q, want wtp-0001, wtp-0002", first.ShortID, second.ShortID)
		}
		assertOnlyLegacyAllocationIndex(t, filepath.Join(root, ".wtp"))
	})

	t.Run("non-Git explicit branch metadata stays global", func(t *testing.T) {
		root := filepath.Join(fixture, "non-Git explicit branch")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}

		explicit := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "explicit metadata", "--git-branch", "recorded-outside-git")
		continued := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "global continuation")
		if explicit.ShortID != "wtp-0001" || continued.ShortID != "wtp-0002" {
			t.Fatalf("non-Git short IDs = %q, %q, want wtp-0001, wtp-0002", explicit.ShortID, continued.ShortID)
		}
		if explicit.GitBranch != "recorded-outside-git" {
			t.Fatalf("explicit non-Git branch metadata = %q, want recorded-outside-git", explicit.GitBranch)
		}
		assertOnlyLegacyAllocationIndex(t, filepath.Join(root, ".wtp"))
		index, err := os.ReadFile(filepath.Join(root, ".wtp", "meta", "index.json"))
		if err != nil {
			t.Fatalf("ReadFile(non-Git index) error = %v", err)
		}
		if !strings.Contains(string(index), `"next": 3`) {
			t.Fatalf("non-Git index = %q, want next 3", index)
		}
	})

	t.Run("existing legacy index continues from its stored next value", func(t *testing.T) {
		root := filepath.Join(fixture, "existing legacy index")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		first := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "existing legacy task")
		if first.ShortID != "wtp-0001" {
			t.Fatalf("first legacy short ID = %q, want wtp-0001", first.ShortID)
		}
		indexPath := filepath.Join(root, ".wtp", "meta", "index.json")
		if err := os.WriteFile(indexPath, []byte("{\n  \"next\": 5\n}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(existing index) error = %v", err)
		}

		continued := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "continued legacy task")
		if continued.ShortID != "wtp-0005" {
			t.Fatalf("continued legacy short ID = %q, want wtp-0005", continued.ShortID)
		}
		index, err := os.ReadFile(indexPath)
		if err != nil {
			t.Fatalf("ReadFile(continued index) error = %v", err)
		}
		if !strings.Contains(string(index), `"next": 6`) {
			t.Fatalf("continued index = %q, want next 6", index)
		}
	})

	t.Run("named branch opens legacy store without changing old IDs", func(t *testing.T) {
		root := createIntegrationRepository(t, fixture, "named branch legacy repository")
		runConfigGit(t, root, "checkout", "--detach")
		legacy := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "legacy task")
		legacyIndexPath := filepath.Join(root, ".wtp", "meta", "index.json")
		legacyIndexBefore, err := os.ReadFile(legacyIndexPath)
		if err != nil {
			t.Fatalf("ReadFile(legacy index before branch) error = %v", err)
		}
		legacyTaskPath := filepath.Join(root, ".wtp", string(core.StatusTodo), legacy.ShortID+".json")
		legacyTaskBefore, err := os.ReadFile(legacyTaskPath)
		if err != nil {
			t.Fatalf("ReadFile(legacy task before branch) error = %v", err)
		}

		runConfigGit(t, root, "switch", "-c", "legacy-reader")
		listed := runCLIJSONTasks(t, root, "--json", "task", "list")
		if len(listed) != 1 || listed[0].ShortID != legacy.ShortID {
			t.Fatalf("named branch legacy list = %#v, want old task %s", listed, legacy.ShortID)
		}
		legacyIndexAfterOpen, err := os.ReadFile(legacyIndexPath)
		if err != nil {
			t.Fatalf("ReadFile(legacy index after branch open) error = %v", err)
		}
		if string(legacyIndexAfterOpen) != string(legacyIndexBefore) {
			t.Fatalf("legacy index changed while opening named branch: got %q, want %q", legacyIndexAfterOpen, legacyIndexBefore)
		}
		legacyTaskAfterOpen, err := os.ReadFile(legacyTaskPath)
		if err != nil {
			t.Fatalf("ReadFile(legacy task after branch open) error = %v", err)
		}
		if string(legacyTaskAfterOpen) != string(legacyTaskBefore) {
			t.Fatalf("legacy task changed while opening named branch: got %q, want %q", legacyTaskAfterOpen, legacyTaskBefore)
		}

		updated := runCLIJSONTask(t, root, "--json", "task", "update", legacy.ShortID, "--description", "updated by named branch")
		if updated.ShortID != legacy.ShortID || updated.Description != "updated by named branch" {
			t.Fatalf("named branch legacy update = %#v, want unchanged short ID", updated.Task)
		}
		branchTask := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "named branch task")
		if want := "wtp-" + core.NewBranchScope("legacy-reader").BranchID + "-0001"; branchTask.ShortID != want {
			t.Fatalf("named branch short ID = %q, want %q", branchTask.ShortID, want)
		}
		legacyIndexAfterCreate, err := os.ReadFile(legacyIndexPath)
		if err != nil {
			t.Fatalf("ReadFile(legacy index after branch create) error = %v", err)
		}
		if string(legacyIndexAfterCreate) != string(legacyIndexBefore) {
			t.Fatalf("legacy index changed during named branch create: got %q, want %q", legacyIndexAfterCreate, legacyIndexBefore)
		}
	})
}

func TestRunScopesTasksAcrossLinkedWorktreesUsingSharedStore(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	mainRoot := createIntegrationRepository(t, fixture, "shared scope main")
	sharedStore := filepath.Join(fixture, "shared scoped task store")
	mainBranch := "scope/main-worktree"
	firstLinkedBranch := "scope/linked-one"
	secondLinkedBranch := "scope/linked-two"

	// Seed a legacy task from detached HEAD. Named branches must use it only as
	// a fallback, never as their own allocation namespace.
	runConfigGit(t, mainRoot, "checkout", "--detach")
	writeConfigFile(t, mainRoot, `{"wtpDir":`+jsonString(sharedStore)+`}`)
	legacy := runCLIJSONTask(t, mainRoot, "--json", "task", "create", "--title", "legacy fallback")
	if legacy.ShortID != "wtp-0001" {
		t.Fatalf("legacy short ID = %q, want wtp-0001", legacy.ShortID)
	}
	runConfigGit(t, mainRoot, "switch", "-c", mainBranch)

	firstLinkedRoot := filepath.Join(fixture, "linked worktree one")
	secondLinkedRoot := filepath.Join(fixture, "linked worktree two")
	runConfigGit(t, mainRoot, "worktree", "add", "-b", firstLinkedBranch, firstLinkedRoot)
	runConfigGit(t, mainRoot, "worktree", "add", "-b", secondLinkedBranch, secondLinkedRoot)
	for _, root := range []string{mainRoot, firstLinkedRoot, secondLinkedRoot} {
		writeConfigFile(t, root, `{"wtpDir":`+jsonString(sharedStore)+`}`)
	}
	mainNested := filepath.Join(mainRoot, "nested", "cli invocation")
	firstLinkedNested := filepath.Join(firstLinkedRoot, "nested", "cli invocation")
	if err := os.MkdirAll(mainNested, 0o755); err != nil {
		t.Fatalf("MkdirAll(main nested) error = %v", err)
	}
	if err := os.MkdirAll(firstLinkedNested, 0o755); err != nil {
		t.Fatalf("MkdirAll(first linked nested) error = %v", err)
	}

	mainTask := runCLIJSONTask(t, mainNested, "--json", "task", "create", "--title", "main task", "--priority", "low")
	firstLinkedTask := runCLIJSONTask(t, firstLinkedNested, "--json", "task", "create", "--title", "first linked task", "--priority", "low")
	secondLinkedTask := runCLIJSONTask(t, secondLinkedRoot, "--json", "task", "create", "--title", "second linked task", "--priority", "urgent")

	assertScopedSequence(t, mainTask, mainBranch, 1)
	assertScopedSequence(t, firstLinkedTask, firstLinkedBranch, 1)
	assertScopedSequence(t, secondLinkedTask, secondLinkedBranch, 1)
	assertDistinctBranchTokens(t, mainBranch, firstLinkedBranch, secondLinkedBranch)
	assertEquivalentPath(t, mainTask.WorktreeDir, mainRoot)
	assertEquivalentPath(t, firstLinkedTask.WorktreeDir, firstLinkedRoot)

	// A branch's own work ranks ahead of both legacy fallback and urgent work
	// belonging to another branch.
	if ready := runCLIJSONTask(t, mainNested, "--json", "task", "ready"); ready.ID != mainTask.ID {
		t.Fatalf("main ready task = %s, want local task %s", ready.ShortID, mainTask.ShortID)
	}
	if claimed := runCLIJSONTask(t, mainNested, "--json", "task", "next", "--agent", "Main"); claimed.ID != mainTask.ID {
		t.Fatalf("main next task = %s, want local task %s", claimed.ShortID, mainTask.ShortID)
	}
	if ready := runCLIJSONTask(t, firstLinkedNested, "--json", "task", "ready"); ready.ID != firstLinkedTask.ID {
		t.Fatalf("first linked ready task = %s, want local task %s", ready.ShortID, firstLinkedTask.ShortID)
	}
	if claimed := runCLIJSONTask(t, firstLinkedNested, "--json", "task", "next", "--agent", "First"); claimed.ID != firstLinkedTask.ID {
		t.Fatalf("first linked next task = %s, want local task %s", claimed.ShortID, firstLinkedTask.ShortID)
	}
	firstLinkedSecond := runCLIJSONTask(t, firstLinkedNested, "--json", "task", "create", "--title", "foreign explicit start", "--priority", "urgent")
	assertScopedSequence(t, firstLinkedSecond, firstLinkedBranch, 2)
	if ready := runCLIJSONTask(t, secondLinkedRoot, "--json", "task", "ready"); ready.ID != secondLinkedTask.ID {
		t.Fatalf("second linked ready task = %s, want local task %s", ready.ShortID, secondLinkedTask.ShortID)
	}
	if claimed := runCLIJSONTask(t, secondLinkedRoot, "--json", "task", "next", "--agent", "Second"); claimed.ID != secondLinkedTask.ID {
		t.Fatalf("second linked next task = %s, want local task %s", claimed.ShortID, secondLinkedTask.ShortID)
	}

	// Once its local work is claimed, main may select the old global task but
	// still must not select the eligible task from the first linked branch.
	if ready := runCLIJSONTask(t, mainNested, "--json", "task", "ready"); ready.ID != legacy.ID {
		t.Fatalf("main fallback ready task = %s, want legacy task %s", ready.ShortID, legacy.ShortID)
	}
	if claimed := runCLIJSONTask(t, mainNested, "--json", "task", "next", "--agent", "Main"); claimed.ID != legacy.ID {
		t.Fatalf("main fallback next task = %s, want legacy task %s", claimed.ShortID, legacy.ShortID)
	}

	// Explicit identifiers intentionally cross the automatic-selection scope.
	startedForeign := runCLIJSONTask(t, mainNested, "--json", "task", "start", firstLinkedSecond.ShortID, "--agent", "Main")
	if startedForeign.ID != firstLinkedSecond.ID || startedForeign.Status != core.StatusInProgress || startedForeign.Readiness.Claimable {
		t.Fatalf("explicit foreign start = %#v, want started but unclaimable foreign task", startedForeign)
	}
	readyOutput, err := runCLIProcess(mainNested, "--json", "task", "ready")
	if err != nil || strings.TrimSpace(readyOutput) != "null" {
		t.Fatalf("main ready after all local and legacy work claimed: error=%v output=%q, want null", err, readyOutput)
	}

	allTasks := runCLIJSONTasks(t, mainNested, "--json", "task", "list")
	if len(allTasks) != 5 {
		t.Fatalf("shared-store list count = %d, want 5", len(allTasks))
	}
	assertTaskManifest(t, sharedStore, legacy, mainTask, firstLinkedTask, firstLinkedSecond, secondLinkedTask)
	assertIndexManifest(t, sharedStore, map[string]allocationIndexExpectation{
		"index.json": {next: 2},
		"index-" + integrationBranchToken(mainBranch) + ".json":         {branch: mainBranch, next: 2},
		"index-" + integrationBranchToken(firstLinkedBranch) + ".json":  {branch: firstLinkedBranch, next: 3},
		"index-" + integrationBranchToken(secondLinkedBranch) + ".json": {branch: secondLinkedBranch, next: 2},
	})
}

func TestRunMergesIndependentlyTrackedBranchScopedStores(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	root := createIntegrationRepository(t, fixture, "tracked merge repository")
	firstBranch := "merge/first-topic"
	secondBranch := "merge/second-topic"

	runConfigGit(t, root, "switch", "-c", firstBranch)
	firstNested := filepath.Join(root, "nested", "first invocation")
	if err := os.MkdirAll(firstNested, 0o755); err != nil {
		t.Fatalf("MkdirAll(first nested) error = %v", err)
	}
	firstTask := runCLIJSONTask(t, firstNested, "--json", "task", "create", "--title", "first independently tracked task")
	assertScopedSequence(t, firstTask, firstBranch, 1)
	runConfigGit(t, root, "add", ".wtp")
	runConfigGit(t, root, "commit", "-m", "add first branch scoped task")

	runConfigGit(t, root, "switch", "-")
	runConfigGit(t, root, "switch", "-c", secondBranch)
	secondTask := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "second independently tracked task")
	assertScopedSequence(t, secondTask, secondBranch, 1)
	runConfigGit(t, root, "add", ".wtp")
	runConfigGit(t, root, "commit", "-m", "add second branch scoped task")

	runConfigGit(t, root, "switch", firstBranch)
	runConfigGit(t, root, "merge", "--no-edit", secondBranch)

	merged := runCLIJSONTasks(t, firstNested, "--json", "task", "list")
	if len(merged) != 2 {
		t.Fatalf("merged task count = %d, want 2", len(merged))
	}
	assertTaskManifest(t, filepath.Join(root, ".wtp"), firstTask, secondTask)
	assertIndexManifest(t, filepath.Join(root, ".wtp"), map[string]allocationIndexExpectation{
		"index-" + integrationBranchToken(firstBranch) + ".json":  {branch: firstBranch, next: 2},
		"index-" + integrationBranchToken(secondBranch) + ".json": {branch: secondBranch, next: 2},
	})

	continuedFirst := runCLIJSONTask(t, firstNested, "--json", "task", "create", "--title", "first branch task after merge")
	assertScopedSequence(t, continuedFirst, firstBranch, 2)
	assertTaskManifest(t, filepath.Join(root, ".wtp"), firstTask, secondTask, continuedFirst)
	assertIndexManifest(t, filepath.Join(root, ".wtp"), map[string]allocationIndexExpectation{
		"index-" + integrationBranchToken(firstBranch) + ".json":  {branch: firstBranch, next: 3},
		"index-" + integrationBranchToken(secondBranch) + ".json": {branch: secondBranch, next: 2},
	})
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

func TestRunExplicitlyOperatesOnForeignScopedTaskAcrossBranches(t *testing.T) {
	requireGitForConfigTest(t)

	fixture := t.TempDir()
	root := createIntegrationRepository(t, fixture, "foreign explicit operations repository")
	sharedStore := filepath.Join(fixture, "foreign explicit operations store")
	writeConfigFile(t, root, `{"wtpDir":`+jsonString(sharedStore)+`}`)

	runConfigGit(t, root, "switch", "-c", "foreign-operations")
	foreignDependency := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Foreign dependency", "--agent", "Tony")
	foreignTarget := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Foreign explicit target", "--description", "original description", "--agent", "Tony", "--depends-on", foreignDependency.ShortID)

	runConfigGit(t, root, "switch", "-c", "current-operations")
	listed := runCLIJSONTasks(t, root, "--json", "task", "list", "--agent", "Tony")
	if len(listed) != 2 {
		t.Fatalf("foreign task list count = %d, want 2", len(listed))
	}
	var listedTarget core.TaskView
	for _, task := range listed {
		if task.ID == foreignTarget.ID {
			listedTarget = task
			break
		}
	}
	if listedTarget.ID == "" || listedTarget.Readiness.Claimable || !listedTarget.Readiness.Blocked {
		t.Fatalf("listed foreign target = %#v, want visible, unclaimable, and blocked", listedTarget)
	}

	shown := runCLIJSONTask(t, root, "--json", "task", "show", foreignTarget.ID, "--agent", "Tony")
	if shown.ID != foreignTarget.ID || shown.ShortID != foreignTarget.ShortID || shown.Readiness.Claimable {
		t.Fatalf("foreign UUID show = %#v, want matching unclaimable task", shown)
	}

	graphOutput, err := runCLIProcess(root, "--json", "graph", "--status", "todo")
	if err != nil {
		t.Fatalf("foreign graph: %v\n%s", err, graphOutput)
	}
	for _, needle := range []string{foreignDependency.ShortID, foreignDependency.Title, foreignTarget.ShortID, foreignTarget.Title} {
		if !strings.Contains(graphOutput, needle) {
			t.Fatalf("foreign graph missing %q: %s", needle, graphOutput)
		}
	}

	readyOutput, err := runCLIProcess(root, "--json", "task", "ready", "--agent", "Tony")
	if err != nil {
		t.Fatalf("foreign ready: %v\n%s", err, readyOutput)
	}
	if strings.TrimSpace(readyOutput) != "null" {
		t.Fatalf("foreign ready output = %q, want null for no eligible task", readyOutput)
	}
	nextOutput, err := runCLIProcess(root, "task", "next", "--agent", "Tony")
	if err == nil || !strings.Contains(nextOutput, provider.ErrNoEligibleTask.Error()) {
		t.Fatalf("foreign next error = %v output = %q, want no eligible task", err, nextOutput)
	}

	blockedStartOutput, err := runCLIProcess(root, "task", "start", foreignTarget.ShortID, "--agent", "Tony")
	if err == nil || !strings.Contains(blockedStartOutput, "unresolved dependencies") {
		t.Fatalf("blocked foreign start error = %v output = %q, want dependency error", err, blockedStartOutput)
	}

	startedDependency := runCLIJSONTask(t, root, "--json", "task", "start", foreignDependency.ID, "--agent", "Tony")
	if startedDependency.Status != core.StatusInProgress || startedDependency.Assignee != "Tony" {
		t.Fatalf("foreign dependency start = %#v, want inProgress for Tony", startedDependency.Task)
	}
	doneDependency := runCLIJSONTask(t, root, "--json", "task", "done", foreignDependency.ShortID, "--agent", "Tony")
	if doneDependency.Status != core.StatusDone {
		t.Fatalf("foreign dependency done = %#v, want done", doneDependency.Task)
	}

	updated := runCLIJSONTask(t, root, "--json", "task", "update", foreignTarget.ID, "--description", "updated from current branch")
	if updated.Description != "updated from current branch" {
		t.Fatalf("foreign UUID update = %#v, want updated description", updated.Task)
	}
	commented := runCLIJSONTask(t, root, "--json", "task", "comment", foreignTarget.ShortID, "--agent", "Tony", "--message", "commented from current branch")
	if len(commented.Comments) != 1 || commented.Comments[0].Message != "commented from current branch" {
		t.Fatalf("foreign short-ID comment = %#v, want one comment", commented.Comments)
	}

	started := runCLIJSONTask(t, root, "--json", "task", "start", foreignTarget.ID, "--agent", "Tony")
	if started.Status != core.StatusInProgress || started.Readiness.Claimable {
		t.Fatalf("foreign UUID start = %#v, want inProgress and unclaimable", started)
	}
	paused := runCLIJSONTask(t, root, "--json", "task", "pause", foreignTarget.ShortID, "--agent", "Tony")
	if paused.Status != core.StatusPaused || paused.Readiness.Claimable {
		t.Fatalf("foreign short-ID pause = %#v, want paused and unclaimable", paused)
	}
	restarted := runCLIJSONTask(t, root, "--json", "task", "start", foreignTarget.ShortID, "--agent", "Tony")
	if restarted.Status != core.StatusInProgress {
		t.Fatalf("foreign short-ID restart = %#v, want inProgress", restarted)
	}
	completed := runCLIJSONTask(t, root, "--json", "task", "done", foreignTarget.ID, "--agent", "Tony")
	if completed.Status != core.StatusDone || len(completed.Comments) != 1 {
		t.Fatalf("foreign UUID done = %#v, want done with comment", completed.Task)
	}

	unknownOutput, err := runCLIProcess(root, "task", "show", "wtp-9999")
	if err == nil || !strings.Contains(unknownOutput, `task "wtp-9999" not found`) {
		t.Fatalf("unknown foreign identifier error = %v output = %q", err, unknownOutput)
	}
	ambiguousOutput, err := runCLIProcess(root, "task", "show", foreignTarget.ID, foreignDependency.ID)
	if err == nil || !strings.Contains(ambiguousOutput, "expected exactly one task ID") {
		t.Fatalf("ambiguous CLI identifier error = %v output = %q", err, ambiguousOutput)
	}
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
	if !strings.Contains(output, "parse "+canonicalTestPath(configPath)) {
		t.Fatalf("Run() output = %q, want parse error naming %s", output, canonicalTestPath(configPath))
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
	if !strings.Contains(output, "initialize flat-file storage at "+canonicalTestPath(storagePath)) {
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
	if _, err := os.Stat(filepath.Join(exportDir, "handoffs.json")); err != nil {
		t.Fatalf("legacy export missing handoffs: %v", err)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})
	if output, err := runCLIProcess(root, "--export-tasks="+exportDir); err != nil {
		t.Fatalf("repeat legacy export: %v\n%s", err, output)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})
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
	if _, err := os.Stat(filepath.Join(exportDir, "handoffs.json")); err != nil {
		t.Fatalf("exported handoffs missing: %v", err)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})
	if output, err := runCLIProcess(root, "--json", "export", "--out", exportDir); err != nil {
		t.Fatalf("repeat public export: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("ReadDir(export) after repeat: %v", err)
	}
	if got, want := entryNames(entries), []string{created.ID + ".json", "handoffs.json"}; !slices.Equal(got, want) {
		t.Fatalf("repeat public export entries = %v, want %v", got, want)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})
	if got := storageManifest(t, filepath.Join(root, ".wtp")); got != before {
		t.Fatalf("export changed active storage:\nbefore %s\nafter %s", before, got)
	}
	output, err = runCLIProcess(root, "export", "--out", filepath.Join(root, ".wtp"))
	if err == nil || !strings.Contains(output, "overlaps active storage") {
		t.Fatalf("overlapping public export error = %v, output = %q", err, output)
	}
}

func TestRunPublicExportOfEmptyStoreWritesManagedHandoffs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty export repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	exportDir := filepath.Join(root, "empty snapshot")
	output, err := runCLIProcess(root, "--json", "export", "--out", exportDir)
	if err != nil {
		t.Fatalf("empty public export: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("ReadDir(empty export): %v", err)
	}
	if got, want := entryNames(entries), []string{"handoffs.json"}; !slices.Equal(got, want) {
		t.Fatalf("empty export entries = %v, want %v", got, want)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})

	if output, err := runCLIProcess(root, "--json", "export", "--out", exportDir); err != nil {
		t.Fatalf("repeat empty public export: %v\n%s", err, output)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), []core.Handoff{})
}

func TestRunPublicExportIncludesExactRetainedHandoffsAndCleansStaleTasks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "populated export repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	created := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "exported task")
	global := runCLIJSONHandoff(t, root, "--json", "handoff", "write", "--agent", "Ada", "--message", "Global retained context")
	taskScoped := runCLIJSONHandoff(t, root, "--json", "handoff", "write", "--task", created.ShortID, "--agent", "Tony", "--message", "Task retained context")
	wantHandoffs := []core.Handoff{global.Handoff, taskScoped.Handoff}

	exportDir := filepath.Join(root, "populated snapshot")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(export) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportDir, created.ID+".json"), []byte("old task snapshot\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(current task) error = %v", err)
	}
	staleID := "00000000-0000-4000-8000-000000000099"
	if err := os.WriteFile(filepath.Join(exportDir, staleID+".json"), []byte("stale task snapshot\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(stale task) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportDir, "handoffs.json"), []byte("old handoffs\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old handoffs) error = %v", err)
	}

	output, err := runCLIProcess(root, "--json", "export", "--out", exportDir)
	if err != nil {
		t.Fatalf("populated public export: %v\n%s", err, output)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), wantHandoffs)
	if _, err := os.Stat(filepath.Join(exportDir, staleID+".json")); !os.IsNotExist(err) {
		t.Fatalf("stale task snapshot still exists after export: %v", err)
	}
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("ReadDir(populated export): %v", err)
	}
	if got, want := entryNames(entries), []string{created.ID + ".json", "handoffs.json"}; !slices.Equal(got, want) {
		t.Fatalf("populated export entries = %v, want %v", got, want)
	}

	if output, err := runCLIProcess(root, "--json", "export", "--out", exportDir); err != nil {
		t.Fatalf("repeat populated public export: %v\n%s", err, output)
	}
	assertExportedHandoffs(t, filepath.Join(exportDir, "handoffs.json"), wantHandoffs)
}

func TestRunPublicExportRejectsEveryOtherUnmanagedEntry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unmanaged export repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	created := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "exported task")
	exportDir := filepath.Join(root, "unmanaged snapshot")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(export) error = %v", err)
	}
	currentPath := filepath.Join(exportDir, created.ID+".json")
	currentBefore := []byte("old task snapshot\n")
	if err := os.WriteFile(currentPath, currentBefore, 0o644); err != nil {
		t.Fatalf("WriteFile(current task) error = %v", err)
	}
	unmanagedNames := []string{
		"notes.txt",
		"another-dir",
		"task.json",
		"handoffs.json.bak",
		"00000000-0000-0000-0000-000000000099.txt",
		".keep",
	}
	for _, name := range unmanagedNames {
		path := filepath.Join(exportDir, name)
		if strings.HasSuffix(name, "-dir") {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("Mkdir(%s) error = %v", name, err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	output, err := runCLIProcess(root, "export", "--out", exportDir)
	wantUnmanaged := append([]string(nil), unmanagedNames...)
	slices.Sort(wantUnmanaged)
	wantError := "unmanaged entries: " + strings.Join(wantUnmanaged, ", ")
	if err == nil || !strings.Contains(output, wantError) {
		t.Fatalf("unmanaged public export error = %v, output = %q, want %s", err, output, wantError)
	}
	currentAfter, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("ReadFile(current after rejection): %v", readErr)
	}
	if string(currentAfter) != string(currentBefore) {
		t.Fatalf("current task snapshot changed after rejection: got %q, want %q", currentAfter, currentBefore)
	}
	for _, name := range unmanagedNames {
		if _, statErr := os.Stat(filepath.Join(exportDir, name)); statErr != nil {
			t.Fatalf("unmanaged entry %s changed after rejection: %v", name, statErr)
		}
	}
}

func TestRunPublicExportRejectsCorruptHandoffsBeforeRefreshingFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corrupt export repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	created := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "exported task")
	exportDir := filepath.Join(root, "corrupt snapshot")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(export) error = %v", err)
	}
	currentPath := filepath.Join(exportDir, created.ID+".json")
	currentBefore := []byte("old task snapshot\n")
	if err := os.WriteFile(currentPath, currentBefore, 0o644); err != nil {
		t.Fatalf("WriteFile(current task) error = %v", err)
	}
	stalePath := filepath.Join(exportDir, "00000000-0000-4000-8000-000000000099.json")
	if err := os.WriteFile(stalePath, []byte("stale task snapshot\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(stale task) error = %v", err)
	}
	exportHandoffsPath := filepath.Join(exportDir, "handoffs.json")
	exportHandoffsBefore := []byte("old exported handoffs\n")
	if err := os.WriteFile(exportHandoffsPath, exportHandoffsBefore, 0o644); err != nil {
		t.Fatalf("WriteFile(export handoffs) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".wtp", "handoffs.json"), []byte("not valid JSON"), 0o644); err != nil {
		t.Fatalf("WriteFile(active handoffs) error = %v", err)
	}

	output, err := runCLIProcess(root, "--json", "export", "--out", exportDir)
	if err == nil || !strings.Contains(output, "corrupt handoff file") {
		t.Fatalf("corrupt public export error = %v, output = %q, want corrupt handoff file", err, output)
	}
	currentAfter, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("ReadFile(current after corrupt rejection): %v", readErr)
	}
	if string(currentAfter) != string(currentBefore) {
		t.Fatalf("current task snapshot changed before corrupt handoff validation: got %q, want %q", currentAfter, currentBefore)
	}
	if _, statErr := os.Stat(stalePath); statErr != nil {
		t.Fatalf("stale task snapshot removed before corrupt handoff validation: %v", statErr)
	}
	exportHandoffsAfter, readErr := os.ReadFile(exportHandoffsPath)
	if readErr != nil {
		t.Fatalf("ReadFile(export handoffs after corrupt rejection): %v", readErr)
	}
	if string(exportHandoffsAfter) != string(exportHandoffsBefore) {
		t.Fatalf("export handoffs changed before corrupt handoff validation: got %q, want %q", exportHandoffsAfter, exportHandoffsBefore)
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

func runCLIJSONHandoff(t *testing.T, dir string, args ...string) provider.HandoffWriteResult {
	t.Helper()
	output, err := runCLIProcess(dir, args...)
	if err != nil {
		t.Fatalf("wtp %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var result provider.HandoffWriteResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode handoff output %q: %v", output, err)
	}
	return result
}

func runCLIJSONHandoffs(t *testing.T, dir string, args ...string) provider.HandoffListResult {
	t.Helper()
	output, err := runCLIProcess(dir, args...)
	if err != nil {
		t.Fatalf("wtp %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var result provider.HandoffListResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode handoff list output %q: %v", output, err)
	}
	return result
}

func runCLIJSONHandoffPurge(t *testing.T, dir string, args ...string) provider.HandoffPurgeResult {
	t.Helper()
	output, err := runCLIProcess(dir, args...)
	if err != nil {
		t.Fatalf("wtp %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var result provider.HandoffPurgeResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode handoff purge output %q: %v", output, err)
	}
	return result
}

func assertHandoffMessages(t *testing.T, handoffs []core.Handoff, want []string) {
	t.Helper()
	messages := make([]string, 0, len(handoffs))
	for _, handoff := range handoffs {
		messages = append(messages, handoff.Message)
	}
	slices.Sort(messages)
	slices.Sort(want)
	if !slices.Equal(messages, want) {
		t.Fatalf("handoff messages = %v, want %v", messages, want)
	}
}

func assertExportedHandoffs(t *testing.T, path string, want []core.Handoff) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var got struct {
		Handoffs []core.Handoff `json:"handoffs"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode exported handoffs %s: %v", path, err)
	}
	if got.Handoffs == nil {
		t.Fatalf("exported handoffs = nil, want empty or populated array")
	}
	if !slices.Equal(got.Handoffs, want) {
		t.Fatalf("exported handoffs = %#v, want %#v", got.Handoffs, want)
	}
}

func assertOnlyLegacyAllocationIndex(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "meta"))
	if err != nil {
		t.Fatalf("ReadDir(%s/meta) error = %v", root, err)
	}
	var regularFiles []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			regularFiles = append(regularFiles, entry.Name())
		}
	}
	slices.Sort(regularFiles)
	if !slices.Equal(regularFiles, []string{"index.json"}) {
		t.Fatalf("allocation index files = %v, want only index.json", regularFiles)
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

type allocationIndexExpectation struct {
	branch string
	next   int
}

func assertScopedSequence(t *testing.T, task core.TaskView, branch string, sequence int) {
	t.Helper()
	parts, err := core.ParseShortID(task.ShortID)
	if err != nil {
		t.Fatalf("ParseShortID(%q): %v", task.ShortID, err)
	}
	if !parts.IsScoped() || parts.BranchID != integrationBranchToken(branch) || parts.Sequence != fmt.Sprintf("%04d", sequence) {
		t.Fatalf("task short ID = %q, want branch token %s and sequence %04d", task.ShortID, integrationBranchToken(branch), sequence)
	}
}

func assertDistinctBranchTokens(t *testing.T, branches ...string) {
	t.Helper()
	tokens := make(map[string]string, len(branches))
	for _, branch := range branches {
		token := integrationBranchToken(branch)
		if len(token) != 8 {
			t.Fatalf("branch token for %q = %q, want exactly 8 hexadecimal characters", branch, token)
		}
		if previous, found := tokens[token]; found {
			t.Fatalf("branch token collision: %q and %q both use %s", previous, branch, token)
		}
		tokens[token] = branch
	}
}

func integrationBranchToken(branch string) string {
	digest := sha256.Sum256([]byte(branch))
	return hex.EncodeToString(digest[:4])
}

func assertTaskManifest(t *testing.T, root string, tasks ...core.TaskView) {
	t.Helper()
	want := make([]string, 0, len(tasks))
	for _, task := range tasks {
		want = append(want, task.ShortID+".json")
	}
	slices.Sort(want)

	var got []string
	for _, status := range []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusPaused, core.StatusDone} {
		entries, err := os.ReadDir(filepath.Join(root, string(status)))
		if err != nil {
			t.Fatalf("ReadDir(%s) error = %v", status, err)
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				got = append(got, entry.Name())
			}
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("task manifest = %v, want %v", got, want)
	}
}

func assertIndexManifest(t *testing.T, root string, want map[string]allocationIndexExpectation) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "meta"))
	if err != nil {
		t.Fatalf("ReadDir(%s/meta) error = %v", root, err)
	}
	gotNames := make([]string, 0, len(want))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			gotNames = append(gotNames, entry.Name())
		}
	}
	wantNames := make([]string, 0, len(want))
	for name := range want {
		wantNames = append(wantNames, name)
	}
	slices.Sort(gotNames)
	slices.Sort(wantNames)
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("allocation index manifest = %v, want %v", gotNames, wantNames)
	}
	for name, expected := range want {
		data, readErr := os.ReadFile(filepath.Join(root, "meta", name))
		if readErr != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, readErr)
		}
		var index struct {
			Branch string `json:"branch"`
			Next   int    `json:"next"`
		}
		if decodeErr := json.Unmarshal(data, &index); decodeErr != nil {
			t.Fatalf("decode allocation index %s: %v", name, decodeErr)
		}
		if index.Branch != expected.branch || index.Next != expected.next {
			t.Fatalf("allocation index %s = %#v, want branch %q next %d", name, index, expected.branch, expected.next)
		}
	}
}

func runCLIProcess(dir string, args ...string) (string, error) {
	commandArgs := append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)
	command := exec.Command(os.Args[0], commandArgs...)
	command.Dir = dir
	command.Env = append(os.Environ(), "WTP_CLI_PROCESS=1")
	output, err := command.CombinedOutput()
	return string(output), err
}

func canonicalTestPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
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
