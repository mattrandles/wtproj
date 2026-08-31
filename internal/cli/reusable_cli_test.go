package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestReusableCreateAcceptsFlagsInAnyOrderAndReturnsTypedJSON(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer

	if err := Run([]string{
		"--json", "reusable", "create",
		"--instructions", "Check café ✓\n- **bold** markdown",
		"--title", "Run the checks",
		"--name", "Checks",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(reusable create) error = %v", err)
	}
	var definition core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &definition); err != nil {
		t.Fatalf("decode created definition: %v; output = %q", err, stdout.String())
	}
	if definition.ID == "" || definition.Name != "Checks" || definition.Title != "Run the checks" || definition.Instructions != "Check café ✓\n- **bold** markdown" {
		t.Fatalf("created definition = %#v", definition)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestReusableListJSONIsStableAndSortedAndEmptyListIsTyped(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"reusable", "create", "--name", "Zeta", "--title", "Z", "--instructions", "z"}, &stdout, &stderr); err != nil {
		t.Fatalf("create Zeta error = %v", err)
	}
	if err := Run([]string{"reusable", "create", "--name", "alpha", "--title", "A", "--instructions", "a"}, &stdout, &stderr); err != nil {
		t.Fatalf("create alpha error = %v", err)
	}

	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("first list error = %v", err)
	}
	first := stdout.String()
	var definitions []core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &definitions); err != nil {
		t.Fatalf("decode list: %v; output = %q", err, first)
	}
	if len(definitions) != 2 || definitions[0].Name != "alpha" || definitions[1].Name != "Zeta" {
		t.Fatalf("list definitions = %#v, want alpha then Zeta", definitions)
	}

	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("second list error = %v", err)
	}
	if got := stdout.String(); got != first {
		t.Fatalf("list JSON changed between identical reads:\nfirst:  %ssecond: %s", first, got)
	}

	emptyDir := t.TempDir()
	chdir(t, emptyDir)
	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("empty list error = %v", err)
	}
	if got := stdout.String(); got != "[]\n" {
		t.Fatalf("empty list JSON = %q, want typed empty array", got)
	}
}

func TestReusableShowHumanOutputIndentsMultilineInstructions(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"reusable", "create", "--name", "Docs", "--title", "Read docs", "--instructions", "first line\n- **bold**\nsecond line"}, &stdout, &stderr); err != nil {
		t.Fatalf("create error = %v", err)
	}
	stdout.Reset()
	if err := Run([]string{"reusable", "show", "docs"}, &stdout, &stderr); err != nil {
		t.Fatalf("show error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"name: Docs\n",
		"title: Read docs\n",
		"instructions:\n  first line\n  - **bold**\n  second line\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("human output missing %q: %q", want, output)
		}
	}
	if strings.Contains(output, "instructions: first line") || strings.Contains(output, "\n- **bold**\n") {
		t.Fatalf("multiline instructions were not safely indented: %q", output)
	}

	stdout.Reset()
	if err := Run([]string{"reusable", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("human list error = %v", err)
	}
	if got, want := stdout.String(), "Docs\tRead docs\n"; got != want {
		t.Fatalf("human list = %q, want %q", got, want)
	}
}

func TestReusableCommandValidationErrorsAreClear(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing subcommand", args: []string{"reusable"}, want: "reusable subcommand is required"},
		{name: "unsupported subcommand", args: []string{"reusable", "edit"}, want: `unknown reusable subcommand "edit"`},
		{name: "create extra positional", args: []string{"reusable", "create", "--name", "n", "--title", "t", "--instructions", "i", "extra"}, want: reusableCreateUsage},
		{name: "create missing name", args: []string{"reusable", "create", "--title", "t", "--instructions", "i"}, want: "reusable create --name is required"},
		{name: "create empty title", args: []string{"reusable", "create", "--name", "n", "--title", " \t", "--instructions", "i"}, want: "reusable create --title cannot be empty"},
		{name: "list selector", args: []string{"reusable", "list", "selector"}, want: reusableListUsage},
		{name: "show missing selector", args: []string{"reusable", "show"}, want: reusableShowUsage},
		{name: "show extra selector", args: []string{"reusable", "show", "one", "two"}, want: reusableShowUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			var stdout, stderr bytes.Buffer
			err := Run(test.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run(%v) error = %v, want containing %q", test.args, err, test.want)
			}
		})
	}
}

