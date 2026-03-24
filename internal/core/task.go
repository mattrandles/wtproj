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

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "inProgress"
	StatusPaused     Status = "paused"
	StatusDone       Status = "done"
)

var validStatuses = []Status{
	StatusTodo,
	StatusInProgress,
	StatusPaused,
	StatusDone,
}

type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type Task struct {
	ID           string     `json:"id"`
	ShortID      string     `json:"shortId"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Status       Status     `json:"status"`
	Assignee     string     `json:"assignee,omitempty"`
	Dependencies []string   `json:"dependencies"`
	Comments     []Comment  `json:"comments"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	StartedAt    *time.Time `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
}

type CreateTaskInput struct {
	Title        string
	Description  string
	Assignee     string
	Dependencies []string
}

func ParseStatus(value string) (Status, error) {
	status := Status(value)
	if !slices.Contains(validStatuses, status) {
		return "", fmt.Errorf("invalid status %q", value)
	}
	return status, nil
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
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() {
		return errors.New("task timestamps are required")
	}
	return nil
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
