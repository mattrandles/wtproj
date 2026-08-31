package flatfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestNewInitializesEmptyPlanningStorage(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	items, err := p.loadPlanningItems()
	if err != nil {
		t.Fatalf("loadPlanningItems() error = %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("empty planning items = %#v, want non-nil empty slice", items)
	}
	for _, status := range core.PlanningStatuses() {
		path := filepath.Join(root, planningDirectory, string(status))
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("planning status directory %s missing: info=%v err=%v", status, info, err)
		}
	}
}

func TestPlanningLoaderAcceptsShortAndLegacyFilenamesWithoutRewrite(t *testing.T) {
	root := t.TempDir()
	legacy := planningStorageTask(t, "15c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusPlanned)
	legacyPath := filepath.Join(root, planningDirectory, string(legacy.Status), legacy.ID+".json")
	writePlanningStorageItem(t, legacyPath, legacy)
	canonical := planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.PlanningStatusToplan)
	canonicalPath := filepath.Join(root, planningDirectory, string(canonical.Status), canonical.ShortID+".json")
	writePlanningStorageItem(t, canonicalPath, canonical)
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy) error = %v", err)
	}
	canonicalBefore, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("ReadFile(canonical) error = %v", err)
	}

	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	items, err := p.loadPlanningItems()
	if err != nil {
		t.Fatalf("loadPlanningItems() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("planning items = %#v, want both filename forms", items)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy after open) error = %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("valid legacy planning record was rewritten:\nbefore: %s\nafter:  %s", before, after)
	}
	canonicalAfter, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("ReadFile(canonical after open) error = %v", err)
	}
	if !bytes.Equal(canonicalAfter, canonicalBefore) {
		t.Fatalf("valid short-ID planning record was rewritten:\nbefore: %s\nafter:  %s", canonicalBefore, canonicalAfter)
	}
	if _, err := os.Stat(filepath.Join(root, planningDirectory, string(legacy.Status), legacy.ShortID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy UUID planning filename was migrated, err=%v", err)
	}
}