func TestReusableCreateAndShowReportDuplicateAndUnknownDefinitions(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer

	create := []string{"reusable", "create", "--name", "Docs", "--title", "Read docs", "--instructions", "Read"}
	if err := Run(create, &stdout, &stderr); err != nil {
		t.Fatalf("first create error = %v", err)
	}
	if err := Run([]string{"reusable", "create", "--name", " docs ", "--title", "Other", "--instructions", "Other"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate create error = %v, want duplicate-name error", err)
	}
	if err := Run([]string{"reusable", "show", "missing"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), `reusable task "missing" not found`) {
		t.Fatalf("unknown show error = %v, want unknown-definition error", err)
	}
}

func TestReusableUpdateCLIUsesSelectorsAndPreservesNoOpDefinition(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--json", "reusable", "create", "--name", "Checks", "--title", "Run checks", "--instructions", "Original"}, &stdout, &stderr); err != nil {
		t.Fatalf("create error = %v", err)
	}
	var created core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		t.Fatalf("decode created definition: %v", err)
	}

	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "update", "checks", "--title", "Updated", "--instructions=New instructions"}, &stdout, &stderr); err != nil {
		t.Fatalf("update by name error = %v", err)
	}
	var updated core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated definition: %v", err)
	}
	if updated.ID != created.ID || updated.Name != created.Name || updated.Title != "Updated" || updated.Instructions != "New instructions" || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("updated definition = %#v, want same ID/createdAt and changed fields", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated updatedAt = %s, want after %s", updated.UpdatedAt, created.UpdatedAt)
	}

	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "update", created.ID, "--name", "cHeCkS"}, &stdout, &stderr); err != nil {
		t.Fatalf("casing-only UUID update error = %v", err)
	}
	var casingUpdated core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &casingUpdated); err != nil {
		t.Fatalf("decode casing-only definition: %v", err)
	}
	if casingUpdated.Name != "cHeCkS" || casingUpdated.ID != created.ID || !casingUpdated.UpdatedAt.After(updated.UpdatedAt) {
		t.Fatalf("casing-only definition = %#v", casingUpdated)
	}

	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "update", "checks", "--name", "  cHeCkS  ", "--title", "Updated", "--instructions", "New instructions"}, &stdout, &stderr); err != nil {
		t.Fatalf("normalized no-op update error = %v", err)
	}
	var noOp core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &noOp); err != nil {
		t.Fatalf("decode no-op definition: %v", err)
	}
	if noOp.Name != "cHeCkS" || noOp.Title != "Updated" || noOp.Instructions != "New instructions" || noOp.UpdatedAt != casingUpdated.UpdatedAt {
		t.Fatalf("no-op definition = %#v, want casing update retained", noOp)
	}

	stdout.Reset()
	if err := Run([]string{"reusable", "show", casingUpdated.ID}, &stdout, &stderr); err != nil {
		t.Fatalf("show after update error = %v", err)
	}
	if !strings.Contains(stdout.String(), "name: cHeCkS\n") || !strings.Contains(stdout.String(), "id: "+created.ID+"\n") {
		t.Fatalf("human updated output = %q", stdout.String())
	}

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing fields", args: []string{"reusable", "update", created.ID}, want: "requires at least one field"},
		{name: "empty name", args: []string{"reusable", "update", created.ID, "--name="}, want: "--name cannot be empty"},
		{name: "empty title", args: []string{"reusable", "update", created.ID, "--title", " \t"}, want: "--title cannot be empty"},
		{name: "empty instructions", args: []string{"reusable", "update", created.ID, "--instructions", ""}, want: "--instructions cannot be empty"},
		{name: "extra positional", args: []string{"reusable", "update", created.ID, "extra", "--title", "later"}, want: reusableUpdateUsage},
		{name: "unknown option", args: []string{"reusable", "update", created.ID, "--force"}, want: "flag provided but not defined"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := Run(test.args, &out, &errOut)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run(%v) error = %v, want containing %q", test.args, err, test.want)
			}
		})
	}
}

