package core

import (
	"reflect"
	"testing"
)

func TestNormalizeGroupingFilter(t *testing.T) {
	filter := GroupingFilter{
		IssueID: " \tGH-42\n", Project: "\u2003WTP\u00a0", Milestone: " Release Candidate ",
		Version: " v1.0-RC1 ", FeatureID: " Feature-7 ", Feature: " Task Grouping ",
	}
	original := filter
	want := GroupingFilter{
		IssueID: "GH-42", Project: "WTP", Milestone: "Release Candidate",
		Version: "v1.0-RC1", FeatureID: "Feature-7", Feature: "Task Grouping",
	}
	got := NormalizeGroupingFilter(filter)
	if got != want {
		t.Fatalf("NormalizeGroupingFilter() = %#v, want %#v", got, want)
	}
	if filter != original {
		t.Fatalf("normalization changed the original filter: %#v", filter)
	}
	if again := NormalizeGroupingFilter(got); again != got {
		t.Fatalf("normalization is not idempotent: %#v", again)
	}
	blank := GroupingFilter{
		IssueID: " ", Project: "\t", Milestone: "\n", Version: "\r", FeatureID: "\u2003", Feature: "\u00a0",
	}
	if got := NormalizeGroupingFilter(blank); got != (GroupingFilter{}) {
		t.Fatalf("whitespace-only filter = %#v, want empty", got)
	}
}

func TestMatchesGroupingFilter(t *testing.T) {
	task := Task{
		IssueID: "GH-42", Project: "WTP", Milestone: "Release Candidate",
		Version: "v1.0-RC1", FeatureID: "Feature-7", Feature: "Task Grouping",
	}
	all := GroupingFilter{
		IssueID: "gh-42", Project: "wtp", Milestone: "release candidate",
		Version: "V1.0-rc1", FeatureID: "feature-7", Feature: "task grouping",
	}
	tests := []struct {
		name   string
		task   Task
		filter GroupingFilter
		want   bool
	}{
		{name: "no restrictions", task: task, want: true},
		{name: "legacy task without restrictions", want: true},
		{name: "legacy task missing selected metadata", filter: all},
		{name: "all fields case insensitive", task: task, filter: all, want: true},
		{name: "trim selector", task: task, filter: GroupingFilter{Project: "\twtp \n"}, want: true},
		{name: "blank selector is unrestricted", filter: GroupingFilter{Feature: " \t\n"}, want: true},
		{name: "no substring match", task: task, filter: GroupingFilter{Feature: "Grouping"}},
		{name: "no prefix match", task: task, filter: GroupingFilter{IssueID: "GH-4"}},
		{name: "no wildcard match", task: task, filter: GroupingFilter{FeatureID: "Feature-*"}},
		{name: "no version interpretation", task: task, filter: GroupingFilter{Version: "1.0-RC1"}},
		{name: "internal whitespace is significant", task: task, filter: GroupingFilter{Feature: "Task  Grouping"}},
		{name: "task whitespace is significant", task: Task{Project: " WTP "}, filter: GroupingFilter{Project: "WTP"}},
		{name: "unicode simple case folding", task: Task{Feature: "Σ"}, filter: GroupingFilter{Feature: "ς"}, want: true},
		{name: "feature rename preserves key selection", task: Task{FeatureID: "Feature-7", Feature: "New Name"}, filter: GroupingFilter{FeatureID: "feature-7"}, want: true},
		{name: "feature name does not substitute for key", task: Task{Feature: "Feature-7"}, filter: GroupingFilter{FeatureID: "feature-7"}},
		{name: "feature key does not substitute for name", task: Task{FeatureID: "Feature-7"}, filter: GroupingFilter{Feature: "feature-7"}},
		{name: "same name different key", task: task, filter: GroupingFilter{FeatureID: "Feature-8", Feature: "Task Grouping"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.task
			if got := MatchesGroupingFilter(test.task, test.filter); got != test.want {
				t.Fatalf("MatchesGroupingFilter() = %v, want %v", got, test.want)
			}
			if !reflect.DeepEqual(test.task, before) {
				t.Fatal("matching modified stored task metadata")
			}
		})
	}

	// Every selector must be enforced even when the other five match.
	for _, test := range []struct {
		name   string
		mutate func(*GroupingFilter)
	}{
		{"issueId", func(f *GroupingFilter) { f.IssueID = "GH-43" }},
		{"project", func(f *GroupingFilter) { f.Project = "Other Project" }},
		{"milestone", func(f *GroupingFilter) { f.Milestone = "Next Milestone" }},
		{"version", func(f *GroupingFilter) { f.Version = "v2" }},
		{"featureId", func(f *GroupingFilter) { f.FeatureID = "Feature-8" }},
		{"feature", func(f *GroupingFilter) { f.Feature = "Other Feature" }},
	} {
		t.Run("AND/"+test.name, func(t *testing.T) {
			filter := all
			test.mutate(&filter)
			if MatchesGroupingFilter(task, filter) {
				t.Fatalf("matched despite a different %s", test.name)
			}
		})
	}
}
