package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestTaskCreateAndUpdatePreserveReusableFlagOrderAndSelectors(t *testing.T) {
	p := &updateTestProvider{}
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	if err := runTaskCreate(ctx, []string{
		"--title", "Assigned task",
		"--reusable", "Checks",
		"--reusable=Review",
		"--reusable", "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
	}); err != nil {
		t.Fatalf("runTaskCreate() error = %v", err)
	}
	if got, want := p.gotCreateInput.ReusableTasks, []string{"Checks", "Review", "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("create reusable selectors = %#v, want %#v", got, want)
	}

	if err := runTaskUpdate(ctx, []string{
		"wtp-0028",
		"--reusable=Review",
		"--reusable", "Checks",
	}); err != nil {
		t.Fatalf("runTaskUpdate() error = %v", err)
	}
	if !p.gotInput.ReusableTasks.Set {
		t.Fatal("update reusable selectors were not marked set")
	}
	if got, want := p.gotInput.ReusableTasks.Value, []string{"Review", "Checks"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("update reusable selectors = %#v, want %#v", got, want)
	}
}

func TestRewriteLegacyArgsCreatePreservesReusableOrderAndEmptyAssignment(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{
		"--create-task", "--title", "Legacy assigned",
		"--reusable", "First", "--reusable=Second", "--reusable=",
	})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs() error = %v", err)
	}
	want := []string{
		"task", "create", "--title", "Legacy assigned",
		"--reusable", "First", "--reusable", "Second", "--reusable=",
	}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("rewritten legacy args = %#v, want %#v", got.args, want)
	}
}

func TestTaskUpdateReusableClearAndInvalidAssignments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "mixed empty and nonempty", args: []string{"wtp-0028", "--reusable=", "--reusable", "Checks"}, want: "cannot mix empty"},
		{name: "multiple empty", args: []string{"wtp-0028", "--reusable=", "--reusable="}, want: "single --reusable="},
		{name: "duplicate", args: []string{"wtp-0028", "--reusable", "Checks", "--reusable=checks"}, want: "duplicates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &updateTestProvider{}
			ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
			err := runTaskUpdate(ctx, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runTaskUpdate(%v) error = %v, want %q", test.args, err, test.want)
			}
			if p.updateCalls != 0 {
				t.Fatalf("invalid reusable assignment called provider %d times", p.updateCalls)
			}
		})
	}

	p := &updateTestProvider{}
	ctx := context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := runTaskUpdate(ctx, []string{"wtp-0028", "--reusable="}); err != nil {
		t.Fatalf("runTaskUpdate(clear) error = %v", err)
	}
	if !p.gotInput.ReusableTasks.Set || p.gotInput.ReusableTasks.Value == nil || len(p.gotInput.ReusableTasks.Value) != 0 {
		t.Fatalf("clear input = %#v, want set empty slice", p.gotInput.ReusableTasks)
	}
}

func TestTaskAssignmentRenderingIsOrderedAndMultilineAndJSONIsAdditive(t *testing.T) {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	task := core.TaskView{
		Task: core.Task{
			ID:              "25c3806a-bd1b-424d-889b-29e5b06679b8",
			ShortID:         "wtp-0028",
			Title:           "Assigned task",
			Status:          core.StatusTodo,
			Dependencies:    []string{},
			Comments:        []core.Comment{},
			CreatedAt:       now,
			UpdatedAt:       now,
			ReusableTaskIDs: []string{"7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "25c3806a-bd1b-424d-889b-29e5b06679b8"},
		},
		ReusableTasks: []core.ReusableTaskDefinition{
			{ID: "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", Name: "First", Title: "First title", Instructions: "line one\n- **line two**"},
			{ID: "25c3806a-bd1b-424d-889b-29e5b06679b8", Name: "Second", Title: "Second title", Instructions: "Second instructions"},
		},
	}

	var human bytes.Buffer
	if err := printValue(context{stdout: &human}, task); err != nil {
		t.Fatalf("human task output error = %v", err)
	}
	output := human.String()
	first := strings.Index(output, "- name: First")
	second := strings.Index(output, "- name: Second")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("reusable assignment order not rendered: %q", output)
	}
	for _, want := range []string{
		"reusableTasks:\n",
		"- name: First\n  title: First title\n  instructions:\n    line one\n    - **line two**\n",
		"- name: Second\n  title: Second title\n  instructions:\n    Second instructions\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("human task output missing %q: %q", want, output)
		}
	}

	var jsonOut bytes.Buffer
	if err := printValue(context{stdout: &jsonOut, jsonOut: true}, task); err != nil {
		t.Fatalf("JSON task output error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(jsonOut.Bytes(), &fields); err != nil {
		t.Fatalf("decode task JSON: %v", err)
	}
	for _, field := range []string{"reusableTaskIds", "reusableTasks"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("task JSON missing %q: %s", field, jsonOut.String())
		}
	}
	var decoded core.TaskView
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("decode task view: %v", err)
	}
	if !reflect.DeepEqual(decoded.ReusableTaskIDs, task.ReusableTaskIDs) || !reflect.DeepEqual(decoded.ReusableTasks, task.ReusableTasks) {
		t.Fatalf("decoded reusable view = %#v/%#v, want %#v/%#v", decoded.ReusableTaskIDs, decoded.ReusableTasks, task.ReusableTaskIDs, task.ReusableTasks)
	}
}

func TestTaskShowAndClaimsRenderTheSameReusableSection(t *testing.T) {
	task := core.TaskView{
		Task: core.Task{
			ID:           "25c3806a-bd1b-424d-889b-29e5b06679b8",
			ShortID:      "wtp-0028",
			Title:        "Claimable assignment",
			Status:       core.StatusInProgress,
			Dependencies: []string{},
			Comments:     []core.Comment{},
			CreatedAt:    time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
			UpdatedAt:    time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		},
		ReusableTasks: []core.ReusableTaskDefinition{{
			ID: "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", Name: "Checks", Title: "Run checks", Instructions: "first\nsecond",
		}},
	}
	p := &claimOutputTestProvider{task: task}
	for _, test := range []struct {
		name string
		run  func(context) error
	}{
		{name: "show", run: func(ctx context) error { return runTaskGet(ctx, []string{task.ShortID}, "show") }},
		{name: "start", run: func(ctx context) error {
			return runTaskTransition(ctx, "start", core.StatusInProgress, []string{task.ShortID})
		}},
		{name: "next", run: func(ctx context) error { return runTaskNext(ctx, nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := test.run(context{provider: p, stdout: &stdout, stderr: &stderr}); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			for _, want := range []string{"reusableTasks:", "- name: Checks", "  title: Run checks", "    first", "    second"} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("%s output missing %q: %q", test.name, want, stdout.String())
				}
			}
		})
	}
}

func TestTaskCreateAndUpdateReusableUsageMentionsRepeatableFlag(t *testing.T) {
	for _, usage := range []string{taskCreateUsage, taskUpdateUsage} {
		if !strings.Contains(usage, "--reusable NAME_OR_ID ...") {
			t.Fatalf("usage %q does not document repeatable reusable flag", usage)
		}
	}
}

func (p *claimOutputTestProvider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	return p.task, nil
}
