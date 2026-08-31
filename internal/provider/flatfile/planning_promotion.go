package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/reusablejson"
)

// NewReadOnly constructs a provider without creating or repairing any store
// paths. It is intended for preview/read-only operations against an existing
// store; PreviewPlanningPromotion also accepts an absent store and reports a
// deterministic no-match error.
func NewReadOnly(root string, invocationScope *core.BranchScope) (*Provider, error) {
	return NewReadOnlyWithCatalog(root, invocationScope, core.DefaultStatusCatalog())
}

// NewReadOnlyWithCatalog is the catalog-aware read-only provider constructor.
func NewReadOnlyWithCatalog(root string, invocationScope *core.BranchScope, catalog core.StatusCatalog) (*Provider, error) {
	if len(catalog.Statuses()) == 0 {
		catalog = core.DefaultStatusCatalog()
	}
	p := &Provider{root: root, catalog: catalog, fs: defaultFileSystem()}
	if invocationScope != nil {
		scope := *invocationScope
		p.invocationScope = &scope
	}
	if p.catalog.Contains(core.Status(planningDirectory)) {
		return nil, fmt.Errorf("configured execution status %q collides with reserved planning namespace", planningDirectory)
	}
	return p, nil
}

// PreviewPlanningPromotion selects and validates one dependency-closed group
// under the ordinary global lock. The read-only snapshot path deliberately
// bypasses recovery, metadata repair, residue cleanup, initialization,
// allocation, timestamps, and journals.
func (p *Provider) PreviewPlanningPromotion(grouping core.GroupingFilter) (provider.PlanningPromotionResult[core.PlanningItemView], error) {
	if !grouping.HasSelector() {
		return provider.PlanningPromotionResult[core.PlanningItemView]{}, errors.New("planning promotion requires at least one grouping selector")
	}
	result := provider.PlanningPromotionResult[core.PlanningItemView]{DryRun: true, Items: []core.PlanningItemView{}}
	if err := p.previewStorePresent(); err != nil {
		return provider.PlanningPromotionResult[core.PlanningItemView]{}, err
	}

	err := p.withGlobalLock(func() error {
		if err := p.rejectPendingJournalsForPreview(); err != nil {
			return err
		}
		snapshot, catalog, err := p.loadReadOnlyPlanningValidationSnapshot()
		if err != nil {
			return err
		}
		selected, err := core.SelectPlanningPromotion(snapshot.planningItems, snapshot.tasks, grouping)
		if err != nil {
			return err
		}
		result.Items, err = p.decoratePlanningItems(selected, catalog)
		if err != nil {
			return err
		}
		result.Count = len(result.Items)
		return nil
	})
	if err != nil {
		return provider.PlanningPromotionResult[core.PlanningItemView]{}, err
	}
	return result, nil
}

func (p *Provider) previewStorePresent() error {
	entries, err := os.ReadDir(p.root)
	if errors.Is(err, os.ErrNotExist) {
		return noPlanningPromotionMatch()
	}
	if err != nil {
		return fmt.Errorf("inspect planning promotion store %s: %w", p.root, err)
	}
	if len(entries) == 0 {
		return noPlanningPromotionMatch()
	}
	return nil
}

func noPlanningPromotionMatch() error {
	return errors.New("no planned planning items match promotion filters")
}

func (p *Provider) rejectPendingJournalsForPreview() error {
	paths := []string{
		p.batchUpdateJournalPath(),
		p.reusableUpdateJournalPath(),
		filepath.Join(p.root, "meta", planningPromoteJournalName),
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("planning promotion preview requires recovery: pending journal %s", filepath.Base(path))
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect planning promotion journal %s: %w", path, err)
		}
	}
	return nil
}

func (p *Provider) loadReadOnlyPlanningValidationSnapshot() (validationSnapshot, core.ReusableTaskCatalog, error) {
	catalog, err := p.loadReusableTaskCatalogReadOnly()
	if err != nil {
		return validationSnapshot{}, core.ReusableTaskCatalog{}, err
	}
	tasks, err := p.loadTaskPartitionReadOnly(&catalog)
	if err != nil {
		return validationSnapshot{}, core.ReusableTaskCatalog{}, err
	}
	planningItems, err := p.loadPlanningItemsWithCatalogReadOnly(&catalog)
	if err != nil {
		return validationSnapshot{}, core.ReusableTaskCatalog{}, err
	}
	if err := core.ValidateDependenciesAcrossLifecycles("", nil, tasks, planningItems); err != nil {
		return validationSnapshot{}, core.ReusableTaskCatalog{}, fmt.Errorf("invalid dependency graph: %w", err)
	}
	return validationSnapshot{tasks: tasks, planningItems: planningItems}, catalog, nil
}

