package flatfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

var canonicalExportFilenamePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)

type indexFile struct {
	// Branch is populated only for named-branch indexes. Keeping the exact
	// branch name beside its shortened token makes a token collision
	// actionable instead of silently sharing an allocation sequence.
	Branch string `json:"branch,omitempty"`
	Next   int    `json:"next"`
}

type Provider struct {
	root            string
	invocationScope *core.BranchScope
	catalog         core.StatusCatalog
	fs              fileSystem
}

// New initializes flat-file storage for one invocation. A nil scope preserves
// the legacy global namespace. A supplied scope is copied so later caller
// changes cannot alter the provider's namespace.
func New(root string, invocationScope *core.BranchScope) (*Provider, error) {
	return NewWithCatalog(root, invocationScope, core.DefaultStatusCatalog())
}

// NewWithCatalog initializes flat-file storage for one invocation with its
// immutable status catalog. New remains the legacy-default wrapper.
func NewWithCatalog(root string, invocationScope *core.BranchScope, catalog core.StatusCatalog) (*Provider, error) {
	if len(catalog.Statuses()) == 0 {
		catalog = core.DefaultStatusCatalog()
	}
	p := &Provider{root: root, catalog: catalog, fs: defaultFileSystem()}
	if invocationScope != nil {
		scope := *invocationScope
		p.invocationScope = &scope
	}
	if err := p.ensureLayout(); err != nil {
		return nil, err
	}
	return p, nil
}

// StatusCatalog returns the catalog captured by this provider.
func (p *Provider) StatusCatalog() core.StatusCatalog { return p.catalog }

// InvocationScope returns a copy of the scope captured when this provider was
// initialized. A nil result preserves the legacy global namespace.
func (p *Provider) InvocationScope() *core.BranchScope {
	if p.invocationScope == nil {
		return nil
	}
	scope := *p.invocationScope
	return &scope
}

func (p *Provider) ensureLayout() error {
	dirs := []string{p.root, filepath.Join(p.root, "meta"), filepath.Join(p.root, "meta", "locks")}
	for _, status := range p.statuses() {
		dirs = append(dirs, p.statusDir(status))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return p.withGlobalLock(func() error {
		if err := p.recoverBatchUpdate(); err != nil {
			return err
		}
		if err := p.validateUnconfiguredStatusDirs(); err != nil {
			return err
		}
		indexPath := p.indexPath()
		if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
			if err := p.writeIndex(p.initialIndex()); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", indexPath, err)
		} else if _, err := p.readIndex(); err != nil {
			return err
		}
		return p.migrateTaskFilenames()
	})
}

// statuses returns the configured on-disk directory order. The catalog always
// contains the four legacy states first, so the no-additional-states layout
// remains byte-for-byte compatible with the legacy provider.
func (p *Provider) statuses() []core.Status {
	definitions := p.catalog.Statuses()
	statuses := make([]core.Status, len(definitions))
	for index, definition := range definitions {
		statuses[index] = definition.Name
	}
	return statuses
}

func (p *Provider) validateUnconfiguredStatusDirs() error {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return fmt.Errorf("read flat-file storage root %s: %w", p.root, err)
	}
	configured := make(map[string]struct{}, len(p.statuses()))
	for _, status := range p.statuses() {
		configured[string(status)] = struct{}{}
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "meta" {
			continue
		}
		if _, ok := configured[entry.Name()]; ok {
			continue
		}
		dir := filepath.Join(p.root, entry.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read unconfigured status directory %s: %w", dir, err)
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, file.Name())
			var task struct {
				Status core.Status `json:"status"`
			}
			if err := readJSON(path, &task); err != nil {
				return fmt.Errorf("corrupt task file %s: %w", path, err)
			}
			if _, err := p.catalog.ParseStatus(string(task.Status)); err != nil {
				return fmt.Errorf("task file %s uses status %q absent from active configuration", path, task.Status)
			}
			return fmt.Errorf("task file %s is stored in unconfigured status directory %s", path, entry.Name())
		}
	}
	return nil
}

func (p *Provider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	var views []core.TaskView
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		allTasks := append([]core.Task(nil), tasks...)
		if filter.Status != nil {
			filtered := tasks[:0]
			for _, task := range tasks {
				if task.Status == *filter.Status {
					filtered = append(filtered, task)
				}
			}
			tasks = filtered
		}
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
				return tasks[i].ShortID < tasks[j].ShortID
			}
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		})
		views = p.decorateTasks(tasks, allTasks, filter.Agent)
		return nil
	})
	return views, err
}

