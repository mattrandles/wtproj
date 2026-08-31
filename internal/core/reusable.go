package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const ReusableTaskCatalogVersion = 1

// ReusableTaskDefinition is a store-global advisory item, not a backlog task.
// ID is a canonical lowercase UUID and remains stable across edits. Name is
// trimmed, non-empty, and unique within its catalog under strings.EqualFold.
// Title and Instructions are required, trimmed text; internal whitespace and
// Unicode are preserved. Timestamps are non-zero UTC instants, with UpdatedAt
// no earlier than CreatedAt. Definitions have no lifecycle or execution state.
type ReusableTaskDefinition struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Title        string    `json:"title"`
	Instructions string    `json:"instructions"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ReusableTaskCatalog is the version-1 .wtp/reusable.json document. Definitions
// is an ordered, non-null array. A missing file is represented by
// EmptyReusableTaskCatalog, without creating a file; an existing malformed file
// is not an empty catalog. Strict JSON decoding and file I/O belong to codecs
// and providers, not these types.
type ReusableTaskCatalog struct {
	Version     int                      `json:"version"`
	Definitions []ReusableTaskDefinition `json:"definitions"`
}

func EmptyReusableTaskCatalog() ReusableTaskCatalog {
	return ReusableTaskCatalog{
		Version:     ReusableTaskCatalogVersion,
		Definitions: []ReusableTaskDefinition{},
	}
}

// CreateReusableTaskInput supplies required text, trimmed by the provider
// before validation. The provider assigns the UUID and UTC timestamps.
type CreateReusableTaskInput struct {
	Name         string
	Title        string
	Instructions string
}

// UpdateReusableTaskInput is a partial edit. At least one field must be set;
// omission preserves a field, while a supplied blank value is invalid after
// trimming. ID and CreatedAt cannot be edited. A normalized no-op preserves
// UpdatedAt; an actual change advances it monotonically.
type UpdateReusableTaskInput struct {
	Name         OptionalString
	Title        OptionalString
	Instructions OptionalString
}

func (d ReusableTaskDefinition) Validate() error {
	if !canonicalUUIDPattern.MatchString(d.ID) {
		return fmt.Errorf("reusable definition id %q must be a canonical lowercase UUID", d.ID)
	}
	for _, field := range []struct{ name, value string }{
		{"name", d.Name}, {"title", d.Title}, {"instructions", d.Instructions},
	} {
		trimmed := strings.TrimSpace(field.value)
		if trimmed == "" {
			return fmt.Errorf("reusable definition %s is required", field.name)
		}
		if trimmed != field.value {
			return fmt.Errorf("reusable definition %s must be trimmed", field.name)
		}
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		return errors.New("reusable definition timestamps are required")
	}
	if !isUTC(d.CreatedAt) || !isUTC(d.UpdatedAt) {
		return errors.New("reusable definition timestamps must be in UTC")
	}
	if d.UpdatedAt.Before(d.CreatedAt) {
		return errors.New("reusable definition updatedAt cannot be before createdAt")
	}
	return nil
}

// Validate checks the in-memory wrapper and catalog-wide identity rules. It
// never trims, sorts, deduplicates, or otherwise repairs stored definitions.
func (c ReusableTaskCatalog) Validate() error {
	if c.Version != ReusableTaskCatalogVersion {
		return fmt.Errorf("unsupported reusable catalog version %d", c.Version)
	}
	if c.Definitions == nil {
		return errors.New("reusable catalog definitions must be an array")
	}
	ids := make(map[string]struct{}, len(c.Definitions))
	for index, definition := range c.Definitions {
		if err := definition.Validate(); err != nil {
			return fmt.Errorf("reusable catalog definition %d: %w", index, err)
		}
		if _, exists := ids[definition.ID]; exists {
			return fmt.Errorf("reusable definition id %q is duplicated", definition.ID)
		}
		ids[definition.ID] = struct{}{}
		for _, previous := range c.Definitions[:index] {
			if strings.EqualFold(previous.Name, definition.Name) {
				return fmt.Errorf("reusable definition name %q is duplicated", definition.Name)
			}
		}
	}
	return nil
}

// validateReusableTaskIDList checks stored IDs without requiring a catalog.
// Task.Validate uses this so legacy tasks remain independently valid.
func validateReusableTaskIDList(ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for index, id := range ids {
		if !canonicalUUIDPattern.MatchString(id) {
			return fmt.Errorf("task reusableTaskIds %d %q must be a canonical lowercase UUID", index, id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("task reusableTaskIds id %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// ResolveReusableTasks resolves stored UUID references against the current
// catalog in assignment order, independent of catalog order or definition
// names. Unknown or duplicate IDs are errors, never silently dropped. The
// returned slice is independent of the catalog and empty for no assignments.
// Providers must use one locked snapshot for tasks and their catalog.
func ResolveReusableTasks(ids []string, catalog ReusableTaskCatalog) ([]ReusableTaskDefinition, error) {
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	if err := validateReusableTaskIDList(ids); err != nil {
		return nil, err
	}
	byID := make(map[string]ReusableTaskDefinition, len(catalog.Definitions))
	for _, definition := range catalog.Definitions {
		byID[definition.ID] = definition
	}
	resolved := make([]ReusableTaskDefinition, 0, len(ids))
	for _, id := range ids {
		definition, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("task reusableTaskIds id %q does not exist", id)
		}
		resolved = append(resolved, definition)
	}
	return resolved, nil
}
