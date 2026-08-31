package flatfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
)

// loadPlanningItems reads only the nested planning namespace. It deliberately
// does not migrate legacy UUID filenames or repair metadata: planning records
// have no execution lifecycle repair path. The only allowed read-side change
// is best-effort cleanup of a fully validated older status-move residue.
// Callers hold the global lock when a stable store snapshot is required.
func (p *Provider) loadPlanningItems() ([]core.PlanningItem, error) {
	catalog, err := p.loadReusableTaskCatalog()
	if err != nil {
		return nil, fmt.Errorf("load reusable catalog for planning records: %w", err)
	}
	return p.loadPlanningItemsWithCatalog(&catalog)
}

func (p *Provider) loadPlanningItemsWithCatalog(reusableCatalog *core.ReusableTaskCatalog) ([]core.PlanningItem, error) {
	return p.loadPlanningItemsWithCatalogMode(reusableCatalog, true)
}

// loadPlanningItemsWithCatalogReadOnly performs the same validation as the
// normal planning loader but refuses to clean status-move residue. Preview
// must report that recovery is required rather than changing the store while
// answering a read request.
func (p *Provider) loadPlanningItemsWithCatalogReadOnly(reusableCatalog *core.ReusableTaskCatalog) ([]core.PlanningItem, error) {
	return p.loadPlanningItemsWithCatalogMode(reusableCatalog, false)
}

func (p *Provider) loadPlanningItemsWithCatalogMode(reusableCatalog *core.ReusableTaskCatalog, cleanResidue bool) ([]core.PlanningItem, error) {
	if err := p.validatePlanningNamespace(); err != nil {
		return nil, err
	}

	itemsByID := make(map[string]core.PlanningItem)
	pathsByID := make(map[string]string)
	idsByShortID := make(map[string]string)
	var residuePaths []string
	for _, status := range core.PlanningStatuses() {
		dir := p.planningStatusDir(status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read planning status directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				nestedPath := filepath.Join(dir, entry.Name())
				contains, err := containsJSONFile(nestedPath)
				if err != nil {
					return nil, fmt.Errorf("scan nested planning directory %s: %w", nestedPath, err)
				}
				if contains {
					return nil, fmt.Errorf("planning status directory %s contains nested JSON record %s", dir, nestedPath)
				}
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read planning record %s: %w", path, err)
			}
			item, err := planningjson.Decode(data)
			if err != nil {
				return nil, fmt.Errorf("corrupt planning record %s: %w", path, err)
			}
			if item.Status != status {
				return nil, fmt.Errorf("planning record %s status %s does not match directory %s", path, item.Status, status)
			}
			if !isPlanningItemFilename(entry.Name(), item) {
				return nil, invalidPlanningItemFilenameError(path, item)
			}
			if existingID, ok := idsByShortID[item.ShortID]; ok && existingID != item.ID {
				return nil, fmt.Errorf("planning shortId %s is used by both %s and %s", item.ShortID, existingID, item.ID)
			}
			idsByShortID[item.ShortID] = item.ID

			existing, ok := itemsByID[item.ID]
			if !ok {
				itemsByID[item.ID] = item
				pathsByID[item.ID] = path
				continue
			}
			if existing.ShortID != item.ShortID {
				return nil, fmt.Errorf("planning record %s has conflicting shortIds %s and %s", item.ID, existing.ShortID, item.ShortID)
			}
			if item.UpdatedAt.Equal(existing.UpdatedAt) {
				if reflect.DeepEqual(existing, item) {
					return nil, fmt.Errorf("duplicate canonical planning id %s in %s and %s", item.ID, pathsByID[item.ID], path)
				}
				return nil, fmt.Errorf("conflicting planning copies %s and %s have the same updatedAt", pathsByID[item.ID], path)
			}
			older, newer := existing, item
			olderPath, newerPath := pathsByID[item.ID], path
			if existing.UpdatedAt.After(item.UpdatedAt) {
				older, newer = item, existing
				olderPath, newerPath = path, pathsByID[item.ID]
			}
			if !isPlanningStatusMoveResidue(older, newer) {
				return nil, fmt.Errorf("duplicate canonical planning id %s in %s and %s is not valid status-move residue", item.ID, olderPath, newerPath)
			}
			itemsByID[item.ID] = newer
			pathsByID[item.ID] = newerPath
			residuePaths = append(residuePaths, olderPath)
		}
	}

	items := make([]core.PlanningItem, 0, len(itemsByID))
	for _, item := range itemsByID {
		items = append(items, item)
	}
	if !cleanResidue && len(residuePaths) > 0 {
		sort.Strings(residuePaths)
		return nil, fmt.Errorf("planning promotion preview requires recovery: planning status-move residue remains at %s", residuePaths[0])
	}
	// Cleanup follows execution residue behavior: it happens only after the
	// whole planning namespace is valid, and a deletion failure leaves the
	// newer complete record available for a future retry.
	if cleanResidue {
		for _, path := range residuePaths {
			_ = p.removeFile(path)
		}
	}
	if reusableCatalog != nil {
		for _, item := range items {
			if _, err := core.ResolveReusableTasks(item.ReusableTaskIDs, *reusableCatalog); err != nil {
				return nil, fmt.Errorf("planning item %s has unresolved reusableTaskIds: %w", item.ShortID, err)
			}
		}
	}
	return items, nil
}

