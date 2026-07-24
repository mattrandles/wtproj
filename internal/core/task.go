package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shortIDPattern       = regexp.MustCompile(`^wtp-[0-9]{4,}$`)
)

type Status string
type Priority string
type Estimate string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "inProgress"
	StatusPaused     Status = "paused"
	StatusDone       Status = "done"

	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"

	EstimateXS Estimate = "xs"
	EstimateS  Estimate = "s"
	EstimateM  Estimate = "m"
	EstimateL  Estimate = "l"
	EstimateXL Estimate = "xl"
)

var validStatuses = []Status{
	StatusTodo,
	StatusInProgress,
	StatusPaused,
	StatusDone,
}

var validPriorities = []Priority{
	PriorityLow,
	PriorityMedium,
	PriorityHigh,
	PriorityUrgent,
}

var validEstimates = []Estimate{
	EstimateXS,
	EstimateS,
	EstimateM,
	EstimateL,
	EstimateXL,
}

type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type TaskReadiness struct {
	Claimable              bool   `json:"claimable"`
	Blocked                bool   `json:"blocked"`
	BlockedReason          string `json:"blockedReason,omitempty"`
	DependencyCount        int    `json:"dependencyCount"`
	ReverseDependencyCount int    `json:"reverseDependencyCount"`
}