func (p *Provider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	var view core.TaskView
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		task, err := resolveTask(idOrShortID, tasks)
		if err != nil {
			return err
		}
		view = p.decorateTask(task, tasks, agent)
		return nil
	})
	return view, err
}

func (p *Provider) CreateTask(input core.CreateTaskInput) (core.TaskView, error) {
	var created core.Task
	var tasksAfter []core.Task
	err := p.withGlobalLock(func() error {
		now := time.Now().UTC()
		id, err := core.NewID()
		if err != nil {
			return err
		}
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		resolvedDependencies, err := resolveDependencyIDs(input.Dependencies, tasks)
		if err != nil {
			return err
		}
		if err := core.ValidateDependenciesWithCatalog(p.catalog, "", resolvedDependencies, tasks); err != nil {
			return err
		}
		index, err := p.readIndex()
		if err != nil {
			return err
		}
		index, shortID, err := p.nextAvailableShortID(index, tasks)
		if err != nil {
			return err
		}
		task := core.Task{
			ID:           id,
			ShortID:      shortID,
			Title:        strings.TrimSpace(input.Title),
			Description:  strings.TrimSpace(input.Description),
			Priority:     input.Priority,
			Estimate:     input.Estimate,
			Lane:         strings.TrimSpace(input.Lane),
			Model:        strings.TrimSpace(input.Model),
			GitRepo:      strings.TrimSpace(input.GitRepo),
			GitBranch:    strings.TrimSpace(input.GitBranch),
			WorktreeName: strings.TrimSpace(input.WorktreeName),
			WorktreeDir:  strings.TrimSpace(input.WorktreeDir),
			Status:       input.Status,
			Assignee:     strings.TrimSpace(input.Assignee),
			Dependencies: resolvedDependencies,
			Comments:     []core.Comment{},
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if task.Status == "" {
			task.Status = core.StatusTodo
		}
		if err := p.catalog.NormalizeTaskStatus(&task, task.Status, now); err != nil {
			return err
		}
		if err := task.ValidateWithCatalog(p.catalog); err != nil {
			return err
		}
		index.Next++
		if err := p.writeIndex(index); err != nil {
			return err
		}
		if err := p.writeTask(task); err != nil {
			// Permission failures are deterministic environmental rejection: roll
			// back the allocation publication. Other replacement failures retain
			// the documented monotonic allocation gap used by the focused seam
			// tests, while still preserving every task byte.
			if errors.Is(err, os.ErrPermission) {
				rollback := index
				rollback.Next--
				if rollbackErr := p.writeIndex(rollback); rollbackErr != nil {
					return fmt.Errorf("write task: %v; restore allocation index: %w", err, rollbackErr)
				}
			}
			return err
		}
		faultPoint("create-publication")
		created = task
		tasksAfter = append(tasks, task)
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	return p.decorateTask(created, tasksAfter, ""), nil
}

func (p *Provider) UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error) {
	var updated core.Task
	var tasksAfter []core.Task
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		task, err := resolveTask(idOrShortID, tasks)
		if err != nil {
			return err
		}

		if input.Title.Set {
			task.Title = strings.TrimSpace(input.Title.Value)
		}
		if input.Description.Set {
			task.Description = strings.TrimSpace(input.Description.Value)
		}
		if input.Status.Set {
			if !p.allowedStatusTransition(task.Status, input.Status.Value) {
				return fmt.Errorf("invalid status transition from %s to %s", task.Status, input.Status.Value)
			}
		}
		if input.Priority.Set {
			task.Priority = input.Priority.Value
		}
		if input.Estimate.Set {
			task.Estimate = input.Estimate.Value
		}
		if input.Lane.Set {
			task.Lane = strings.TrimSpace(input.Lane.Value)
		}
		if input.Model.Set {
			task.Model = strings.TrimSpace(input.Model.Value)
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
			resolvedDependencies, err := resolveDependencyIDs(splitCSV(input.Dependencies.Value), tasks)
			if err != nil {
				return err
			}
			if err := core.ValidateDependenciesWithCatalog(p.catalog, task.ID, resolvedDependencies, tasks); err != nil {
				return err
			}
			task.Dependencies = resolvedDependencies
		}

		now := nextTaskMutationTime(task)
		if input.Status.Set {
			if input.Status.Value == core.StatusInProgress {
				if err := p.validateStartable(task, tasks); err != nil {
					return err
				}
			}
			if err := p.catalog.NormalizeTaskStatus(&task, input.Status.Value, now); err != nil {
				return err
			}
		}
		task.UpdatedAt = now
		if err := task.ValidateWithCatalog(p.catalog); err != nil {
			return err
		}
		if err := p.replaceTask(task); err != nil {
			return err
		}
		updated = task
		tasksAfter = replaceTaskInMemory(tasks, task)
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	return p.decorateTask(updated, tasksAfter, ""), nil
}

func (p *Provider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
	var updated core.Task
	var tasksAfter []core.Task
	var handoffs []core.Handoff
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		task, err := resolveTask(idOrShortID, tasks)
		if err != nil {
			return err
		}
		if !p.allowedStatusTransition(task.Status, target) {
			return fmt.Errorf("invalid status transition from %s to %s", task.Status, target)
		}
		if target == core.StatusInProgress {
			if err := p.validateStartable(task, tasks); err != nil {
				return err
			}
			storedHandoffs, err := p.loadHandoffs()
			if err != nil {
				return err
			}
			handoffs = taskHandoffs(storedHandoffs, task.ID)
		}
		now := nextTaskMutationTime(task)
		task.UpdatedAt = now
		if strings.TrimSpace(actor) != "" {
			task.Assignee = strings.TrimSpace(actor)
		}
		if err := p.catalog.NormalizeTaskStatus(&task, target, now); err != nil {
			return err
		}
		if err := task.ValidateWithCatalog(p.catalog); err != nil {
			return err
		}
		if err := p.replaceTask(task); err != nil {
			return err
		}
		updated = task
		tasksAfter = replaceTaskInMemory(tasks, task)
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	view := p.decorateTask(updated, tasksAfter, "")
	view.Handoffs = handoffs
	return view, nil
}

func (p *Provider) AddComment(idOrShortID, actor, message string) (core.TaskView, error) {
	var updated core.Task
	var tasksAfter []core.Task
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		task, err := resolveTask(idOrShortID, tasks)
		if err != nil {
			return err
		}
		commentID, err := core.NewID()
		if err != nil {
			return err
		}
		now := nextTaskMutationTime(task)
		task.Comments = append(task.Comments, core.Comment{
			ID:        commentID,
			Author:    strings.TrimSpace(actor),
			Message:   strings.TrimSpace(message),
			CreatedAt: now,
		})
		task.UpdatedAt = now
		if err := task.ValidateWithCatalog(p.catalog); err != nil {
			return err
		}
		if err := p.replaceTask(task); err != nil {
			return err
		}
		updated = task
		tasksAfter = replaceTaskInMemory(tasks, task)
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	return p.decorateTask(updated, tasksAfter, ""), nil
}

func (p *Provider) PeekNextTask(agent string) (core.TaskView, error) {
	var view core.TaskView
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		task, err := p.selectNextEligibleTask(tasks, agent)
		if err != nil {
			return err
		}
		view = p.decorateTask(task, tasks, agent)
		return nil
	})
	return view, err
}

