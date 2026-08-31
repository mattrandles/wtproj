package flatfile

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestTaskViewsResolveReusableDefinitionsLiveAcrossReadsAndClaims(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Checks", Title: "Run checks", Instructions: "Run the focused checks."})
	if err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Review", Title: "Review output", Instructions: "Review the completed output."})
	if err != nil {
		t.Fatalf("CreateReusableTask(second) error = %v", err)
	}
	third, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Release", Title: "Release output", Instructions: "Prepare the release."})
	if err != nil {
		t.Fatalf("CreateReusableTask(third) error = %v", err)
	}

	zero, err := p.CreateTask(core.CreateTaskInput{Title: "Zero assignments"})
	if err != nil {
		t.Fatalf("CreateTask(zero) error = %v", err)
	}
	one, err := p.CreateTask(core.CreateTaskInput{Title: "One assignment", ReusableTasks: []string{first.ID}})
	if err != nil {
		t.Fatalf("CreateTask(one) error = %v", err)
	}
	many, err := p.CreateTask(core.CreateTaskInput{
		Title:         "Many assignments",
		ReusableTasks: []string{third.ID, first.ID, second.ID},
	})
	if err != nil {
		t.Fatalf("CreateTask(many) error = %v", err)
	}
	assertReusableView(t, zero, nil)
	assertReusableView(t, one, []core.ReusableTaskDefinition{first})
	assertReusableView(t, many, []core.ReusableTaskDefinition{third, first, second})

	_, err = p.UpdateReusableTask(first.ID, core.UpdateReusableTaskInput{
		Name:         core.OptionalString{Set: true, Value: "Checks renamed"},
		Title:        core.OptionalString{Set: true, Value: "Run renamed checks"},
		Instructions: core.OptionalString{Set: true, Value: "Use the renamed check instructions."},
	})
	if err != nil {
		t.Fatalf("UpdateReusableTask(first) error = %v", err)
	}
	firstUpdated, err := p.GetReusableTask(first.ID)
	if err != nil {
		t.Fatalf("GetReusableTask(first) error = %v", err)
	}
	if firstUpdated.ID != first.ID || firstUpdated.Name != "Checks renamed" {
		t.Fatalf("updated reusable definition = %#v, want stable ID and renamed display name", firstUpdated)
	}

	gotOne, err := p.GetTask(one.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(one) error = %v", err)
	}
	assertReusableView(t, gotOne, []core.ReusableTaskDefinition{firstUpdated})
	if !slices.Equal(gotOne.ReusableTaskIDs, []string{first.ID}) {
		t.Fatalf("one assignment IDs = %v, want [%s]", gotOne.ReusableTaskIDs, first.ID)
	}

	listed, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	listedMany := findTaskView(t, listed, many.ID)
	assertReusableView(t, listedMany, []core.ReusableTaskDefinition{third, firstUpdated, second})
	jsonList, err := json.Marshal(listed)
	if err != nil {
		t.Fatalf("Marshal(JSON list) error = %v", err)
	}
	var decoded []core.TaskView
	if err := json.Unmarshal(jsonList, &decoded); err != nil {
		t.Fatalf("Unmarshal(JSON list) error = %v", err)
	}
	assertReusableView(t, findTaskView(t, decoded, many.ID), []core.ReusableTaskDefinition{third, firstUpdated, second})

	updated, err := p.UpdateTask(many.ShortID, core.UpdateTaskInput{Title: core.OptionalString{Set: true, Value: "Many renamed"}})
	if err != nil {
		t.Fatalf("UpdateTask(many) error = %v", err)
	}
	assertReusableView(t, updated, []core.ReusableTaskDefinition{third, firstUpdated, second})
	if !slices.Equal(updated.ReusableTaskIDs, []string{third.ID, first.ID, second.ID}) {
		t.Fatalf("updated assignment IDs = %v, want original order", updated.ReusableTaskIDs)
	}

	started, err := p.UpdateTaskStatus(one.ShortID, core.StatusInProgress, "Starter")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(one) error = %v", err)
	}
	assertReusableView(t, started, []core.ReusableTaskDefinition{firstUpdated})
	if _, err := p.UpdateTaskStatus(zero.ShortID, core.StatusInProgress, ""); err != nil {
		t.Fatalf("UpdateTaskStatus(zero in progress) error = %v", err)
	}
	if _, err := p.UpdateTaskStatus(zero.ShortID, core.StatusDone, ""); err != nil {
		t.Fatalf("UpdateTaskStatus(zero done) error = %v", err)
	}

	preview, err := p.PeekNextTask("Previewer")
	if err != nil {
		t.Fatalf("PeekNextTask() error = %v", err)
	}
	if preview.ID != many.ID {
		t.Fatalf("PeekNextTask() = %s, want many-assignment task %s", preview.ID, many.ID)
	}
	assertReusableView(t, preview, []core.ReusableTaskDefinition{third, firstUpdated, second})
	claimed, err := p.GetNextTask("Claimer")
	if err != nil {
		t.Fatalf("GetNextTask() error = %v", err)
	}
	if claimed.ID != many.ID || claimed.Status != core.StatusInProgress || claimed.Assignee != "Claimer" {
		t.Fatalf("claim = %#v, want many task in progress for Claimer", claimed.Task)
	}
	assertReusableView(t, claimed, []core.ReusableTaskDefinition{third, firstUpdated, second})

	batch, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           claimed.ShortID,
		ExpectedUpdatedAt: claimed.UpdatedAt,
		Title:             core.OptionalString{Set: true, Value: "Many batch renamed"},
	}}})
	if err != nil {
		t.Fatalf("BatchUpdate(claimed) error = %v", err)
	}
	if len(batch.Updated) != 1 {
		t.Fatalf("BatchUpdate updated count = %d, want 1", len(batch.Updated))
	}
	assertReusableView(t, batch.Updated[0], []core.ReusableTaskDefinition{third, firstUpdated, second})
}

