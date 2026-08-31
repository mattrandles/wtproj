package core

import "strings"

// GroupingFilter selects tasks by optional grouping metadata. Empty fields do
// not restrict selection; all non-empty fields must match. FeatureID selects
// the stable grouping key, while Feature selects the human-readable name.
// Neither field is derived from or substituted for the other.
type GroupingFilter struct {
	IssueID   string
	Project   string
	Milestone string
	Version   string
	FeatureID string
	Feature   string
}

// NormalizeGroupingFilter trims surrounding whitespace from each selector.
// Whitespace-only selectors become unrestricted, just like omitted selectors.
// Callers accepting explicit selectors should reject empty values before
// normalization if their input contract requires a value. Casing is preserved.
func NormalizeGroupingFilter(filter GroupingFilter) GroupingFilter {
	filter.IssueID = strings.TrimSpace(filter.IssueID)
	filter.Project = strings.TrimSpace(filter.Project)
	filter.Milestone = strings.TrimSpace(filter.Milestone)
	filter.Version = strings.TrimSpace(filter.Version)
	filter.FeatureID = strings.TrimSpace(filter.FeatureID)
	filter.Feature = strings.TrimSpace(filter.Feature)
	return filter
}

// MatchesGroupingFilter reports whether task satisfies every non-empty
// selector after normalization, using case-insensitive exact comparisons.
// It does not modify task metadata or interpret IDs, names, or versions.
func MatchesGroupingFilter(task Task, filter GroupingFilter) bool {
	filter = NormalizeGroupingFilter(filter)
	return matchesGroupingValue(task.IssueID, filter.IssueID) &&
		matchesGroupingValue(task.Project, filter.Project) &&
		matchesGroupingValue(task.Milestone, filter.Milestone) &&
		matchesGroupingValue(task.Version, filter.Version) &&
		matchesGroupingValue(task.FeatureID, filter.FeatureID) &&
		matchesGroupingValue(task.Feature, filter.Feature)
}

func matchesGroupingValue(value, selector string) bool {
	return selector == "" || strings.EqualFold(value, selector)
}
