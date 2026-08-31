package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

const (
	batchUpdateJournalVersion = 1
	batchUpdateJournalName    = "batch-update.json"
	batchJournalPrepared      = "prepared"
	batchJournalCommitted     = "committed"
)

type batchUpdateJournal struct {
	Version int                       `json:"version"`
	State   string                    `json:"state"`
	Entries []batchUpdateJournalEntry `json:"entries"`
}

type batchUpdateJournalEntry struct {
	Before      core.Task                `json:"before"`
	After       core.Task                `json:"after"`
	BeforeFiles []batchUpdateJournalFile `json:"beforeFiles,omitempty"`
}

type batchUpdateJournalFile struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

type resolvedBatchUpdate struct {
	before  core.Task
	after   core.Task
	changed bool
}

// BatchUpdate validates and publishes a set of optimistic task patches as one
// flat-file transaction. Every decision is made while holding the repository's
// global lock, so readers and competing writers observe either endpoint.
func (p *Provider) BatchUpdate(request provider.BatchUpdateRequest) (provider.BatchUpdateResult, error) {
	var result provider.BatchUpdateResult
	err := p.withGlobalLock(func() error {
		snapshot, err := p.loadValidationSnapshot(nil)
		if err != nil {
			return err
		}
		tasks := snapshot.tasks
		if len(request.Tasks) == 0 {
			return errors.New("batch update requires at least one task")
		}

		resolved, finalTasks, err := p.prepareBatchUpdates(request.Tasks, tasks, snapshot.planningItems)
		if err != nil {
			return err
		}

		journal := batchUpdateJournal{
			Version: batchUpdateJournalVersion,
			State:   batchJournalPrepared,
			Entries: make([]batchUpdateJournalEntry, 0, len(resolved)),
		}
		for _, update := range resolved {
			if !update.changed {
				continue
			}
			beforeFiles, err := p.snapshotTaskFiles(update.before)
			if err != nil {
				return fmt.Errorf("snapshot batch task %s: %w", update.before.ShortID, err)
			}
			journal.Entries = append(journal.Entries, batchUpdateJournalEntry{
				Before:      update.before,
				After:       update.after,
				BeforeFiles: beforeFiles,
			})
		}

		if len(journal.Entries) > 0 {
			faultPoint("batch-update-before-journal")
			if err := p.writeBatchUpdateJournal(journal); err != nil {
				return p.recoverAfterBatchError(fmt.Errorf("prepare batch update journal: %w", err))
			}
			faultPoint("batch-update-prepared")
			for index, entry := range journal.Entries {
				if err := p.replaceTask(entry.After); err != nil {
					return p.recoverAfterBatchError(fmt.Errorf("publish batch task %s: %w", entry.After.ShortID, err))
				}
				faultPoint(fmt.Sprintf("batch-update-replacement-%d", index+1))
				faultPoint("batch-update-publication")
			}

			journal.State = batchJournalCommitted
			if err := p.writeBatchUpdateJournal(journal); err != nil {
				return p.recoverAfterBatchError(fmt.Errorf("commit batch update journal: %w", err))
			}
			faultPoint("batch-update-committed")
			faultPoint("batch-update-cleanup")
			if err := p.removeBatchUpdateJournal(); err != nil {
				return fmt.Errorf("clean up committed batch update journal: %w", err)
			}
		}

		result.Updated = make([]core.TaskView, 0, len(journal.Entries))
		result.Unchanged = make([]core.TaskView, 0, len(resolved)-len(journal.Entries))
		for _, update := range resolved {
			view, err := p.decorateTask(update.after, finalTasks, snapshot.planningItems, "")
			if err != nil {
				return err
			}
			if update.changed {
				result.Updated = append(result.Updated, view)
			} else {
				result.Unchanged = append(result.Unchanged, view)
			}
		}
		return nil
	})
	if err != nil {
		return provider.BatchUpdateResult{}, err
	}
	return result, nil
}

