package core

import (
	"fmt"
	"time"
)

// PlanningStatus belongs to the planning lifecycle, never StatusCatalog.
// The normative contract is docs/planning-lifecycle.md (architecture v1).
type PlanningStatus string

const (
	PlanningStatusToplan     PlanningStatus = "toplan"
	PlanningStatusResearched PlanningStatus = "researched"
	PlanningStatusPlanned    PlanningStatus = "planned"
	PlanningStatusRejected   PlanningStatus = "rejected"
)

var fixedPlanningStatuses = [...]PlanningStatus{
	PlanningStatusToplan,
	PlanningStatusResearched,
	PlanningStatusPlanned,
	PlanningStatusRejected,
}

var fixedPlanningTransitions = [...]PlanningTransition{
	{From: PlanningStatusToplan, To: PlanningStatusResearched},
	{From: PlanningStatusToplan, To: PlanningStatusRejected},
	{From: PlanningStatusResearched, To: PlanningStatusToplan},
	{From: PlanningStatusResearched, To: PlanningStatusPlanned},
	{From: PlanningStatusResearched, To: PlanningStatusRejected},
	{From: PlanningStatusPlanned, To: PlanningStatusResearched},
	{From: PlanningStatusPlanned, To: PlanningStatusRejected},
	{From: PlanningStatusRejected, To: PlanningStatusToplan},
}

// PlanningStatusCatalog is the immutable, fixed planning lifecycle catalog.
// It intentionally has no configurable entries and is separate from
// StatusCatalog, so execution status names cannot change planning semantics.
type PlanningStatusCatalog struct {
	statuses    []PlanningStatus
	transitions map[PlanningStatus]map[PlanningStatus]struct{}
}

// DefaultPlanningStatusCatalog returns the fixed planning lifecycle catalog.
func DefaultPlanningStatusCatalog() PlanningStatusCatalog {
	statuses := append([]PlanningStatus(nil), fixedPlanningStatuses[:]...)
	transitions := make(map[PlanningStatus]map[PlanningStatus]struct{}, len(fixedPlanningTransitions))
	for _, transition := range fixedPlanningTransitions {
		if transitions[transition.From] == nil {
			transitions[transition.From] = make(map[PlanningStatus]struct{})
		}
		transitions[transition.From][transition.To] = struct{}{}
	}
	return PlanningStatusCatalog{statuses: statuses, transitions: transitions}
}

// FixedPlanningStatusCatalog is a descriptive alias for
// DefaultPlanningStatusCatalog.
func FixedPlanningStatusCatalog() PlanningStatusCatalog {
	return DefaultPlanningStatusCatalog()
}

func (c PlanningStatusCatalog) valid() PlanningStatusCatalog {
	if len(c.statuses) == 0 {
		return DefaultPlanningStatusCatalog()
	}
	return c
}

// Statuses returns planning statuses in canonical storage/report order.
func (c PlanningStatusCatalog) Statuses() []PlanningStatus {
	c = c.valid()
	return append([]PlanningStatus(nil), c.statuses...)
}

