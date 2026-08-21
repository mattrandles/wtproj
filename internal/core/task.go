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
	shortIDPattern       = regexp.MustCompile(`^wtp-(?:(?P<branchID>[0-9a-f]{8})-)?(?P<sequence>[0-9]{4,})$`)
)

const shortIDFormat = "wtp-NNNN or wtp-BBBBBBBB-NNNN (B is exactly 8 lowercase hexadecimal characters and N is at least four decimal digits)"

// ShortIDParts is the validated structure of a task short ID. Sequence
// preserves its decimal spelling, including leading zeroes, so parsing does
// not impose a narrower numeric limit than the identifier contract.
type ShortIDParts struct {
	Legacy   bool
	BranchID string
	Sequence string
}

// IsScoped reports whether the short ID belongs to a named branch scope.
func (p ShortIDParts) IsScoped() bool {
	return p.BranchID != ""
}

// IsLegacy reports whether the short ID belongs to the unscoped legacy
// namespace.
func (p ShortIDParts) IsLegacy() bool {
	return p.BranchID == ""
}

// ParseShortID validates value and extracts its branch scope and sequence.
// A legacy wtp-NNNN ID has no BranchID and sets Legacy to true.
func ParseShortID(value string) (ShortIDParts, error) {
	matches := shortIDPattern.FindStringSubmatch(value)
	if matches == nil {
		return ShortIDParts{}, fmt.Errorf("short ID %q must match %s", value, shortIDFormat)
	}

	branchIndex := shortIDPattern.SubexpIndex("branchID")
	sequenceIndex := shortIDPattern.SubexpIndex("sequence")
	branchID := matches[branchIndex]
	return ShortIDParts{
		Legacy:   branchID == "",
		BranchID: branchID,
		Sequence: matches[sequenceIndex],
	}, nil
}

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
	Handoffs  []Handoff     `json:"handoffs,omitempty"`
}

type CreateTaskInput struct {
	Title        string
	Description  string
	Status       Status
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

type OptionalStatus struct {
	Set   bool
	Value Status
}

type UpdateTaskInput struct {
	Title        OptionalString
	Description  OptionalString
	Status       OptionalStatus
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
	return DefaultStatusCatalog().ParseStatus(value)
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
	return t.ValidateWithCatalog(DefaultStatusCatalog())
}

// ValidateWithCatalog validates a task against an invocation's status
// catalog.
func (t Task) ValidateWithCatalog(catalog StatusCatalog) error {
	if !canonicalUUIDPattern.MatchString(t.ID) {
		return fmt.Errorf("task id %q must be a canonical lowercase UUID", t.ID)
	}
	if _, err := ParseShortID(t.ShortID); err != nil {
		return fmt.Errorf("task shortId %q must match %s", t.ShortID, shortIDFormat)
	}
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("task title is required")
	}
	if _, err := catalog.ParseStatus(string(t.Status)); err != nil {
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
	if err := t.validateLifecycleTimestamps(catalog); err != nil {
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

func (t Task) validateLifecycleTimestamps(catalog StatusCatalog) error {
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

	if err := catalog.NormalizeLifecycle(t.Status, t.StartedAt, t.CompletedAt); err != nil {
		return err
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
	return AllowedTransitionWithCatalog(DefaultStatusCatalog(), from, to)
}

// AllowedTransitionWithCatalog validates a lifecycle transition against an
// invocation catalog. Built-in transitions retain their legacy behavior;
// project-defined states follow their lifecycle category.
func AllowedTransitionWithCatalog(catalog StatusCatalog, from, to Status) bool {
	if !catalog.Contains(from) || !catalog.Contains(to) || from == to {
		return false
	}
	switch from {
	case StatusTodo:
		return to == StatusInProgress || catalog.CategoryOf(to) == StatusCategoryBlocked
	case StatusInProgress:
		return to == StatusPaused || to == StatusDone || catalog.CategoryOf(to) == StatusCategoryWaiting || catalog.CategoryOf(to) == StatusCategoryBlocked || catalog.CategoryOf(to) == StatusCategoryFailed
	case StatusPaused:
		return to == StatusInProgress || to == StatusDone || catalog.CategoryOf(to) == StatusCategoryWaiting || catalog.CategoryOf(to) == StatusCategoryBlocked || catalog.CategoryOf(to) == StatusCategoryFailed
	case StatusDone:
		return false
	}
	fromCategory := catalog.CategoryOf(from)
	switch fromCategory {
	case StatusCategoryWaiting:
		return to == StatusInProgress || to == StatusPaused || to == StatusDone || catalog.CategoryOf(to) == StatusCategoryFailed || catalog.CategoryOf(to) == StatusCategoryBlocked
	case StatusCategoryBlocked:
		return to == StatusTodo || to == StatusInProgress
	case StatusCategoryFailed:
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
