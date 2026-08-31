package flatfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/reusablejson"
)

// generateReusableTaskID is a seam for collision coverage. Production uses
// cryptographically-random version-4 UUIDs from core.NewID.
var generateReusableTaskID = core.NewID

var _ provider.ReusableTaskProvider = (*Provider)(nil)
var _ provider.ReusableTaskMutationProvider = (*Provider)(nil)

const reusableFilename = "reusable.json"

// ListReusableTasks returns the store-global reusable definitions in a stable
// order. It deliberately does not use invocationScope: catalog definitions
// belong to the task store, rather than to one branch's task namespace.
func (p *Provider) ListReusableTasks() ([]core.ReusableTaskDefinition, error) {
	definitions := []core.ReusableTaskDefinition{}
	err := p.withGlobalLock(func() error {
		catalog, err := p.loadReusableTaskCatalog()
		if err != nil {
			return err
		}
		definitions = append(definitions, catalog.Definitions...)
		sort.Slice(definitions, func(i, j int) bool {
			left, right := strings.ToLower(definitions[i].Name), strings.ToLower(definitions[j].Name)
			if left == right {
				return definitions[i].ID < definitions[j].ID
			}
			return left < right
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return definitions, nil
}

// GetReusableTask resolves a canonical UUID before a case-insensitive exact
// name. It returns the stored definition unchanged, including its stable ID.
func (p *Provider) GetReusableTask(nameOrID string) (core.ReusableTaskDefinition, error) {
	var definition core.ReusableTaskDefinition
	err := p.withGlobalLock(func() error {
		catalog, err := p.loadReusableTaskCatalog()
		if err != nil {
			return err
		}
		definition, err = resolveReusableTaskDefinition(nameOrID, catalog)
		return err
	})
	if err != nil {
		return core.ReusableTaskDefinition{}, err
	}
	return definition, nil
}

// CreateReusableTask appends one new store-global definition after validating
// the complete resulting catalog, then publishes its codec bytes atomically.
func (p *Provider) CreateReusableTask(input core.CreateReusableTaskInput) (core.ReusableTaskDefinition, error) {
	var created core.ReusableTaskDefinition
	err := p.withGlobalLock(func() error {
		catalog, err := p.loadReusableTaskCatalog()
		if err != nil {
			return err
		}

		id, err := nextReusableTaskID(catalog)
		if err != nil {
			return err
		}
		now := nextReusableCatalogMutationTime(catalog)
		definition := core.ReusableTaskDefinition{
			ID:           id,
			Name:         strings.TrimSpace(input.Name),
			Title:        strings.TrimSpace(input.Title),
			Instructions: strings.TrimSpace(input.Instructions),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		catalog.Definitions = append(catalog.Definitions, definition)
		if err := catalog.Validate(); err != nil {
			return err
		}
		data, err := reusablejson.Encode(catalog)
		if err != nil {
			return fmt.Errorf("encode reusable catalog: %w", err)
		}
		if err := writeBytesAtomicWithFileSystem(p.reusableTaskCatalogPath(), data, p.fs); err != nil {
			return fmt.Errorf("write reusable catalog: %w", err)
		}
		created = definition
		return nil
	})
	if err != nil {
		return core.ReusableTaskDefinition{}, err
	}
	return created, nil
}

// UpdateReusableTask applies a partial edit to one store-global definition.
// The selector is resolved before the edit, so a rename changes only the
// definition's display name; UUID references stored on tasks remain valid.
// Normalized no-ops return the existing definition without publishing a new
// catalog or advancing any timestamp.
func (p *Provider) UpdateReusableTask(nameOrID string, input core.UpdateReusableTaskInput) (core.ReusableTaskDefinition, error) {
	if !input.Name.Set && !input.Title.Set && !input.Instructions.Set {
		return core.ReusableTaskDefinition{}, errors.New("reusable definition update requires at least one field")
	}

	var updated core.ReusableTaskDefinition
	err := p.withGlobalLock(func() error {
		catalog, err := p.loadReusableTaskCatalog()
		if err != nil {
			return err
		}
		index := -1
		for candidateIndex, definition := range catalog.Definitions {
			if definition.ID == strings.TrimSpace(nameOrID) {
				index = candidateIndex
				break
			}
		}
		if index == -1 {
			selector := strings.TrimSpace(nameOrID)
			if selector == "" {
				return errors.New("reusable task selector is required")
			}
			for candidateIndex, definition := range catalog.Definitions {
				if strings.EqualFold(definition.Name, selector) {
					index = candidateIndex
					break
				}
			}
		}
		if index == -1 {
			return fmt.Errorf("reusable task %q not found", strings.TrimSpace(nameOrID))
		}

		before := catalog.Definitions[index]
		candidate := before
		if input.Name.Set {
			candidate.Name = strings.TrimSpace(input.Name.Value)
		}
		if input.Title.Set {
			candidate.Title = strings.TrimSpace(input.Title.Value)
		}
		if input.Instructions.Set {
			candidate.Instructions = strings.TrimSpace(input.Instructions.Value)
		}
		if candidate == before {
			updated = before
			return nil
		}

		candidate.UpdatedAt = nextReusableCatalogMutationTime(catalog)
		catalog.Definitions[index] = candidate
		if err := catalog.Validate(); err != nil {
			return err
		}
		data, err := reusablejson.Encode(catalog)
		if err != nil {
			return fmt.Errorf("encode reusable catalog: %w", err)
		}
		if err := writeBytesAtomicWithFileSystem(p.reusableTaskCatalogPath(), data, p.fs); err != nil {
			return fmt.Errorf("write reusable catalog: %w", err)
		}
		updated = candidate
		return nil
	})
	if err != nil {
		return core.ReusableTaskDefinition{}, err
	}
	return updated, nil
}

// DeleteReusableTask removes one store-global definition and detaches its UUID
// from every task in every configured status directory. The complete catalog
// and task endpoint set is journaled before the first replacement, so readers
// and writers sharing this store observe either the before or after state.
func (p *Provider) DeleteReusableTask(nameOrID string) (provider.ReusableTaskDeleteResult, error) {
	var result provider.ReusableTaskDeleteResult
	err := p.withGlobalLock(func() error {
		beforeCatalog, err := p.loadReusableTaskCatalog()
		if err != nil {
			return err
		}
		deleted, err := resolveReusableTaskDefinition(nameOrID, beforeCatalog)
		if err != nil {
			return err
		}

		// Reusable definitions are shared by all branch-scoped providers in this
		// store. Use the read-only scan here instead of loadTasks: deletion must
		// never trigger task metadata repair, residue cleanup, or an audit comment
		// on an otherwise unrelated task.
		tasks, err := p.loadTasksForReusableDelete()
		if err != nil {
			return err
		}
		afterCatalog := beforeCatalog
		afterCatalog.Definitions = append([]core.ReusableTaskDefinition(nil), beforeCatalog.Definitions...)
		for index, definition := range afterCatalog.Definitions {
			if definition.ID != deleted.ID {
				continue
			}
			afterCatalog.Definitions = append(afterCatalog.Definitions[:index], afterCatalog.Definitions[index+1:]...)
			break
		}
		if err := afterCatalog.Validate(); err != nil {
			return fmt.Errorf("validate reusable catalog after delete: %w", err)
		}

		beforeCatalogData, err := os.ReadFile(p.reusableTaskCatalogPath())
		if err != nil {
			return fmt.Errorf("read reusable catalog before delete: %w", err)
		}
		afterCatalogData, err := reusablejson.Encode(afterCatalog)
		if err != nil {
			return fmt.Errorf("encode reusable catalog after delete: %w", err)
		}
		journal := reusableUpdateJournal{
			Version:        reusableUpdateJournalVersion,
			State:          reusableUpdateJournalPrepared,
			ReusableTaskID: deleted.ID,
			Catalog: reusableUpdateJournalChange{
				Before: reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: append([]byte(nil), beforeCatalogData...)},
				After:  reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: afterCatalogData},
			},
			AffectedTasks: []reusableUpdateJournalChange{},
		}

		for _, task := range tasks {
			if !slices.Contains(task.ReusableTaskIDs, deleted.ID) {
				continue
			}
			beforeData, target, err := p.snapshotReusableTask(task)
			if err != nil {
				return fmt.Errorf("snapshot task %s for reusable delete: %w", task.ShortID, err)
			}
			after := cloneTask(task)
			after.ReusableTaskIDs = detachReusableTaskID(task.ReusableTaskIDs, deleted.ID)
			after.UpdatedAt = nextTaskMutationTime(task)
			if err := after.ValidateWithCatalog(p.catalog); err != nil {
				return fmt.Errorf("validate detached task %s: %w", task.ShortID, err)
			}
			afterData, err := encodeReusableTaskSnapshot(after)
			if err != nil {
				return fmt.Errorf("encode detached task %s: %w", task.ShortID, err)
			}
			journal.AffectedTasks = append(journal.AffectedTasks, reusableUpdateJournalChange{
				Before: reusableUpdateJournalSnapshot{Path: target, Exists: true, Data: beforeData},
				After:  reusableUpdateJournalSnapshot{Path: target, Exists: true, Data: afterData},
			})
		}

		if err := p.publishReusableUpdate(journal); err != nil {
			return err
		}
		result = provider.ReusableTaskDeleteResult{Deleted: deleted, DetachedTaskCount: len(journal.AffectedTasks)}
		return nil
	})
	if err != nil {
		return provider.ReusableTaskDeleteResult{}, err
	}
	return result, nil
}

func (p *Provider) loadTasksForReusableDelete() ([]core.Task, error) {
	if err := p.validateUnconfiguredStatusDirs(); err != nil {
		return nil, err
	}
	tasks := make([]core.Task, 0)
	seenIDs := make(map[string]string)
	seenShortIDs := make(map[string]string)
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
				return nil, fmt.Errorf("invalid task file %s: %w", path, err)
			}
			if !isTaskFilename(entry.Name(), task) {
				return nil, invalidTaskFilenameError(path, task)
			}
			if previous, exists := seenIDs[task.ID]; exists {
				return nil, fmt.Errorf("duplicate canonical task id %s in %s and %s", task.ID, previous, path)
			}
			if previous, exists := seenShortIDs[task.ShortID]; exists {
				return nil, fmt.Errorf("task shortId %s is used by both %s and %s", task.ShortID, previous, path)
			}
			seenIDs[task.ID] = path
			seenShortIDs[task.ShortID] = path
			tasks = append(tasks, task)
		}
	}
	if err := core.ValidateDependenciesWithCatalog(p.catalog, "", nil, tasks); err != nil {
		return nil, fmt.Errorf("invalid dependency graph: %w", err)
	}
	sort.Slice(tasks, func(i, j int) bool {
		left := filepath.ToSlash(filepath.Join(string(tasks[i].Status), tasks[i].ShortID))
		right := filepath.ToSlash(filepath.Join(string(tasks[j].Status), tasks[j].ShortID))
		return left < right
	})
	return tasks, nil
}