func (p *Provider) PeekNextTasks(agent string, limit int) ([]core.TaskView, error) {
	var views []core.TaskView
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		selected, err := p.selectEligibleTasks(tasks, agent, limit)
		if err != nil {
			return err
		}
		views = p.decorateTasks(selected, tasks, agent)
		return nil
	})
	return views, err
}

func (p *Provider) GetNextTask(agent string) (core.TaskView, error) {
	var claimed core.Task
	var tasksAfter []core.Task
	var handoffs []core.Handoff
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		next, err := p.selectNextEligibleTask(tasks, agent)
		if err != nil {
			return err
		}
		storedHandoffs, err := p.loadHandoffs()
		if err != nil {
			return err
		}
		handoffs = taskHandoffs(storedHandoffs, next.ID)

		now := nextTaskMutationTime(next)
		next.UpdatedAt = now
		if agent != "" {
			next.Assignee = agent
		}
		if err := p.catalog.NormalizeTaskStatus(&next, core.StatusInProgress, now); err != nil {
			return err
		}
		if err := next.ValidateWithCatalog(p.catalog); err != nil {
			return err
		}
		if err := p.replaceTask(next); err != nil {
			return err
		}
		claimed = next
		tasksAfter = replaceTaskInMemory(tasks, next)
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	view := p.decorateTask(claimed, tasksAfter, agent)
	view.Handoffs = handoffs
	return view, nil
}

