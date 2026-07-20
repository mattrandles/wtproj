package flatfile

import (
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

var statusOrder = []core.Status{
	core.StatusTodo,
	core.StatusInProgress,
	core.StatusPaused,
	core.StatusDone,
}

var canonicalExportFilenamePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)

type indexFile struct {
	Next int `json:"next"`
}

type Provider struct {
	root string
	fs   fileSystem
}

func New(root string) (*Provider, error) {
	p := &Provider{root: root, fs: defaultFileSystem()}
	if err := p.ensureLayout(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) ensureLayout() error {
	dirs := []string{
		p.root,
		p.statusDir(core.StatusTodo),
		p.statusDir(core.StatusInProgress),
		p.statusDir(core.StatusPaused),
		p.statusDir(core.StatusDone),
		filepath.Join(p.root, "meta"),
		filepath.Join(p.root, "meta", "locks"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return p.withGlobalLock(func() error {
		indexPath := p.indexPath()
		if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
			if err := p.writeJSONAtomic(indexPath, indexFile{Next: 1}); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", indexPath, err)
		}
		return p.migrateTaskFilenames()
	})
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
		views = decorateTasks(tasks, allTasks, filter.Agent)
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
		view = decorateTask(task, tasks, agent)
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
		if err := core.ValidateDependencies("", resolvedDependencies, tasks); err != nil {
			return err
		}
		index, err := p.readIndex()
		if err != nil {
			return err
		}
		shortID := fmt.Sprintf("wtp-%04d", index.Next)
		task := core.Task{
			ID:           id,
			ShortID:      shortID,
			Title:        strings.TrimSpace(input.Title),
			Description:  strings.TrimSpace(input.Description),
			Priority:     input.Priority,
			Estimate:     input.Estimate,
			Lane:         strings.TrimSpace(input.Lane),
			Model:        strings.TrimSpace(input.Model),
			Status:       core.StatusTodo,
			Assignee:     strings.TrimSpace(input.Assignee),
			Dependencies: resolvedDependencies,
			Comments:     []core.Comment{},
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := task.Validate(); err != nil {
			return err
		}
		index.Next++
		if err := p.writeIndex(index); err != nil {
			return err
		}
		if err := p.writeTask(task); err != nil {
			return err
		}
		created = task
		tasksAfter = append(tasks, task)
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	return decorateTask(created, tasksAfter, ""), nil
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
		if input.Assignee.Set {
			task.Assignee = strings.TrimSpace(input.Assignee.Value)
		}
		if input.Dependencies.Set {
			resolvedDependencies, err := resolveDependencyIDs(splitCSV(input.Dependencies.Value), tasks)
			if err != nil {
				return err
			}
			if err := core.ValidateDependencies(task.ID, resolvedDependencies, tasks); err != nil {
				return err
			}
			task.Dependencies = resolvedDependencies
		}

		task.UpdatedAt = time.Now().UTC()
		if err := task.Validate(); err != nil {
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
	return decorateTask(updated, tasksAfter, ""), nil
}

func (p *Provider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error) {
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
		if !core.AllowedTransition(task.Status, target) {
			return fmt.Errorf("invalid status transition from %s to %s", task.Status, target)
		}
		if target == core.StatusInProgress {
			if err := validateStartable(task, tasks); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		task.Status = target
		task.UpdatedAt = now
		if strings.TrimSpace(actor) != "" {
			task.Assignee = strings.TrimSpace(actor)
		}
		if target == core.StatusInProgress && task.StartedAt == nil {
			task.StartedAt = &now
		}
		if target == core.StatusDone {
			task.CompletedAt = &now
		}
		if err := task.Validate(); err != nil {
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
	return decorateTask(updated, tasksAfter, ""), nil
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
		now := time.Now().UTC()
		task.Comments = append(task.Comments, core.Comment{
			ID:        commentID,
			Author:    strings.TrimSpace(actor),
			Message:   strings.TrimSpace(message),
			CreatedAt: now,
		})
		task.UpdatedAt = now
		if err := task.Validate(); err != nil {
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
	return decorateTask(updated, tasksAfter, ""), nil
}

func (p *Provider) PeekNextTask(agent string) (core.TaskView, error) {
	var view core.TaskView
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		task, err := selectNextEligibleTask(tasks, agent)
		if err != nil {
			return err
		}
		view = decorateTask(task, tasks, agent)
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
		selected, err := selectEligibleTasks(tasks, agent, limit)
		if err != nil {
			return err
		}
		views = decorateTasks(selected, tasks, agent)
		return nil
	})
	return views, err
}

func (p *Provider) GetNextTask(agent string) (core.TaskView, error) {
	var claimed core.Task
	var tasksAfter []core.Task
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		next, err := selectNextEligibleTask(tasks, agent)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		next.Status = core.StatusInProgress
		next.UpdatedAt = now
		if agent != "" {
			next.Assignee = agent
		}
		if next.StartedAt == nil {
			next.StartedAt = &now
		}
		if err := next.Validate(); err != nil {
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
	return decorateTask(claimed, tasksAfter, agent), nil
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
			if !info.Mode().IsRegular() || !canonicalExportFilenamePattern.MatchString(entry.Name()) {
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
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
		expected := make(map[string]struct{}, len(tasks))
		for _, task := range tasks {
			name := task.ID + ".json"
			expected[name] = struct{}{}
			path := filepath.Join(exportDir, name)
			if err := p.writeJSONAtomic(path, task); err != nil {
				return err
			}
		}
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
	tasksByID := make(map[string]core.Task)
	pathsByID := make(map[string]string)
	idsByShortID := make(map[string]string)
	for _, status := range statusOrder {
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
			if err := task.Validate(); err != nil {
				return nil, fmt.Errorf("invalid task file %s: %w", path, err)
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
			if !isStatusMoveResidue(older, newer) {
				return nil, fmt.Errorf("duplicate canonical task id %s in %s and %s is not valid status-move residue", task.ID, olderPath, newerPath)
			}
			tasksByID[task.ID] = newer
			pathsByID[task.ID] = newerPath
		}
	}
	tasks := make([]core.Task, 0, len(tasksByID))
	for _, task := range tasksByID {
		tasks = append(tasks, task)
	}
	if err := core.ValidateDependencies("", nil, tasks); err != nil {
		return nil, fmt.Errorf("invalid dependency graph: %w", err)
	}
	return tasks, nil
}

func (p *Provider) replaceTask(task core.Task) error {
	targetPath := filepath.Join(p.statusDir(task.Status), task.ShortID+".json")
	if err := p.writeJSONAtomic(targetPath, task); err != nil {
		return err
	}

	for _, status := range statusOrder {
		paths := []string{
			filepath.Join(p.statusDir(status), task.ShortID+".json"),
			filepath.Join(p.statusDir(status), task.ID+".json"),
		}
		for _, path := range paths {
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
	return p.writeJSONAtomic(filepath.Join(p.statusDir(task.Status), task.ShortID+".json"), task)
}

func (p *Provider) readIndex() (indexFile, error) {
	var index indexFile
	if err := readJSON(p.indexPath(), &index); err != nil {
		return indexFile{}, fmt.Errorf("read index: %w", err)
	}
	if index.Next == 0 {
		index.Next = 1
	}
	return index, nil
}

func (p *Provider) writeIndex(index indexFile) error {
	return p.writeJSONAtomic(p.indexPath(), index)
}

func (p *Provider) statusDir(status core.Status) string {
	return filepath.Join(p.root, string(status))
}

func (p *Provider) indexPath() string {
	return filepath.Join(p.root, "meta", "index.json")
}

func (p *Provider) migrateTaskFilenames() error {
	type migration struct {
		currentPath  string
		expectedPath string
		task         core.Task
	}
	var migrations []migration
	tasksByPath := make(map[string]core.Task)

	for _, status := range statusOrder {
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
			if err := task.Validate(); err != nil {
				return fmt.Errorf("invalid task file %s: %w", currentPath, err)
			}
			tasksByPath[currentPath] = task
			expectedPath := filepath.Join(dir, task.ShortID+".json")
			if currentPath == expectedPath {
				continue
			}
			if entry.Name() != task.ID+".json" {
				return fmt.Errorf("task file %s must use shortId filename %s (or canonical UUID legacy filename)", currentPath, filepath.Base(expectedPath))
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

func isStatusMoveResidue(older, newer core.Task) bool {
	if older.ID != newer.ID || older.ShortID != newer.ShortID || !core.AllowedTransition(older.Status, newer.Status) {
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

func validateStartable(task core.Task, tasks []core.Task) error {
	blocked := []string{}
	for _, dependencyID := range task.Dependencies {
		for _, candidate := range tasks {
			if candidate.ID == dependencyID && candidate.Status != core.StatusDone {
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

func selectNextEligibleTask(tasks []core.Task, agent string) (core.Task, error) {
	eligible, err := selectEligibleTasks(tasks, agent, 1)
	if err != nil {
		return core.Task{}, err
	}
	if len(eligible) > 0 {
		return eligible[0], nil
	}
	return core.Task{}, provider.ErrNoEligibleTask
}

func selectEligibleTasks(tasks []core.Task, agent string, limit int) ([]core.Task, error) {
	if limit <= 0 {
		return nil, errors.New("ready task limit must be greater than zero")
	}
	eligible := make([]core.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.Status != core.StatusPaused && task.Status != core.StatusTodo {
			continue
		}
		if err := validateStartable(task, tasks); err == nil {
			eligible = append(eligible, task)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
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
	agent = strings.TrimSpace(agent)
	if agent == "" {
		if len(eligible) == 0 {
			return nil, provider.ErrNoEligibleTask
		}
		return eligible[:min(limit, len(eligible))], nil
	}

	selected := make([]core.Task, 0, min(limit, len(eligible)))
	for _, task := range eligible {
		if task.Assignee == agent {
			selected = append(selected, task)
			if len(selected) == limit {
				return selected, nil
			}
		}
	}
	for _, task := range eligible {
		if task.Assignee == "" {
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

func decorateTasks(tasks []core.Task, allTasks []core.Task, agent string) []core.TaskView {
	views := make([]core.TaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, decorateTask(task, allTasks, agent))
	}
	return views
}

func decorateTask(task core.Task, allTasks []core.Task, agent string) core.TaskView {
	blockedReason := blockedReason(task, allTasks)
	return core.TaskView{
		Task: task,
		Readiness: core.TaskReadiness{
			Claimable:              isClaimable(task, allTasks, agent),
			Blocked:                blockedReason != "",
			BlockedReason:          blockedReason,
			DependencyCount:        len(task.Dependencies),
			ReverseDependencyCount: reverseDependencyCount(task.ID, allTasks),
		},
	}
}

func blockedReason(task core.Task, tasks []core.Task) string {
	blocked := []string{}
	for _, dependencyID := range task.Dependencies {
		for _, candidate := range tasks {
			if candidate.ID == dependencyID && candidate.Status != core.StatusDone {
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

func isClaimable(task core.Task, tasks []core.Task, agent string) bool {
	if task.Status != core.StatusPaused && task.Status != core.StatusTodo {
		return false
	}
	if blockedReason(task, tasks) != "" {
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

func (p *Provider) removeFile(path string) error {
	if err := p.fs.remove(path); err != nil {
		return err
	}
	return p.fs.syncDirectory(filepath.Dir(path))
}

var _ provider.Provider = (*Provider)(nil)

func init() {
	if !slices.Equal(statusOrder, []core.Status{
		core.StatusTodo,
		core.StatusInProgress,
		core.StatusPaused,
		core.StatusDone,
	}) {
		panic("unexpected status order")
	}
}
