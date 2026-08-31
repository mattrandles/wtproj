package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

type planningEditorCLIProvider struct {
	provider.Provider
	updatedID    string
	updatedInput core.UpdatePlanningItemInput
	statusID     string
	status       core.PlanningStatus
	updateCalls  int
	statusCalls  int
	view         core.PlanningItemView
}

func (p *planningEditorCLIProvider) UpdatePlanningItem(id string, input core.UpdatePlanningItemInput) (core.PlanningItemView, error) {
	p.updateCalls++
	p.updatedID = id
	p.updatedInput = input
	return p.view, nil
}

func (p *planningEditorCLIProvider) SetPlanningStatus(id string, status core.PlanningStatus) (core.PlanningItemView, error) {
	p.statusCalls++
	p.statusID = id
	p.status = status
	return p.view, nil
}

func planningEditorCLIView() core.PlanningItemView {
	return core.PlanningItemView{PlanningItem: core.PlanningItem{
		ID: "11111111-1111-4111-8111-111111111111", ShortID: "wtp-0d6e4079-0001",
		Title: "Updated", Status: core.PlanningStatusResearched,
	}}
}

func TestRunPlanningUpdatePassesFullMutableSurfaceAndPreservesOrder(t *testing.T) {
	p := &planningEditorCLIProvider{view: planningEditorCLIView()}
	var stdout bytes.Buffer
	err := runPlanningUpdate(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, []string{
		"wtp-0d6e4079-0001", "--title", "new title", "--description", "details",
		"--priority", "high", "--estimate", "l", "--lane", "planning", "--model", "model",
		"--issue-id", "ISSUE-1", "--project", "Apollo", "--milestone", "MVP", "--version", "v2",
		"--feature-id", "FEAT-1", "--feature", "Search", "--git-repo", "/repo", "--git-branch", "main",
		"--worktree-name", "wtproj", "--worktree-dir", "/repo/worktree", "--agent", "Ada",
		"--depends-on", "dep-a, dep-b", "--depends-on", "dep-c", "--reusable", "Second", "--reusable=First",
	})
	if err != nil {
		t.Fatalf("runPlanningUpdate() error = %v", err)
	}
	input := p.updatedInput
	if p.updatedID != "wtp-0d6e4079-0001" || p.updateCalls != 1 {
		t.Fatalf("update call = %q (%d), want one call with the planning ID", p.updatedID, p.updateCalls)
	}
	if input.Title.Value != "new title" || input.Description.Value != "details" || input.Priority.Value != core.PriorityHigh || input.Estimate.Value != core.EstimateL ||
		input.Lane.Value != "planning" || input.Model.Value != "model" || input.Assignee.Value != "Ada" ||
		input.GitRepo.Value != "/repo" || input.WorktreeDir.Value != "/repo/worktree" {
		t.Fatalf("basic update input = %#v", input)
	}
	if !input.Title.Set || !input.Description.Set || !input.Priority.Set || !input.Estimate.Set || !input.Assignee.Set || !input.Dependencies.Set || !input.ReusableTasks.Set {
		t.Fatalf("mutable fields were not marked supplied: %#v", input)
	}
	if !reflect.DeepEqual(input.Dependencies.Value, []string{"dep-a", "dep-b", "dep-c"}) || !reflect.DeepEqual(input.ReusableTasks.Value, []string{"Second", "First"}) {
		t.Fatalf("ordered selectors = %#v / %#v", input.Dependencies.Value, input.ReusableTasks.Value)
	}
	if stdout.String() != "wtp-0d6e4079-0001\tUpdated\tresearched\n" {
		t.Fatalf("human result = %q", stdout.String())
	}
}

func TestRunPlanningUpdateSupportsExplicitClearingAndRequiresChange(t *testing.T) {
	p := &planningEditorCLIProvider{view: planningEditorCLIView()}
	if err := runPlanningUpdate(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{
		p.view.ShortID, "--description=", "--priority=", "--estimate=", "--lane=", "--depends-on=", "--reusable=",
	}); err != nil {
		t.Fatalf("clear update error = %v", err)
	}
	input := p.updatedInput
	if !input.Description.Set || input.Description.Value != "" || !input.Priority.Set || input.Priority.Value != "" || !input.Estimate.Set || input.Estimate.Value != "" ||
		!input.Dependencies.Set || input.Dependencies.Value == nil || len(input.Dependencies.Value) != 0 || !input.ReusableTasks.Set || input.ReusableTasks.Value == nil || len(input.ReusableTasks.Value) != 0 {
		t.Fatalf("clear input = %#v", input)
	}

	for _, args := range [][]string{{p.view.ShortID}, {p.view.ShortID, "--status", "planned"}, {p.view.ShortID, "--title", "one", "--title", "two"}, {p.view.ShortID, "--title", " "}} {
		p.updateCalls = 0
		err := runPlanningUpdate(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, args)
		if err == nil {
			t.Errorf("runPlanningUpdate(%v) succeeded, want validation error", args)
		}
		if p.updateCalls != 0 {
			t.Errorf("runPlanningUpdate(%v) reached provider", args)
		}
	}
	if err := runPlanningUpdate(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{p.view.ShortID, "--reusable", "First", "--reusable", "first"}); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate reusable error = %v", err)
	}
}

func TestRunPlanningSetStatusIsExactAndHasConsistentOutput(t *testing.T) {
	for _, jsonOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonOut], func(t *testing.T) {
			p := &planningEditorCLIProvider{view: planningEditorCLIView()}
			var stdout bytes.Buffer
			err := runPlanningSetStatus(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: jsonOut}, []string{p.view.ShortID, "planned"})
			if err != nil {
				t.Fatalf("runPlanningSetStatus() error = %v", err)
			}
			if p.statusCalls != 1 || p.statusID != p.view.ShortID || p.status != core.PlanningStatusPlanned {
				t.Fatalf("status call = %q %q (%d), want planned once", p.statusID, p.status, p.statusCalls)
			}
			if jsonOut {
				var got core.PlanningItemView
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Status != core.PlanningStatusResearched {
					t.Fatalf("JSON result = %q, error %v", stdout.String(), err)
				}
			} else if stdout.String() != "wtp-0d6e4079-0001\tUpdated\tresearched\n" {
				t.Fatalf("text result = %q", stdout.String())
			}
		})
	}

	for _, args := range [][]string{{}, {"id"}, {"id", "planned", "extra"}, {"id", " planned "}, {"id", "todo"}, {"id", "planned", "--agent", "Ada"}, {"--agent", "Ada", "id", "planned"}} {
		p := &planningEditorCLIProvider{view: planningEditorCLIView()}
		err := runPlanningSetStatus(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, args)
		if err == nil {
			t.Errorf("runPlanningSetStatus(%v) succeeded, want error", args)
		}
		if p.statusCalls != 0 {
			t.Errorf("runPlanningSetStatus(%v) reached provider", args)
		}
	}
}
