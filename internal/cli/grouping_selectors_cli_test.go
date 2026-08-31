package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

type groupingSelectorTestProvider struct {
	graphTestProvider
	lastTaskFilter provider.TaskFilter
	previewFilter  provider.SelectionFilter
	claimFilter    provider.SelectionFilter
	previewCalls   int
	claimCalls     int
}

func (p *groupingSelectorTestProvider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	p.lastTaskFilter = filter
	p.listCalls++
	var matched []core.TaskView
	for _, task := range p.tasks {
		if core.MatchesGroupingFilter(task.Task, filter.Grouping) {
			matched = append(matched, task)
		}
	}
	return matched, nil
}

func (p *groupingSelectorTestProvider) PeekNextTaskWithFilter(filter provider.SelectionFilter) (core.TaskView, error) {
	p.previewCalls++
	p.previewFilter = filter
	return p.matchingTask(filter.Grouping), nil
}

func (p *groupingSelectorTestProvider) PeekNextTasksWithFilter(filter provider.SelectionFilter, limit int) ([]core.TaskView, error) {
	p.previewCalls++
	p.previewFilter = filter
	if limit <= 0 {
		return nil, errors.New("invalid limit")
	}
	return []core.TaskView{p.matchingTask(filter.Grouping)}, nil
}

func (p *groupingSelectorTestProvider) GetNextTaskWithFilter(filter provider.SelectionFilter) (core.TaskView, error) {
	p.claimCalls++
	p.claimFilter = filter
	return p.matchingTask(filter.Grouping), nil
}

func (p *groupingSelectorTestProvider) matchingTask(filter core.GroupingFilter) core.TaskView {
	for _, task := range p.tasks {
		if core.MatchesGroupingFilter(task.Task, filter) {
			return task
		}
	}
	return core.TaskView{}
}

func TestGroupingSelectorParserSupportsAllFieldsAndRejectsInvalidValues(t *testing.T) {
	args := []string{
		"--issue-id", "ISSUE-42",
		"--project=Apollo",
		"--milestone", "MVP",
		"--version=v1",
		"--feature-id", "FEAT-7",
		"--feature=Search",
	}
	flags := flag.NewFlagSet("grouping test", flag.ContinueOnError)
	parser := newGroupingSelectorParser(flags)
	if err := flags.Parse(args); err != nil {
		t.Fatalf("parse grouping selectors: %v", err)
	}
	gotFilter := parser.Filter()
	if got, want := gotFilter, (core.GroupingFilter{
		IssueID: "ISSUE-42", Project: "Apollo", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-7", Feature: "Search",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("grouping filter = %#v, want %#v", got, want)
	}

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "duplicate", args: []string{"--project", "Apollo", "--project=Another"}, want: "more than once"},
		{name: "empty separate", args: []string{"--feature", " \t"}, want: "cannot be empty"},
		{name: "empty assignment", args: []string{"--version="}, want: "cannot be empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flags := flag.NewFlagSet("grouping test", flag.ContinueOnError)
			newGroupingSelectorParser(flags)
			if err := flags.Parse(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTaskListGroupingSelectorsRenderTextAndJSONResults(t *testing.T) {
	matching := graphTaskView("match", "wtp-0001", "Matched", core.StatusTodo, nil, "2026-08-31T10:00:00Z")
	matching.Project = "Apollo"
	matching.Feature = "Search"
	nonMatching := graphTaskView("other", "wtp-0002", "Other", core.StatusTodo, nil, "2026-08-31T10:01:00Z")
	nonMatching.Project = "Zeus"

	for _, jsonOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonOut], func(t *testing.T) {
			p := &groupingSelectorTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{matching, nonMatching}}}
			var stdout, stderr bytes.Buffer
			if err := runTaskList(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: jsonOut}, []string{"--project", "apollo", "--feature", "Search"}); err != nil {
				t.Fatalf("runTaskList() error = %v", err)
			}
			if p.lastTaskFilter.Grouping != (core.GroupingFilter{Project: "apollo", Feature: "Search"}) {
				t.Fatalf("task list grouping filter = %#v", p.lastTaskFilter.Grouping)
			}
			if jsonOut {
				var got []core.TaskView
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
					t.Fatalf("decode task list JSON: %v", err)
				}
				if len(got) != 1 || got[0].ID != matching.ID {
					t.Fatalf("task list JSON = %#v, want only matching task", got)
				}
			} else if got := stdout.String(); !strings.Contains(got, "wtp-0001\ttodo") || strings.Contains(got, "wtp-0002") {
				t.Fatalf("task list text = %q", got)
			}
		})
	}
}