func (p *Provider) prepareBatchUpdates(inputs []core.BatchTaskUpdateInput, tasks []core.Task, planningItems []core.PlanningItem) ([]resolvedBatchUpdate, []core.Task, error) {
	resolved := make([]resolvedBatchUpdate, 0, len(inputs))
	seenTaskIDs := make(map[string]int, len(inputs))
	var reusableCatalog core.ReusableTaskCatalog
	for _, input := range inputs {
		if input.ReusableTasks.Set {
			catalog, err := p.loadReusableTaskCatalog()
			if err != nil {
				return nil, nil, err
			}
			reusableCatalog = catalog
			break
		}
	}

	for index, input := range inputs {
		if !hasBatchPatch(input) {
			return nil, nil, fmt.Errorf("batch task %d has no mutable fields", index+1)
		}
		before, err := resolveBatchTask(input, tasks)
		if err != nil {
			return nil, nil, fmt.Errorf("batch task %d: %w", index+1, err)
		}
		if previous, duplicate := seenTaskIDs[before.ID]; duplicate {
			return nil, nil, fmt.Errorf("batch task %d duplicates task %s from row %d", index+1, before.ShortID, previous+1)
		}
		seenTaskIDs[before.ID] = index
		if input.ExpectedUpdatedAt.IsZero() {
			return nil, nil, fmt.Errorf("batch task %d (%s) expected updatedAt is required", index+1, before.ShortID)
		}
		if !before.UpdatedAt.Equal(input.ExpectedUpdatedAt) {
			return nil, nil, fmt.Errorf("%w for %s: expected updatedAt %s, current updatedAt is %s", provider.ErrStaleTask, before.ShortID, input.ExpectedUpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), before.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
		}

		after := cloneTask(before)
		if err := p.applyBatchPatch(&after, input, tasks, planningItems, reusableCatalog); err != nil {
			return nil, nil, fmt.Errorf("batch task %d (%s): %w", index+1, before.ShortID, err)
		}
		resolved = append(resolved, resolvedBatchUpdate{
			before:  before,
			after:   after,
			changed: !mutableTaskFieldsEqual(before, after),
		})
	}

	for index := range resolved {
		update := &resolved[index]
		if !update.changed {
			continue
		}
		statusChanged := update.before.Status != update.after.Status
		if statusChanged && !p.allowedStatusTransition(update.before.Status, update.after.Status) {
			return nil, nil, fmt.Errorf("batch task %d (%s): invalid status transition from %s to %s", index+1, update.before.ShortID, update.before.Status, update.after.Status)
		}
		now := nextTaskMutationTime(update.before)
		if statusChanged {
			if err := p.catalog.NormalizeTaskStatus(&update.after, update.after.Status, now); err != nil {
				return nil, nil, fmt.Errorf("batch task %d (%s): %w", index+1, update.before.ShortID, err)
			}
		}
		update.after.UpdatedAt = now
		if err := update.after.ValidateWithCatalog(p.catalog); err != nil {
			return nil, nil, fmt.Errorf("batch task %d (%s): %w", index+1, update.before.ShortID, err)
		}
	}

	finalTasks := append([]core.Task(nil), tasks...)
	for _, update := range resolved {
		if update.changed {
			finalTasks = replaceTaskInMemory(finalTasks, update.after)
		}
	}
	for index, update := range resolved {
		if update.changed && update.before.Status != update.after.Status && update.after.Status == core.StatusInProgress {
			if err := p.validateStartable(update.after, finalTasks, planningItems); err != nil {
				return nil, nil, fmt.Errorf("batch task %d (%s): %w", index+1, update.before.ShortID, err)
			}
		}
	}
	if err := core.ValidateDependenciesAcrossLifecycles("", nil, finalTasks, planningItems); err != nil {
		return nil, nil, fmt.Errorf("invalid final dependency graph: %w", err)
	}

	return resolved, finalTasks, nil
}

func resolveBatchTask(input core.BatchTaskUpdateInput, tasks []core.Task) (core.Task, error) {
	id := strings.TrimSpace(input.ID)
	shortID := strings.TrimSpace(input.ShortID)
	if id == "" && shortID == "" {
		return core.Task{}, errors.New("id or shortId is required")
	}
	if id == "" {
		return resolveTask(shortID, tasks)
	}
	if shortID == "" {
		return resolveTask(id, tasks)
	}
	byID, err := resolveTask(id, tasks)
	if err != nil {
		return core.Task{}, fmt.Errorf("resolve id %q: %w", id, err)
	}
	byShortID, err := resolveTask(shortID, tasks)
	if err != nil {
		return core.Task{}, fmt.Errorf("resolve shortId %q: %w", shortID, err)
	}
	if byID.ID != byShortID.ID {
		return core.Task{}, fmt.Errorf("id %q and shortId %q identify different tasks", id, shortID)
	}
	return byID, nil
}

