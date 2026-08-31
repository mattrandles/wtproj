package provider

import "github.com/mattrandles/wtproj/internal/core"

// PlanningFilter is shared by planning list and report. Nil Status means all
// four statuses. Grouping uses the shared trimmed, case-insensitive exact AND
// matcher across all six fields; no agent, claim, or branch-scope filter.
type PlanningFilter struct {
	Status   *core.PlanningStatus
	Grouping core.GroupingFilter
}

// PlanningReader queries only planning records, including foreign branch IDs.
// Lists are non-nil and sorted by CreatedAt, then ShortID. Selectors are exact
// UUIDs or complete short IDs; execution IDs fail lookup. Views resolve live
// reusable definitions from the same locked snapshot and never load handoffs.
type PlanningReader interface {
	ListPlanningItems(filter PlanningFilter) ([]core.PlanningItemView, error)
	GetPlanningItem(idOrShortID string) (core.PlanningItemView, error)
}

type PlanningCreator interface {
	CreatePlanningItem(input core.CreatePlanningItemInput) (core.PlanningItemView, error)
}

// PlanningEditor cannot mutate status through a metadata patch or set an
// execution actor. Real changes advance UpdatedAt monotonically; no-ops
// preserve exact bytes. Status moves obey core.PlanningTransitions.
type PlanningEditor interface {
	UpdatePlanningItem(idOrShortID string, input core.UpdatePlanningItemInput) (core.PlanningItemView, error)
	SetPlanningStatus(idOrShortID string, target core.PlanningStatus) (core.PlanningItemView, error)
}

// PlanningPromotionResult has the same JSON envelope for preview and publish,
// while retaining distinct planning/execution item types at compile time.
// Count == len(Items), both are nonzero on success, and Items is in selection
// order. Preview returns planning views; publish returns resulting task views.
type PlanningPromotionResult[T core.PlanningItemView | core.TaskView] struct {
	DryRun bool `json:"dryRun"`
	Count  int  `json:"count"`
	Items  []T  `json:"items"`
}

// PlanningPromoter requires at least one non-empty grouping selector, selects
// only planned records, and rejects a selection that is not dependency-closed.
// Preview uses the read lock/snapshot with no repair, timestamps, allocation,
// or journal writes. Publish reselects and validates under one global lock;
// preview is not a reservation. See the architecture for recovery ordering.
type PlanningPromoter interface {
	PreviewPlanningPromotion(grouping core.GroupingFilter) (PlanningPromotionResult[core.PlanningItemView], error)
	PromotePlanningItems(grouping core.GroupingFilter) (PlanningPromotionResult[core.TaskView], error)
}

// PlanningProvider is an optional flat-file capability, separate from Provider
// and ReusableTaskProvider. Narrow interfaces permit incremental adoption
// without advertising unimplemented operations or widening execution queries.
// There is deliberately no delete, comment, batch, graph, claim, or export
// method here. Canonical export remains Provider.ExportCanonical.
type PlanningProvider interface {
	PlanningReader
	PlanningCreator
	PlanningEditor
	PlanningPromoter
}