// snapshotReusableTask returns the exact bytes at the canonical task target.
// Store initialization migrates legacy UUID filenames before mutations, and
// all normal task writers use this short-ID target.
func (p *Provider) snapshotReusableTask(task core.Task) ([]byte, string, error) {
	target := filepath.ToSlash(filepath.Join(string(task.Status), task.ShortID+".json"))
	path, err := p.resolveReusableUpdateTarget(target)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return append([]byte(nil), data...), target, nil
}

func encodeReusableTaskSnapshot(task core.Task) ([]byte, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// publishReusableUpdate executes the prepared/committed endpoint protocol.
// Catalog is endpoint one and affected tasks follow in journal order; every
// failure before commit restores the prepared before snapshots. Once commit
// is durable, cleanup errors retain the journal for roll-forward retry.
func (p *Provider) publishReusableUpdate(journal reusableUpdateJournal) error {
	faultPoint("reusable-update-before-journal")
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		return p.recoverAfterReusableError(fmt.Errorf("prepare reusable update journal: %w", err))
	}
	faultPoint("reusable-update-prepared")

	changes := append([]reusableUpdateJournalChange{journal.Catalog}, journal.AffectedTasks...)
	for index, change := range changes {
		path, err := p.resolveReusableUpdateTarget(change.After.Path)
		if err != nil {
			return p.recoverAfterReusableError(fmt.Errorf("resolve reusable update replacement %d: %w", index+1, err))
		}
		if err := writeBytesAtomicWithFileSystem(path, change.After.Data, p.fs); err != nil {
			return p.recoverAfterReusableError(fmt.Errorf("publish reusable update replacement %d: %w", index+1, err))
		}
		faultPoint(fmt.Sprintf("reusable-update-replacement-%d", index+1))
		faultPoint("reusable-update-publication")
	}

	if err := transitionReusableUpdateJournal(&journal, reusableUpdateJournalCommitted); err != nil {
		return p.recoverAfterReusableError(fmt.Errorf("commit reusable update: %w", err))
	}
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		return p.recoverAfterReusableError(fmt.Errorf("publish committed reusable update journal: %w", err))
	}
	faultPoint("reusable-update-committed")
	faultPoint("reusable-update-cleanup")
	if err := p.removeReusableUpdateJournal(); err != nil {
		return fmt.Errorf("clean up committed reusable update journal (journal retained): %w", err)
	}
	return nil
}