func (p *Provider) applyBatchPatch(task *core.Task, input core.BatchTaskUpdateInput, tasks []core.Task, planningItems []core.PlanningItem, reusableCatalog core.ReusableTaskCatalog) error {
	if input.Title.Set {
		task.Title = strings.TrimSpace(input.Title.Value)
	}
	if input.Description.Set {
		task.Description = strings.TrimSpace(input.Description.Value)
	}
	if input.Status.Set {
		if _, err := p.catalog.ParseStatus(string(input.Status.Value)); err != nil {
			return err
		}
		task.Status = input.Status.Value
	}
	if input.Priority.Set {
		priority, err := core.ParsePriority(string(input.Priority.Value))
		if err != nil {
			return err
		}
		task.Priority = priority
	}
	if input.Estimate.Set {
		estimate, err := core.ParseEstimate(string(input.Estimate.Value))
		if err != nil {
			return err
		}
		task.Estimate = estimate
	}
	if input.Lane.Set {
		task.Lane = strings.TrimSpace(input.Lane.Value)
	}
	if input.Model.Set {
		task.Model = strings.TrimSpace(input.Model.Value)
	}
	if input.IssueID.Set {
		task.IssueID = strings.TrimSpace(input.IssueID.Value)
	}
	if input.Project.Set {
		task.Project = strings.TrimSpace(input.Project.Value)
	}
	if input.Milestone.Set {
		task.Milestone = strings.TrimSpace(input.Milestone.Value)
	}
	if input.Version.Set {
		task.Version = strings.TrimSpace(input.Version.Value)
	}
	if input.FeatureID.Set {
		task.FeatureID = strings.TrimSpace(input.FeatureID.Value)
	}
	if input.Feature.Set {
		task.Feature = strings.TrimSpace(input.Feature.Value)
	}
	if input.GitRepo.Set {
		task.GitRepo = strings.TrimSpace(input.GitRepo.Value)
	}
	if input.GitBranch.Set {
		task.GitBranch = strings.TrimSpace(input.GitBranch.Value)
	}
	if input.WorktreeName.Set {
		task.WorktreeName = strings.TrimSpace(input.WorktreeName.Value)
	}
	if input.WorktreeDir.Set {
		task.WorktreeDir = strings.TrimSpace(input.WorktreeDir.Value)
	}
	if input.Assignee.Set {
		task.Assignee = strings.TrimSpace(input.Assignee.Value)
	}
	if input.Dependencies.Set {
		resolvedDependencies, err := resolveDependencyIDs(input.Dependencies.Value, tasks, planningItems)
		if err != nil {
			return err
		}
		task.Dependencies = resolvedDependencies
	}
	if input.ReusableTasks.Set {
		resolvedReusableTaskIDs, err := resolveReusableTaskIDs(input.ReusableTasks.Value, reusableCatalog)
		if err != nil {
			return err
		}
		task.ReusableTaskIDs = resolvedReusableTaskIDs
	}
	return nil
}

func hasBatchPatch(input core.BatchTaskUpdateInput) bool {
	return input.Title.Set || input.Description.Set || input.Status.Set || input.Priority.Set || input.Estimate.Set ||
		input.Lane.Set || input.Model.Set || input.IssueID.Set || input.Project.Set || input.Milestone.Set || input.Version.Set ||
		input.FeatureID.Set || input.Feature.Set || input.GitRepo.Set || input.GitBranch.Set || input.WorktreeName.Set ||
		input.WorktreeDir.Set || input.Assignee.Set || input.Dependencies.Set || input.ReusableTasks.Set
}

func mutableTaskFieldsEqual(left, right core.Task) bool {
	return left.Title == right.Title &&
		left.Description == right.Description &&
		left.Status == right.Status &&
		left.Priority == right.Priority &&
		left.Estimate == right.Estimate &&
		left.Lane == right.Lane &&
		left.Model == right.Model &&
		left.IssueID == right.IssueID &&
		left.Project == right.Project &&
		left.Milestone == right.Milestone &&
		left.Version == right.Version &&
		left.FeatureID == right.FeatureID &&
		left.Feature == right.Feature &&
		left.GitRepo == right.GitRepo &&
		left.GitBranch == right.GitBranch &&
		left.WorktreeName == right.WorktreeName &&
		left.WorktreeDir == right.WorktreeDir &&
		left.Assignee == right.Assignee &&
		slices.Equal(left.Dependencies, right.Dependencies) &&
		slices.Equal(left.ReusableTaskIDs, right.ReusableTaskIDs)
}

func cloneTask(task core.Task) core.Task {
	cloned := task
	if task.Dependencies != nil {
		cloned.Dependencies = append([]string{}, task.Dependencies...)
	}
	if task.ReusableTaskIDs != nil {
		cloned.ReusableTaskIDs = append([]string{}, task.ReusableTaskIDs...)
	}
	if task.Comments != nil {
		cloned.Comments = append([]core.Comment{}, task.Comments...)
	}
	if task.StartedAt != nil {
		startedAt := *task.StartedAt
		cloned.StartedAt = &startedAt
	}
	if task.CompletedAt != nil {
		completedAt := *task.CompletedAt
		cloned.CompletedAt = &completedAt
	}
	return cloned
}

func (p *Provider) batchUpdateJournalPath() string {
	return filepath.Join(p.root, "meta", batchUpdateJournalName)
}