func TestTaskReadyAndNextPassIdenticalGroupingFilters(t *testing.T) {
	p := &groupingSelectorTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{graphTaskView("match", "wtp-0001", "Matched", core.StatusTodo, nil, "2026-08-31T10:00:00Z")}}}
	args := []string{"--issue-id=ISSUE-42", "--project", "Apollo", "--milestone", "MVP", "--version", "v1", "--feature-id", "FEAT-7", "--feature", "Search", "--agent", "Tony"}
	if err := runTaskReady(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, args); err != nil {
		t.Fatalf("runTaskReady() error = %v", err)
	}
	if err := runTaskNext(context{provider: p, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, args); err != nil {
		t.Fatalf("runTaskNext() error = %v", err)
	}
	if p.previewCalls != 1 || p.claimCalls != 1 {
		t.Fatalf("preview calls = %d, claim calls = %d", p.previewCalls, p.claimCalls)
	}
	if !reflect.DeepEqual(p.previewFilter, p.claimFilter) {
		t.Fatalf("preview filter = %#v, claim filter = %#v", p.previewFilter, p.claimFilter)
	}
	want := provider.SelectionFilter{Agent: "Tony", Grouping: core.GroupingFilter{
		IssueID: "ISSUE-42", Project: "Apollo", Milestone: "MVP", Version: "v1", FeatureID: "FEAT-7", Feature: "Search",
	}}
	if !reflect.DeepEqual(p.previewFilter, want) {
		t.Fatalf("selection filter = %#v, want %#v", p.previewFilter, want)
	}
}

func TestGraphGroupingSelectorsLimitTextJSONAndEdges(t *testing.T) {
	root := graphTaskView("root", "wtp-0001", "Matched root", core.StatusTodo, []string{"inside", "outside"}, "2026-08-31T10:00:00Z")
	root.Project = "Apollo"
	inside := graphTaskView("inside", "wtp-0002", "Matched dependency", core.StatusTodo, nil, "2026-08-31T10:01:00Z")
	inside.Project = "Apollo"
	outside := graphTaskView("outside", "wtp-0003", "Outside dependency", core.StatusTodo, nil, "2026-08-31T10:02:00Z")
	outside.Project = "Zeus"
	for _, jsonOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonOut], func(t *testing.T) {
			p := &groupingSelectorTestProvider{graphTestProvider: graphTestProvider{tasks: []core.TaskView{root, inside, outside}}}
			var stdout, stderr bytes.Buffer
			if err := runGraph(context{provider: p, stdout: &stdout, stderr: &stderr, jsonOut: jsonOut}, []string{"--project", "Apollo"}); err != nil {
				t.Fatalf("runGraph() error = %v", err)
			}
			if p.lastTaskFilter.Grouping != (core.GroupingFilter{Project: "Apollo"}) {
				t.Fatalf("graph grouping filter = %#v", p.lastTaskFilter.Grouping)
			}
			if !jsonOut && (strings.Contains(stdout.String(), "outside") || strings.Contains(stdout.String(), "Outside dependency")) {
				t.Fatalf("graph included unmatched task/edge: %q", stdout.String())
			}
			if jsonOut {
				var nodes []graphNode
				if err := json.Unmarshal(stdout.Bytes(), &nodes); err != nil {
					t.Fatalf("decode graph JSON: %v", err)
				}
				if len(nodes) != 1 || nodes[0].Task == nil || nodes[0].Task.ID != root.ID || len(nodes[0].Dependencies) != 1 || nodes[0].Dependencies[0].Task.ID != inside.ID {
					t.Fatalf("graph JSON = %#v", nodes)
				}
			}
		})
	}
}

func TestLegacyGroupingSelectorsUseSharedValidationAndRewrite(t *testing.T) {
	got, err := rewriteLegacyArgs([]string{"--get-next-task", "--project", "Apollo", "--feature=Search"})
	if err != nil {
		t.Fatalf("rewriteLegacyArgs() error = %v", err)
	}
	want := []string{"task", "next", "--project", "Apollo", "--feature", "Search"}
	if !reflect.DeepEqual(got.args, want) {
		t.Fatalf("legacy next args = %v, want %v", got.args, want)
	}

	for _, args := range [][]string{
		{"--get-tasks", "--project", "Apollo", "--project", "Zeus"},
		{"--get-next-task", "--feature="},
		{"--get-tasks", "--milestone", " \t"},
	} {
		if _, err := rewriteLegacyArgs(args); err == nil {
			t.Fatalf("rewriteLegacyArgs(%v) unexpectedly succeeded", args)
		}
	}
}