func TestTaskReadyAndNextResolveReusableDefinitionsForBranchScopeAndAssignee(t *testing.T) {
	root := t.TempDir()
	current, err := New(root, core.NewBranchScope("feature/current"))
	if err != nil {
		t.Fatalf("New(current) error = %v", err)
	}
	legacy, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(legacy) error = %v", err)
	}
	foreign, err := New(root, core.NewBranchScope("feature/foreign"))
	if err != nil {
		t.Fatalf("New(foreign) error = %v", err)
	}
	definition, err := current.CreateReusableTask(core.CreateReusableTaskInput{Name: "Scoped", Title: "Scoped title", Instructions: "Scoped instructions."})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	grouping := core.GroupingFilter{Project: "live scope"}
	currentTask, err := current.CreateTask(core.CreateTaskInput{Title: "Current", Project: grouping.Project, ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask(current) error = %v", err)
	}
	legacyTask, err := legacy.CreateTask(core.CreateTaskInput{Title: "Legacy", Project: grouping.Project, ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask(legacy) error = %v", err)
	}
	foreignTask, err := foreign.CreateTask(core.CreateTaskInput{Title: "Foreign", Project: grouping.Project, ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask(foreign) error = %v", err)
	}
	filter := provider.SelectionFilter{Agent: "ScopeAgent", Grouping: core.GroupingFilter{Project: "LIVE SCOPE"}}

	ready, err := current.PeekNextTasksWithFilter(filter, 3)
	if err != nil {
		t.Fatalf("PeekNextTasksWithFilter() error = %v", err)
	}
	if got, want := taskViewIDsForReusableViews(ready), []string{currentTask.ID, legacyTask.ID}; !slices.Equal(got, want) {
		t.Fatalf("ready IDs = %v, want current and legacy only %v", got, want)
	}
	for _, view := range ready {
		assertReusableView(t, view, []core.ReusableTaskDefinition{definition})
	}
	claimed, err := current.GetNextTaskWithFilter(filter)
	if err != nil {
		t.Fatalf("GetNextTaskWithFilter() error = %v", err)
	}
	if claimed.ID != currentTask.ID || claimed.Status != core.StatusInProgress {
		t.Fatalf("scoped claim = %#v, want current task in progress", claimed.Task)
	}
	assertReusableView(t, claimed, []core.ReusableTaskDefinition{definition})
	if stored := readReusableAssignmentStoredTask(t, foreign, foreignTask.Task); stored.Status != core.StatusTodo {
		t.Fatalf("foreign task status = %s, want todo", stored.Status)
	}

	assigned, err := current.CreateTask(core.CreateTaskInput{Title: "Assigned Alice", Project: grouping.Project, Assignee: "Alice", ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask(assigned) error = %v", err)
	}
	unassigned, err := current.CreateTask(core.CreateTaskInput{Title: "Unassigned", Project: grouping.Project, ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask(unassigned) error = %v", err)
	}
	assigneeFilter := provider.SelectionFilter{Agent: "Bob", Grouping: grouping}
	assigneeReady, err := current.PeekNextTasksWithFilter(assigneeFilter, 3)
	if err != nil {
		t.Fatalf("PeekNextTasksWithFilter(assignee) error = %v", err)
	}
	if got, want := taskViewIDsForReusableViews(assigneeReady), []string{unassigned.ID, legacyTask.ID}; !slices.Equal(got, want) {
		t.Fatalf("assignee-filtered ready IDs = %v, want %v", got, want)
	}
	if slices.Contains(taskViewIDsForReusableViews(assigneeReady), currentTask.ID) {
		t.Fatal("assignee-filtered ready unexpectedly included already claimed current task")
	}
	for _, view := range assigneeReady {
		assertReusableView(t, view, []core.ReusableTaskDefinition{definition})
	}

	if slices.Contains(taskViewIDsForReusableViews(assigneeReady), assigned.ID) {
		t.Fatalf("assignee-filtered ready unexpectedly included Alice-assigned task %s", assigned.ID)
	}
}

func TestClaimAttachesHandoffsAndLiveReusableDefinitionsTogether(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	definition, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Handoff", Title: "Handoff title", Instructions: "Handoff instructions."})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Handoff claim", ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := p.WriteHandoff(provider.HandoffWriteRequest{Task: task.ShortID, Author: "writer", Message: "Retained task context"}); err != nil {
		t.Fatalf("WriteHandoff() error = %v", err)
	}
	if _, err := p.UpdateReusableTask(definition.ID, core.UpdateReusableTaskInput{Title: core.OptionalString{Set: true, Value: "Live handoff title"}}); err != nil {
		t.Fatalf("UpdateReusableTask() error = %v", err)
	}
	live, err := p.GetReusableTask(definition.ID)
	if err != nil {
		t.Fatalf("GetReusableTask() after edit error = %v", err)
	}

	claimed, err := p.GetNextTask("handoff-agent")
	if err != nil {
		t.Fatalf("GetNextTask() error = %v", err)
	}
	if len(claimed.Handoffs) != 1 || claimed.Handoffs[0].Message != "Retained task context" {
		t.Fatalf("claim handoffs = %#v, want retained task context", claimed.Handoffs)
	}
	assertReusableView(t, claimed, []core.ReusableTaskDefinition{live})
}

func TestTaskViewReadsRejectCorruptReusableAssignments(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	definition, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Valid", Title: "Valid", Instructions: "Valid instructions."})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	task, err := p.CreateTask(core.CreateTaskInput{Title: "Corrupt assignment", ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	stored := readReusableAssignmentStoredTask(t, p, task.Task)
	stored.ReusableTaskIDs = []string{"25c3806a-bd1b-424d-889b-29e5b06679b8"}
	writeReusableAssignmentTask(t, p, stored)

	for _, operation := range []struct {
		name string
		call func() error
	}{
		{name: "list", call: func() error { _, err := p.ListTasks(provider.TaskFilter{}); return err }},
		{name: "get", call: func() error { _, err := p.GetTask(task.ShortID, ""); return err }},
		{name: "ready", call: func() error { _, err := p.PeekNextTask(""); return err }},
		{name: "next", call: func() error { _, err := p.GetNextTask(""); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), task.ShortID) {
				t.Fatalf("%s error = %v, want unresolved reusable reference for %s", operation.name, err, task.ShortID)
			}
		})
	}
}

func assertReusableView(t *testing.T, view core.TaskView, want []core.ReusableTaskDefinition) {
	t.Helper()
	if !slices.Equal(view.ReusableTasks, want) {
		t.Fatalf("task %s reusableTasks = %#v, want %#v", view.ShortID, view.ReusableTasks, want)
	}
}

func findTaskView(t *testing.T, views []core.TaskView, id string) core.TaskView {
	t.Helper()
	for _, view := range views {
		if view.ID == id {
			return view
		}
	}
	t.Fatalf("task view %s not found", id)
	return core.TaskView{}
}

func taskViewIDsForReusableViews(views []core.TaskView) []string {
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	return ids
}
