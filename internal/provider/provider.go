package provider

import (
	"errors"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

var ErrNoEligibleTask = errors.New("no eligible task found")

var ErrStaleTask = errors.New("stale task update")

type TaskFilter struct {
	Status   *core.Status
	Agent    string
	Grouping core.GroupingFilter
}

// SelectionFilter provides the non-status constraints used when previewing or
// atomically claiming the next eligible task. Grouping is applied before the
// provider's branch scope, lifecycle, dependency, assignee, priority, and age
// selection rules.
type SelectionFilter struct {
	Agent    string
	Grouping core.GroupingFilter
}

type HandoffWriteRequest struct {
	Task    string
	Author  string
	Message string
	Replace bool
}

type HandoffWriteResult struct {
	Handoff    core.Handoff `json:"handoff"`
	ScopeCount int          `json:"scopeCount"`
}

type HandoffFilter struct {
	Task      string
	AllScopes bool
	Limit     int
}

type HandoffListResult struct {
	Handoffs             []core.Handoff `json:"handoffs"`
	TotalMatching        int            `json:"totalMatching"`
	HasMore              bool           `json:"hasMore"`
	OtherScopesAvailable bool           `json:"otherScopesAvailable"`
}

type HandoffPurgeRequest struct {
	ID        string
	Task      string
	Global    bool
	AllScopes bool
	Before    *time.Time
}

type HandoffPurgeResult struct {
	Purged int `json:"purged"`
}

type BatchUpdateRequest struct {
	Tasks []core.BatchTaskUpdateInput
}

type BatchUpdateResult struct {
	Updated   []core.TaskView `json:"updated"`
	Unchanged []core.TaskView `json:"unchanged"`
}

type Provider interface {
	StatusCatalog() core.StatusCatalog
	ListTasks(filter TaskFilter) ([]core.TaskView, error)
	WriteHandoff(request HandoffWriteRequest) (HandoffWriteResult, error)
	ListHandoffs(filter HandoffFilter) (HandoffListResult, error)
	PurgeHandoffs(request HandoffPurgeRequest) (HandoffPurgeResult, error)
	GetTask(idOrShortID, agent string) (core.TaskView, error)
	CreateTask(input core.CreateTaskInput) (core.TaskView, error)
	UpdateTask(idOrShortID string, input core.UpdateTaskInput) (core.TaskView, error)
	BatchUpdate(request BatchUpdateRequest) (BatchUpdateResult, error)
	UpdateTaskStatus(idOrShortID string, target core.Status, actor string) (core.TaskView, error)
	AddComment(idOrShortID, actor, message string) (core.TaskView, error)
	PeekNextTask(agent string) (core.TaskView, error)
	PeekNextTasks(agent string, limit int) ([]core.TaskView, error)
	GetNextTask(agent string) (core.TaskView, error)
	PeekNextTaskWithFilter(filter SelectionFilter) (core.TaskView, error)
	PeekNextTasksWithFilter(filter SelectionFilter, limit int) ([]core.TaskView, error)
	GetNextTaskWithFilter(filter SelectionFilter) (core.TaskView, error)
	ExportCanonical(outDir string) error
}
