package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/runtimecontext"
)

type planningCreateTestProvider struct {
	provider.Provider
	got  core.CreatePlanningItemInput
	view core.PlanningItemView
}

func (p *planningCreateTestProvider) CreatePlanningItem(input core.CreatePlanningItemInput) (core.PlanningItemView, error) {
	p.got = input
	return p.view, nil
}

func TestRunPlanningCreatePassesFullMetadataAndDiscoversOrigin(t *testing.T) {
	created := core.PlanningItemView{PlanningItem: core.PlanningItem{ID: "25c3806a-bd1b-424d-889b-29e5b06679b8", ShortID: "wtp-0001", Title: "created", Status: core.PlanningStatusToplan}}
	p := &planningCreateTestProvider{view: created}
	var stdout, stderr bytes.Buffer
	err := runPlanningCreate(context{
		provider: p,
		invocation: runtimecontext.Context{
			RepositoryRoot: "/discovered/repository",
			Branch:         "discovered-branch",
			WorktreeName:   "discovered-worktree",
			WorktreeRoot:   "/discovered/worktree",
		},
		stdout: &stdout,
		stderr: &stderr,
	}, []string{
		"--title", "  title  ", "--description", " description ", "--status", "rejected",
		"--priority", "high", "--estimate", "m", "--lane", " lane ", "--model", " model ",
		"--issue-id", "ISSUE-1", "--project", "Project", "--milestone", "MVP", "--version", "v1",
		"--feature-id", "FEAT-1", "--feature", "Feature", "--agent", "Alice",
		"--depends-on", "first, second", "--depends-on", "third",
		"--reusable", "First", "--reusable=Second",
	})
	if err != nil {
		t.Fatalf("runPlanningCreate() error = %v", err)
	}
	if p.got.Title != "  title  " || p.got.Description != " description " || p.got.Status != core.PlanningStatusRejected || p.got.Priority != core.PriorityHigh || p.got.Estimate != core.EstimateM {
		t.Fatalf("basic create input = %#v", p.got)
	}
	if p.got.Lane != " lane " || p.got.Model != " model " || p.got.Assignee != "Alice" {
		t.Fatalf("text create input = %#v", p.got)
	}
	if p.got.GitRepo != "/discovered/repository" || p.got.GitBranch != "discovered-branch" || p.got.WorktreeName != "discovered-worktree" || p.got.WorktreeDir != "/discovered/worktree" {
		t.Fatalf("discovered origin = %#v", p.got)
	}
	if !reflect.DeepEqual(p.got.Dependencies, []string{"first", "second", "third"}) || !reflect.DeepEqual(p.got.ReusableTasks, []string{"First", "Second"}) {
		t.Fatalf("repeatable selectors = %#v / %#v", p.got.Dependencies, p.got.ReusableTasks)
	}
	if stdout.Len() == 0 {
		t.Fatal("planning create did not print the typed view")
	}
}

func TestRunPlanningCreateExplicitEmptyOriginSuppressesDiscovery(t *testing.T) {
	p := &planningCreateTestProvider{}
	err := runPlanningCreate(context{
		provider: p,
		invocation: runtimecontext.Context{
			RepositoryRoot: "/discovered/repository", Branch: "branch",
			WorktreeName: "worktree", WorktreeRoot: "/discovered/worktree",
		},
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
	}, []string{
		"--title", "title", "--git-repo=", "--git-branch=", "--worktree-name=", "--worktree-dir=",
	})
	if err != nil {
		t.Fatalf("runPlanningCreate() error = %v", err)
	}
	if p.got.GitRepo != "" || p.got.GitBranch != "" || p.got.WorktreeName != "" || p.got.WorktreeDir != "" {
		t.Fatalf("explicit empty origin did not suppress discovery: %#v", p.got)
	}
}

func TestRunPlanningCreateAcceptsFlagsInAnyOrder(t *testing.T) {
	p := &planningCreateTestProvider{}
	err := runPlanningCreate(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{
		"--feature", "Search", "--title", "ordered", "--agent", "Ada", "--status", "planned",
		"--project", "Apollo", "--depends-on", "first", "--description", "details", "--priority", "high",
		"--reusable", "Review", "--estimate", "m", "--feature-id", "FEAT-7", "--lane", "cli",
	})
	if err != nil {
		t.Fatalf("runPlanningCreate() error = %v", err)
	}
	if p.got.Title != "ordered" || p.got.Status != core.PlanningStatusPlanned || p.got.Project != "Apollo" || p.got.FeatureID != "FEAT-7" {
		t.Fatalf("parser-order input = %#v", p.got)
	}
}