type Task struct {
	ID           string     `json:"id"`
	ShortID      string     `json:"shortId"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Priority     Priority   `json:"priority,omitempty"`
	Estimate     Estimate   `json:"estimate,omitempty"`
	Lane         string     `json:"lane,omitempty"`
	Model        string     `json:"model,omitempty"`
	GitRepo      string     `json:"gitRepo,omitempty"`
	GitBranch    string     `json:"gitBranch,omitempty"`
	WorktreeName string     `json:"worktreeName,omitempty"`
	WorktreeDir  string     `json:"worktreeDir,omitempty"`
	Status       Status     `json:"status"`
	Assignee     string     `json:"assignee,omitempty"`
	Dependencies []string   `json:"dependencies"`
	Comments     []Comment  `json:"comments"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	StartedAt    *time.Time `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
}

type TaskView struct {
	Task
	Readiness TaskReadiness `json:"readiness"`
}

type CreateTaskInput struct {
	Title        string
	Description  string
	Priority     Priority
	Estimate     Estimate
	Lane         string
	Model        string
	GitRepo      string
	GitBranch    string
	WorktreeName string
	WorktreeDir  string
	Assignee     string
	Dependencies []string
}

type OptionalString struct {
	Set   bool
	Value string
}

type OptionalPriority struct {
	Set   bool
	Value Priority
}

type OptionalEstimate struct {
	Set   bool
	Value Estimate
}

type UpdateTaskInput struct {
	Title        OptionalString
	Description  OptionalString
	Priority     OptionalPriority
	Estimate     OptionalEstimate
	Lane         OptionalString
	Model        OptionalString
	GitRepo      OptionalString
	GitBranch    OptionalString
	WorktreeName OptionalString
	WorktreeDir  OptionalString
	Assignee     OptionalString
	Dependencies OptionalString
}

func ParseStatus(value string) (Status, error) {
	status := Status(value)
	if !slices.Contains(validStatuses, status) {
		return "", fmt.Errorf("invalid status %q", value)
	}
	return status, nil
}

func ParsePriority(value string) (Priority, error) {
	priority := Priority(strings.TrimSpace(strings.ToLower(value)))
	if priority == "" {
		return "", nil
	}
	if !slices.Contains(validPriorities, priority) {
		return "", fmt.Errorf("invalid priority %q", value)
	}
	return priority, nil
}

func ParseEstimate(value string) (Estimate, error) {
	estimate := Estimate(strings.TrimSpace(strings.ToLower(value)))
	if estimate == "" {
		return "", nil
	}
	if !slices.Contains(validEstimates, estimate) {
		return "", fmt.Errorf("invalid estimate %q", value)
	}
	return estimate, nil
}

func (t Task) Validate() error {
	if !canonicalUUIDPattern.MatchString(t.ID) {
		return fmt.Errorf("task id %q must be a canonical lowercase UUID", t.ID)
	}
	if !shortIDPattern.MatchString(t.ShortID) {
		return fmt.Errorf("task shortId %q must match wtp-NNNN (at least four digits)", t.ShortID)
	}
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("task title is required")
	}
	if _, err := ParseStatus(string(t.Status)); err != nil {
		return err
	}
	if _, err := ParsePriority(string(t.Priority)); err != nil {
		return err
	}
	if _, err := ParseEstimate(string(t.Estimate)); err != nil {
		return err
	}
	if t.Lane != "" && strings.TrimSpace(t.Lane) == "" {
		return errors.New("task lane cannot be blank")
	}
	if t.Model != "" && strings.TrimSpace(t.Model) == "" {
		return errors.New("task model cannot be blank")
	}
	if err := validateOptionalAbsolutePath("gitRepo", t.GitRepo); err != nil {
		return err
	}
	if t.GitBranch != "" && strings.TrimSpace(t.GitBranch) == "" {
		return errors.New("task gitBranch cannot be blank")
	}
	if t.WorktreeName != "" && strings.TrimSpace(t.WorktreeName) == "" {
		return errors.New("task worktreeName cannot be blank")
	}
	if err := validateOptionalAbsolutePath("worktreeDir", t.WorktreeDir); err != nil {
		return err
	}
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() {
		return errors.New("task timestamps are required")
	}
	if !isUTC(t.CreatedAt) || !isUTC(t.UpdatedAt) {
		return errors.New("task timestamps must be in UTC")
	}
	if t.UpdatedAt.Before(t.CreatedAt) {
		return errors.New("task updatedAt cannot be before createdAt")
	}
	for index, dependency := range t.Dependencies {
		if !canonicalUUIDPattern.MatchString(dependency) {
			return fmt.Errorf("task dependency %d %q must be a canonical lowercase UUID", index, dependency)
		}
	}
	commentIDs := make(map[string]struct{}, len(t.Comments))
	for index, comment := range t.Comments {
		if !canonicalUUIDPattern.MatchString(comment.ID) {
			return fmt.Errorf("task comment %d id %q must be a canonical lowercase UUID", index, comment.ID)
		}
		if _, exists := commentIDs[comment.ID]; exists {
			return fmt.Errorf("task comment id %s is duplicated", comment.ID)
		}
		commentIDs[comment.ID] = struct{}{}
		if comment.Author != "" && strings.TrimSpace(comment.Author) == "" {
			return fmt.Errorf("task comment %d author cannot be blank", index)
		}
		if strings.TrimSpace(comment.Message) == "" {
			return fmt.Errorf("task comment %d message is required", index)
		}
		if comment.CreatedAt.IsZero() {
			return fmt.Errorf("task comment %d createdAt is required", index)
		}
		if !isUTC(comment.CreatedAt) {
			return fmt.Errorf("task comment %d createdAt must be in UTC", index)
		}
		if comment.CreatedAt.Before(t.CreatedAt) || comment.CreatedAt.After(t.UpdatedAt) {
			return fmt.Errorf("task comment %d createdAt must be between task createdAt and updatedAt", index)
		}
	}
	if err := t.validateLifecycleTimestamps(); err != nil {
		return err
	}
	return nil
}

func validateOptionalAbsolutePath(field, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("task %s cannot be blank", field)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("task %s %q must be an absolute path", field, value)
	}
	return nil
}

func (t Task) validateLifecycleTimestamps() error {
	if t.StartedAt != nil {
		if !isUTC(*t.StartedAt) {
			return errors.New("task startedAt must be in UTC")
		}
		if t.StartedAt.Before(t.CreatedAt) || t.StartedAt.After(t.UpdatedAt) {
			return errors.New("task startedAt must be between createdAt and updatedAt")
		}
	}
	if t.CompletedAt != nil {
		if !isUTC(*t.CompletedAt) {
			return errors.New("task completedAt must be in UTC")
		}
		if t.CompletedAt.Before(t.CreatedAt) || t.CompletedAt.After(t.UpdatedAt) {
			return errors.New("task completedAt must be between createdAt and updatedAt")
		}
	}

	switch t.Status {
	case StatusTodo:
		if t.StartedAt != nil || t.CompletedAt != nil {
			return errors.New("todo task cannot have startedAt or completedAt")
		}
	case StatusInProgress, StatusPaused:
		if t.StartedAt == nil {
			return fmt.Errorf("%s task requires startedAt", t.Status)
		}
		if t.CompletedAt != nil {
			return fmt.Errorf("%s task cannot have completedAt", t.Status)
		}
	case StatusDone:
		if t.StartedAt == nil || t.CompletedAt == nil {
			return errors.New("done task requires startedAt and completedAt")
		}
	}
	if t.StartedAt != nil && t.CompletedAt != nil && t.CompletedAt.Before(*t.StartedAt) {
		return errors.New("task completedAt cannot be before startedAt")
	}
	return nil
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

func PriorityRank(priority Priority) int {
	switch priority {
	case PriorityUrgent:
		return 4
	case PriorityHigh:
		return 3
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 1
	default:
		return 0
	}
}

func AllowedTransition(from, to Status) bool {
	switch from {
	case StatusTodo:
		return to == StatusInProgress
	case StatusInProgress:
		return to == StatusPaused || to == StatusDone
	case StatusPaused:
		return to == StatusInProgress || to == StatusDone
	case StatusDone:
		return false
	default:
		return false
	}
}

func NewID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(bytes[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", raw[0:8], raw[8:12], raw[12:16], raw[16:20], raw[20:32]), nil
}
