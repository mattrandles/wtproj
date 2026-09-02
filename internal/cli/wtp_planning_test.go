package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func planningSkillPath(t *testing.T, parts ...string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(append([]string{"..", "..", "skills", "wtp-codex-planning"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func planningRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// These are instruction contracts, supplemented below by behavioral tests of
// the preview helper and real CLI. They cannot certify an agent's judgment.
func TestWTPPlanningSkillContract(t *testing.T) {
	skill := planningRead(t, planningSkillPath(t, "SKILL.md"))
	if !strings.HasPrefix(skill, "---\nname: wtp-codex-planning\ndescription: ") {
		t.Fatal("skill must be discoverable by its own name and description")
	}
	documents := map[string][]string{
		"SKILL.md": {
			"Research without writes", "request_user_input", "ask_user_questions",
			"Which enabled models and reasoning efforts should receive these tasks",
			"what percentage should each receive (total 100%)?",
			"This targets task counts, not tokens, cost,",
			"do not silently normalize a 90% total or substitute a different model",
			"largest fractional remainders", "documented complexity/risk reasons",
			"Would you like to amend these numbered tasks, refuse/cancel them, or",
			"approve (task up) this proposal in WTP?",
			"Approval of an older revision does not apply",
			"Refuse/cancel:** create nothing, mutate nothing, and stop",
			"Never create or mutate WTP tasks until explicit approval",
			"comments, handoff writes, or setup operations",
			"do not use them as a pre-approval dry run",
			"Do not ask the user to disable plan mode and repeat the planning request",
			"if the tool forbids permission questions",
			"not evidence of user authorization",
			"explain the runtime restriction, and stop without bypassing it",
		},
		"references/proposal.md": {
			"Proposal revision:", "Acceptance / verification commands and expected outcomes",
			"Requested % | Rounded quota | Assigned | Actual % | Deviation",
			"do not write a plan file before approval", "It never calls WTP",
			"do not install anything just to plan", "Do not invent `wtp task create --dry-run`",
		},
		"references/handoff.md": {
			"one creation per task, in topological order",
			"shortId` and canonical UUID `id`", "stored as canonical UUIDs",
			"wtp --json task show EXACT_ID", "Stop at the first failed creation or verification",
			"Confirmed created", "Uncertain outcome", "Remaining/not attempted",
			"Do not recreate while uncertain", "title alone is not a unique match",
			"never report the entire proposal as created", "Never repeat the whole batch",
			"Do not start `codex-wtp-loop`",
		},
	}
	for path, requirements := range documents {
		content := strings.Join(strings.Fields(planningRead(t, planningSkillPath(t, path))), " ")
		for _, requirement := range requirements {
			if !strings.Contains(content, requirement) {
				t.Errorf("%s missing planning contract %q", path, requirement)
			}
		}
	}
	for _, path := range []string{"references/proposal.md", "references/handoff.md"} {
		if !strings.Contains(skill, "]("+path+")") {
			t.Errorf("skill does not route to %s", path)
		}
	}
	ui := planningRead(t, planningSkillPath(t, "agents", "openai.yaml"))
	if !strings.Contains(ui, "$wtp-codex-planning") || strings.Contains(ui, "allow_implicit_invocation: false") {
		t.Fatal("UI metadata must invoke the discoverable planning skill")
	}
	readme := planningRead(t, filepath.Join(planningSkillPath(t), "..", "..", "README.md"))
	for _, name := range []string{"wtp-codex-planning", "setup-wtp", "task-management", "codex-wtp-loop"} {
		if !strings.Contains(readme, "](skills/"+name+"/SKILL.md)") {
			t.Errorf("README skill index missing %s", name)
		}
	}
}

func planningPython(t *testing.T) string {
	t.Helper()
	type candidate struct {
		name string
		args []string
	}
	for _, candidate := range []candidate{
		{name: "python3"},
		{name: "python"},
		{name: "py", args: []string{"-3"}},
	} {
		python, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		checkArgs := append(append([]string{}, candidate.args...), "-c", "import sys; raise SystemExit(0 if sys.version_info[0] == 3 else 1)")
		if err := exec.Command(python, checkArgs...).Run(); err != nil {
			continue
		}
		if candidate.name == "py" {
			output, err := exec.Command(python, append(append([]string{}, candidate.args...), "-c", "import sys; print(sys.executable)")...).Output()
			if err != nil {
				continue
			}
			return strings.TrimSpace(string(output))
		}
		return python
	}
	t.Skip("Python 3 unavailable; planning helper checks require Python 3")
	return ""
}

func TestWTPPlanningProposalSuite(t *testing.T) {
	cmd := exec.Command(planningPython(t), "-B", planningSkillPath(t, "scripts", "test_proposal.py"))
	cmd.Dir = t.TempDir()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("planning helper contracts: %v\n%s", err, output)
	}
}

type planningPreview struct {
	Digest       string `json:"digest"`
	HandoffReady bool   `json:"handoffReady"`
	Commands     []struct {
		Number int      `json:"number"`
		Argv   []string `json:"argv"`
	} `json:"commands"`
}

func planningPrepare(t *testing.T, dir string, proposal map[string]any, args ...string) planningPreview {
	t.Helper()
	data, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(planningPython(t), append([]string{"-B", planningSkillPath(t, "scripts", "proposal.py")}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare proposal: %v\n%s", err, output)
	}
	var preview planningPreview
	if err := json.Unmarshal(output, &preview); err != nil {
		t.Fatalf("decode preview: %v\n%s", err, output)
	}
	return preview
}

func TestWTPPlanningGeneratedCommandsAgainstCLI(t *testing.T) {
	planningPython(t)
	requireGitForConfigTest(t)
	root := createIntegrationRepository(t, t.TempDir(), "planning fixture")
	runConfigGit(t, root, "checkout", "-b", "planning-contract")
	document := planningRead(t, planningSkillPath(t, "references", "proposal.md"))
	sample := strings.SplitN(strings.SplitN(document, "```json\n", 2)[1], "```", 2)[0]
	var proposal map[string]any
	if err := json.Unmarshal([]byte(sample), &proposal); err != nil {
		t.Fatal(err)
	}
	proposal["context"] = map[string]any{"root": root, "branch": "planning-contract", "store": filepath.Join(root, ".wtp")}
	first := proposal["tasks"].([]any)[0].(map[string]any)
	first["title"] = "Literal $(touch must-not-exist) `pwd` \"quoted\""
	first["metadata"] = map[string]any{
		"issueId": "ISSUE-42", "project": "Example", "milestone": "MVP", "version": "v1",
		"featureId": "FEAT-7", "feature": "Settings", "gitRepo": root,
		"gitBranch": "planning-contract", "worktreeName": filepath.Base(root), "worktreeDir": root,
	}
	proposal["supportedMetadata"] = []string{"lane", "issueId", "project", "milestone", "version", "featureId", "feature",
		"gitRepo", "gitBranch", "worktreeName", "worktreeDir"}
	second := make(map[string]any)
	for key, value := range first {
		second[key] = value
	}
	second["number"], second["title"], second["dependencies"] = 2, "Verify integration", []any{1}
	// Reverse display order exercises forward references without predicting IDs.
	proposal["tasks"] = []any{second, first}
	preview := planningPrepare(t, root, proposal)
	if preview.HandoffReady {
		t.Fatal("preview incorrectly approves creation")
	}
	approved := planningPrepare(t, root, proposal, "--decision", "approve", "--approved-digest", preview.Digest)
	if !approved.HandoffReady || !reflect.DeepEqual(preview.Commands, approved.Commands) {
		t.Fatal("approval changed reviewed commands")
	}
	if _, err := os.Stat(filepath.Join(root, ".wtp")); !os.IsNotExist(err) {
		t.Fatalf("preview/approval helper opened the store: %v", err)
	}
	if len(approved.Commands) != 2 || approved.Commands[0].Number != 1 || approved.Commands[1].Number != 2 {
		t.Fatalf("wrong creation order: %#v", approved.Commands)
	}
	created := map[int]core.TaskView{}
	for _, command := range approved.Commands {
		argv := append([]string(nil), command.Argv...)
		if argv[0] != "wtp" {
			t.Fatal("non-canonical executable")
		}
		for i, arg := range argv {
			if arg == "--depends-on" {
				if argv[i+1] != "@1" || created[1].ShortID == "" {
					t.Fatalf("unresolved dependency: %v", argv)
				}
				argv[i+1] = created[1].ShortID
			}
		}
		task := runCLIJSONTask(t, root, argv[1:]...)
		created[command.Number] = task
		readback := runCLIJSONTask(t, root, "--json", "task", "show", task.ShortID)
		if task.ID == "" || task.ShortID == "" || !reflect.DeepEqual(task.Task, readback.Task) {
			t.Fatalf("creation/readback mismatch: %#v / %#v", task.Task, readback.Task)
		}
		if task.Status != core.StatusTodo || task.Assignee != "" || task.Priority != core.PriorityHigh || task.Estimate != core.EstimateM || task.Lane != "settings" || task.Model != "example-model high" {
			t.Fatalf("lost lifecycle, estimate, lane or assignment metadata: %#v", task.Task)
		}
		if task.IssueID != "ISSUE-42" || task.Project != "Example" || task.Milestone != "MVP" || task.Version != "v1" || task.FeatureID != "FEAT-7" || task.Feature != "Settings" {
			t.Fatalf("lost grouping metadata: %#v", task.Task)
		}
		assertEquivalentPath(t, task.GitRepo, root)
		assertEquivalentPath(t, task.WorktreeDir, root)
		if task.GitBranch != "planning-contract" || task.WorktreeName != filepath.Base(root) {
			t.Fatalf("lost worktree metadata: %#v", task.Task)
		}
		if command.Number == 1 {
			if task.Title != first["title"] || !task.Readiness.Claimable {
				t.Fatalf("independent task changed or not ready: %#v", task)
			}
		} else if !reflect.DeepEqual(task.Dependencies, []string{created[1].ID}) || !task.Readiness.Blocked {
			t.Fatalf("dependency not canonical or dependent task incorrectly ready: %#v", task)
		}
		for _, text := range []string{first["description"].(string), first["acceptance"].(string), first["verification"].(string), first["assignmentReason"].(string), "Scope:"} {
			if !strings.Contains(task.Description, text) {
				t.Errorf("description lost approved detail %q", text)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("literal task title executed as shell: %v", err)
	}
	// A failed later command must leave the confirmed creations intact. Recovery
	// is a documented agent workflow; the helper never executes or retries it.
	if output, err := runCLIProcess(root, "--json", "task", "create", "--title", "Missing prerequisite", "--depends-on", "wtp-99999999-9999"); err == nil {
		t.Fatalf("unknown dependency unexpectedly succeeded: %s", output)
	}
	if tasks := runCLIJSONTasks(t, root, "--json", "task", "list"); len(tasks) != len(created) {
		t.Fatalf("failed creation changed confirmed task count: %d", len(tasks))
	}
	// Reused IDs are outside the new-task distribution and resolve to the same
	// canonical dependency UUID as proposal placeholders did above.
	second["dependencies"] = []any{created[1].ShortID}
	proposal["tasks"] = []any{second}
	proposal["existingTaskIds"] = []string{created[1].ShortID}
	reused := planningPrepare(t, root, proposal)
	reused = planningPrepare(t, root, proposal, "--decision", "approve", "--approved-digest", reused.Digest)
	task := runCLIJSONTask(t, root, reused.Commands[0].Argv[1:]...)
	if !reflect.DeepEqual(task.Dependencies, []string{created[1].ID}) {
		t.Fatalf("reused existing dependency lost its UUID: %#v", task)
	}
}