func (p *Provider) writeBatchUpdateJournal(journal batchUpdateJournal) error {
	return p.writeJSONAtomic(p.batchUpdateJournalPath(), journal)
}

func (p *Provider) removeBatchUpdateJournal() error {
	err := p.removeFile(p.batchUpdateJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (p *Provider) recoverAfterBatchError(operationErr error) error {
	faultPoint("batch-update-rollback")
	if recoveryErr := p.recoverBatchUpdate(); recoveryErr != nil {
		return fmt.Errorf("%v; batch recovery failed and journal was retained: %w", operationErr, recoveryErr)
	}
	return operationErr
}

// recoverBatchUpdate restores every before snapshot from a prepared journal or
// republishes every after snapshot from a committed journal. The journal is
// removed only after all replacements and cleanup complete successfully.
func (p *Provider) recoverBatchUpdate() error {
	path := p.batchUpdateJournalPath()
	var journal batchUpdateJournal
	if err := readJSON(path, &journal); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read batch update journal %s: %w", path, err)
	}
	if err := p.validateBatchUpdateJournal(journal); err != nil {
		return fmt.Errorf("invalid batch update journal %s: %w", path, err)
	}

	for _, entry := range journal.Entries {
		if journal.State == batchJournalPrepared && len(entry.BeforeFiles) > 0 {
			if err := p.restoreBatchTaskFiles(entry); err != nil {
				return fmt.Errorf("recover %s batch task %s: %w", journal.State, entry.Before.ShortID, err)
			}
			continue
		}
		task := entry.Before
		if journal.State == batchJournalCommitted {
			task = entry.After
		}
		if err := p.replaceTask(task); err != nil {
			return fmt.Errorf("recover %s batch task %s: %w", journal.State, task.ShortID, err)
		}
	}
	if err := p.removeBatchUpdateJournal(); err != nil {
		return fmt.Errorf("remove recovered batch update journal: %w", err)
	}
	return nil
}

func (p *Provider) snapshotTaskFiles(task core.Task) ([]batchUpdateJournalFile, error) {
	var files []batchUpdateJournalFile
	for _, status := range p.statuses() {
		for _, filename := range taskFilenames(task) {
			path := p.taskPath(status, filename)
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			files = append(files, batchUpdateJournalFile{Path: path, Data: append([]byte(nil), data...)})
		}
	}
	if len(files) == 0 {
		return nil, errors.New("no task file found")
	}
	return files, nil
}

func (p *Provider) restoreBatchTaskFiles(entry batchUpdateJournalEntry) error {
	for _, status := range p.statuses() {
		for _, filename := range taskFilenames(entry.Before) {
			path := p.taskPath(status, filename)
			if err := p.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}
	for _, file := range entry.BeforeFiles {
		if err := writeBytesAtomicWithFileSystem(file.Path, file.Data, p.fs); err != nil {
			return fmt.Errorf("restore %s: %w", file.Path, err)
		}
	}
	return nil
}

func (p *Provider) validateBatchUpdateJournal(journal batchUpdateJournal) error {
	if journal.Version != batchUpdateJournalVersion {
		return fmt.Errorf("unsupported version %d", journal.Version)
	}
	if journal.State != batchJournalPrepared && journal.State != batchJournalCommitted {
		return fmt.Errorf("invalid state %q", journal.State)
	}
	if len(journal.Entries) == 0 {
		return errors.New("entries are required")
	}
	seen := make(map[string]struct{}, len(journal.Entries))
	for index, entry := range journal.Entries {
		if err := entry.Before.ValidateWithCatalog(p.catalog); err != nil {
			return fmt.Errorf("entry %d before task: %w", index, err)
		}
		if err := entry.After.ValidateWithCatalog(p.catalog); err != nil {
			return fmt.Errorf("entry %d after task: %w", index, err)
		}
		if entry.Before.ID != entry.After.ID || entry.Before.ShortID != entry.After.ShortID {
			return fmt.Errorf("entry %d identifiers do not match", index)
		}
		if _, duplicate := seen[entry.Before.ID]; duplicate {
			return fmt.Errorf("entry %d duplicates task %s", index, entry.Before.ID)
		}
		seen[entry.Before.ID] = struct{}{}
		if !entry.After.UpdatedAt.After(entry.Before.UpdatedAt) {
			return fmt.Errorf("entry %d after updatedAt must advance", index)
		}
		if !entry.Before.CreatedAt.Equal(entry.After.CreatedAt) || !slices.Equal(entry.Before.Comments, entry.After.Comments) {
			return fmt.Errorf("entry %d changes immutable task history", index)
		}
		if mutableTaskFieldsEqual(entry.Before, entry.After) {
			return fmt.Errorf("entry %d has no mutable changes", index)
		}
	}
	return nil
}