func (p *Provider) recoverAfterReusableError(operationErr error) error {
	faultPoint("reusable-update-rollback")
	if recoveryErr := p.recoverReusableUpdate(); recoveryErr != nil {
		return fmt.Errorf("%v; reusable update recovery failed and journal was retained: %w", operationErr, recoveryErr)
	}
	return operationErr
}

func (p *Provider) reusableTaskCatalogPath() string {
	return filepath.Join(p.root, reusableFilename)
}

func (p *Provider) loadReusableTaskCatalog() (core.ReusableTaskCatalog, error) {
	if err := p.recoverPendingJournals(); err != nil {
		return core.ReusableTaskCatalog{}, err
	}
	catalog, err := reusablejson.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		return core.ReusableTaskCatalog{}, err
	}
	if err := catalog.Validate(); err != nil {
		return core.ReusableTaskCatalog{}, fmt.Errorf("validate reusable catalog: %w", err)
	}
	return catalog, nil
}

func resolveReusableTaskDefinition(nameOrID string, catalog core.ReusableTaskCatalog) (core.ReusableTaskDefinition, error) {
	selector := strings.TrimSpace(nameOrID)
	if selector == "" {
		return core.ReusableTaskDefinition{}, errors.New("reusable task selector is required")
	}
	for _, definition := range catalog.Definitions {
		if definition.ID == selector {
			return definition, nil
		}
	}
	for _, definition := range catalog.Definitions {
		if strings.EqualFold(definition.Name, selector) {
			return definition, nil
		}
	}
	return core.ReusableTaskDefinition{}, fmt.Errorf("reusable task %q not found", selector)
}