func (p *Provider) ExportCanonical(outDir string) error {
	return p.withGlobalLock(func() error {
		exportDir, err := resolvePath(outDir)
		if err != nil {
			return fmt.Errorf("resolve export dir %q: %w", outDir, err)
		}
		storageDir, err := resolvePath(p.root)
		if err != nil {
			return fmt.Errorf("resolve active storage dir %s: %w", p.root, err)
		}
		if pathsOverlap(exportDir, storageDir) {
			return fmt.Errorf("export directory %s overlaps active storage %s", exportDir, storageDir)
		}
		if err := os.MkdirAll(exportDir, 0o755); err != nil {
			return fmt.Errorf("create export dir %s: %w", exportDir, err)
		}

		entries, err := os.ReadDir(exportDir)
		if err != nil {
			return fmt.Errorf("read export dir %s: %w", exportDir, err)
		}
		var unmanaged []string
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect export entry %s: %w", filepath.Join(exportDir, entry.Name()), err)
			}
			if !info.Mode().IsRegular() || !isManagedCanonicalExportEntry(entry.Name()) {
				unmanaged = append(unmanaged, entry.Name())
			}
		}
		if len(unmanaged) != 0 {
			sort.Strings(unmanaged)
			return fmt.Errorf("export directory %s contains unmanaged entries: %s", exportDir, strings.Join(unmanaged, ", "))
		}

		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		handoffs, err := p.loadHandoffs()
		if err != nil {
			return err
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
		expected := make(map[string]struct{}, len(tasks)+1)
		expected[handoffsFilename] = struct{}{}
		for _, task := range tasks {
			name := task.ID + ".json"
			expected[name] = struct{}{}
			path := filepath.Join(exportDir, name)
			if err := p.writeJSONAtomic(path, task); err != nil {
				return err
			}
		}
		if err := p.writeJSONAtomic(filepath.Join(exportDir, handoffsFilename), handoffFile{Handoffs: handoffs}); err != nil {
			return err
		}
		faultPoint("export-publication")
		for _, entry := range entries {
			if _, keep := expected[entry.Name()]; keep {
				continue
			}
			path := filepath.Join(exportDir, entry.Name())
			if err := p.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove stale export file %s: %w", path, err)
			}
		}
		return nil
	})
}

func isManagedCanonicalExportEntry(name string) bool {
	return name == handoffsFilename || canonicalExportFilenamePattern.MatchString(name)
}

func resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	current := filepath.Clean(absPath)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	parent = normalizePathForComparison(parent)
	child = normalizePathForComparison(child)
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (p *Provider) loadTasks() ([]core.Task, error) {
	if err := p.recoverBatchUpdate(); err != nil {
		return nil, err
	}
	if err := p.validateUnconfiguredStatusDirs(); err != nil {
		return nil, err
	}
	tasksByID := make(map[string]core.Task)
	pathsByID := make(map[string]string)
	idsByShortID := make(map[string]string)
	var residuePaths []string
	var repairs []taskRepair
	repairClock := time.Now().UTC()
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
			task, repaired, err := p.validateOrRepairTask(task, repairClock)
			if err != nil {
				return nil, fmt.Errorf("invalid task file %s: %w", path, err)
			}
			if repaired {
				repairs = append(repairs, taskRepair{path: path, task: task})
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
	tasks := make([]core.Task, 0, len(tasksByID))
	for _, task := range tasksByID {
		tasks = append(tasks, task)
	}
	if err := core.ValidateDependenciesWithCatalog(p.catalog, "", nil, tasks); err != nil {
		return nil, fmt.Errorf("invalid dependency graph: %w", err)
	}
	residueSet := make(map[string]struct{}, len(residuePaths))
	for _, path := range residuePaths {
		residueSet[path] = struct{}{}
	}
	for _, repair := range repairs {
		if _, discarded := residueSet[repair.path]; discarded {
			continue
		}
		if err := p.writeJSONAtomic(repair.path, repair.task); err != nil {
			return nil, fmt.Errorf("repair task file %s: %w", repair.path, err)
		}
	}
	// A process can be terminated after publishing the new status file but
	// before removing the old one. The newer file is the complete winning
	// state; remove only the older copy after the whole store has validated and
	// any repair has been published atomically.
	for _, path := range residuePaths {
		// Cleanup is best effort. The newer copy is already a complete valid
		// state, and retaining a valid older residue is safer than turning a
		// successful read into data loss when deletion is temporarily denied.
		_ = p.removeFile(path)
	}
	return tasks, nil
}

type taskRepair struct {
	path string
	task core.Task
}

const automaticRepairMessage = "WTP automatically repaired task metadata: updatedAt was advanced to cover task creation, comments, and lifecycle timestamps."

// validateOrRepairTask repairs only an updatedAt value that is absent or
// earlier than an otherwise valid timestamp owned by the task. All other
// corruption remains a hard validation error.
func (p *Provider) validateOrRepairTask(task core.Task, repairClock time.Time) (core.Task, bool, error) {
	validationErr := task.ValidateWithCatalog(p.catalog)
	if validationErr == nil {
		return task, false, nil
	}

	latest := latestTaskTimestamp(task)
	if task.CreatedAt.IsZero() || (!task.UpdatedAt.IsZero() && !task.UpdatedAt.Before(latest)) {
		return task, false, validationErr
	}
	repairedAt := repairClock.UTC()
	if !repairedAt.After(latest) {
		repairedAt = latest.Add(time.Nanosecond).UTC()
	}
	repaired := task
	repaired.UpdatedAt = repairedAt
	repaired.Comments = append(repaired.Comments, core.Comment{
		ID:        automaticRepairCommentID(task, repairedAt),
		Author:    "wtp",
		Message:   automaticRepairMessage,
		CreatedAt: repairedAt,
	})
	if err := repaired.ValidateWithCatalog(p.catalog); err != nil {
		return task, false, validationErr
	}
	return repaired, true, nil
}

// latestTaskTimestamp returns the greatest timestamp that updatedAt must
// cover. Including the previous updatedAt also makes normal mutations
// monotonic when the wall clock moves backward.
func latestTaskTimestamp(task core.Task) time.Time {
	latest := task.CreatedAt
	if task.UpdatedAt.After(latest) {
		latest = task.UpdatedAt
	}
	for _, comment := range task.Comments {
		if comment.CreatedAt.After(latest) {
			latest = comment.CreatedAt
		}
	}
	if task.StartedAt != nil && task.StartedAt.After(latest) {
		latest = *task.StartedAt
	}
	if task.CompletedAt != nil && task.CompletedAt.After(latest) {
		latest = *task.CompletedAt
	}
	return latest
}

func nextTaskMutationTime(task core.Task) time.Time {
	now := time.Now().UTC()
	latest := latestTaskTimestamp(task)
	if now.After(latest) {
		return now
	}
	return latest.Add(time.Nanosecond).UTC()
}

// automaticRepairCommentID is stable for identical copies encountered in one
// repair pass, so recovery does not turn duplicate legacy/canonical files into
// conflicting task contents.
func automaticRepairCommentID(task core.Task, repairedAt time.Time) string {
	seed := task.ID + "\x00" + task.UpdatedAt.Format(time.RFC3339Nano) + "\x00" + repairedAt.Format(time.RFC3339Nano)
	for salt := 0; ; salt++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", seed, salt)))
		digest[6] = (digest[6] & 0x0f) | 0x50
		digest[8] = (digest[8] & 0x3f) | 0x80
		encoded := hex.EncodeToString(digest[:16])
		candidate := encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
		if !slices.ContainsFunc(task.Comments, func(comment core.Comment) bool { return comment.ID == candidate }) {
			return candidate
		}
	}
}

func (p *Provider) replaceTask(task core.Task) error {
	targetPath := p.taskPath(task.Status, task.ShortID)
	if err := p.writeJSONAtomic(targetPath, task); err != nil {
		return err
	}
	faultPoint("status-move-publication")

	for _, status := range p.statuses() {
		for _, filename := range taskFilenames(task) {
			path := p.taskPath(status, filename)
			if path == targetPath {
				continue
			}
			err := p.removeFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}
	return nil
}

func (p *Provider) writeTask(task core.Task) error {
	return p.writeJSONAtomic(p.taskPath(task.Status, task.ShortID), task)
}

func (p *Provider) readIndex() (indexFile, error) {
	var index indexFile
	if err := readJSON(p.indexPath(), &index); err != nil {
		return indexFile{}, fmt.Errorf("read index: %w", err)
	}
	if p.invocationScope != nil && index.Branch != p.invocationScope.Branch {
		return indexFile{}, fmt.Errorf("branch index token %s belongs to branch %q, not %q; inspect %s for a stale index or 32-bit branch-ID collision", p.invocationScope.BranchID, index.Branch, p.invocationScope.Branch, p.indexPath())
	}
	if index.Next < 1 {
		index.Next = 1
	}
	return index, nil
}

func (p *Provider) writeIndex(index indexFile) error {
	if p.invocationScope != nil {
		if index.Branch != "" && index.Branch != p.invocationScope.Branch {
			return fmt.Errorf("branch index token %s belongs to branch %q, not %q; inspect %s for a stale index or 32-bit branch-ID collision", p.invocationScope.BranchID, index.Branch, p.invocationScope.Branch, p.indexPath())
		}
		index.Branch = p.invocationScope.Branch
	}
	return p.writeJSONAtomic(p.indexPath(), index)
}

func (p *Provider) nextAvailableShortID(index indexFile, tasks []core.Task) (indexFile, string, error) {
	usedShortIDs := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		usedShortIDs[task.ShortID] = struct{}{}
	}

	maxInt := int(^uint(0) >> 1)
	for {
		if index.Next >= maxInt {
			return indexFile{}, "", fmt.Errorf("task short ID sequence exhausted for %s", p.indexPath())
		}
		shortID := p.shortID(index.Next)
		if _, used := usedShortIDs[shortID]; !used {
			return index, shortID, nil
		}
		index.Next++
	}
}