func TestReusableDeleteCLIUsesUUIDAndDetachesEveryStatusIncludingDone(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--json", "reusable", "create", "--name", "Delete me", "--title", "Delete title", "--instructions", "Delete instructions"}, &stdout, &stderr); err != nil {
		t.Fatalf("create deleted definition error = %v", err)
	}
	var deleted core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &deleted); err != nil {
		t.Fatalf("decode deleted definition: %v", err)
	}
	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "create", "--name", "Keep", "--title", "Keep title", "--instructions", "Keep instructions"}, &stdout, &stderr); err != nil {
		t.Fatalf("create retained definition error = %v", err)
	}
	var retained core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &retained); err != nil {
		t.Fatalf("decode retained definition: %v", err)
	}
	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "create", "--name", "Unused", "--title", "Unused title", "--instructions", "Unused instructions"}, &stdout, &stderr); err != nil {
		t.Fatalf("create unreferenced definition error = %v", err)
	}
	var unused core.ReusableTaskDefinition
	if err := json.Unmarshal(stdout.Bytes(), &unused); err != nil {
		t.Fatalf("decode unreferenced definition: %v", err)
	}

	p, _, err := providerAndContextForInvocation(".")
	if err != nil {
		t.Fatalf("providerAndContextForInvocation() error = %v", err)
	}
	created := make([]core.TaskView, 0, 3)
	for _, status := range []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusDone} {
		task, err := p.CreateTask(core.CreateTaskInput{
			Title:         "references " + string(status),
			Status:        status,
			ReusableTasks: []string{deleted.ID, retained.ID},
		})
		if err != nil {
			t.Fatalf("CreateTask(%s) error = %v", status, err)
		}
		created = append(created, task)
	}

	stdout.Reset()
	if err := Run([]string{"--json", "reusable", "delete", deleted.ID}, &stdout, &stderr); err != nil {
		t.Fatalf("delete by UUID error = %v", err)
	}
	var result provider.ReusableTaskDeleteResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode delete result: %v; output = %q", err, stdout.String())
	}
	if result.Deleted.ID != deleted.ID || result.Deleted.Name != deleted.Name || result.DetachedTaskCount != len(created) {
		t.Fatalf("delete result = %#v, want deleted definition and count %d", result, len(created))
	}
	if _, err := p.(provider.ReusableTaskProvider).GetReusableTask(deleted.ID); err == nil {
		t.Fatal("deleted definition still resolves")
	}
	for _, before := range created {
		after, err := p.GetTask(before.ID, "")
		if err != nil {
			t.Fatalf("GetTask(%s) after delete error = %v", before.ShortID, err)
		}
		if len(after.ReusableTasks) != 1 || after.ReusableTasks[0].ID != retained.ID {
			t.Fatalf("task %s reusable view = %#v, want retained definition only", before.ShortID, after.ReusableTasks)
		}
		if !after.UpdatedAt.After(before.UpdatedAt) {
			t.Fatalf("task %s updatedAt = %s, want after %s", before.ShortID, after.UpdatedAt, before.UpdatedAt)
		}
	}

	stdout.Reset()
	if err := Run([]string{"reusable", "delete", "unused"}, &stdout, &stderr); err != nil {
		t.Fatalf("unreferenced human delete error = %v", err)
	}
	wantSummary := "deleted reusable definition \"Unused\" (" + unused.ID + "); detached 0 tasks\n"
	if got := stdout.String(); got != wantSummary {
		t.Fatalf("human delete summary = %q, want %q", got, wantSummary)
	}

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "missing selector", args: []string{"reusable", "delete"}},
		{name: "extra selector", args: []string{"reusable", "delete", "one", "two"}},
		{name: "force is not supported", args: []string{"reusable", "delete", "one", "--force"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := Run(test.args, &out, &errOut); err == nil || !strings.Contains(err.Error(), reusableDeleteUsage) {
				t.Fatalf("Run(%v) error = %v, want usage", test.args, err)
			}
		})
	}
}

func TestReusableMutationCLIPropagatesConcurrentAndStaleErrors(t *testing.T) {
	concurrentErr := errors.New("concurrent reusable mutation")
	staleErr := errors.New("stale reusable definition")
	for _, test := range []struct {
		name string
		run  func(context) error
		want error
	}{
		{
			name: "concurrent update",
			run: func(ctx context) error {
				return runReusableUpdate(ctx, []string{"Checks", "--title", "new"})
			},
			want: concurrentErr,
		},
		{
			name: "stale delete",
			run: func(ctx context) error {
				return runReusableDelete(ctx, []string{"Checks"})
			},
			want: staleErr,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &reusableMutationErrorCLIProvider{updateErr: concurrentErr, deleteErr: staleErr}
			err := test.run(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}})
			if !errors.Is(err, test.want) {
				t.Fatalf("mutation error = %v, want %v", err, test.want)
			}
		})
	}
}

type reusableMutationErrorCLIProvider struct {
	provider.Provider
	updateErr error
	deleteErr error
}

var _ provider.ReusableTaskMutationProvider = (*reusableMutationErrorCLIProvider)(nil)

func (p *reusableMutationErrorCLIProvider) ListReusableTasks() ([]core.ReusableTaskDefinition, error) {
	return nil, nil
}

func (p *reusableMutationErrorCLIProvider) GetReusableTask(string) (core.ReusableTaskDefinition, error) {
	return core.ReusableTaskDefinition{}, nil
}

func (p *reusableMutationErrorCLIProvider) CreateReusableTask(core.CreateReusableTaskInput) (core.ReusableTaskDefinition, error) {
	return core.ReusableTaskDefinition{}, nil
}

func (p *reusableMutationErrorCLIProvider) UpdateReusableTask(string, core.UpdateReusableTaskInput) (core.ReusableTaskDefinition, error) {
	return core.ReusableTaskDefinition{}, p.updateErr
}

func (p *reusableMutationErrorCLIProvider) DeleteReusableTask(string) (provider.ReusableTaskDeleteResult, error) {
	return provider.ReusableTaskDeleteResult{}, p.deleteErr
}
