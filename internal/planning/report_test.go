package planning_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planning"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestAggregatePlanningReportTableDriven(t *testing.T) {
	researched := core.PlanningStatusResearched
	allItems := []core.PlanningItemView{
		planningItem(core.PlanningStatusToplan, "Apollo", "v1", "MVP", "ISSUE-1", "FEAT-1", "Search"),
		planningItem(core.PlanningStatusResearched, "apollo", "v1", "MVP", "issue-1", "feat-1", "search"),
		planningItem(core.PlanningStatusPlanned, "Zeus", "v2", "", "ISSUE-2", "FEAT-2", "Browse"),
	}
	allGrouping := core.GroupingFilter{
		IssueID: " issue-1 ", Project: " APOLLO ", Milestone: "mvp", Version: "V1",
		FeatureID: "FEAT-1", Feature: "SEARCH",
	}

	tests := []struct {
		name       string
		items      []core.PlanningItemView
		options    planning.Options
		want       planning.Report
		wantFilter provider.PlanningFilter
	}{
		{
			name:  "empty data",
			items: nil,
			want:  report(),
			wantFilter: provider.PlanningFilter{
				Grouping: core.GroupingFilter{},
			},
		},
		{
			name: "partial metadata",
			items: []core.PlanningItemView{
				planningItem(core.PlanningStatusToplan, "Apollo", "", "", "", "", ""),
				planningItem(core.PlanningStatusPlanned, "", "v2", "M2", "", "", ""),
			},
			want: report(
				item(core.PlanningStatusToplan, "Apollo", "", ""),
				item(core.PlanningStatusPlanned, "", "v2", "M2"),
			),
		},
		{
			name:    "combined filters",
			items:   allItems,
			options: planning.Options{Status: &researched, Grouping: allGrouping},
			want: report(
				item(core.PlanningStatusResearched, "apollo", "v1", "MVP"),
			),
			wantFilter: provider.PlanningFilter{Status: &researched, Grouping: core.GroupingFilter{
				IssueID: "issue-1", Project: "APOLLO", Milestone: "mvp", Version: "V1",
				FeatureID: "FEAT-1", Feature: "SEARCH",
			}},
		},
		{
			name: "mixed casing remains distinct",
			items: []core.PlanningItemView{
				planningItem(core.PlanningStatusToplan, "Apollo", "v1", "MVP", "", "", ""),
				planningItem(core.PlanningStatusToplan, "apollo", "v1", "MVP", "", "", ""),
			},
			want: report(
				item(core.PlanningStatusToplan, "Apollo", "v1", "MVP"),
				item(core.PlanningStatusToplan, "apollo", "v1", "MVP"),
			),
		},
		{
			name: "zero counts are retained",
			items: []core.PlanningItemView{
				planningItem(core.PlanningStatusRejected, "Apollo", "v1", "MVP", "", "", ""),
			},
			want: report(item(core.PlanningStatusRejected, "Apollo", "v1", "MVP")),
		},
		{
			name: "nested order is deterministic",
			items: []core.PlanningItemView{
				planningItem(core.PlanningStatusToplan, "zeta", "v2", "b", "", "", ""),
				planningItem(core.PlanningStatusToplan, "Alpha", "v1", "A", "", "", ""),
				planningItem(core.PlanningStatusToplan, "", "", "", "", "", ""),
				planningItem(core.PlanningStatusToplan, "Alpha", "", "", "", "", ""),
				planningItem(core.PlanningStatusToplan, "Alpha", "v1", "", "", "", ""),
			},
			want: report(
				item(core.PlanningStatusToplan, "", "", ""),
				item(core.PlanningStatusToplan, "Alpha", "", ""),
				item(core.PlanningStatusToplan, "Alpha", "v1", ""),
				item(core.PlanningStatusToplan, "Alpha", "v1", "A"),
				item(core.PlanningStatusToplan, "zeta", "v2", "b"),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &reportReader{items: test.items}
			got, err := planning.Aggregate(reader, test.options)
			if err != nil {
				t.Fatalf("Aggregate() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Aggregate() = %#v, want %#v", got, test.want)
			}
			if !reflect.DeepEqual(reader.filter, test.wantFilter) {
				t.Fatalf("reader filter = %#v, want %#v", reader.filter, test.wantFilter)
			}
			if reader.getCalls != 0 {
				t.Fatalf("GetPlanningItem calls = %d, want 0", reader.getCalls)
			}
		})
	}
}

