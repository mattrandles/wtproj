// Package planning owns renderer-neutral planning reports, separate from
// execution stats. Aggregation consumes provider.PlanningReader only.
package planning

import (
	"fmt"
	"sort"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

// StatusCount uses the established value/count bucket shape. Every summary
// contains all four buckets in core.PlanningStatuses order, including zeros.
type StatusCount struct {
	Value core.PlanningStatus `json:"value"`
	Count int                 `json:"count"`
}

type Summary struct {
	TotalItems   int           `json:"totalItems"`
	StatusCounts []StatusCount `json:"statusCounts"`
}

// Report has a fixed project -> version -> milestone hierarchy. Arrays are
// non-nil, observed values only, sorted empty first then Go string lexical
// order. Values preserve casing (case variants are separate buckets); empty
// string means unset in JSON and is rendered as (unset) in human output.
type Report struct {
	Summary
	Projects []Project `json:"projects"`
}

type Project struct {
	Value string `json:"value"`
	Summary
	Versions []Version `json:"versions"`
}

type Version struct {
	Value string `json:"value"`
	Summary
	Milestones []Milestone `json:"milestones"`
}

type Milestone struct {
	Value string `json:"value"`
	Summary
}

// Options controls planning report aggregation. A nil Status selects all
// planning statuses. Grouping is the shared six-field exact matcher used by
// planning list and other grouping-aware queries.
type Options struct {
	Status   *core.PlanningStatus
	Grouping core.GroupingFilter
}

// ReportOptions is a descriptive alias for Options.
type ReportOptions = Options

// Aggregate loads planning records through the narrow planning reader and
// returns a deterministic project -> version -> milestone report. It never
// loads executable tasks, stats, or handoffs.
func Aggregate(reader provider.PlanningReader, options Options) (Report, error) {
	if reader == nil {
		return Report{}, fmt.Errorf("planning report provider is nil")
	}

	filter := provider.PlanningFilter{
		Status:   options.Status,
		Grouping: core.NormalizeGroupingFilter(options.Grouping),
	}
	if options.Status != nil {
		status, err := core.ParsePlanningStatus(string(*options.Status))
		if err != nil {
			return Report{}, err
		}
		filter.Status = &status
	}
	items, err := reader.ListPlanningItems(filter)
	if err != nil {
		return Report{}, fmt.Errorf("list planning items for report: %w", err)
	}

	root := reportBuilder{Summary: newSummary(), projects: make(map[string]*projectBuilder)}
	for _, item := range items {
		if filter.Status != nil && item.Status != *filter.Status {
			continue
		}
		if !matchesGrouping(item, filter.Grouping) {
			continue
		}

		root.add(item.Status)
		project := root.projects[item.Project]
		if project == nil {
			project = &projectBuilder{value: item.Project, Summary: newSummary(), versions: make(map[string]*versionBuilder)}
			root.projects[item.Project] = project
		}
		project.add(item.Status)

		version := project.versions[item.Version]
		if version == nil {
			version = &versionBuilder{value: item.Version, Summary: newSummary(), milestones: make(map[string]*milestoneBuilder)}
			project.versions[item.Version] = version
		}
		version.add(item.Status)

		milestone := version.milestones[item.Milestone]
		if milestone == nil {
			milestone = &milestoneBuilder{value: item.Milestone, Summary: newSummary()}
			version.milestones[item.Milestone] = milestone
		}
		milestone.add(item.Status)
	}

	return root.report(), nil
}

// Build is a descriptive alias for Aggregate for callers constructing a
// report as part of a command or another renderer.
func Build(reader provider.PlanningReader, options Options) (Report, error) {
	return Aggregate(reader, options)
}

type reportBuilder struct {
	Summary
	projects map[string]*projectBuilder
}

type projectBuilder struct {
	value string
	Summary
	versions map[string]*versionBuilder
}

type versionBuilder struct {
	value string
	Summary
	milestones map[string]*milestoneBuilder
}

type milestoneBuilder struct {
	value string
	Summary
}

func newSummary() Summary {
	statuses := core.PlanningStatuses()
	counts := make([]StatusCount, len(statuses))
	for i, status := range statuses {
		counts[i] = StatusCount{Value: status}
	}
	return Summary{StatusCounts: counts}
}

func (s *Summary) add(status core.PlanningStatus) {
	s.TotalItems++
	for i, candidate := range s.StatusCounts {
		if candidate.Value == status {
			s.StatusCounts[i].Count++
			return
		}
	}
}

func (r reportBuilder) report() Report {
	projects := make([]Project, 0, len(r.projects))
	for _, value := range sortedKeys(r.projects) {
		projects = append(projects, r.projects[value].project())
	}
	return Report{Summary: r.Summary, Projects: projects}
}

func (p *projectBuilder) project() Project {
	versions := make([]Version, 0, len(p.versions))
	for _, value := range sortedKeys(p.versions) {
		versions = append(versions, p.versions[value].version())
	}
	return Project{Value: p.value, Summary: p.Summary, Versions: versions}
}

func (v *versionBuilder) version() Version {
	milestones := make([]Milestone, 0, len(v.milestones))
	for _, value := range sortedKeys(v.milestones) {
		milestones = append(milestones, Milestone{Value: v.milestones[value].value, Summary: v.milestones[value].Summary})
	}
	return Version{Value: v.value, Summary: v.Summary, Milestones: milestones}
}

func (r *reportBuilder) add(status core.PlanningStatus)    { r.Summary.add(status) }
func (p *projectBuilder) add(status core.PlanningStatus)   { p.Summary.add(status) }
func (v *versionBuilder) add(status core.PlanningStatus)   { v.Summary.add(status) }
func (m *milestoneBuilder) add(status core.PlanningStatus) { m.Summary.add(status) }

func sortedKeys[T any](values map[string]*T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "" {
			return keys[j] != ""
		}
		if keys[j] == "" {
			return false
		}
		return keys[i] < keys[j]
	})
	return keys
}

func matchesGrouping(item core.PlanningItemView, filter core.GroupingFilter) bool {
	return core.MatchesGroupingFilter(core.Task{
		IssueID: item.IssueID, Project: item.Project, Milestone: item.Milestone,
		Version: item.Version, FeatureID: item.FeatureID, Feature: item.Feature,
	}, filter)
}
