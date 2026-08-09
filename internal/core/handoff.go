package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Handoff is retained context for a later worker. An empty TaskID identifies a
// global handoff; otherwise TaskID contains the canonical UUID of its task.
type Handoff struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId,omitempty"`
	Author    string    `json:"author,omitempty"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

func (h Handoff) Validate() error {
	if !canonicalUUIDPattern.MatchString(h.ID) {
		return fmt.Errorf("handoff id %q must be a canonical lowercase UUID", h.ID)
	}
	if h.TaskID != "" && !canonicalUUIDPattern.MatchString(h.TaskID) {
		return fmt.Errorf("handoff taskId %q must be a canonical lowercase UUID", h.TaskID)
	}
	if h.Author != "" && strings.TrimSpace(h.Author) == "" {
		return errors.New("handoff author cannot be blank")
	}
	if strings.TrimSpace(h.Message) == "" {
		return errors.New("handoff message is required")
	}
	if h.Message != strings.TrimSpace(h.Message) {
		return errors.New("handoff message must be trimmed")
	}
	if h.CreatedAt.IsZero() {
		return errors.New("handoff createdAt is required")
	}
	if !isUTC(h.CreatedAt) {
		return errors.New("handoff createdAt must be in UTC")
	}
	return nil
}
