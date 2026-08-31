package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestTaskReusableAssignmentsCreateReplaceReorderAndClear(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, second := createReusableAssignmentDefinitions(t, p)

	created, err := p.CreateTask(core.CreateTaskInput{
		Title:         "Assigned",
		ReusableTasks: []string{"  " + second.Name + "  ", first.ID},
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	assertReusableAssignmentIDs(t, created.Task, []string{second.ID, first.ID})
	assertReusableAssignmentIDs(t, readReusableAssignmentStoredTask(t, p, created.Task), []string{second.ID, first.ID})

	preserved, err := p.UpdateTask(created.ShortID, core.UpdateTaskInput{
		Title: core.OptionalString{Set: true, Value: "Assigned renamed"},
	})
	if err != nil {
		t.Fatalf("UpdateTask(preserve) error = %v", err)
	}
	assertReusableAssignmentIDs(t, preserved.Task, []string{second.ID, first.ID})

	replaced, err := p.UpdateTask(created.ID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{first.Name}},
	})
	if err != nil {
		t.Fatalf("UpdateTask(replace) error = %v", err)
	}
	assertReusableAssignmentIDs(t, replaced.Task, []string{first.ID})

	reordered, err := p.UpdateTask(created.ShortID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{first.ID, second.Name}},
	})
	if err != nil {
		t.Fatalf("UpdateTask(reorder) error = %v", err)
	}
	assertReusableAssignmentIDs(t, reordered.Task, []string{first.ID, second.ID})

	cleared, err := p.UpdateTask(created.ShortID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{}},
	})
	if err != nil {
		t.Fatalf("UpdateTask(clear) error = %v", err)
	}
	assertReusableAssignmentIDs(t, cleared.Task, nil)
}

func TestTaskReusableAssignmentsSupportLegacyAndTerminalTaskMutation(t *testing.T) {
	root := t.TempDir()
	legacy, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(legacy) error = %v", err)
	}
	first, second := createReusableAssignmentDefinitions(t, legacy)

	created, err := legacy.CreateTask(core.CreateTaskInput{Title: "Legacy assigned", ReusableTasks: []string{first.Name}})
	if err != nil {
		t.Fatalf("CreateTask(legacy) error = %v", err)
	}
	if !strings.HasPrefix(created.ShortID, "wtp-") || strings.Count(created.ShortID, "-") != 1 {
		t.Fatalf("legacy short ID = %q, want legacy format", created.ShortID)
	}
	assertReusableAssignmentIDs(t, created.Task, []string{first.ID})

	inProgress, err := legacy.UpdateTaskStatus(created.ShortID, core.StatusInProgress, "agent")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(in progress) error = %v", err)
	}
	updatedInProgress, err := legacy.UpdateTask(inProgress.ShortID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{second.ID, first.ID}},
	})
	if err != nil {
		t.Fatalf("UpdateTask(in progress assignments) error = %v", err)
	}
	assertReusableAssignmentIDs(t, updatedInProgress.Task, []string{second.ID, first.ID})

	done, err := legacy.UpdateTaskStatus(updatedInProgress.ShortID, core.StatusDone, "agent")
	if err != nil {
		t.Fatalf("UpdateTaskStatus(done) error = %v", err)
	}
	updatedDone, err := legacy.UpdateTask(done.ShortID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{first.Name}},
	})
	if err != nil {
		t.Fatalf("UpdateTask(done assignments) error = %v", err)
	}
	assertReusableAssignmentIDs(t, updatedDone.Task, []string{first.ID})
	if !updatedDone.UpdatedAt.After(done.UpdatedAt) {
		t.Fatalf("completed task updatedAt = %s, want after %s", updatedDone.UpdatedAt, done.UpdatedAt)
	}
}