func TestRunPlanningCreateAcceptsEveryPlanningStatusAndDefaults(t *testing.T) {
	for _, want := range []core.PlanningStatus{
		core.PlanningStatusToplan,
		core.PlanningStatusResearched,
		core.PlanningStatusPlanned,
		core.PlanningStatusRejected,
	} {
		t.Run(string(want), func(t *testing.T) {
			p := &planningCreateTestProvider{}
			args := []string{"--title", "status"}
			if want != core.PlanningStatusToplan {
				args = append(args, "--status", string(want))
			}
			if err := runPlanningCreate(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, args); err != nil {
				t.Fatalf("runPlanningCreate() error = %v", err)
			}
			if p.got.Status != want {
				t.Fatalf("status = %q, want %q", p.got.Status, want)
			}
		})
	}
}

func TestRunPlanningCreateRejectsInvalidCombinationsBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "blank title", args: []string{"--title", " \t"}, want: "--title is required"},
		{name: "explicit blank status", args: []string{"--title", "title", "--status="}, want: "invalid planning status"},
		{name: "execution status", args: []string{"--title", "title", "--status", "inProgress"}, want: "invalid planning status"},
		{name: "padded status", args: []string{"--title", "title", "--status", " planned "}, want: "invalid planning status"},
		{name: "blank grouping", args: []string{"--title", "title", "--project", " \t"}, want: "project cannot be blank"},
		{name: "extra positional", args: []string{"--title", "title", "unexpected"}, want: "usage: wtp planning create"},
		{name: "unknown flag", args: []string{"--title", "title", "--assignee", "Ada"}, want: "flag provided but not defined"},
		{name: "duplicate singleton", args: []string{"--title", "first", "--title", "second"}, want: "may only be specified once"},
		{name: "empty dependency occurrence", args: []string{"--title", "title", "--depends-on="}, want: "--depends-on occurrence 1 cannot be empty"},
		{name: "empty reusable occurrence", args: []string{"--title", "title", "--reusable="}, want: "--reusable cannot be empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &planningCreateTestProvider{}
			err := runPlanningCreate(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if p.got.Title != "" {
				t.Fatal("invalid planning create reached provider")
			}
		})
	}
}

func TestRunPlanningCreateHumanAndJSONOutput(t *testing.T) {
	view := core.PlanningItemView{PlanningItem: core.PlanningItem{
		ID: "25c3806a-bd1b-424d-889b-29e5b06679b8", ShortID: "wtp-0001", Title: "Plan 世界 ✓", Status: core.PlanningStatusPlanned,
	}}
	for _, test := range []struct {
		name    string
		jsonOut bool
		want    string
	}{
		{name: "human", want: "wtp-0001\tPlan 世界 ✓\tplanned\n"},
		{name: "json", jsonOut: true, want: "\"shortId\": \"wtp-0001\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &planningCreateTestProvider{view: view}
			var stdout bytes.Buffer
			if err := runPlanningCreate(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: test.jsonOut}, []string{"--title", "Plan 世界 ✓"}); err != nil {
				t.Fatalf("runPlanningCreate() error = %v", err)
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("output = %q, want containing %q", stdout.String(), test.want)
			}
			if test.jsonOut {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
					t.Fatalf("JSON error = %v", err)
				}
				if _, wrapped := fields["planningItem"]; wrapped {
					t.Fatal("JSON unexpectedly wrapped planning item")
				}
				if _, readiness := fields["readiness"]; readiness {
					t.Fatal("planning JSON unexpectedly contains execution readiness")
				}
			}
		})
	}
}

func TestRunPlanningCreateRootJSONDefaultsAndUnicode(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--json", "planning", "create", "--title", "計画 世界 ✓", "--feature", "探索"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run() error = %v\nstderr: %s", err, stderr.String())
	}
	var item core.PlanningItemView
	if err := json.Unmarshal(stdout.Bytes(), &item); err != nil {
		t.Fatalf("planning JSON error = %v\n%s", err, stdout.String())
	}
	if item.Title != "計画 世界 ✓" || item.Feature != "探索" || item.Status != core.PlanningStatusToplan {
		t.Fatalf("planning item = %#v", item)
	}
	if item.ShortID == "" || item.ID == "" {
		t.Fatalf("planning item lacks stable identity: %#v", item)
	}
	if strings.Contains(stdout.String(), "readiness") {
		t.Fatalf("root planning JSON contains execution field: %s", stdout.String())
	}
}

func TestPlanningCreateIsolatedFromLegacyGlobalFlags(t *testing.T) {
	args := []string{"planning", "create", "--title", "title", "--create-task"}
	got, err := rewriteLegacyArgs(args)
	if err != nil {
		t.Fatalf("rewriteLegacyArgs() error = %v", err)
	}
	if !reflect.DeepEqual(got.args, args) || got.found {
		t.Fatalf("rewriteLegacyArgs() = %#v, want unchanged modern command", got)
	}

	dir := t.TempDir()
	chdir(t, dir)
	var stdout, stderr bytes.Buffer
	err = Run(args, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("Run() error = %v, want planning parser error", err)
	}
}
