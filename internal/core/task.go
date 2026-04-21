package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
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
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("task id is required")
	}
	if strings.TrimSpace(t.ShortID) == "" {
		return errors.New("task shortId is required")
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
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() {
		return errors.New("task timestamps are required")
	}
	return nil
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