func TestAggregatePlanningReportReturnsReaderErrors(t *testing.T) {
	wantErr := errors.New("planning records unavailable")
	_, err := planning.Aggregate(&reportReader{err: wantErr}, planning.Options{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Aggregate() error = %v, want %v", err, wantErr)
	}
}

func TestAggregatePlanningReportRejectsInvalidStatus(t *testing.T) {
	invalid := core.PlanningStatus("approved")
	reader := &reportReader{}
	if _, err := planning.Aggregate(reader, planning.Options{Status: &invalid}); err == nil {
		t.Fatal("Aggregate() accepted invalid planning status")
	}
	if reader.listCalls != 0 {
		t.Fatal("invalid status reached the planning reader")
	}
}

type reportReader struct {
	items     []core.PlanningItemView
	err       error
	filter    provider.PlanningFilter
	listCalls int
	getCalls  int
}

func (r *reportReader) ListPlanningItems(filter provider.PlanningFilter) ([]core.PlanningItemView, error) {
	r.listCalls++
	r.filter = filter
	if r.err != nil {
		return nil, r.err
	}
	return r.items, nil
}

func (r *reportReader) GetPlanningItem(string) (core.PlanningItemView, error) {
	r.getCalls++
	return core.PlanningItemView{}, errors.New("unexpected GetPlanningItem call")
}

func planningItem(status core.PlanningStatus, project, version, milestone, issueID, featureID, feature string) core.PlanningItemView {
	return core.PlanningItemView{PlanningItem: core.PlanningItem{
		Status: status, Project: project, Version: version, Milestone: milestone,
		IssueID: issueID, FeatureID: featureID, Feature: feature,
	}}
}

func item(status core.PlanningStatus, project, version, milestone string) reportItem {
	return reportItem{status: status, project: project, version: version, milestone: milestone}
}

type reportItem struct {
	status                      core.PlanningStatus
	project, version, milestone string
}

func report(items ...reportItem) planning.Report {
	root := planning.Report{Summary: summary(0, 0, 0, 0, 0), Projects: []planning.Project{}}
	for _, item := range items {
		root.Summary = addSummary(root.Summary, item.status)
		project := findProject(&root, item.project)
		project.Summary = addSummary(project.Summary, item.status)
		version := findVersion(project, item.version)
		version.Summary = addSummary(version.Summary, item.status)
		milestone := findMilestone(version, item.milestone)
		milestone.Summary = addSummary(milestone.Summary, item.status)
	}
	sort.Slice(root.Projects, func(i, j int) bool { return reportValueLess(root.Projects[i].Value, root.Projects[j].Value) })
	for projectIndex := range root.Projects {
		project := &root.Projects[projectIndex]
		sort.Slice(project.Versions, func(i, j int) bool { return reportValueLess(project.Versions[i].Value, project.Versions[j].Value) })
		for versionIndex := range project.Versions {
			sort.Slice(project.Versions[versionIndex].Milestones, func(i, j int) bool {
				return reportValueLess(project.Versions[versionIndex].Milestones[i].Value, project.Versions[versionIndex].Milestones[j].Value)
			})
		}
	}
	return root
}

func reportValueLess(left, right string) bool {
	if left == "" {
		return right != ""
	}
	if right == "" {
		return false
	}
	return left < right
}

func summary(total, toplan, researched, planned, rejected int) planning.Summary {
	return planning.Summary{
		TotalItems: total,
		StatusCounts: []planning.StatusCount{
			{Value: core.PlanningStatusToplan, Count: toplan},
			{Value: core.PlanningStatusResearched, Count: researched},
			{Value: core.PlanningStatusPlanned, Count: planned},
			{Value: core.PlanningStatusRejected, Count: rejected},
		},
	}
}

func addSummary(current planning.Summary, status core.PlanningStatus) planning.Summary {
	current.TotalItems++
	for index := range current.StatusCounts {
		if current.StatusCounts[index].Value == status {
			current.StatusCounts[index].Count++
		}
	}
	return current
}

func findProject(report *planning.Report, value string) *planning.Project {
	for index := range report.Projects {
		if report.Projects[index].Value == value {
			return &report.Projects[index]
		}
	}
	report.Projects = append(report.Projects, planning.Project{Value: value, Summary: summary(0, 0, 0, 0, 0), Versions: []planning.Version{}})
	return &report.Projects[len(report.Projects)-1]
}

func findVersion(project *planning.Project, value string) *planning.Version {
	for index := range project.Versions {
		if project.Versions[index].Value == value {
			return &project.Versions[index]
		}
	}
	project.Versions = append(project.Versions, planning.Version{Value: value, Summary: summary(0, 0, 0, 0, 0), Milestones: []planning.Milestone{}})
	return &project.Versions[len(project.Versions)-1]
}

func findMilestone(version *planning.Version, value string) *planning.Milestone {
	for index := range version.Milestones {
		if version.Milestones[index].Value == value {
			return &version.Milestones[index]
		}
	}
	version.Milestones = append(version.Milestones, planning.Milestone{Value: value, Summary: summary(0, 0, 0, 0, 0)})
	return &version.Milestones[len(version.Milestones)-1]
}
