//go:build wtp_fault_injection

package flatfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestBatchUpdateFaultInjectionRecoveryMatrix(t *testing.T) {
	points := []struct {
		name        string
		expectAfter bool
	}{
		{name: "batch-update-before-journal"},
		{name: "batch-update-prepared"},
		{name: "batch-update-replacement-1"},
		{name: "batch-update-replacement-2"},
		{name: "batch-update-replacement-3"},
		{name: "batch-update-committed", expectAfter: true},
		{name: "batch-update-cleanup", expectAfter: true},
	}
	for _, point := range points {
		t.Run(point.name, func(t *testing.T) {
			root, tasks := createFaultFixture(t)
			runFaultChild(t, root, point.name, false)
			assertRecoveredFaultFixture(t, root, tasks, point.expectAfter)
		})
	}

	t.Run("while-rolling-back", func(t *testing.T) {
		root, tasks := createFaultFixture(t)
		runFaultChild(t, root, "batch-update-rollback", true)
		assertRecoveredFaultFixture(t, root, tasks, false)
	})
}

func TestBatchUpdateFaultInjectionChild(t *testing.T) {
	root := os.Getenv("WTP_BATCH_ROOT")
	if root == "" {
		t.Skip("fault-injection child")
	}
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tasks, err := p.ListTasks(provider.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("fixture task count = %d, want 3", len(tasks))
	}
	if os.Getenv("WTP_BATCH_FAIL_TASK") == "1" {
		target := p.taskPath(core.StatusTodo, tasks[0].ShortID)
		realReplace := p.fs.replace
		p.fs.replace = func(source, path string) error {
			if path == target {
				return errors.New("injected task failure before rollback")
			}
			return realReplace(source, path)
		}
	}
	_, err = p.BatchUpdate(faultBatchRequest(tasks))
	if err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
}

func createFaultFixture(t *testing.T) (string, []core.TaskView) {
	t.Helper()
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tasks := make([]core.TaskView, 0, 3)
	for _, title := range []string{"one", "two", "three"} {
		task, err := p.CreateTask(core.CreateTaskInput{Title: title})
		if err != nil {
			t.Fatalf("CreateTask(%q) error = %v", title, err)
		}
		tasks = append(tasks, task)
	}
	return root, tasks
}

func faultBatchRequest(tasks []core.TaskView) provider.BatchUpdateRequest {
	return provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{ShortID: tasks[0].ShortID, ExpectedUpdatedAt: tasks[0].UpdatedAt, Title: core.OptionalString{Set: true, Value: "one changed"}},
		{ShortID: tasks[1].ShortID, ExpectedUpdatedAt: tasks[1].UpdatedAt, Status: core.OptionalStatus{Set: true, Value: core.StatusInProgress}},
		{ShortID: tasks[2].ShortID, ExpectedUpdatedAt: tasks[2].UpdatedAt, Title: core.OptionalString{Set: true, Value: "three changed"}},
	}}
}

func runFaultChild(t *testing.T, root, point string, failTask bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBatchUpdateFaultInjectionChild$")
	cmd.Env = append(os.Environ(), "WTP_FAULT_POINT="+point, "WTP_BATCH_ROOT="+root)
	if failTask {
		cmd.Env = append(cmd.Env, "WTP_BATCH_FAIL_TASK=1")
	}
	err := cmd.Run()
	if exitCode := cmd.ProcessState.ExitCode(); exitCode != 97 {
		t.Fatalf("fault child exit code = %d, err=%v", exitCode, err)
	}
	if err := os.Remove(filepath.Join(root, "meta", "wtp.lock")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove crash lock marker: %v", err)
	}
}

func assertRecoveredFaultFixture(t *testing.T, root string, original []core.TaskView, expectAfter bool) {
	t.Helper()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("recovery New() error = %v", err)
	}
	for index, want := range original {
		got, err := p.GetTask(want.ShortID, "")
		if err != nil {
			t.Fatalf("GetTask(%s) error = %v", want.ShortID, err)
		}
		if expectAfter && index != 1 && got.Title != want.Title+" changed" {
			t.Fatalf("task %s title = %q, want changed title", want.ShortID, got.Title)
		}
		if !expectAfter && got.Title != want.Title || !expectAfter && got.Status != core.StatusTodo {
			t.Fatalf("task %s after rollback = %#v, want original task", want.ShortID, got.Task)
		}
		if expectAfter && index == 1 && got.Status != core.StatusInProgress {
			t.Fatalf("status task %s status = %s, want inProgress", want.ShortID, got.Status)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "meta", batchUpdateJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered journal stat = %v, want absent", err)
	}
}
