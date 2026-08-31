//go:build wtp_fault_injection

package flatfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestReusableDeleteFaultInjectionAtEveryPublicationBoundary(t *testing.T) {
	points := []struct {
		name        string
		rollforward bool
	}{
		{name: "before-journal"},
		{name: "prepared"},
		{name: "replacement-1"},
		{name: "replacement-2"},
		{name: "replacement-3"},
		{name: "publication"},
		{name: "committed", rollforward: true},
		{name: "cleanup", rollforward: true},
	}
	for _, point := range points {
		t.Run(point.name, func(t *testing.T) {
			root, deleted, tasks, beforeCatalog, beforeTasks := reusableDeleteFaultFixture(t)
			runReusableDeleteFaultChild(t, root, "reusable-update-"+point.name)

			if err := os.Remove(filepath.Join(root, "meta", "wtp.lock")); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("remove crash lock marker: %v", err)
			}
			p, err := New(root, nil)
			if err != nil {
				t.Fatalf("New(recovery) error = %v", err)
			}
			if point.rollforward {
				if _, err := p.GetReusableTask(deleted.ID); err == nil || !strings.Contains(err.Error(), "not found") {
					t.Fatalf("roll-forward deleted lookup error = %v", err)
				}
				for _, task := range tasks {
					stored, err := readTaskFile(p, task)
					if err != nil {
						t.Fatalf("read rolled-forward task %s: %v", task.ShortID, err)
					}
					if slices.Contains(stored.ReusableTaskIDs, deleted.ID) {
						t.Fatalf("rolled-forward task %s still references deleted ID: %v", task.ShortID, stored.ReusableTaskIDs)
					}
				}
			} else {
				if _, err := p.GetReusableTask(deleted.ID); err != nil {
					t.Fatalf("rollback deleted lookup error = %v, want definition: %v", err, beforeCatalog)
				}
				for index, task := range tasks {
					stored, err := os.ReadFile(p.taskPath(task.Status, task.ShortID))
					if err != nil {
						t.Fatalf("read rolled-back task %s: %v", task.ShortID, err)
					}
					if !slices.Equal(stored, beforeTasks[index]) {
						t.Fatalf("rolled-back task %s bytes changed", task.ShortID)
					}
				}
				if got, err := os.ReadFile(p.reusableTaskCatalogPath()); err != nil || !slices.Equal(got, beforeCatalog) {
					t.Fatalf("rolled-back catalog bytes = %q, error=%v; want %q", got, err, beforeCatalog)
				}
			}
			if _, err := New(root, nil); err != nil {
				t.Fatalf("New(retry recovery) error = %v", err)
			}
			if _, err := os.Stat(p.reusableUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reusable journal after retry = %v, want absent", err)
			}
		})
	}
}

func TestReusableDeleteFaultInjectionChild(t *testing.T) {
	root := os.Getenv("WTP_REUSABLE_DELETE_ROOT")
	if root == "" {
		t.Skip("fault-injection child")
	}
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(fault child) error = %v", err)
	}
	if _, err := p.DeleteReusableTask("a5c3806a-bd1b-424d-889b-29e5b06679b8"); err != nil {
		t.Fatalf("DeleteReusableTask(fault child) error = %v", err)
	}
	t.Fatal("fault point did not terminate delete child")
}

func reusableDeleteFaultFixture(t *testing.T) (string, core.ReusableTaskDefinition, []core.Task, []byte, [][]byte) {
	t.Helper()
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New(fault fixture) error = %v", err)
	}
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Deleted", mustRFC3339Time(t, "2026-03-24T14:10:04Z"))
	retained := reusableDefinitionForTest("b5c3806a-bd1b-424d-889b-29e5b06679b8", "Retained", deleted.CreatedAt)
	writeReusableCatalogForTest(t, p, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted, retained}})
	tasks := []core.Task{
		deleteTestTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo, []string{deleted.ID, retained.ID}),
		deleteTestTask("35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.StatusInProgress, []string{retained.ID, deleted.ID}),
	}
	for _, task := range tasks {
		if err := p.writeTask(task); err != nil {
			t.Fatalf("write fault fixture task: %v", err)
		}
	}
	beforeCatalog, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("read fault fixture catalog: %v", err)
	}
	beforeTasks := make([][]byte, len(tasks))
	for index, task := range tasks {
		beforeTasks[index], err = os.ReadFile(p.taskPath(task.Status, task.ShortID))
		if err != nil {
			t.Fatalf("read fault fixture task: %v", err)
		}
	}
	return p.root, deleted, tasks, beforeCatalog, beforeTasks
}

func runReusableDeleteFaultChild(t *testing.T, root, point string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReusableDeleteFaultInjectionChild$")
	cmd.Env = append(os.Environ(), "WTP_FAULT_POINT="+point, "WTP_REUSABLE_DELETE_ROOT="+root)
	err := cmd.Run()
	if err == nil || cmd.ProcessState.ExitCode() != 97 {
		t.Fatalf("fault child %s exit = %v, code=%d", point, err, cmd.ProcessState.ExitCode())
	}
}
