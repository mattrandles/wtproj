package flatfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestUpdatePlanningItemPatchesMetadataAndPreservesIdentity(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repoPath := filepath.Join(t.TempDir(), "repo")
	worktreePath := filepath.Join(repoPath, "worktree")
	dependency, err := p.CreateTask(core.CreateTaskInput{Title: "dependency"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "first", Instructions: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "second", Instructions: "second"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "before", Description: "old", Dependencies: []string{dependency.ShortID}, ReusableTasks: []string{first.ID, second.Name},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.planningStatusDir(created.Status), created.ShortID+".json")
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := p.UpdatePlanningItem(created.ShortID, core.UpdatePlanningItemInput{
		Title:        core.OptionalString{Set: true, Value: "after"},
		Description:  core.OptionalString{Set: true, Value: "new description"},
		Priority:     core.OptionalPriority{Set: true, Value: core.PriorityHigh},
		Estimate:     core.OptionalEstimate{Set: true, Value: core.EstimateL},
		Lane:         core.OptionalString{Set: true, Value: "backend"},
		Model:        core.OptionalString{Set: true, Value: "model"},
		IssueID:      core.OptionalString{Set: true, Value: "ISSUE-1"},
		Project:      core.OptionalString{Set: true, Value: "Project"},
		Milestone:    core.OptionalString{Set: true, Value: "MVP"},
		Version:      core.OptionalString{Set: true, Value: "v2"},
		FeatureID:    core.OptionalString{Set: true, Value: "FEAT-1"},
		Feature:      core.OptionalString{Set: true, Value: "Feature"},
		GitRepo:      core.OptionalString{Set: true, Value: repoPath},
		GitBranch:    core.OptionalString{Set: true, Value: "branch"},
		WorktreeName: core.OptionalString{Set: true, Value: "worktree"},
		WorktreeDir:  core.OptionalString{Set: true, Value: worktreePath},
		Assignee:     core.OptionalString{Set: true, Value: "Alice"},
		Dependencies: core.OptionalStrings{Set: true, Value: []string{dependency.ID}},
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{second.Name,
			first.ID}},
	})
	if err != nil {
		t.Fatalf("UpdatePlanningItem() error = %v", err)
	}
	if updated.ID != created.ID || updated.ShortID != created.ShortID || !updated.CreatedAt.Equal(created.CreatedAt) || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("identity/timestamps changed incorrectly: before=%#v after=%#v", created.PlanningItem, updated.PlanningItem)
	}
	if updated.Status != core.PlanningStatusToplan || updated.StartedAt != nil || updated.CompletedAt != nil {
		t.Fatalf("planning lifecycle changed unexpectedly: %#v", updated.PlanningItem)
	}
	if !reflect.DeepEqual(updated.ReusableTaskIDs, []string{second.ID, first.ID}) || !reflect.DeepEqual(updated.Dependencies, []string{dependency.ID}) {
		t.Fatalf("canonical assignments = %v / %v", updated.ReusableTaskIDs, updated.Dependencies)
	}
	if !reflect.DeepEqual(updated.ReusableTasks, []core.ReusableTaskDefinition{second, first}) {
		t.Fatalf("reusable view order = %#v", updated.ReusableTasks)
	}
	if data, err := os.ReadFile(path); err != nil || bytes.Equal(data, beforeBytes) {
		t.Fatalf("updated record bytes = %q, err=%v; expected a changed record", data, err)
	}

	unchangedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := p.UpdatePlanningItem(updated.ShortID, core.UpdatePlanningItemInput{Title: core.OptionalString{Set: true, Value: "after"}})
	if err != nil {
		t.Fatalf("normalized no-op error = %v", err)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged.PlanningItem, updated.PlanningItem) || !bytes.Equal(afterBytes, unchangedBytes) {
		t.Fatalf("no-op changed item or bytes: item=%#v bytes changed=%v", unchanged.PlanningItem, !bytes.Equal(afterBytes, unchangedBytes))
	}
}

