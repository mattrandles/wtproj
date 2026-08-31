package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestCreatePlanningItemAcceptsFullMetadataAndResolvesReferences(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	repoPath := filepath.Join(t.TempDir(), "repo")
	worktreePath := filepath.Join(repoPath, "worktree")
	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "dependency"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "First title", Instructions: "first"})
	if err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "Second title", Instructions: "second"})
	if err != nil {
		t.Fatalf("CreateReusableTask(second) error = %v", err)
	}

	view, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title:         "  planning title  ",
		Description:   "  planning description  ",
		Status:        core.PlanningStatusPlanned,
		Priority:      core.PriorityHigh,
		Estimate:      core.EstimateM,
		Lane:          "  planning  ",
		Model:         "  model  ",
		IssueID:       "  ISSUE-1  ",
		Project:       "  Project  ",
		Milestone:     "  MVP  ",
		Version:       "  v1  ",
		FeatureID:     "  FEAT-1  ",
		Feature:       "  Feature  ",
		GitRepo:       "  " + repoPath + "  ",
		GitBranch:     "  feature/planning  ",
		WorktreeName:  "  planning  ",
		WorktreeDir:   "  " + worktreePath + "  ",
		Assignee:      "  Alice  ",
		Dependencies:  []string{"  " + dependency.ShortID + "  "},
		ReusableTasks: []string{" second ", first.ID},
	})
	if err != nil {
		t.Fatalf("CreatePlanningItem() error = %v", err)
	}
	if view.Status != core.PlanningStatusPlanned || view.ShortID != "wtp-0002" {
		t.Fatalf("created planning identity/status = %s/%s", view.ShortID, view.Status)
	}
	if view.Title != "planning title" || view.Description != "planning description" || view.Lane != "planning" || view.Model != "model" || view.Assignee != "Alice" {
		t.Fatalf("trimmed metadata = %#v", view.PlanningItem)
	}
	if view.IssueID != "ISSUE-1" || view.Project != "Project" || view.Milestone != "MVP" || view.Version != "v1" || view.FeatureID != "FEAT-1" || view.Feature != "Feature" {
		t.Fatalf("trimmed grouping metadata = %#v", view.PlanningItem)
	}
	if view.GitRepo != repoPath || view.GitBranch != "feature/planning" || view.WorktreeName != "planning" || view.WorktreeDir != worktreePath {
		t.Fatalf("trimmed origin metadata = %#v", view.PlanningItem)
	}
	if !slices.Equal(view.Dependencies, []string{dependency.ID}) {
		t.Fatalf("resolved dependencies = %v, want [%s]", view.Dependencies, dependency.ID)
	}
	if !slices.Equal(view.ReusableTaskIDs, []string{second.ID, first.ID}) || !slices.EqualFunc(view.ReusableTasks, []core.ReusableTaskDefinition{second, first}, func(a, b core.ReusableTaskDefinition) bool { return a == b }) {
		t.Fatalf("resolved reusable assignments = %v / %#v", view.ReusableTaskIDs, view.ReusableTasks)
	}
	if view.StartedAt != nil || view.CompletedAt != nil || !view.CreatedAt.Equal(view.UpdatedAt) || view.CreatedAt.Location() != time.UTC {
		t.Fatalf("planning lifecycle timestamps = created %v updated %v started %v completed %v", view.CreatedAt, view.UpdatedAt, view.StartedAt, view.CompletedAt)
	}
	data, err := os.ReadFile(filepath.Join(p.root, planningDirectory, string(view.Status), view.ShortID+".json"))
	if err != nil {
		t.Fatalf("read planning record: %v", err)
	}
	if !strings.Contains(string(data), `"startedAt": null`) || !strings.Contains(string(data), `"completedAt": null`) {
		t.Fatalf("planning record omitted null lifecycle fields: %s", data)
	}
}

