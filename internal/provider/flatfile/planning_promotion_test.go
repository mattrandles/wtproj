package flatfile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestPreviewPlanningPromotionReturnsExactPlannedSelectionWithoutMutation(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	executable, err := p.CreateTask(core.CreateTaskInput{Title: "executable dependency"})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	planned, err := p.CreatePlanningItem(core.CreatePlanningItemInput{
		Title: "planned promotion", Status: core.PlanningStatusPlanned, Project: "Apollo", Feature: "Search",
		Dependencies: []string{executable.ShortID},
	})
	if err != nil {
		t.Fatalf("CreatePlanningItem(planned) error = %v", err)
	}
	if _, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "researched", Status: core.PlanningStatusResearched, Project: "Apollo", Feature: "Search"}); err != nil {
		t.Fatalf("CreatePlanningItem(researched) error = %v", err)
	}

	before := snapshotRegularFiles(t, root)
	got, err := p.PreviewPlanningPromotion(core.GroupingFilter{Project: " apOLLo ", Feature: "SEARCH"})
	if err != nil {
		t.Fatalf("PreviewPlanningPromotion() error = %v", err)
	}
	if !got.DryRun || got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("preview result = %#v, want dryRun/count/items 1", got)
	}
	if !reflect.DeepEqual(got.Items[0].PlanningItem, planned.PlanningItem) {
		t.Fatalf("preview item = %#v, want exact planned record %#v", got.Items[0].PlanningItem, planned.PlanningItem)
	}
	if after := snapshotRegularFiles(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("preview changed persistent store:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestPreviewPlanningPromotionRejectsTransitiveMissingPlanningDependency(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	missing, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "outside", Status: core.PlanningStatusPlanned, Project: "Other"})
	if err != nil {
		t.Fatalf("CreatePlanningItem(missing) error = %v", err)
	}
	executable, err := p.CreateTask(core.CreateTaskInput{Title: "bridge", Dependencies: []string{missing.ShortID}})
	if err != nil {
		t.Fatalf("CreateTask(bridge) error = %v", err)
	}
	if _, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "root", Status: core.PlanningStatusPlanned, Project: "Apollo", Dependencies: []string{executable.ShortID}}); err != nil {
		t.Fatalf("CreatePlanningItem(root) error = %v", err)
	}

	_, err = p.PreviewPlanningPromotion(core.GroupingFilter{Project: "Apollo"})
	if err == nil || !strings.Contains(err.Error(), "wtp-0003 (planning planned)") || !strings.Contains(err.Error(), "wtp-0001") {
		t.Fatalf("PreviewPlanningPromotion() error = %v, want deterministic missing chain", err)
	}
}

func TestPreviewPlanningPromotionUsesStorageCycleValidation(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	task := validTask("15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
	item := planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.PlanningStatusPlanned)
	task.Dependencies = []string{item.ID}
	item.Dependencies = []string{task.ID}
	if err := p.writeTask(task); err != nil {
		t.Fatalf("writeTask() error = %v", err)
	}
	writePlanningStorageItem(t, p.planningStatusDir(item.Status)+"/"+item.ShortID+".json", item)

	_, err = p.PreviewPlanningPromotion(core.GroupingFilter{Project: "unused"})
	if err == nil || !strings.Contains(err.Error(), "cyclic dependency detected") {
		t.Fatalf("PreviewPlanningPromotion() error = %v, want storage cycle validation", err)
	}
}

func TestNewReadOnlyAndPreviewDoNotCreateAbsentStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent", ".wtp")
	p, err := NewReadOnly(root, nil)
	if err != nil {
		t.Fatalf("NewReadOnly() error = %v", err)
	}
	_, err = p.PreviewPlanningPromotion(core.GroupingFilter{Project: "Apollo"})
	if err == nil || !strings.Contains(err.Error(), "no planned planning items match promotion filters") {
		t.Fatalf("PreviewPlanningPromotion() error = %v, want no-match", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("read-only preview created absent store: stat error = %v", statErr)
	}
}

func snapshotRegularFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || strings.HasSuffix(info.Name(), ".lock") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = append([]byte(nil), data...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