func (p *Provider) loadReusableTaskCatalogReadOnly() (core.ReusableTaskCatalog, error) {
	catalog, err := reusablejson.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		return core.ReusableTaskCatalog{}, err
	}
	if err := catalog.Validate(); err != nil {
		return core.ReusableTaskCatalog{}, fmt.Errorf("validate reusable catalog: %w", err)
	}
	return catalog, nil
}

func (p *Provider) loadTaskPartitionReadOnly(reusableCatalog *core.ReusableTaskCatalog) ([]core.Task, error) {
	if err := p.validateUnconfiguredStatusDirs(); err != nil {
		return nil, err
	}
	tasksByID := make(map[string]core.Task)
	pathsByID := make(map[string]string)
	idsByShortID := make(map[string]string)
	var residuePaths []string
	for _, status := range p.statuses() {
		dir := p.statusDir(status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			var task core.Task
			if err := readJSON(path, &task); err != nil {
				return nil, fmt.Errorf("corrupt task file %s: %w", path, err)
			}
			if task.Status != status {
				return nil, fmt.Errorf("task file %s status %s does not match directory %s", path, task.Status, status)
			}
			if err := task.ValidateWithCatalog(p.catalog); err != nil {
				_, repairable, repairErr := p.validateOrRepairTask(task, time.Time{})
				if repairErr == nil && repairable {
					return nil, fmt.Errorf("planning promotion preview requires recovery: task file %s needs metadata repair", path)
				}
				return nil, fmt.Errorf("invalid task file %s: %w", path, err)
			}
			if !isTaskFilename(entry.Name(), task) {
				return nil, invalidTaskFilenameError(path, task)
			}
			if existingID, ok := idsByShortID[task.ShortID]; ok && existingID != task.ID {
				return nil, fmt.Errorf("task shortId %s is used by both %s and %s", task.ShortID, existingID, task.ID)
			}
			idsByShortID[task.ShortID] = task.ID
			existing, ok := tasksByID[task.ID]
			if !ok {
				tasksByID[task.ID] = task
				pathsByID[task.ID] = path
				continue
			}
			if existing.ShortID != task.ShortID {
				return nil, fmt.Errorf("task %s has conflicting shortIds %s and %s", task.ID, existing.ShortID, task.ShortID)
			}
			if task.UpdatedAt.Equal(existing.UpdatedAt) {
				if reflect.DeepEqual(existing, task) {
					return nil, fmt.Errorf("duplicate canonical task id %s in %s and %s", task.ID, pathsByID[task.ID], path)
				}
				return nil, fmt.Errorf("conflicting task copies %s and %s have the same updatedAt", pathsByID[task.ID], path)
			}
			older, newer := existing, task
			olderPath, newerPath := pathsByID[task.ID], path
			if existing.UpdatedAt.After(task.UpdatedAt) {
				older, newer = task, existing
				olderPath, newerPath = path, pathsByID[task.ID]
			}
			if !p.isStatusMoveResidue(older, newer) {
				return nil, fmt.Errorf("duplicate canonical task id %s in %s and %s is not valid status-move residue", task.ID, olderPath, newerPath)
			}
			tasksByID[task.ID] = newer
			pathsByID[task.ID] = newerPath
			residuePaths = append(residuePaths, olderPath)
		}
	}
	if len(residuePaths) > 0 {
		sort.Strings(residuePaths)
		return nil, fmt.Errorf("planning promotion preview requires recovery: task status-move residue remains at %s", residuePaths[0])
	}
	tasks := make([]core.Task, 0, len(tasksByID))
	for _, task := range tasksByID {
		if reusableCatalog != nil {
			if _, err := core.ResolveReusableTasks(task.ReusableTaskIDs, *reusableCatalog); err != nil {
				return nil, fmt.Errorf("task %s has unresolved reusableTaskIds: %w", task.ShortID, err)
			}
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// Keep the method's provider capability explicit.
var _ interface {
	PreviewPlanningPromotion(core.GroupingFilter) (provider.PlanningPromotionResult[core.PlanningItemView], error)
} = (*Provider)(nil)
