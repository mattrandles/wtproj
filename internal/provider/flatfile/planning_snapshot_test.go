package flatfile

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestValidationSnapshotRejectsCrossLifecycleIdentityCollisions(t *testing.T) {
	for _, test := range []struct {
		name string
		item core.PlanningItem
		want string
	}{
		{
			name: "uuid",
			item: planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.PlanningStatusToplan),
			want: "used by both executable and planning records",
		},
		{
			name: "short ID",
			item: planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan),
			want: "shortId wtp-0001 is used by both",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			task := validTask("15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
			if test.name == "short ID" {
				task.ID = "35c3806a-bd1b-424d-889b-29e5b06679b8"
			}
			if err := p.writeTask(task); err != nil {
				t.Fatalf("writeTask() error = %v", err)
			}
			writePlanningStorageItem(t, p.planningStatusDir(test.item.Status)+"/"+test.item.ShortID+".json", test.item)

			if _, err := p.GetTask(task.ShortID, ""); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("GetTask() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidationSnapshotAllowsCrossLifecycleDependenciesButBlocksPlanningReadiness(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	planning := planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusPlanned)
	executable := validTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.StatusTodo)
	executable.Dependencies = []string{planning.ID}
	if err := p.writeTask(executable); err != nil {
		t.Fatalf("writeTask() error = %v", err)
	}
	writePlanningStorageItem(t, p.planningStatusDir(planning.Status)+"/"+planning.ShortID+".json", planning)

	view, err := p.GetTask(executable.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !view.Readiness.Blocked || view.Readiness.Claimable {
		t.Fatalf("readiness = %#v, want planning dependency to block execution", view.Readiness)
	}
	wantReason := "unresolved dependencies: wtp-0001 (Planning storage; planning planned)"
	if view.Readiness.BlockedReason != wantReason {
		t.Fatalf("blocked reason = %q, want %q", view.Readiness.BlockedReason, wantReason)
	}
	if _, err := p.UpdateTaskStatus(executable.ShortID, core.StatusInProgress, "worker"); err == nil || !strings.Contains(err.Error(), "blocked by unresolved dependencies") {
		t.Fatalf("UpdateTaskStatus() error = %v, want unresolved planning dependency", err)
	}
}

func TestValidationSnapshotAcceptsPlanningDependencyOnExecutable(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	executable := validTask("15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
	planning := planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.PlanningStatusResearched)
	planning.Dependencies = []string{executable.ID}
	if err := p.writeTask(executable); err != nil {
		t.Fatalf("writeTask() error = %v", err)
	}
	writePlanningStorageItem(t, p.planningStatusDir(planning.Status)+"/"+planning.ShortID+".json", planning)

	snapshot, err := p.loadValidationSnapshot(nil)
	if err != nil {
		t.Fatalf("loadValidationSnapshot() error = %v", err)
	}
	if len(snapshot.tasks) != 1 || len(snapshot.planningItems) != 1 {
		t.Fatalf("snapshot partitions = %#v, want one record in each lifecycle", snapshot)
	}
}

func TestValidationSnapshotRejectsCrossLifecycleCycleAndMissingReference(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*core.Task, *core.PlanningItem)
		want      string
	}{
		{
			name: "cycle",
			configure: func(task *core.Task, item *core.PlanningItem) {
				task.Dependencies = []string{item.ID}
				item.Dependencies = []string{task.ID}
			},
			want: "cyclic dependency detected",
		},
		{
			name: "planning missing reference",
			configure: func(_ *core.Task, item *core.PlanningItem) {
				item.Dependencies = []string{"35c3806a-bd1b-424d-889b-29e5b06679b8"}
			},
			want: "does not exist",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			task := validTask("15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
			item := planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.PlanningStatusResearched)
			test.configure(&task, &item)
			if err := p.writeTask(task); err != nil {
				t.Fatalf("writeTask() error = %v", err)
			}
			writePlanningStorageItem(t, p.planningStatusDir(item.Status)+"/"+item.ShortID+".json", item)

			if _, err := p.GetTask(task.ShortID, ""); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("GetTask() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTaskAllocationSkipsPlanningShortIDsInItsBranchScope(t *testing.T) {
	root := t.TempDir()
	scope := core.NewBranchScope("feature/planning-index")
	p, err := New(root, scope)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	planning := planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-"+scope.BranchID+"-0001", core.PlanningStatusToplan)
	writePlanningStorageItem(t, p.planningStatusDir(planning.Status)+"/"+planning.ShortID+".json", planning)
	if err := p.writeIndex(indexFile{Branch: scope.Branch, Next: 1}); err != nil {
		t.Fatalf("writeIndex() error = %v", err)
	}

	created, err := p.CreateTask(core.CreateTaskInput{Title: "after planning allocation"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if want := "wtp-" + scope.BranchID + "-0002"; created.ShortID != want {
		t.Fatalf("short ID = %q, want %q", created.ShortID, want)
	}
}

func TestConcurrentTaskAllocationSkipsPlanningNamespace(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	planning := planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
	writePlanningStorageItem(t, p.planningStatusDir(planning.Status)+"/"+planning.ShortID+".json", planning)

	const count = 8
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			created, err := p.CreateTask(core.CreateTaskInput{Title: fmt.Sprintf("concurrent %d", i)})
			if err != nil {
				errs <- err
				return
			}
			ids <- created.ShortID
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}
	seen := map[string]bool{planning.ShortID: true}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate cross-namespace short ID %q", id)
		}
		seen[id] = true
	}
	if len(seen) != count+1 {
		t.Fatalf("allocated IDs = %v, want %d unique IDs", seen, count+1)
	}
}