// validatePlanningNamespace prevents the planning root from being treated as
// an arbitrary execution-status directory. Records must appear directly in a
// configured planning status leaf; empty auxiliary directories are harmless.
func (p *Provider) validatePlanningNamespace() error {
	entries, err := os.ReadDir(p.planningDir())
	if err != nil {
		return fmt.Errorf("read planning storage root %s: %w", p.planningDir(), err)
	}
	configured := make(map[string]struct{}, len(core.PlanningStatuses()))
	for _, status := range core.PlanningStatuses() {
		configured[string(status)] = struct{}{}
	}
	for _, entry := range entries {
		path := filepath.Join(p.planningDir(), entry.Name())
		if !entry.IsDir() {
			if strings.HasSuffix(entry.Name(), ".json") {
				return fmt.Errorf("planning record %s is stored directly in planning namespace", path)
			}
			continue
		}
		if _, ok := configured[entry.Name()]; ok {
			continue
		}
		contains, err := containsJSONFile(path)
		if err != nil {
			return fmt.Errorf("scan unconfigured planning status directory %s: %w", path, err)
		}
		if contains {
			return fmt.Errorf("planning record is stored in unconfigured planning status directory %s", path)
		}
	}
	return nil
}

func containsJSONFile(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

func (p *Provider) planningDir() string {
	return filepath.Join(p.root, planningDirectory)
}

func (p *Provider) planningStatusDir(status core.PlanningStatus) string {
	return filepath.Join(p.planningDir(), string(status))
}

func planningItemFilenames(item core.PlanningItem) []string {
	return []string{item.ShortID, item.ID}
}

func isPlanningItemFilename(name string, item core.PlanningItem) bool {
	return name == item.ShortID+".json" || name == item.ID+".json"
}

func invalidPlanningItemFilenameError(path string, item core.PlanningItem) error {
	return fmt.Errorf("planning record %s must use shortId filename %s (or canonical UUID legacy filename)", path, item.ShortID+".json")
}

func isPlanningStatusMoveResidue(older, newer core.PlanningItem) bool {
	if older.ID != newer.ID || older.ShortID != newer.ShortID || older.Status == newer.Status || !core.AllowedPlanningTransition(older.Status, newer.Status) {
		return false
	}
	want := older
	want.Status = newer.Status
	want.UpdatedAt = newer.UpdatedAt
	return reflect.DeepEqual(want, newer)
}