func TestPlanningLoaderRejectsMalformedWrongDirectoryAndNestedRecords(t *testing.T) {
	tests := []struct {
		name  string
		write func(t *testing.T, root string)
		want  string
	}{
		{
			name: "malformed JSON in configured planning status", want: "corrupt planning record",
			write: func(t *testing.T, root string) {
				path := filepath.Join(root, planningDirectory, string(core.PlanningStatusToplan), "wtp-0001.json")
				if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "status does not match directory", want: "does not match directory",
			write: func(t *testing.T, root string) {
				item := planningStorageTask(t, "25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusPlanned)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(core.PlanningStatusResearched), item.ShortID+".json"), item)
			},
		},
		{
			name: "record directly in planning root", want: "stored directly in planning namespace",
			write: func(t *testing.T, root string) {
				item := planningStorageTask(t, "35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, item.ShortID+".json"), item)
			},
		},
		{
			name: "record in unknown nested planning status", want: "unconfigured planning status directory",
			write: func(t *testing.T, root string) {
				item := planningStorageTask(t, "45c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, "draft", "nested", item.ShortID+".json"), item)
			},
		},
		{
			name: "record hidden below configured status", want: "contains nested JSON record",
			write: func(t *testing.T, root string) {
				item := planningStorageTask(t, "55c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(item.Status), "collision", item.ShortID+".json"), item)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := New(root, nil); err != nil {
				t.Fatalf("New() setup error = %v", err)
			}
			test.write(t, root)
			if _, err := New(root, nil); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPlanningLoaderRejectsDuplicateIdentities(t *testing.T) {
	tests := []struct {
		name  string
		write func(t *testing.T, root string)
		want  string
	}{
		{
			name: "identical canonical ID copies", want: "duplicate canonical planning id",
			write: func(t *testing.T, root string) {
				item := planningStorageTask(t, "65c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(item.Status), item.ShortID+".json"), item)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(item.Status), item.ID+".json"), item)
			},
		},
		{
			name: "different IDs share short ID", want: "planning shortId wtp-0001 is used by both",
			write: func(t *testing.T, root string) {
				first := planningStorageTask(t, "75c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
				second := planningStorageTask(t, "85c3806a-bd1b-424d-889b-29e5b06679b8", first.ShortID, core.PlanningStatusResearched)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(first.Status), first.ShortID+".json"), first)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(second.Status), second.ShortID+".json"), second)
			},
		},
		{
			name: "same ID has conflicting short IDs", want: "conflicting shortIds",
			write: func(t *testing.T, root string) {
				first := planningStorageTask(t, "95c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
				second := planningStorageTask(t, first.ID, "wtp-0002", core.PlanningStatusResearched)
				second.UpdatedAt = second.UpdatedAt.Add(time.Nanosecond)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(first.Status), first.ShortID+".json"), first)
				writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(second.Status), second.ShortID+".json"), second)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := New(root, nil); err != nil {
				t.Fatalf("New() setup error = %v", err)
			}
			test.write(t, root)
			if _, err := New(root, nil); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPlanningLoaderRecognizesOnlyValidStatusMoveResidue(t *testing.T) {
	root := t.TempDir()
	if _, err := New(root, nil); err != nil {
		t.Fatalf("New() setup error = %v", err)
	}
	older := planningStorageTask(t, "a5c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.PlanningStatusToplan)
	newer := older
	newer.Status = core.PlanningStatusResearched
	newer.UpdatedAt = newer.UpdatedAt.Add(time.Nanosecond)
	olderPath := filepath.Join(root, planningDirectory, string(older.Status), older.ShortID+".json")
	writePlanningStorageItem(t, olderPath, older)
	writePlanningStorageItem(t, filepath.Join(root, planningDirectory, string(newer.Status), newer.ShortID+".json"), newer)

	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() with status-move residue error = %v", err)
	}
	items, err := p.loadPlanningItems()
	if err != nil {
		t.Fatalf("loadPlanningItems() error = %v", err)
	}
	if len(items) != 1 || items[0].Status != core.PlanningStatusResearched {
		t.Fatalf("planning items after cleanup = %#v", items)
	}
	if _, err := os.Stat(olderPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("older planning status-move residue remains, err=%v", err)
	}

	invalidRoot := t.TempDir()
	if _, err := New(invalidRoot, nil); err != nil {
		t.Fatalf("New() invalid setup error = %v", err)
	}
	invalidOlder := planningStorageTask(t, "b5c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.PlanningStatusToplan)
	invalidNewer := invalidOlder
	invalidNewer.Status = core.PlanningStatusPlanned // toplan -> planned is not a direct transition.
	invalidNewer.UpdatedAt = invalidNewer.UpdatedAt.Add(time.Nanosecond)
	writePlanningStorageItem(t, filepath.Join(invalidRoot, planningDirectory, string(invalidOlder.Status), invalidOlder.ShortID+".json"), invalidOlder)
	writePlanningStorageItem(t, filepath.Join(invalidRoot, planningDirectory, string(invalidNewer.Status), invalidNewer.ShortID+".json"), invalidNewer)
	if _, err := New(invalidRoot, nil); err == nil || !strings.Contains(err.Error(), "not valid status-move residue") {
		t.Fatalf("New() invalid residue error = %v", err)
	}
}

func TestNewRejectsExecutionStatusPlanningNamespaceCollision(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{{Name: core.Status(planningDirectory), Category: core.StatusCategoryWaiting}})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	if _, err := NewWithCatalog(t.TempDir(), nil, catalog); err == nil || !strings.Contains(err.Error(), "reserved planning namespace") {
		t.Fatalf("NewWithCatalog() error = %v, want planning namespace collision", err)
	}
}

func TestPlanningStoreLoadsRejectDeletedReusableDefinitionAcrossOperations(t *testing.T) {
	type operation struct {
		name string
		call func(*Provider, core.PlanningItem) error
	}
	operations := []operation{
		{name: "list", call: func(p *Provider, _ core.PlanningItem) error {
			_, err := p.ListPlanningItems(provider.PlanningFilter{Status: planningStatusPointer(core.PlanningStatusRejected)})
			return err
		}},
		{name: "show", call: func(p *Provider, item core.PlanningItem) error {
			_, err := p.GetPlanningItem(item.ID)
			return err
		}},
		{name: "create", call: func(p *Provider, _ core.PlanningItem) error {
			_, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "blocked by corruption"})
			return err
		}},
		{name: "update", call: func(p *Provider, item core.PlanningItem) error {
			_, err := p.UpdatePlanningItem(item.ID, core.UpdatePlanningItemInput{Title: core.OptionalString{Set: true, Value: "updated"}})
			return err
		}},
		{name: "set-status", call: func(p *Provider, item core.PlanningItem) error {
			_, err := p.SetPlanningStatus(item.ID, core.PlanningStatusResearched)
			return err
		}},
	}
	for _, test := range operations {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			p, err := New(root, nil)
			if err != nil {
				t.Fatalf("New() setup error = %v", err)
			}
			definition, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Deleted", Title: "Deleted title", Instructions: "Deleted instructions"})
			if err != nil {
				t.Fatalf("CreateReusableTask() error = %v", err)
			}
			created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "corrupt", ReusableTasks: []string{definition.ID}})
			if err != nil {
				t.Fatalf("CreatePlanningItem() error = %v", err)
			}
			catalog := core.EmptyReusableTaskCatalog()
			writeReusableCatalogForTest(t, p, catalog)
			if err := test.call(p, created.PlanningItem); err == nil || !strings.Contains(err.Error(), "planning item "+created.ShortID+" has unresolved reusableTaskIds") || !strings.Contains(err.Error(), "does not exist") {
				t.Fatalf("%s error = %v, want deleted-definition corruption", test.name, err)
			}
		})
	}

	root := t.TempDir()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New() setup error = %v", err)
	}
	definition, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Deleted", Title: "Deleted title", Instructions: "Deleted instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	created, err := p.CreatePlanningItem(core.CreatePlanningItemInput{Title: "corrupt on reopen", ReusableTasks: []string{definition.ID}})
	if err != nil {
		t.Fatalf("CreatePlanningItem() error = %v", err)
	}
	writeReusableCatalogForTest(t, p, core.EmptyReusableTaskCatalog())
	if _, err := New(root, nil); err == nil || !strings.Contains(err.Error(), "planning item "+created.ShortID+" has unresolved reusableTaskIds") {
		t.Fatalf("New() after deleted-definition corruption error = %v", err)
	}
}

func planningStatusPointer(status core.PlanningStatus) *core.PlanningStatus {
	return &status
}

func planningStorageTask(t *testing.T, id, shortID string, status core.PlanningStatus) core.PlanningItem {
	t.Helper()
	created, err := time.Parse(time.RFC3339Nano, "2026-08-31T08:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse() error = %v", err)
	}
	return core.PlanningItem{
		ID: id, ShortID: shortID, Title: "Planning storage", Status: status,
		Dependencies: []string{}, Comments: []core.Comment{}, CreatedAt: created, UpdatedAt: created,
	}
}

func writePlanningStorageItem(t *testing.T, path string, item core.PlanningItem) {
	t.Helper()
	data, err := planningjson.Encode(item)
	if err != nil {
		t.Fatalf("planningjson.Encode() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