func TestUpdatePlanningItemClearsOptionalFieldsAndReordersAssignments(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repoPath := filepath.Join(t.TempDir(), "repo")
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "first", Instructions: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "second", Instructions: "second"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "title", Description: "description", Priority: core.PriorityHigh, Estimate: core.EstimateM,
		Lane: "lane", Model: "model", IssueID: "issue", Project: "project", Milestone: "milestone", Version: "version",
		FeatureID: "feature-id", Feature: "feature", GitRepo: repoPath, GitBranch: "branch", WorktreeName: "name", WorktreeDir: filepath.Join(repoPath, "worktree"),
		Assignee: "agent", ReusableTasks: []string{first.ID, second.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	empty := core.OptionalString{Set: true}
	updated, err := p.UpdatePlanningItem(created.ID, core.UpdatePlanningItemInput{
		Description: empty, Priority: core.OptionalPriority{Set: true}, Estimate: core.OptionalEstimate{Set: true},
		Lane: empty, Model: empty, IssueID: empty, Project: empty, Milestone: empty, Version: empty,
		FeatureID: empty, Feature: empty, GitRepo: empty, GitBranch: empty, WorktreeName: empty, WorktreeDir: empty, Assignee: empty,
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{second.Name, first.Name}},
	})
	if err != nil {
		t.Fatalf("clear/reorder update error = %v", err)
	}
	if updated.Title != created.Title || updated.Description != "" || updated.Priority != "" || updated.Estimate != "" ||
		updated.Lane != "" || updated.Model != "" || updated.IssueID != "" || updated.Project != "" || updated.Milestone != "" || updated.Version != "" ||
		updated.FeatureID != "" || updated.Feature != "" || updated.GitRepo != "" || updated.GitBranch != "" || updated.WorktreeName != "" || updated.WorktreeDir != "" || updated.Assignee != "" {
		t.Fatalf("optional fields were not cleared: %#v", updated.PlanningItem)
	}
	if !reflect.DeepEqual(updated.ReusableTaskIDs, []string{second.ID, first.ID}) {
		t.Fatalf("reordered assignments = %v", updated.ReusableTaskIDs)
	}
}

func TestUpdatePlanningItemReplacesClearsAndReordersReusableAssignments(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "first", Instructions: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "second", Instructions: "second"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Third", Title: "third", Instructions: "third"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "assignments", ReusableTasks: []string{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}

	replaced, err := p.UpdatePlanningItem(created.ID, core.UpdatePlanningItemInput{ReusableTasks: core.OptionalStrings{Set: true, Value: []string{third.Name}}})
	if err != nil {
		t.Fatalf("replacement error = %v", err)
	}
	if !slices.Equal(replaced.ReusableTaskIDs, []string{third.ID}) || !slices.EqualFunc(replaced.ReusableTasks, []core.ReusableTaskDefinition{third}, func(a, b core.ReusableTaskDefinition) bool { return a == b }) {
		t.Fatalf("replacement assignments = %#v / %#v", replaced.ReusableTaskIDs, replaced.ReusableTasks)
	}

	cleared, err := p.UpdatePlanningItem(created.ID, core.UpdatePlanningItemInput{ReusableTasks: core.OptionalStrings{Set: true, Value: []string{}}})
	if err != nil {
		t.Fatalf("clear error = %v", err)
	}
	if len(cleared.ReusableTaskIDs) != 0 || len(cleared.ReusableTasks) != 0 {
		t.Fatalf("cleared assignments = %#v / %#v", cleared.ReusableTaskIDs, cleared.ReusableTasks)
	}

	reordered, err := p.UpdatePlanningItem(created.ID, core.UpdatePlanningItemInput{ReusableTasks: core.OptionalStrings{Set: true, Value: []string{second.ID, first.ID}}})
	if err != nil {
		t.Fatalf("reorder error = %v", err)
	}
	if !slices.Equal(reordered.ReusableTaskIDs, []string{second.ID, first.ID}) || !slices.EqualFunc(reordered.ReusableTasks, []core.ReusableTaskDefinition{second, first}, func(a, b core.ReusableTaskDefinition) bool { return a == b }) {
		t.Fatalf("reordered assignments = %#v / %#v", reordered.ReusableTaskIDs, reordered.ReusableTasks)
	}
}

