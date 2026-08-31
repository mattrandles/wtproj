package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestDeleteReusableTaskDetachesEveryStatusAndBranchInAssignmentOrder(t *testing.T) {
	root := t.TempDir()
	current, err := New(root, core.NewBranchScope("feature/current"))
	if err != nil {
		t.Fatalf("New(current) error = %v", err)
	}
	legacy, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(legacy) error = %v", err)
	}
	other, err := New(root, core.NewBranchScope("feature/other"))
	if err != nil {
		t.Fatalf("New(other) error = %v", err)
	}
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Deleted", mustRFC3339Time(t, "2026-03-24T14:10:04Z"))
	retained := reusableDefinitionForTest("b5c3806a-bd1b-424d-889b-29e5b06679b8", "Retained", deleted.CreatedAt)
	last := reusableDefinitionForTest("c5c3806a-bd1b-424d-889b-29e5b06679b8", "Last", deleted.CreatedAt)
	writeReusableCatalogForTest(t, current, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted, retained, last}})

	tasks := []struct {
		provider *Provider
		task     core.Task
	}{
		{current, deleteTestTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo, []string{retained.ID, deleted.ID, last.ID})},
		{legacy, deleteTestTask("35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.StatusInProgress, []string{deleted.ID, last.ID})},
		{other, deleteTestTask("45c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0003", core.StatusDone, []string{last.ID, deleted.ID})},
		{current, deleteTestTask("55c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0004", core.StatusPaused, []string{retained.ID, last.ID})},
	}
	for _, item := range tasks {
		if err := item.provider.writeTask(item.task); err != nil {
			t.Fatalf("writeTask(%s) error = %v", item.task.ShortID, err)
		}
	}

	result, err := current.DeleteReusableTask(" deleted ")
	if err != nil {
		t.Fatalf("DeleteReusableTask(name) error = %v", err)
	}
	if result.Deleted != deleted || result.DetachedTaskCount != 3 {
		t.Fatalf("delete result = %#v, want deleted definition and count 3", result)
	}
	if _, err := legacy.GetReusableTask(deleted.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("deleted definition lookup error = %v, want not found", err)
	}

	for _, item := range tasks {
		stored, err := readTaskFile(item.provider, item.task)
		if err != nil {
			t.Fatalf("readTaskFile(%s) error = %v", item.task.ShortID, err)
		}
		want := detachReusableTaskID(item.task.ReusableTaskIDs, deleted.ID)
		if !slices.Equal(stored.ReusableTaskIDs, want) {
			t.Fatalf("task %s assignments = %v, want %v", item.task.ShortID, stored.ReusableTaskIDs, want)
		}
		if slices.Contains(item.task.ReusableTaskIDs, deleted.ID) && !stored.UpdatedAt.After(item.task.UpdatedAt) {
			t.Fatalf("task %s updatedAt = %s, want after %s", item.task.ShortID, stored.UpdatedAt, item.task.UpdatedAt)
		}
		if stored.Title != item.task.Title || stored.Status != item.task.Status || !slices.Equal(stored.Dependencies, item.task.Dependencies) || !slices.Equal(stored.Comments, item.task.Comments) {
			t.Fatalf("task %s unrelated fields changed: got %#v, want title/status/dependencies/comments from %#v", item.task.ShortID, stored, item.task)
		}
	}
	unchanged, err := readTaskFile(current, tasks[3].task)
	if err != nil {
		t.Fatalf("read unreferenced task error = %v", err)
	}
	if !unchanged.UpdatedAt.Equal(tasks[3].task.UpdatedAt) || !slices.Equal(unchanged.ReusableTaskIDs, tasks[3].task.ReusableTaskIDs) {
		t.Fatalf("unreferenced task changed = %#v, want unchanged %#v", unchanged, tasks[3].task)
	}
}

func TestDeleteReusableTaskUnreferencedUsesJournalPath(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Unused", mustRFC3339Time(t, "2026-03-24T14:10:04Z"))
	retained := reusableDefinitionForTest("b5c3806a-bd1b-424d-889b-29e5b06679b8", "Retained", deleted.CreatedAt)
	writeReusableCatalogForTest(t, p, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted, retained}})

	result, err := p.DeleteReusableTask(deleted.ID)
	if err != nil {
		t.Fatalf("DeleteReusableTask(UUID) error = %v", err)
	}
	if result.Deleted != deleted || result.DetachedTaskCount != 0 {
		t.Fatalf("delete result = %#v, want unused definition and count 0", result)
	}
	if _, err := os.Stat(p.reusableUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reusable update journal stat = %v, want absent", err)
	}
}

func TestDeleteReusableTaskScansConfiguredCustomStatuses(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{
		{Name: "review", Category: core.StatusCategoryWaiting},
	})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	p, err := NewWithCatalog(t.TempDir(), nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Review helper", mustRFC3339Time(t, "2026-03-24T14:10:04Z"))
	writeReusableCatalogForTest(t, p, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted}})
	task := deleteTestTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.Status("review"), []string{deleted.ID})
	started := task.CreatedAt
	task.StartedAt = &started
	if err := p.writeTask(task); err != nil {
		t.Fatalf("write custom-status task: %v", err)
	}
	result, err := p.DeleteReusableTask("review helper")
	if err != nil {
		t.Fatalf("DeleteReusableTask(custom status) error = %v", err)
	}
	if result.DetachedTaskCount != 1 {
		t.Fatalf("custom-status detached count = %d, want 1", result.DetachedTaskCount)
	}
	stored, err := readTaskFile(p, task)
	if err != nil {
		t.Fatalf("read custom-status task: %v", err)
	}
	if len(stored.ReusableTaskIDs) != 0 || !stored.UpdatedAt.After(task.UpdatedAt) {
		t.Fatalf("custom-status task after delete = %#v, want detached and advanced", stored)
	}
}

