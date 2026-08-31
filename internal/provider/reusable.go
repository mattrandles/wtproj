package provider

import "github.com/mattrandles/wtproj/internal/core"

// ReusableTaskDeleteResult reports an atomic delete-and-detach operation.
// DetachedTaskCount counts tasks changed, across every status and branch scope.
type ReusableTaskDeleteResult struct {
	Deleted           core.ReusableTaskDefinition `json:"deleted"`
	DetachedTaskCount int                         `json:"detachedTaskCount"`
}

// ReusableTaskProvider extends Provider with the reusable catalog operations
// that are available once a provider can read and create definitions. It is
// separate from Provider so implementations can adopt the capability without
// placeholder methods claiming support before later mutations are ready.
//
// Selectors are trimmed canonical UUIDs or case-insensitive exact names (using
// strings.EqualFold); an exact UUID match takes precedence over a name match.
// Unknown selectors and duplicate resolved assignments are errors. Task input
// assignments replace the full list in caller order, never sort or deduplicate
// it. Stored tasks contain UUIDs only, so renames do not rewrite assignments.
// All returned task views resolve current definitions from the same snapshot
// as the task, including lists, mutation results, previews, and claims.
//
// Implementations serialize catalog/task mutations under the store-wide lock,
// validate before publication, and return errors without partial writes. A
// missing catalog means empty and read-only calls must not create its file.
// Advisory items do not generate queue tasks, infer group endings, have item
// status, enforce completion, execute commands, or introduce stats dimensions.
type ReusableTaskProvider interface {
	Provider

	// ListReusableTasks returns a non-nil array ordered by lowercased name,
	// then UUID. Definitions are global, with no branch or grouping selectors.
	ListReusableTasks() ([]core.ReusableTaskDefinition, error)
	GetReusableTask(nameOrID string) (core.ReusableTaskDefinition, error)

	// CreateReusableTask trims required text, assigns a collision-free UUID
	// and UTC timestamps, and validates catalog-wide name/ID uniqueness.
	CreateReusableTask(input core.CreateReusableTaskInput) (core.ReusableTaskDefinition, error)
}

// ReusableTaskMutationProvider adds the later edit and delete operations to
// the read/create capability. Keeping it separate lets consumers of list,
// show, and create depend only on the operations they actually need.
type ReusableTaskMutationProvider interface {
	ReusableTaskProvider

	// UpdateReusableTask preserves UUID and CreatedAt on rename, accepts
	// casing-only name changes, and advances UpdatedAt only for actual changes.
	UpdateReusableTask(nameOrID string, input core.UpdateReusableTaskInput) (core.ReusableTaskDefinition, error)

	// DeleteReusableTask atomically removes the definition and detaches its
	// UUID everywhere, retaining other assignments in order. Affected tasks
	// advance UpdatedAt monotonically without comments or lifecycle changes.
	DeleteReusableTask(nameOrID string) (ReusableTaskDeleteResult, error)
}