func (p *Provider) statusDir(status core.Status) string {
	return filepath.Join(p.root, string(status))
}

func (p *Provider) indexPath() string {
	if p.invocationScope != nil {
		return filepath.Join(p.root, "meta", "index-"+p.invocationScope.BranchID+".json")
	}
	return filepath.Join(p.root, "meta", "index.json")
}

func (p *Provider) initialIndex() indexFile {
	if p.invocationScope != nil {
		return indexFile{Branch: p.invocationScope.Branch, Next: 1}
	}
	return indexFile{Next: 1}
}

func (p *Provider) shortID(sequence int) string {
	if p.invocationScope != nil {
		return fmt.Sprintf("wtp-%s-%04d", p.invocationScope.BranchID, sequence)
	}
	return fmt.Sprintf("wtp-%04d", sequence)
}

func (p *Provider) taskPath(status core.Status, filename string) string {
	return filepath.Join(p.statusDir(status), filename+".json")
}

func taskFilenames(task core.Task) []string {
	return []string{task.ShortID, task.ID}
}

func isTaskFilename(name string, task core.Task) bool {
	return name == task.ShortID+".json" || name == task.ID+".json"
}

func invalidTaskFilenameError(path string, task core.Task) error {
	return fmt.Errorf("task file %s must use shortId filename %s (or canonical UUID legacy filename)", path, task.ShortID+".json")
}

func (p *Provider) migrateTaskFilenames() error {
	type migration struct {
		currentPath  string
		expectedPath string
		task         core.Task
	}
	var migrations []migration
	var repairs []taskRepair
	tasksByPath := make(map[string]core.Task)
	repairClock := time.Now().UTC()

	for _, status := range p.statuses() {
		dir := p.statusDir(status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			currentPath := filepath.Join(dir, entry.Name())
			var task core.Task
			if err := readJSON(currentPath, &task); err != nil {
				return fmt.Errorf("corrupt task file %s: %w", currentPath, err)
			}
			if task.Status != status {
				return fmt.Errorf("task file %s status %s does not match directory %s", currentPath, task.Status, status)
			}
			task, repaired, err := p.validateOrRepairTask(task, repairClock)
			if err != nil {
				return fmt.Errorf("invalid task file %s: %w", currentPath, err)
			}
			if repaired {
				repairs = append(repairs, taskRepair{path: currentPath, task: task})
			}
			tasksByPath[currentPath] = task
			expectedPath := p.taskPath(status, task.ShortID)
			if currentPath == expectedPath {
				continue
			}
			if entry.Name() != task.ID+".json" {
				return invalidTaskFilenameError(currentPath, task)
			}
			migrations = append(migrations, migration{currentPath: currentPath, expectedPath: expectedPath, task: task})
		}
	}

	plannedTargets := make(map[string]migration, len(migrations))
	for _, item := range migrations {
		if existing, ok := tasksByPath[item.expectedPath]; ok && !reflect.DeepEqual(existing, item.task) {
			return fmt.Errorf("refuse to overwrite conflicting task file %s while migrating %s", item.expectedPath, item.currentPath)
		}
		if previous, ok := plannedTargets[item.expectedPath]; ok && !reflect.DeepEqual(previous.task, item.task) {
			return fmt.Errorf("refuse to migrate conflicting task files %s and %s to %s", previous.currentPath, item.currentPath, item.expectedPath)
		}
		plannedTargets[item.expectedPath] = item
	}

	migrationSources := make(map[string]struct{}, len(migrations))
	for _, item := range migrations {
		migrationSources[item.currentPath] = struct{}{}
	}
	for _, repair := range repairs {
		if _, migrating := migrationSources[repair.path]; migrating {
			continue
		}
		if err := p.writeJSONAtomic(repair.path, repair.task); err != nil {
			return fmt.Errorf("repair task file %s: %w", repair.path, err)
		}
	}
	for _, item := range migrations {
		if _, exists := tasksByPath[item.expectedPath]; !exists {
			if err := p.writeJSONAtomic(item.expectedPath, item.task); err != nil {
				return err
			}
			tasksByPath[item.expectedPath] = item.task
		}
		if err := p.removeFile(item.currentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy task file %s: %w", item.currentPath, err)
		}
	}
	return nil
}

