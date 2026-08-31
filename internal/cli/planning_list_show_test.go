package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

type planningListShowTestProvider struct {
	provider.Provider
	items      []core.PlanningItemView
	lastFilter provider.PlanningFilter
	listCalls  int
	showCalls  int
	showByID   map[string]core.PlanningItemView
	showID     string
}

func (p *planningListShowTestProvider) ListPlanningItems(filter provider.PlanningFilter) ([]core.PlanningItemView, error) {
	p.listCalls++
	p.lastFilter = filter
	items := make([]core.PlanningItemView, 0, len(p.items))
	for _, item := range p.items {
		if filter.Status != nil && item.Status != *filter.Status {
			continue
		}
		if !core.MatchesGroupingFilter(core.Task{
			IssueID: item.IssueID, Project: item.Project, Milestone: item.Milestone,
			Version: item.Version, FeatureID: item.FeatureID, Feature: item.Feature,
		}, filter.Grouping) {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *planningListShowTestProvider) GetPlanningItem(id string) (core.PlanningItemView, error) {
	p.showCalls++
	p.showID = id
	item, ok := p.showByID[id]
	if !ok {
		return core.PlanningItemView{}, &planningTestError{message: "planning item not found"}
	}
	return item, nil
}

type planningTestError struct{ message string }

func (e *planningTestError) Error() string { return e.message }

func planningViewFixture() core.PlanningItemView {
	created := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	return core.PlanningItemView{PlanningItem: core.PlanningItem{
		ID: "11111111-1111-4111-8111-111111111111", ShortID: "wtp-0d6e4079-0001",
		Title: "Planning item", Description: "first line\nsecond line", Status: core.PlanningStatusPlanned,
		Priority: core.PriorityHigh, Estimate: core.EstimateM, Lane: "planning-cli", Model: "model",
		IssueID: "ISSUE-1", Project: "Apollo", Milestone: "MVP", Version: "v1",
		FeatureID: "FEAT-7", Feature: "Search", GitRepo: "/repo", GitBranch: "main",
		WorktreeName: "wtproj", WorktreeDir: "/repo/worktree", Assignee: "Ada",
		Dependencies: []string{"22222222-2222-4222-8222-222222222222"},
		Comments:     []core.Comment{{ID: "comment-1", Author: "reviewer", Message: "Retained comment", CreatedAt: created}},
		CreatedAt:    created, UpdatedAt: created.Add(time.Hour),
	}, ReusableTasks: []core.ReusableTaskDefinition{
		{Name: "First", Title: "First title", Instructions: "first instructions"},
		{Name: "Second", Title: "Second title", Instructions: "second instructions"},
	}}
}

func TestRunPlanningListFiltersInAnyOrderAndReturnsStableJSONArray(t *testing.T) {
	first := planningViewFixture()
	first.ShortID = "wtp-0d6e4079-0002"
	first.ID = "33333333-3333-4333-8333-333333333333"
	first.Status = core.PlanningStatusToplan
	second := planningViewFixture()
	second.ShortID = "wtp-0d6e4079-0003"
	second.ID = "44444444-4444-4444-8444-444444444444"
	second.Feature = "Other"
	p := &planningListShowTestProvider{items: []core.PlanningItemView{first, second}}
	var stdout, stderr bytes.Buffer
	args := []string{
		"--feature", "sEaRcH", "--status", "toplan", "--project=Apollo", "--issue-id", " issue-1 ",
		"--feature-id", "feat-7", "--version", "V1", "--milestone=MVP",
	}
	if err := runPlanningList(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, args); err != nil {
		t.Fatalf("runPlanningList() error = %v", err)
	}
	wantFilter := provider.PlanningFilter{Grouping: core.GroupingFilter{
		IssueID: "issue-1", Project: "Apollo", Milestone: "MVP", Version: "V1", FeatureID: "feat-7", Feature: "sEaRcH",
	}}
	wantStatus := core.PlanningStatusToplan
	wantFilter.Status = &wantStatus
	if !reflect.DeepEqual(p.lastFilter, wantFilter) {
		t.Fatalf("planning list filter = %#v, want %#v", p.lastFilter, wantFilter)
	}
	var got []core.PlanningItemView
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("planning list JSON = %v\n%s", err, stdout.String())
	}
	if len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("planning list JSON = %#v, want first item only", got)
	}
	if strings.Contains(stdout.String(), "readiness") {
		t.Fatalf("planning list leaked execution view fields: %s", stdout.String())
	}
}

