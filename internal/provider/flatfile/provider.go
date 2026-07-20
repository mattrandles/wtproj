package flatfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"wtp/internal/core"
	"wtp/internal/provider"
)

var statusOrder = []core.Status{
	core.StatusTodo,
	core.StatusInProgress,
	core.StatusPaused,
	core.StatusDone,
}

type indexFile struct {
	Next int `json:"next"`
}

type Provider struct {
	root string
}

func New(root string) (*Provider, error) {
	p := &Provider{root: root}
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
	indexPath := p.indexPath()
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		initial := indexFile{Next: 1}
		if err := writeJSONAtomic(indexPath, initial); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", indexPath, err)
	}
	return p.withGlobalLock(p.migrateTaskFilenames)
}

func (p *Provider) ListTasks(filter provider.TaskFilter) ([]core.TaskView, error) {
	tasks, err := p.loadTasks()
	if err != nil {
		return nil, err
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
	return decorateTasks(tasks, allTasks, filter.Agent), nil
}

func (p *Provider) GetTask(idOrShortID, agent string) (core.TaskView, error) {
	tasks, err := p.loadTasks()
	if err != nil {
		return core.TaskView{}, err
	}
	task, err := resolveTask(idOrShortID, tasks)
	if err != nil {
		return core.TaskView{}, err
	}
	return decorateTask(task, tasks, agent), nil
}

func (p *Provider) CreateTask(input core.CreateTaskInput) (core.TaskView, error) {
	var created core.Task
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
		return nil
	})
	if err != nil {
		return core.TaskView{}, err
	}
	return decorateTask(created, []core.Task{created}, ""), nil
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
	tasks, err := p.loadTasks()
	if err != nil {
		return core.TaskView{}, err
	}
	task, err := selectNextEligibleTask(tasks, agent)
	if err != nil {
		return core.TaskView{}, err
	}
	return decorateTask(task, tasks, agent), nil
}

func (p *Provider) PeekNextTasks(agent string, limit int) ([]core.TaskView, error) {
	tasks, err := p.loadTasks()
	if err != nil {
		return nil, err
	}
	selected, err := selectEligibleTasks(tasks, agent, limit)
	if err != nil {
		return nil, err
	}
	return decorateTasks(selected, tasks, agent), nil
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
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return fmt.Errorf("create export dir %s: %w", outDir, err)
		}
		tasks, err := p.loadTasks()
		if err != nil {
			return err
		}
		for _, task := range tasks {
			path := filepath.Join(outDir, task.ID+".json")
			if err := writeJSONAtomic(path, task); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *Provider) loadTasks() ([]core.Task, error) {
	var tasks []core.Task
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
			tasks = append(tasks, task)
		}
	}
	if err := core.ValidateDependencies("", nil, tasks); err != nil {
		return nil, fmt.Errorf("invalid dependency graph: %w", err)
	}
	return tasks, nil
}

func (p *Provider) replaceTask(task core.Task) error {
	for _, status := range statusOrder {
		paths := []string{
			filepath.Join(p.statusDir(status), task.ShortID+".json"),
			filepath.Join(p.statusDir(status), task.ID+".json"),
		}
		for _, path := range paths {
			err := os.Remove(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}
	return p.writeTask(task)
}

func (p *Provider) writeTask(task core.Task) error {
	return writeJSONAtomic(filepath.Join(p.statusDir(task.Status), task.ShortID+".json"), task)
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
	return writeJSONAtomic(p.indexPath(), index)
}

func (p *Provider) statusDir(status core.Status) string {
	return filepath.Join(p.root, string(status))
}

func (p *Provider) indexPath() string {
	return filepath.Join(p.root, "meta", "index.json")
}

func (p *Provider) migrateTaskFilenames() error {
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
			expectedPath := filepath.Join(dir, task.ShortID+".json")
			if currentPath == expectedPath {
				continue
			}
			if err := writeJSONAtomic(expectedPath, task); err != nil {
				return err
			}
			if err := os.Remove(currentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove legacy task file %s: %w", currentPath, err)
			}
		}
	}
	return nil
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
		temp.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		if err := copyFile(temp.Name(), path); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
