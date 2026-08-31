package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planning"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestRunPlanningReportFiltersInAnyOrderAndReturnsTypedJSON(t *testing.T) {
	first := planningViewFixture()
	first.Status = core.PlanningStatusToplan
	second := planningViewFixture()
	second.Status = core.PlanningStatusRejected
	second.ID = "33333333-3333-4333-8333-333333333333"
	second.ShortID = "wtp-0d6e4079-0002"
	second.Feature = "Other"
	p := &planningListShowTestProvider{items: []core.PlanningItemView{first, second}}
	var stdout, stderr bytes.Buffer
	err := runPlanningReport(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{
		"--feature", "sEaRcH", "--project=Apollo", "--status", "toplan", "--issue-id", " issue-1 ",
		"--feature-id", "feat-7", "--version", "V1", "--milestone=MVP",
	})
	if err != nil {
		t.Fatalf("runPlanningReport() error = %v", err)
	}
	wantStatus := core.PlanningStatusToplan
	wantFilter := provider.PlanningFilter{Status: &wantStatus, Grouping: core.GroupingFilter{
		IssueID: "issue-1", Project: "Apollo", Milestone: "MVP", Version: "V1", FeatureID: "feat-7", Feature: "sEaRcH",
	}}
	if !reflect.DeepEqual(p.lastFilter, wantFilter) {
		t.Fatalf("planning report filter = %#v, want %#v", p.lastFilter, wantFilter)
	}
	var got planning.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("planning report JSON = %v\n%s", err, stdout.String())
	}
	if got.TotalItems != 1 || len(got.Projects) != 1 || got.Projects[0].Value != "Apollo" || got.Projects[0].Versions[0].Milestones[0].Value != "MVP" {
		t.Fatalf("planning report = %#v", got)
	}
	if strings.Contains(stdout.String(), "chart") || strings.Contains(stdout.String(), "stats") || strings.Contains(stdout.String(), "attributes") {
		t.Fatalf("planning report JSON contains compatibility aliases: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("planning report wrote diagnostics on success: %s", stderr.String())
	}
}

func TestRunPlanningReportHumanTreeAndAlignedCounts(t *testing.T) {
	first := planningViewFixture()
	first.Status = core.PlanningStatusToplan
	second := planningViewFixture()
	second.Status = core.PlanningStatusResearched
	second.ID = "33333333-3333-4333-8333-333333333333"
	second.ShortID = "wtp-0d6e4079-0002"
	second.Project = ""
	second.Version = ""
	second.Milestone = ""
	p := &planningListShowTestProvider{items: []core.PlanningItemView{first, second}}
	var stdout bytes.Buffer
	if err := runPlanningReport(context{provider: p, stdout: &stdout, stderr: &bytes.Buffer{}}, nil); err != nil {
		t.Fatalf("runPlanningReport() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"planning report\n", "totalItems: 2", "statusCounts:",
		"toplan      researched   planned   rejected", "1           1            0         0",
		"projects:", "project: (unset)", "versions:", "version: (unset)", "milestones:", "milestone: (unset)",
		"project: Apollo", "version: v1", "milestone: MVP",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("planning report output missing %q:\n%s", want, output)
		}
	}
}

func TestRunPlanningReportReturnsEmptyTypedReport(t *testing.T) {
	p := &planningListShowTestProvider{}
	var stdout, stderr bytes.Buffer
	if err := runPlanningReport(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: true}, []string{"--project", "missing"}); err != nil {
		t.Fatalf("runPlanningReport() error = %v", err)
	}
	var got planning.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("empty planning report JSON = %v\n%s", err, stdout.String())
	}
	if got.Projects == nil || got.TotalItems != 0 || len(got.StatusCounts) != 4 {
		t.Fatalf("empty report = %#v, want zero report with four buckets", got)
	}
	if stdout.String() == "{}\n" || stderr.Len() != 0 {
		t.Fatalf("empty report output/diagnostics = %q / %q", stdout.String(), stderr.String())
	}
}

func TestRunPlanningReportRejectsMalformedArgumentsWithoutProviderOrStdout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "duplicate status", args: []string{"--status", "planned", "--status=rejected"}, want: "may only be specified once"},
		{name: "empty status", args: []string{"--status="}, want: "status cannot be empty"},
		{name: "unsupported status", args: []string{"--status", "inProgress"}, want: "invalid planning status"},
		{name: "empty grouping", args: []string{"--project", " \t"}, want: "cannot be empty"},
		{name: "duplicate grouping", args: []string{"--feature", "Search", "--feature=Other"}, want: "more than once"},
		{name: "extra positional", args: []string{"unexpected"}, want: planningReportUsage},
		{name: "unknown option", args: []string{"--agent", "Ada"}, want: "flag provided but not defined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &planningListShowTestProvider{}
			var stdout, stderr bytes.Buffer
			err := runPlanningReport(context{provider: p, stdout: &stdout, stderr: &stderr}, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runPlanningReport() error = %v, want containing %q", err, test.want)
			}
			if p.listCalls != 0 {
				t.Fatal("malformed planning report reached provider")
			}
			if stdout.Len() != 0 {
				t.Fatalf("malformed planning report wrote stdout: %q", stdout.String())
			}
		})
	}
}