func TestRunPlanningListEmptyAndUnsetFilters(t *testing.T) {
	p := &planningListShowTestProvider{items: []core.PlanningItemView{planningViewFixture()}}
	var stdout, stderr bytes.Buffer
	if err := runPlanningList(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{"--project", "missing"}); err != nil {
		t.Fatalf("runPlanningList() error = %v", err)
	}
	var got []core.PlanningItemView
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("empty planning list JSON = %v\n%s", err, stdout.String())
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("empty planning list = %#v, want non-nil empty array", got)
	}

	stdout.Reset()
	if err := runPlanningList(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{"--project", "apollo", "--feature", "search"}); err != nil {
		t.Fatalf("runPlanningList() mixed-case filter error = %v", err)
	}
	if got, want := stdout.String(), "wtp-0d6e4079-0001\tPlanning item\tplanned\n"; got != want {
		t.Fatalf("compact planning list = %q, want %q", got, want)
	}

	stdout.Reset()
	if err := runPlanningList(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{"--project", "Apollo", "--feature", "missing"}); err != nil {
		t.Fatalf("runPlanningList() unset/mismatch error = %v", err)
	}
	if stdout.String() != "[]\n" {
		t.Fatalf("filtered planning list JSON = %q, want empty array", stdout.String())
	}
}

func TestRunPlanningListRejectsMalformedArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "duplicate status", args: []string{"--status", "planned", "--status=rejected"}, want: "may only be specified once"},
		{name: "empty status", args: []string{"--status="}, want: "status cannot be empty"},
		{name: "empty grouping", args: []string{"--project", " \t"}, want: "cannot be empty"},
		{name: "duplicate grouping", args: []string{"--feature", "Search", "--feature=Other"}, want: "more than once"},
		{name: "extra positional", args: []string{"planned"}, want: planningListUsage},
		{name: "unknown option", args: []string{"--agent", "Ada"}, want: "flag provided but not defined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &planningListShowTestProvider{}
			err := runPlanningList(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runPlanningList() error = %v, want containing %q", err, test.want)
			}
			if p.listCalls != 0 {
				t.Fatal("malformed planning list reached provider")
			}
		})
	}
}

func TestRunPlanningShowAcceptsUUIDAndScopedShortIDAndRendersDeterministically(t *testing.T) {
	item := planningViewFixture()
	p := &planningListShowTestProvider{showByID: map[string]core.PlanningItemView{
		item.ID: item, item.ShortID: item,
	}}
	var first, second bytes.Buffer
	ctx := context{provider: p, stderr: &bytes.Buffer{}}
	if err := runPlanningShow(contextWithOutput(ctx, &first), []string{item.ShortID}); err != nil {
		t.Fatalf("runPlanningShow(short ID) error = %v", err)
	}
	if err := runPlanningShow(contextWithOutput(ctx, &second), []string{item.ID}); err != nil {
		t.Fatalf("runPlanningShow(UUID) error = %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("planning show rendering differs by identifier:\nshort ID:\n%s\nUUID:\n%s", first.String(), second.String())
	}
	output := first.String()
	for _, want := range []string{
		"wtp-0d6e4079-0001 (11111111-1111-4111-8111-111111111111)",
		"title: Planning item", "status: planned", "priority: high", "estimate: m", "lane: planning-cli",
		"description: first line\nsecond line", "issueId: ISSUE-1", "project: Apollo", "milestone: MVP",
		"version: v1", "featureId: FEAT-7", "feature: Search", "gitRepo: /repo", "gitBranch: main",
		"worktreeName: wtproj", "worktreeDir: /repo/worktree", "assignee: Ada",
		"dependencies: 22222222-2222-4222-8222-222222222222", "- name: First", "- name: Second",
		"- reviewer [2026-08-31T12:00:00Z] Retained comment",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("planning show output missing %q:\n%s", want, output)
		}
	}
	if strings.Index(output, "- name: First") > strings.Index(output, "- name: Second") {
		t.Fatal("planning show changed reusable assignment order")
	}
}

func contextWithOutput(ctx context, stdout *bytes.Buffer) context {
	ctx.stdout = stdout
	return ctx
}

func TestRunPlanningShowReturnsStableJSONAndRejectsOptions(t *testing.T) {
	item := planningViewFixture()
	p := &planningListShowTestProvider{showByID: map[string]core.PlanningItemView{item.ID: item}}
	var stdout, stderr bytes.Buffer
	if err := runPlanningShow(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{item.ID}); err != nil {
		t.Fatalf("runPlanningShow(JSON) error = %v", err)
	}
	var got core.PlanningItemView
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("planning show JSON = %v\n%s", err, stdout.String())
	}
	if got.ID != item.ID || len(got.ReusableTasks) != 2 || got.ReusableTasks[0].Name != "First" || got.ReusableTasks[1].Name != "Second" {
		t.Fatalf("planning show JSON = %#v", got)
	}
	if strings.Contains(stdout.String(), "readiness") || strings.Contains(stdout.String(), "handoffs") {
		t.Fatalf("planning show JSON leaked execution fields: %s", stdout.String())
	}

	for _, args := range [][]string{{}, {item.ID, "extra"}, {"--agent", "Ada", item.ID}, {item.ID, "--agent", "Ada"}, {"--bad"}} {
		p.showCalls = 0
		err := runPlanningShow(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, args)
		if err == nil || !strings.Contains(err.Error(), planningShowUsage) {
			t.Errorf("runPlanningShow(%v) error = %v, want usage", args, err)
		}
		if p.showCalls != 0 {
			t.Errorf("malformed show %v reached provider", args)
		}
	}
}
