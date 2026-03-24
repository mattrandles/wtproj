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

func (p *Provider) ListTasks(filter provider.TaskFilter) ([]core.Task, error) {
	tasks, err := p.loadTasks()
	if err != nil {
		return nil, err
	}
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
	return tasks, nil
}

func (p *Provider) GetTask(idOrShortID string) (core.Task, error) {
	tasks, err := p.loadTasks()
	if err != nil {
		return core.Task{}, err
	}
	return resolveTask(idOrShortID, tasks)
}

func (p *Provider) CreateTask(input core.CreateTaskInput) (core.Task, error) {
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
		return core.Task{}, err
	}
	return created, nil
}

func (p *Provider) UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.Task, error) {
	var updated core.Task
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
		return nil
	})
	if err != nil {
		return core.Task{}, err
	}
	return updated, nil
}

func (p *Provider) AddComment(idOrShortID, actor, message string) (core.Task, error) {
	var updated core.Task
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
		return nil
	})
	if err != nil {
		return core.Task{}, err
	}
	return updated, nil
}

func (p *Provider) GetNextTask(agent string) (core.Task, error) {
	var claimed core.Task
	err := p.withGlobalLock(func() error {
		tasks, err := p.loadTasks()
		if err != nil {
			return err
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
			if eligible[i].CreatedAt.Equal(eligible[j].CreatedAt) {
				return eligible[i].ShortID < eligible[j].ShortID
			}
			return eligible[i].CreatedAt.Before(eligible[j].CreatedAt)
		})
		agent = strings.TrimSpace(agent)
		var next core.Task
		found := false
		if agent != "" {
			for _, task := range eligible {
				if task.Assignee == agent {
					next = task
					found = true
					break
				}
			}
			if !found {
				for _, task := range eligible {
					if task.Assignee == "" {
						next = task
						found = true
						break
					}
				}
			}
		}
		if !found && len(eligible) > 0 {
			next = eligible[0]
			found = true
		}
		if !found {
			return errors.New("no eligible task found")
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
		return nil
	})
	if err != nil {
		return core.Task{}, err
	}
	return claimed, nil
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
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
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
