package core

import (
	"fmt"
	"regexp"
	"time"
)

// StatusCategory describes the lifecycle semantics of a status. The first
// four categories are the built-in lifecycle states; the latter three are
// the categories available to project-defined statuses.
type StatusCategory string

const (
	StatusCategoryTodo       StatusCategory = "todo"
	StatusCategoryInProgress StatusCategory = "inProgress"
	StatusCategoryPaused     StatusCategory = "paused"
	StatusCategoryDone       StatusCategory = "done"
	StatusCategoryWaiting    StatusCategory = "waiting"
	StatusCategoryBlocked    StatusCategory = "blocked"
	StatusCategoryFailed     StatusCategory = "failed"
)

// StatusDefinition is the on-disk and in-memory definition of a status.
// Additional project statuses may use only waiting, blocked, or failed.
type StatusDefinition struct {
	Name     Status         `json:"name"`
	Category StatusCategory `json:"category"`
}

var lowerCamelStatusName = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// StatusCatalog is an immutable status catalog. Its slices and maps are kept
// private, and every accessor returns a copy, so one invocation cannot mutate
// another invocation's status set.
type StatusCatalog struct {
	definitions []StatusDefinition
	categories  map[Status]StatusCategory
}

var defaultStatusDefinitions = [...]StatusDefinition{
	{Name: StatusTodo, Category: StatusCategoryTodo},
	{Name: StatusInProgress, Category: StatusCategoryInProgress},
	{Name: StatusPaused, Category: StatusCategoryPaused},
	{Name: StatusDone, Category: StatusCategoryDone},
}

// DefaultStatusCatalog returns a fresh catalog containing the legacy statuses.
func DefaultStatusCatalog() StatusCatalog {
	definitions := make([]StatusDefinition, len(defaultStatusDefinitions))
	copy(definitions, defaultStatusDefinitions[:])
	return newStatusCatalog(definitions)
}

// NewStatusCatalog builds the ordered default-plus-project status catalog.
func NewStatusCatalog(additional []StatusDefinition) (StatusCatalog, error) {
	definitions := make([]StatusDefinition, 0, len(defaultStatusDefinitions)+len(additional))
	definitions = append(definitions, defaultStatusDefinitions[:]...)
	seen := make(map[Status]struct{}, len(definitions)+len(additional))
	for _, definition := range definitions {
		seen[definition.Name] = struct{}{}
	}
	for index, definition := range additional {
		name := definition.Name
		category := definition.Category
		if !lowerCamelStatusName.MatchString(string(name)) {
			return StatusCatalog{}, fmt.Errorf("additional status %d name %q must be lower camel case", index, definition.Name)
		}
		if name == "all" {
			return StatusCatalog{}, fmt.Errorf("additional status %q is reserved", name)
		}
		if _, exists := seen[name]; exists {
			return StatusCatalog{}, fmt.Errorf("additional status %q is duplicated or collides with a default status", name)
		}
		switch category {
		case StatusCategoryWaiting, StatusCategoryBlocked, StatusCategoryFailed:
		default:
			return StatusCatalog{}, fmt.Errorf("additional status %q has invalid category %q; want waiting, blocked, or failed", name, category)
		}
		seen[name] = struct{}{}
		definitions = append(definitions, StatusDefinition{Name: name, Category: category})
	}
	return newStatusCatalog(definitions), nil
}

func newStatusCatalog(definitions []StatusDefinition) StatusCatalog {
	copyDefinitions := append([]StatusDefinition(nil), definitions...)
	categories := make(map[Status]StatusCategory, len(copyDefinitions))
	for _, definition := range copyDefinitions {
		categories[definition.Name] = definition.Category
	}
	return StatusCatalog{definitions: copyDefinitions, categories: categories}
}

// Statuses returns definitions in their configured order.
func (c StatusCatalog) Statuses() []StatusDefinition {
	return append([]StatusDefinition(nil), c.definitions...)
}

// Definitions is an alias for Statuses for callers that prefer the explicit
// definition-oriented name.
func (c StatusCatalog) Definitions() []StatusDefinition { return c.Statuses() }

func (c StatusCatalog) valid() StatusCatalog {
	if len(c.definitions) == 0 {
		return DefaultStatusCatalog()
	}
	return c
}

// Contains reports whether status belongs to the catalog.
func (c StatusCatalog) Contains(status Status) bool {
	c = c.valid()
	_, ok := c.categories[status]
	return ok
}