func (p *Provider) allowedStatusTransition(from, to core.Status) bool {
	if len(p.statuses()) > 4 {
		_, err := p.catalog.ParseStatus(string(to))
		return err == nil
	}
	return core.AllowedTransition(from, to)
}

func (p *Provider) isStatusMoveResidue(older, newer core.Task) bool {
	if older.ID != newer.ID || older.ShortID != newer.ShortID || older.Status == newer.Status || !p.allowedStatusTransition(older.Status, newer.Status) {
		return false
	}
	want := older
	want.Status = newer.Status
	want.Assignee = newer.Assignee
	want.UpdatedAt = newer.UpdatedAt
	want.StartedAt = newer.StartedAt
	want.CompletedAt = newer.CompletedAt
	return reflect.DeepEqual(want, newer)
}

func resolveTask(idOrShortID string, tasks []core.Task) (core.Task, error) {
	idOrShortID = strings.TrimSpace(idOrShortID)
	if idOrShortID == "" {
		return core.Task{}, errors.New("task identifier is required")
	}
	var matches []core.Task
	for _, task := range tasks {
		if task.ID == idOrShortID || task.ShortID == idOrShortID {
			matches = append(matches, task)
		}
	}
	if len(matches) == 0 {
		return core.Task{}, fmt.Errorf("task %q not found", idOrShortID)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, fmt.Sprintf("%s (%s)", match.ShortID, match.ID))
		}
		sort.Strings(ids)
		return core.Task{}, fmt.Errorf("task identifier %q is ambiguous: %s", idOrShortID, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func (p *Provider) validateStartable(task core.Task, tasks []core.Task) error {
	blocked := []string{}
	for _, dependencyID := range task.Dependencies {
		for _, candidate := range tasks {
			if candidate.ID == dependencyID && !p.catalog.DependencyResolved(candidate.Status) {
				blocked = append(blocked, fmt.Sprintf("%s (%s)", candidate.ShortID, candidate.Title))
			}
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("task %s is blocked by unresolved dependencies: %s", task.ShortID, strings.Join(blocked, ", "))
	}
	return nil
}

func (p *Provider) selectNextEligibleTask(tasks []core.Task, agent string) (core.Task, error) {
	eligible, err := p.selectEligibleTasks(tasks, agent, 1)
	if err != nil {
		return core.Task{}, err
	}
	if len(eligible) > 0 {
		return eligible[0], nil
	}
	return core.Task{}, provider.ErrNoEligibleTask
}

func (p *Provider) selectEligibleTasks(tasks []core.Task, agent string, limit int) ([]core.Task, error) {
	if limit <= 0 {
		return nil, errors.New("ready task limit must be greater than zero")
	}
	eligible := make([]core.Task, 0, len(tasks))
	for _, task := range tasks {
		if p.automaticSelectionTier(task) < 0 {
			continue
		}
		if !p.catalog.IsClaimableStatus(task.Status) {
			continue
		}
		if err := p.validateStartable(task, tasks); err == nil {
			eligible = append(eligible, task)
		}
	}
	agent = strings.TrimSpace(agent)
	sort.Slice(eligible, func(i, j int) bool {
		leftTier := p.automaticSelectionTier(eligible[i])
		rightTier := p.automaticSelectionTier(eligible[j])
		if leftTier != rightTier {
			return leftTier < rightTier
		}
		if agent != "" {
			leftAssigneeRank := assigneeRank(eligible[i], agent)
			rightAssigneeRank := assigneeRank(eligible[j], agent)
			if leftAssigneeRank != rightAssigneeRank {
				return leftAssigneeRank < rightAssigneeRank
			}
		}
		if eligible[i].Status != eligible[j].Status {
			return eligible[i].Status == core.StatusPaused
		}
		if core.PriorityRank(eligible[i].Priority) != core.PriorityRank(eligible[j].Priority) {
			return core.PriorityRank(eligible[i].Priority) > core.PriorityRank(eligible[j].Priority)
		}
		if eligible[i].CreatedAt.Equal(eligible[j].CreatedAt) {
			return eligible[i].ShortID < eligible[j].ShortID
		}
		return eligible[i].CreatedAt.Before(eligible[j].CreatedAt)
	})
	if agent == "" {
		if len(eligible) == 0 {
			return nil, provider.ErrNoEligibleTask
		}
		return eligible[:min(limit, len(eligible))], nil
	}

	selected := make([]core.Task, 0, min(limit, len(eligible)))
	for _, task := range eligible {
		if task.Assignee == agent || task.Assignee == "" {
			selected = append(selected, task)
			if len(selected) == limit {
				return selected, nil
			}
		}
	}
	if len(selected) == 0 {
		return nil, provider.ErrNoEligibleTask
	}
	return selected, nil
}

func assigneeRank(task core.Task, agent string) int {
	if task.Assignee == agent {
		return 0
	}
	if task.Assignee == "" {
		return 1
	}
	return 2
}

// automaticSelectionTier returns the preference tier for automatic task
// selection. A named branch may select its own scoped tasks before legacy
// tasks; detached and non-Git invocations have no scope and may select only
// legacy tasks. Tasks from other branch scopes are never automatically
// eligible.
func (p *Provider) automaticSelectionTier(task core.Task) int {
	parts, err := core.ParseShortID(task.ShortID)
	if err != nil {
		return -1
	}
	if parts.IsLegacy() {
		return 1
	}
	if p.invocationScope != nil && parts.BranchID == p.invocationScope.BranchID {
		return 0
	}
	return -1
}

func (p *Provider) decorateTasks(tasks []core.Task, allTasks []core.Task, agent string) []core.TaskView {
	views := make([]core.TaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, p.decorateTask(task, allTasks, agent))
	}
	return views
}

func (p *Provider) decorateTask(task core.Task, allTasks []core.Task, agent string) core.TaskView {
	blockedReason := p.blockedReason(task, allTasks)
	return core.TaskView{
		Task: task,
		Readiness: core.TaskReadiness{
			Claimable:              p.isClaimable(task, allTasks, agent),
			Blocked:                blockedReason != "",
			BlockedReason:          blockedReason,
			DependencyCount:        len(task.Dependencies),
			ReverseDependencyCount: reverseDependencyCount(task.ID, allTasks),
		},
	}
}

func (p *Provider) blockedReason(task core.Task, tasks []core.Task) string {
	blocked := []string{}
	for _, dependencyID := range task.Dependencies {
		for _, candidate := range tasks {
			if candidate.ID == dependencyID && !p.catalog.DependencyResolved(candidate.Status) {
				blocked = append(blocked, fmt.Sprintf("%s (%s)", candidate.ShortID, candidate.Title))
			}
		}
	}
	if len(blocked) == 0 {
		return ""
	}
	sort.Strings(blocked)
	return fmt.Sprintf("unresolved dependencies: %s", strings.Join(blocked, ", "))
}

func (p *Provider) isClaimable(task core.Task, tasks []core.Task, agent string) bool {
	if p.automaticSelectionTier(task) < 0 {
		return false
	}
	if !p.catalog.IsClaimableStatus(task.Status) {
		return false
	}
	if p.blockedReason(task, tasks) != "" {
		return false
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return true
	}
	return task.Assignee == agent || task.Assignee == ""
}

func reverseDependencyCount(taskID string, tasks []core.Task) int {
	count := 0
	for _, task := range tasks {
		if slices.Contains(task.Dependencies, taskID) {
			count++
		}
	}
	return count
}

func replaceTaskInMemory(tasks []core.Task, updated core.Task) []core.Task {
	out := make([]core.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == updated.ID {
			out = append(out, updated)
			continue
		}
		out = append(out, task)
	}
	return out
}

func resolveDependencyIDs(identifiers []string, tasks []core.Task) ([]string, error) {
	resolved := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		task, err := resolveTask(identifier, tasks)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency %q: %w", identifier, err)
		}
		resolved = append(resolved, task.ID)
	}
	return core.NormalizeDependencies(resolved), nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSONAtomic(path string, value any) error {
	return writeJSONAtomicWithFileSystem(path, value, defaultFileSystem())
}

func (p *Provider) writeJSONAtomic(path string, value any) error {
	return writeJSONAtomicWithFileSystem(path, value, p.fs)
}

func writeJSONAtomicWithFileSystem(path string, value any, fs fileSystem) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	defer os.Remove(temp.Name())

	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temp.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temp for %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := fs.replace(temp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := fs.syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync parent for %s: %w", path, err)
	}
	return nil
}

func writeBytesAtomicWithFileSystem(path string, data []byte, fs fileSystem) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temp for %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := fs.replace(temp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := fs.syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync parent for %s: %w", path, err)
	}
	return nil
}

func (p *Provider) removeFile(path string) error {
	if err := p.fs.remove(path); err != nil {
		return err
	}
	return p.fs.syncDirectory(filepath.Dir(path))
}

var _ provider.Provider = (*Provider)(nil)