// Contains reports whether status is one of the fixed planning statuses.
func (c PlanningStatusCatalog) Contains(status PlanningStatus) bool {
	c = c.valid()
	for _, candidate := range c.statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

// ParseStatus accepts only exact, case-sensitive planning status values. It
// does not trim or normalize input and never consults execution statuses.
func (c PlanningStatusCatalog) ParseStatus(value string) (PlanningStatus, error) {
	status := PlanningStatus(value)
	if !c.Contains(status) {
		return "", fmt.Errorf("invalid planning status %q", value)
	}
	return status, nil
}

// Parse is a concise alias for ParseStatus.
func (c PlanningStatusCatalog) Parse(value string) (PlanningStatus, error) {
	return c.ParseStatus(value)
}

// PlanningStatuses returns a fresh copy in storage/report order. Parsing and
// validation use this fixed set, independent of execution configuration.
func PlanningStatuses() []PlanningStatus {
	return DefaultPlanningStatusCatalog().Statuses()
}

type PlanningTransition struct {
	From PlanningStatus
	To   PlanningStatus
}

// PlanningTransitions is the complete revisable transition table. Creation
// may choose any planning status; same-state and unlisted moves are errors.
func PlanningTransitions() []PlanningTransition {
	return append([]PlanningTransition(nil), fixedPlanningTransitions[:]...)
}

// AllowedTransition reports whether a direct planning status move is in the
// fixed lifecycle table. Unknown and same-state moves are rejected.
func (c PlanningStatusCatalog) AllowedTransition(from, to PlanningStatus) bool {
	c = c.valid()
	if !c.Contains(from) || !c.Contains(to) || from == to {
		return false
	}
	_, ok := c.transitions[from][to]
	return ok
}

// CanTransition is a descriptive alias for AllowedTransition.
func (c PlanningStatusCatalog) CanTransition(from, to PlanningStatus) bool {
	return c.AllowedTransition(from, to)
}

// ParsePlanningStatus parses against the fixed planning catalog.
func ParsePlanningStatus(value string) (PlanningStatus, error) {
	return DefaultPlanningStatusCatalog().ParseStatus(value)
}

// ParsePlanningStatusWithCatalog is the function form of
// PlanningStatusCatalog.ParseStatus.
func ParsePlanningStatusWithCatalog(value string, catalog PlanningStatusCatalog) (PlanningStatus, error) {
	return catalog.ParseStatus(value)
}

// AllowedPlanningTransition validates a direct move against the fixed
// planning lifecycle table.
func AllowedPlanningTransition(from, to PlanningStatus) bool {
	return DefaultPlanningStatusCatalog().AllowedTransition(from, to)
}

// ValidatePlanningTimestamps enforces the planning record invariant that
// execution lifecycle timestamps are always absent.
func ValidatePlanningTimestamps(startedAt, completedAt *time.Time) error {
	if startedAt != nil || completedAt != nil {
		return fmt.Errorf("planning record cannot have startedAt or completedAt")
	}
	return nil
}

// NormalizeLifecycle validates a planning status and its timestamps. Unlike
// execution lifecycle normalization, it never creates or repairs timestamps.
func (c PlanningStatusCatalog) NormalizeLifecycle(status PlanningStatus, startedAt, completedAt *time.Time) error {
	if _, err := c.ParseStatus(string(status)); err != nil {
		return err
	}
	return ValidatePlanningTimestamps(startedAt, completedAt)
}

// ValidateLifecycle is a descriptive alias for NormalizeLifecycle.
func (c PlanningStatusCatalog) ValidateLifecycle(status PlanningStatus, startedAt, completedAt *time.Time) error {
	return c.NormalizeLifecycle(status, startedAt, completedAt)
}

// ValidatePlanningLifecycle validates against the fixed planning catalog.
func ValidatePlanningLifecycle(status PlanningStatus, startedAt, completedAt *time.Time) error {
	return DefaultPlanningStatusCatalog().NormalizeLifecycle(status, startedAt, completedAt)
}

// ValidatePlanningLifecycle validates the status and timestamp invariant for
// a planning record. Full metadata validation is intentionally provided by
// the planning serialization/provider layer.
func (p PlanningItem) ValidatePlanningLifecycle() error {
	return ValidatePlanningLifecycle(p.Status, p.StartedAt, p.CompletedAt)
}

// Validate checks the complete planning-record payload. Shared metadata uses
// the same rules as executable tasks, while status and lifecycle timestamps
// use the independent planning contract.
func (p PlanningItem) Validate() error {
	if err := p.ValidatePlanningLifecycle(); err != nil {
		return err
	}
	task := Task{
		ID:              p.ID,
		ShortID:         p.ShortID,
		Title:           p.Title,
		Description:     p.Description,
		Priority:        p.Priority,
		Estimate:        p.Estimate,
		Lane:            p.Lane,
		Model:           p.Model,
		IssueID:         p.IssueID,
		Project:         p.Project,
		Milestone:       p.Milestone,
		Version:         p.Version,
		FeatureID:       p.FeatureID,
		Feature:         p.Feature,
		GitRepo:         p.GitRepo,
		GitBranch:       p.GitBranch,
		WorktreeName:    p.WorktreeName,
		WorktreeDir:     p.WorktreeDir,
		Assignee:        p.Assignee,
		Dependencies:    p.Dependencies,
		Comments:        p.Comments,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
		ReusableTaskIDs: p.ReusableTaskIDs,
	}
	return task.validateMetadata()
}

// PlanningItem preserves Task's entire stored payload and JSON spelling,
// including comments already present in storage. It deliberately does not
// embed Task: its status is independently typed and it must not inherit
// execution validation or lifecycle methods. Contract tests enforce parity
// whenever Task gains metadata. StartedAt and CompletedAt must always be nil.
// Record validation/serialization enforcement is a separate implementation.
type PlanningItem struct {
	ID           string         `json:"id"`
	ShortID      string         `json:"shortId"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Priority     Priority       `json:"priority,omitempty"`
	Estimate     Estimate       `json:"estimate,omitempty"`
	Lane         string         `json:"lane,omitempty"`
	Model        string         `json:"model,omitempty"`
	IssueID      string         `json:"issueId,omitempty"`
	Project      string         `json:"project,omitempty"`
	Milestone    string         `json:"milestone,omitempty"`
	Version      string         `json:"version,omitempty"`
	FeatureID    string         `json:"featureId,omitempty"`
	Feature      string         `json:"feature,omitempty"`
	GitRepo      string         `json:"gitRepo,omitempty"`
	GitBranch    string         `json:"gitBranch,omitempty"`
	WorktreeName string         `json:"worktreeName,omitempty"`
	WorktreeDir  string         `json:"worktreeDir,omitempty"`
	Status       PlanningStatus `json:"status"`
	Assignee     string         `json:"assignee,omitempty"`
	Dependencies []string       `json:"dependencies"`
	Comments     []Comment      `json:"comments"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	StartedAt    *time.Time     `json:"startedAt"`
	CompletedAt  *time.Time     `json:"completedAt"`

	ReusableTaskIDs []string `json:"reusableTaskIds,omitempty"`
}

// PlanningItemView resolves live advisory definitions in assignment order.
// It has no readiness, claimability, or task-scoped handoffs.
type PlanningItemView struct {
	PlanningItem
	ReusableTasks []ReusableTaskDefinition `json:"reusableTasks,omitempty"`
}

// CreatePlanningItemInput has task-create metadata with a planning status.
// Empty status defaults to toplan at the provider/CLI boundary, not in parsing.
// Dependency selectors resolve across both lifecycles. Reusable selectors
// resolve against the single store-global catalog, preserving caller order.
type CreatePlanningItemInput struct {
	Title         string
	Description   string
	Status        PlanningStatus
	Priority      Priority
	Estimate      Estimate
	Lane          string
	Model         string
	IssueID       string
	Project       string
	Milestone     string
	Version       string
	FeatureID     string
	Feature       string
	GitRepo       string
	GitBranch     string
	WorktreeName  string
	WorktreeDir   string
	Assignee      string
	Dependencies  []string
	ReusableTasks []string
}

// UpdatePlanningItemInput intentionally has no Status. Omission preserves;
// set empty values clear optional fields. Dependencies and ReusableTasks
// replace whole lists. Dependencies use a typed list (unlike the legacy
// task-update comma string); reusable assignment order is significant.
type UpdatePlanningItemInput struct {
	Title         OptionalString
	Description   OptionalString
	Priority      OptionalPriority
	Estimate      OptionalEstimate
	Lane          OptionalString
	Model         OptionalString
	IssueID       OptionalString
	Project       OptionalString
	Milestone     OptionalString
	Version       OptionalString
	FeatureID     OptionalString
	Feature       OptionalString
	GitRepo       OptionalString
	GitBranch     OptionalString
	WorktreeName  OptionalString
	WorktreeDir   OptionalString
	Assignee      OptionalString
	Dependencies  OptionalStrings
	ReusableTasks OptionalStrings
}