// resolveReusableTaskIDs resolves caller-facing names or UUIDs to the exact
// UUIDs persisted by tasks. It intentionally preserves caller order rather
// than catalog order, and treats two selectors for the same definition as a
// duplicate assignment.
//
// Callers must hold the global lock while using their catalog snapshot, so a
// rename or delete cannot make resolution and task publication disagree.
func resolveReusableTaskIDs(selectors []string, catalog core.ReusableTaskCatalog) ([]string, error) {
	if len(selectors) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for index, selector := range selectors {
		definition, err := resolveReusableTaskDefinition(selector, catalog)
		if err != nil {
			return nil, fmt.Errorf("reusable task %d: %w", index+1, err)
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return nil, fmt.Errorf("reusable task %d %q duplicates definition %q", index+1, strings.TrimSpace(selector), definition.ID)
		}
		seen[definition.ID] = struct{}{}
		ids = append(ids, definition.ID)
	}
	return ids, nil
}

func nextReusableTaskID(catalog core.ReusableTaskCatalog) (string, error) {
	used := make(map[string]struct{}, len(catalog.Definitions))
	for _, definition := range catalog.Definitions {
		used[definition.ID] = struct{}{}
	}
	for {
		id, err := generateReusableTaskID()
		if err != nil {
			return "", err
		}
		if _, exists := used[id]; !exists {
			return id, nil
		}
	}
}

func nextReusableCatalogMutationTime(catalog core.ReusableTaskCatalog) time.Time {
	latest := time.Time{}
	for _, definition := range catalog.Definitions {
		if definition.CreatedAt.After(latest) {
			latest = definition.CreatedAt
		}
		if definition.UpdatedAt.After(latest) {
			latest = definition.UpdatedAt
		}
	}
	now := time.Now().UTC()
	if now.After(latest) {
		return now
	}
	return latest.Add(time.Nanosecond).UTC()
}