// Category returns a status's lifecycle category.
func (c StatusCatalog) Category(status Status) (StatusCategory, bool) {
	c = c.valid()
	category, ok := c.categories[status]
	return category, ok
}

// CategoryOf returns a status's category, or an empty category for an unknown
// status.
func (c StatusCatalog) CategoryOf(status Status) StatusCategory {
	category, _ := c.Category(status)
	return category
}

// ParseStatus validates a status against this catalog.
func (c StatusCatalog) ParseStatus(value string) (Status, error) {
	status := Status(value)
	if !c.Contains(status) {
		return "", fmt.Errorf("invalid status %q", value)
	}
	return status, nil
}

// ParseStatusWithCatalog is the function form of StatusCatalog.ParseStatus.
func ParseStatusWithCatalog(value string, catalog StatusCatalog) (Status, error) {
	return catalog.ParseStatus(value)
}

// DependencyResolved reports whether a status permits dependent tasks to
// start. Done is the only resolving status; failed is deliberately terminal
// but does not resolve dependencies.
func (c StatusCatalog) DependencyResolved(status Status) bool {
	return status == StatusDone && c.Contains(status)
}

// IsDependencyResolved is the predicate-style alias for DependencyResolved.
func (c StatusCatalog) IsDependencyResolved(status Status) bool {
	return c.DependencyResolved(status)
}

// IsTerminal reports whether a status is completed and cannot transition.
func (c StatusCatalog) IsTerminal(status Status) bool {
	return c.CategoryOf(status) == StatusCategoryDone || c.CategoryOf(status) == StatusCategoryFailed
}

// ResolvesDependencies is a descriptive alias for DependencyResolved.
func (c StatusCatalog) ResolvesDependencies(status Status) bool {
	return c.DependencyResolved(status)
}

// IsClaimableStatus reports the statuses eligible for a direct claim. Custom
// waiting and blocked statuses are intentionally non-claimable.
func (c StatusCatalog) IsClaimableStatus(status Status) bool {
	if !c.Contains(status) {
		return false
	}
	switch c.CategoryOf(status) {
	case StatusCategoryTodo, StatusCategoryPaused:
		return true
	default:
		return false
	}
}

// NormalizeTaskStatus applies destination-state lifecycle semantics to task.
// Missing timestamps are created at now when the destination state requires
// them; reopening states clear timestamps that no longer apply.
func (c StatusCatalog) NormalizeTaskStatus(task *Task, target Status, now time.Time) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}
	if _, err := c.ParseStatus(string(target)); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	task.Status = target
	switch c.CategoryOf(target) {
	case StatusCategoryTodo, StatusCategoryBlocked:
		task.StartedAt = nil
		task.CompletedAt = nil
	case StatusCategoryInProgress, StatusCategoryPaused, StatusCategoryWaiting:
		if task.StartedAt == nil {
			started := now
			task.StartedAt = &started
		}
		task.CompletedAt = nil
	case StatusCategoryDone, StatusCategoryFailed:
		if task.StartedAt == nil {
			started := now
			task.StartedAt = &started
		}
		if task.CompletedAt == nil {
			completed := now
			if completed.Before(*task.StartedAt) {
				completed = *task.StartedAt
			}
			task.CompletedAt = &completed
		}
	}
	return nil
}

// NormalizeLifecycle validates timestamps for a destination state without
// changing them. It is useful to callers that normalize storage themselves.
func (c StatusCatalog) NormalizeLifecycle(status Status, startedAt, completedAt *time.Time) error {
	if _, err := c.ParseStatus(string(status)); err != nil {
		return err
	}
	switch c.CategoryOf(status) {
	case StatusCategoryTodo, StatusCategoryBlocked:
		if startedAt != nil || completedAt != nil {
			return fmt.Errorf("%s task cannot have startedAt or completedAt", status)
		}
	case StatusCategoryInProgress, StatusCategoryPaused, StatusCategoryWaiting:
		if startedAt == nil {
			return fmt.Errorf("%s task requires startedAt", status)
		}
		if completedAt != nil {
			return fmt.Errorf("%s task cannot have completedAt", status)
		}
	case StatusCategoryDone, StatusCategoryFailed:
		if startedAt == nil || completedAt == nil {
			return fmt.Errorf("%s task requires startedAt and completedAt", status)
		}
	}
	if startedAt != nil && completedAt != nil && completedAt.Before(*startedAt) {
		return fmt.Errorf("task completedAt cannot be before startedAt")
	}
	return nil
}