func TestCreatePlanningItemAcceptsEveryExplicitStatusAndDefaults(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	statuses := append([]core.PlanningStatus{""}, core.PlanningStatuses()...)
	for _, status := range statuses {
		view, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: fmt.Sprintf("status %q", status), Status: status})
		if err != nil {
			t.Fatalf("CreatePlanningItem(%q) error = %v", status, err)
		}
		want := status
		if want == "" {
			want = core.PlanningStatusToplan
		}
		if view.Status != want {
			t.Fatalf("status %q created as %q, want %q", status, view.Status, want)
		}
	}
}

func TestCreatePlanningItemValidatesBeforePublishingAllocation(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, test := range []struct {
		name  string
		input core.CreatePlanningItemInput
		want  string
	}{
		{name: "title", input: core.CreatePlanningItemInput{Title: " \t"}, want: "title is required"},
		{name: "status", input: core.CreatePlanningItemInput{Title: "invalid status", Status: " planned "}, want: "invalid planning status"},
		{name: "dependency", input: core.CreatePlanningItemInput{Title: "missing dependency", Dependencies: []string{"missing"}}, want: "not found"},
		{name: "path", input: core.CreatePlanningItemInput{Title: "relative repo", GitRepo: "relative"}, want: "absolute path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := p.CreatePlanningItem(test.input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CreatePlanningItem() error = %v, want %q", err, test.want)
			}
			index, err := p.readIndex()
			if err != nil {
				t.Fatalf("readIndex() error = %v", err)
			}
			if index.Next != 1 {
				t.Fatalf("allocation index advanced after validation error: %#v", index)
			}
			items, err := p.loadPlanningItems()
			if err != nil {
				t.Fatalf("loadPlanningItems() error = %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("planning records published after validation error: %#v", items)
			}
		})
	}
}

func TestCreatePlanningItemWriteFailureLeavesOnlyAllocationGap(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	p.fs.replace = func(source, target string) error {
		if strings.Contains(target, planningDirectory) {
			return errors.New("injected planning write failure")
		}
		return replaceFile(source, target)
	}
	if _, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "write failure"}); err == nil || !strings.Contains(err.Error(), "injected planning write failure") {
		t.Fatalf("CreatePlanningItem() error = %v, want injected failure", err)
	}
	index, err := p.readIndex()
	if err != nil {
		t.Fatalf("readIndex() error = %v", err)
	}
	if index.Next != 2 {
		t.Fatalf("allocation index after write failure = %#v, want one monotonic gap", index)
	}
	items, err := p.loadPlanningItems()
	if err != nil {
		t.Fatalf("loadPlanningItems() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("planning records published after write failure: %#v", items)
	}
}

func TestConcurrentPlanningCreatorsAllocateUniqueUnionIDs(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const count = 16
	shortIDs := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			view, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: fmt.Sprintf("concurrent %d", i)})
			if err != nil {
				errs <- err
				return
			}
			shortIDs <- view.ShortID
		}(i)
	}
	wg.Wait()
	close(shortIDs)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreatePlanningItem() error = %v", err)
		}
	}
	seen := map[string]struct{}{}
	for shortID := range shortIDs {
		if _, exists := seen[shortID]; exists {
			t.Fatalf("duplicate planning short ID %q", shortID)
		}
		seen[shortID] = struct{}{}
	}
	if len(seen) != count {
		t.Fatalf("allocated planning IDs = %v, want %d", seen, count)
	}
	items, err := p.loadPlanningItems()
	if err != nil {
		t.Fatalf("loadPlanningItems() error = %v", err)
	}
	if len(items) != count {
		t.Fatalf("stored planning records = %d, want %d", len(items), count)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	for i := 1; i < len(items); i++ {
		if !items[i].CreatedAt.After(items[i-1].CreatedAt) {
			t.Fatalf("planning timestamps are not monotonic: %v then %v", items[i-1].CreatedAt, items[i].CreatedAt)
		}
	}
}