func TestDeleteReusableTaskRollsBackAfterMidPublicationFailure(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Deleted", mustRFC3339Time(t, "2026-03-24T14:10:04Z"))
	retained := reusableDefinitionForTest("b5c3806a-bd1b-424d-889b-29e5b06679b8", "Retained", deleted.CreatedAt)
	writeReusableCatalogForTest(t, p, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted, retained}})
	task := deleteTestTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo, []string{deleted.ID, retained.ID})
	if err := p.writeTask(task); err != nil {
		t.Fatalf("writeTask() error = %v", err)
	}
	beforeCatalog, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("read catalog before = %v", err)
	}
	beforeTask, err := os.ReadFile(p.taskPath(task.Status, task.ShortID))
	if err != nil {
		t.Fatalf("read task before = %v", err)
	}
	failed := false
	p.fs.replace = func(source, target string) error {
		if target == p.taskPath(task.Status, task.ShortID) && !failed {
			failed = true
			return errors.New("injected task publication failure")
		}
		return replaceFile(source, target)
	}

	if _, err := p.DeleteReusableTask(deleted.ID); err == nil || !strings.Contains(err.Error(), "injected task publication failure") {
		t.Fatalf("DeleteReusableTask() error = %v, want injected failure", err)
	}
	afterCatalog, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("read catalog after = %v", err)
	}
	afterTask, err := os.ReadFile(p.taskPath(task.Status, task.ShortID))
	if err != nil {
		t.Fatalf("read task after = %v", err)
	}
	if !slices.Equal(afterCatalog, beforeCatalog) || !slices.Equal(afterTask, beforeTask) {
		t.Fatalf("rollback did not restore exact endpoints: catalog equal=%v task equal=%v", slices.Equal(afterCatalog, beforeCatalog), slices.Equal(afterTask, beforeTask))
	}
	if _, err := os.Stat(p.reusableUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback journal stat = %v, want absent", err)
	}
}

func TestDeleteReusableTaskSerializesSharedReadersAndWriters(t *testing.T) {
	root := t.TempDir()
	deleter, err := New(root, core.NewBranchScope("feature/delete"))
	if err != nil {
		t.Fatalf("New(deleter) error = %v", err)
	}
	writer, err := New(root, core.NewBranchScope("feature/write"))
	if err != nil {
		t.Fatalf("New(writer) error = %v", err)
	}
	reader, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(reader) error = %v", err)
	}
	deleted, err := deleter.CreateReusableTask(core.CreateReusableTaskInput{Name: "Deleted", Title: "Deleted", Instructions: "Deleted"})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	task := deleteTestTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo, []string{deleted.ID})
	if err := deleter.writeTask(task); err != nil {
		t.Fatalf("writeTask() error = %v", err)
	}

	var group sync.WaitGroup
	var deleteResult provider.ReusableTaskDeleteResult
	var deleteErr error
	var writeErr error
	group.Add(2)
	go func() {
		defer group.Done()
		deleteResult, deleteErr = deleter.DeleteReusableTask(deleted.ID)
	}()
	go func() {
		defer group.Done()
		_, writeErr = writer.UpdateTask(task.ID, core.UpdateTaskInput{Title: core.OptionalString{Set: true, Value: "written concurrently"}})
	}()
	group.Wait()
	if deleteErr != nil || writeErr != nil {
		t.Fatalf("concurrent delete/write errors: delete=%v write=%v", deleteErr, writeErr)
	}
	if deleteResult.DetachedTaskCount != 1 {
		t.Fatalf("concurrent delete count = %d, want 1", deleteResult.DetachedTaskCount)
	}
	if _, err := reader.GetReusableTask(deleted.ID); err == nil {
		t.Fatal("reader observed deleted definition after both operations")
	}
	stored, err := readTaskFile(reader, task)
	if err != nil {
		t.Fatalf("read final task = %v", err)
	}
	if stored.Title != "written concurrently" || len(stored.ReusableTaskIDs) != 0 {
		t.Fatalf("final task = %#v, want writer title and detached assignments", stored)
	}
}

func deleteTestTask(id, shortID string, status core.Status, assignments []string) core.Task {
	task := validTask(id, shortID, status)
	task.ReusableTaskIDs = append([]string(nil), assignments...)
	if status == core.StatusInProgress || status == core.StatusPaused || status == core.StatusDone {
		started := task.CreatedAt
		task.StartedAt = &started
	}
	if status == core.StatusDone {
		completed := task.CreatedAt
		task.CompletedAt = &completed
	}
	return task
}

func readTaskFile(p *Provider, task core.Task) (core.Task, error) {
	data, err := os.ReadFile(p.taskPath(task.Status, task.ShortID))
	if err != nil {
		return core.Task{}, err
	}
	var stored core.Task
	if err := json.Unmarshal(data, &stored); err != nil {
		return core.Task{}, err
	}
	return stored, nil
}

var _ provider.ReusableTaskMutationProvider = (*Provider)(nil)
