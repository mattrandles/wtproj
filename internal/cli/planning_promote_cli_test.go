package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/provider/flatfile"
)

type planningPromoteCLIProvider struct {
	provider.Provider
	previewResult provider.PlanningPromotionResult[core.PlanningItemView]
	publishResult provider.PlanningPromotionResult[core.TaskView]
	previewErr    error
	publishErr    error
	previewCalls  int
	publishCalls  int
	lastFilter    core.GroupingFilter
}

func (p *planningPromoteCLIProvider) PreviewPlanningPromotion(filter core.GroupingFilter) (provider.PlanningPromotionResult[core.PlanningItemView], error) {
	p.previewCalls++
	p.lastFilter = filter
	return p.previewResult, p.previewErr
}

func (p *planningPromoteCLIProvider) PromotePlanningItems(filter core.GroupingFilter) (provider.PlanningPromotionResult[core.TaskView], error) {
	p.publishCalls++
	p.lastFilter = filter
	return p.publishResult, p.publishErr
}

func planningPromotionCLIItem(shortID, title string) core.PlanningItemView {
	return core.PlanningItemView{PlanningItem: core.PlanningItem{
		ID: shortID + "-id", ShortID: shortID, Title: title, Status: core.PlanningStatusPlanned,
	}}
}

func planningPromotionCLITask(shortID, title string) core.TaskView {
	return core.TaskView{Task: core.Task{
		ID: shortID + "-id", ShortID: shortID, Title: title, Status: core.StatusTodo,
	}}
}

func TestRunPlanningPromoteParsesAllGroupingFiltersInAnyOrder(t *testing.T) {
	items := []core.PlanningItemView{
		planningPromotionCLIItem("wtp-0002", "First"),
		planningPromotionCLIItem("wtp-0001", "Second"),
	}
	p := &planningPromoteCLIProvider{previewResult: provider.PlanningPromotionResult[core.PlanningItemView]{DryRun: true, Count: 2, Items: items}}
	var stdout, stderr bytes.Buffer
	err := runPlanningPromote(context{provider: p, stdout: &stdout, stderr: &stderr}, []string{
		"--feature", "Search", "--dry-run", "--project=Apollo", "--issue-id", "ISSUE-1",
		"--feature-id=FEAT-7", "--version", "v1", "--milestone=MVP",
	})
	if err != nil {
		t.Fatalf("runPlanningPromote() error = %v", err)
	}
	wantFilter := core.GroupingFilter{IssueID: "ISSUE-1", Project: "Apollo", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-7", Feature: "Search"}
	if !reflect.DeepEqual(p.lastFilter, wantFilter) || p.previewCalls != 1 || p.publishCalls != 0 {
		t.Fatalf("promotion call = filter %#v, preview %d, publish %d; want %#v, 1, 0", p.lastFilter, p.previewCalls, p.publishCalls, wantFilter)
	}
	want := "would-promote: 2\nwtp-0002\tFirst\tplanned\nwtp-0001\tSecond\tplanned\n"
	if stdout.String() != want {
		t.Fatalf("dry-run text = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("dry-run stderr = %q, want empty", stderr.String())
	}
}

func TestRunPlanningPromoteJSONUsesStableEnvelopeAndPublishesTodoViews(t *testing.T) {
	p := &planningPromoteCLIProvider{publishResult: provider.PlanningPromotionResult[core.TaskView]{
		DryRun: false, Count: 1, Items: []core.TaskView{planningPromotionCLITask("wtp-0001", "Promoted")},
	}}
	var stdout bytes.Buffer
	if err := runPlanningPromote(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}, jsonOut: true}, []string{"--project", "Apollo"}); err != nil {
		t.Fatalf("runPlanningPromote() error = %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("promotion JSON = %v\n%s", err, stdout.String())
	}
	if got := len(envelope); got != 3 {
		t.Fatalf("promotion JSON keys = %d, want 3", got)
	}
	if string(envelope["dryRun"]) != "false" || string(envelope["count"]) != "1" {
		t.Fatalf("promotion JSON envelope = %s", stdout.String())
	}
	var items []core.TaskView
	if err := json.Unmarshal(envelope["items"], &items); err != nil || len(items) != 1 || items[0].Status != core.StatusTodo {
		t.Fatalf("promotion JSON items = %#v, error %v", items, err)
	}
	if p.publishCalls != 1 || p.previewCalls != 0 {
		t.Fatalf("promotion calls = preview %d, publish %d", p.previewCalls, p.publishCalls)
	}
}

func TestRunPlanningPromoteRejectsMalformedArgumentsBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing selector", args: []string{"--dry-run"}, want: "requires at least one grouping selector"},
		{name: "duplicate dry-run", args: []string{"--dry-run", "--dry-run", "--project", "Apollo"}, want: "may only be specified once"},
		{name: "duplicate grouping", args: []string{"--project", "Apollo", "--project=Zeus"}, want: "more than once"},
		{name: "blank grouping", args: []string{"--feature", " \t"}, want: "cannot be empty"},
		{name: "status flag", args: []string{"--status", "planned", "--project", "Apollo"}, want: "flag provided but not defined"},
		{name: "task ID", args: []string{"wtp-0001", "--project", "Apollo"}, want: planningPromoteUsage},
		{name: "extra positional", args: []string{"--project", "Apollo", "extra"}, want: planningPromoteUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &planningPromoteCLIProvider{}
			var stdout, stderr bytes.Buffer
			err := runPlanningPromote(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runPlanningPromote(%v) error = %v, want %q", test.args, err, test.want)
			}
			if p.previewCalls != 0 || p.publishCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("malformed promotion reached provider or stdout: calls %d/%d, stdout %q", p.previewCalls, p.publishCalls, stdout.String())
			}
		})
	}
}

func TestRunPlanningPromoteSurfacesProviderErrorsWithoutPartialOutput(t *testing.T) {
	p := &planningPromoteCLIProvider{previewErr: errors.New("no planned planning items match promotion filters")}
	var stdout bytes.Buffer
	err := runPlanningPromote(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, []string{"--project", "Apollo", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "no planned planning items") {
		t.Fatalf("promotion error = %v, want no-match error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed promotion wrote partial stdout: %q", stdout.String())
	}
}

func TestRunPlanningPromoteDryRunDoesNotInitializeAbsentStore(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	var stdout, stderr bytes.Buffer
	err := Run([]string{"--json", "planning", "promote", "--project", "Apollo", "--dry-run"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no planned planning items") {
		t.Fatalf("Run() error = %v, want no-match error", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".wtp")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dry-run initialized absent store: %v", statErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed dry-run wrote stdout: %q", stdout.String())
	}
}

func TestRunPlanningPromoteFlatfileEndToEnd(t *testing.T) {
	p, err := flatfile.New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "Promote me", Status: core.PlanningStatusPlanned, Project: "Apollo"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runPlanningPromote(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, []string{"--project", "Apollo"}); err != nil {
		t.Fatalf("runPlanningPromote() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "promoted: 1") || !strings.Contains(stdout.String(), item.ShortID+"\ttodo") {
		t.Fatalf("promotion text = %q", stdout.String())
	}
	if _, err := p.GetPlanningItem(item.ID); err == nil {
		t.Fatal("promoted item still resolves through planning show")
	}
	if _, err := p.GetTask(item.ID, ""); err != nil {
		t.Fatalf("promoted item does not resolve through task show: %v", err)
	}
}