func TestPlanningStatusTransitionsReopenRejectedWithoutExecutionTimestamps(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "status", Status: core.PlanningStatusRejected})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := p.SetPlanningStatus(created.ShortID, core.PlanningStatusToplan)
	if err != nil {
		t.Fatalf("rejected reopen error = %v", err)
	}
	if reopened.Status != core.PlanningStatusToplan || !reopened.UpdatedAt.After(created.UpdatedAt) || reopened.StartedAt != nil || reopened.CompletedAt != nil {
		t.Fatalf("reopened planning lifecycle = %#v", reopened.PlanningItem)
	}
	if _, err := p.SetPlanningStatus(reopened.ShortID, core.PlanningStatusPlanned); err == nil || !strings.Contains(err.Error(), "invalid planning status transition") {
		t.Fatalf("invalid direct transition error = %v", err)
	}
	if _, err := p.SetPlanningStatus(reopened.ShortID, core.PlanningStatusToplan); err == nil || !strings.Contains(err.Error(), "invalid planning status transition") {
		t.Fatalf("same-state transition error = %v", err)
	}
}

func TestUpdatePlanningItemRejectsCyclesExecutableIDsAndStaleClock(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := p.CreateTask(core.CreateTaskInput{Title: "executable"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "second", Dependencies: []string{first.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpdatePlanningItem(first.ID, core.UpdatePlanningItemInput{Dependencies: core.OptionalStrings{Set: true, Value: []string{second.ShortID}}}); err == nil || !strings.Contains(err.Error(), "cyclic dependency detected") {
		t.Fatalf("cycle update error = %v", err)
	}
	if _, err := p.UpdatePlanningItem(executable.ID, core.UpdatePlanningItemInput{Title: core.OptionalString{Set: true, Value: "no"}}); err == nil || !strings.Contains(err.Error(), "planning item") {
		t.Fatalf("executable update error = %v", err)
	}
	if _, err := p.SetPlanningStatus(executable.ShortID, core.PlanningStatusResearched); err == nil || !strings.Contains(err.Error(), "planning item") {
		t.Fatalf("executable status error = %v", err)
	}

	stale := first.PlanningItem
	stale.UpdatedAt = time.Now().UTC().Add(24 * time.Hour)
	writePlanningStorageItem(t, filepath.Join(p.planningStatusDir(stale.Status), stale.ShortID+".json"), stale)
	updated, err := p.UpdatePlanningItem(first.ShortID, core.UpdatePlanningItemInput{Description: core.OptionalString{Set: true, Value: "after stale clock"}})
	if err != nil {
		t.Fatalf("stale clock update error = %v", err)
	}
	if !updated.UpdatedAt.After(stale.UpdatedAt) {
		t.Fatalf("updatedAt did not advance beyond stale future clock: %v <= %v", updated.UpdatedAt, stale.UpdatedAt)
	}
}

func TestPlanningUpdateInjectedReplacementPreservesOriginal(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "before"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.planningStatusDir(created.Status), created.ShortID+".json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p.fs.replace = func(source, target string) error {
		if strings.Contains(target, planningDirectory) {
			return errors.New("injected planning replacement failure")
		}
		return replaceFile(source, target)
	}
	if _, err := p.UpdatePlanningItem(created.ShortID, core.UpdatePlanningItemInput{Title: core.OptionalString{Set: true, Value: "after"}}); err == nil || !strings.Contains(err.Error(), "injected planning replacement failure") {
		t.Fatalf("injected replacement error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("record changed after failed atomic replacement: before=%s after=%s", original, after)
	}
}