func TestTaskReusableAssignmentsRejectUnknownAndDuplicateSelectorsAtomically(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, _ := createReusableAssignmentDefinitions(t, p)
	created, err := p.CreateTask(core.CreateTaskInput{Title: "Existing", ReusableTasks: []string{first.ID}})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	path := filepath.Join(p.root, string(created.Status), created.ShortID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(before) error = %v", err)
	}

	if _, err := p.UpdateTask(created.ShortID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{"missing"}},
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("UpdateTask(unknown reusable) error = %v, want not found", err)
	}
	if _, err := p.UpdateTask(created.ShortID, core.UpdateTaskInput{
		ReusableTasks: core.OptionalStrings{Set: true, Value: []string{first.Name, first.ID}},
	}); err == nil || !strings.Contains(err.Error(), "duplicates definition") {
		t.Fatalf("UpdateTask(duplicate reusable) error = %v, want duplicate", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(after) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("task changed after rejected assignments:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestBatchReusableAssignmentsHonorStaleAndAtomicPreparation(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, second := createReusableAssignmentDefinitions(t, p)
	firstTask, err := p.CreateTask(core.CreateTaskInput{Title: "First", ReusableTasks: []string{first.ID}})
	if err != nil {
		t.Fatalf("CreateTask(first) error = %v", err)
	}
	secondTask, err := p.CreateTask(core.CreateTaskInput{Title: "Second"})
	if err != nil {
		t.Fatalf("CreateTask(second) error = %v", err)
	}

	stale, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           firstTask.ShortID,
		ExpectedUpdatedAt: firstTask.UpdatedAt.Add(-1),
		ReusableTasks:     core.OptionalStrings{Set: true, Value: []string{second.Name}},
	}}})
	if !errors.Is(err, provider.ErrStaleTask) || len(stale.Updated) != 0 {
		t.Fatalf("BatchUpdate(stale reusable) result=%#v error=%v, want stale error and no update", stale, err)
	}
	current, err := p.GetTask(firstTask.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask(after stale) error = %v", err)
	}
	assertReusableAssignmentIDs(t, current.Task, []string{first.ID})
	valid, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           firstTask.ShortID,
		ExpectedUpdatedAt: current.UpdatedAt,
		ReusableTasks:     core.OptionalStrings{Set: true, Value: []string{second.Name, first.ID}},
	}}})
	if err != nil || len(valid.Updated) != 1 {
		t.Fatalf("BatchUpdate(reorder reusable) result=%#v error=%v, want one update", valid, err)
	}
	firstTask = valid.Updated[0]
	assertReusableAssignmentIDs(t, firstTask.Task, []string{second.ID, first.ID})

	before, err := os.ReadFile(filepath.Join(p.root, string(firstTask.Status), firstTask.ShortID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(first before atomic failure) error = %v", err)
	}
	_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{ShortID: firstTask.ShortID, ExpectedUpdatedAt: firstTask.UpdatedAt, Title: core.OptionalString{Set: true, Value: "must stay unpublished"}},
		{ShortID: secondTask.ShortID, ExpectedUpdatedAt: secondTask.UpdatedAt, ReusableTasks: core.OptionalStrings{Set: true, Value: []string{"unknown"}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("BatchUpdate(unknown reusable) error = %v, want not found", err)
	}
	after, err := os.ReadFile(filepath.Join(p.root, string(firstTask.Status), firstTask.ShortID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(first after atomic failure) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("first task changed after batch preparation failure:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestLoadTasksRejectsUnresolvedPersistedReusableAssignments(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	created, err := p.CreateTask(core.CreateTaskInput{Title: "Manually corrupted"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	stored := readReusableAssignmentStoredTask(t, p, created.Task)
	stored.ReusableTaskIDs = []string{"25c3806a-bd1b-424d-889b-29e5b06679b8"}
	writeReusableAssignmentTask(t, p, stored)

	if _, err := p.ListTasks(provider.TaskFilter{}); err == nil || !strings.Contains(err.Error(), "task "+created.ShortID+" has unresolved reusableTaskIds") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ListTasks(corrupt reusable assignments) error = %v, want clear unresolved reference", err)
	}
}

func createReusableAssignmentDefinitions(t *testing.T, p *Provider) (core.ReusableTaskDefinition, core.ReusableTaskDefinition) {
	t.Helper()
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Checks", Title: "Run checks", Instructions: "Run the focused checks."})
	if err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	second, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Review", Title: "Review output", Instructions: "Review the completed output."})
	if err != nil {
		t.Fatalf("CreateReusableTask(second) error = %v", err)
	}
	return first, second
}

func assertReusableAssignmentIDs(t *testing.T, task core.Task, want []string) {
	t.Helper()
	if !slices.Equal(task.ReusableTaskIDs, want) {
		t.Fatalf("task %s reusableTaskIds = %v, want %v", task.ShortID, task.ReusableTaskIDs, want)
	}
}

func readReusableAssignmentStoredTask(t *testing.T, p *Provider, task core.Task) core.Task {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.root, string(task.Status), task.ShortID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", task.ShortID, err)
	}
	var stored core.Task
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", task.ShortID, err)
	}
	return stored
}

func writeReusableAssignmentTask(t *testing.T, p *Provider, task core.Task) {
	t.Helper()
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", task.ShortID, err)
	}
	if err := os.WriteFile(filepath.Join(p.root, string(task.Status), task.ShortID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", task.ShortID, err)
	}
}
