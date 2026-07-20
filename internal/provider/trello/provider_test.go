package trello

import (
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/config"
)

func TestResolveConfigRequiresFields(t *testing.T) {
	_, err := resolveConfig(config.Config{Tool: "trello"})
	if err == nil {
		t.Fatal("expected missing-field error")
	}
	if !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveConfigRequiresEnvAndLists(t *testing.T) {
	t.Setenv("TRELLO_API_KEY", "key")
	t.Setenv("TRELLO_TOKEN", "token")

	_, err := resolveConfig(config.Config{
		Tool:      "trello",
		APIKeyEnv: "TRELLO_API_KEY",
		TokenEnv:  "TRELLO_TOKEN",
		BoardID:   "board",
		ListIDs: map[string]string{
			"todo": "todo-list",
		},
	})
	if err == nil {
		t.Fatal("expected missing list-id error")
	}
	if !strings.Contains(err.Error(), "inProgress") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewReturnsNotImplementedAfterValidation(t *testing.T) {
	t.Setenv("TRELLO_API_KEY", "key")
	t.Setenv("TRELLO_TOKEN", "token")

	_, err := New(config.Config{
		Tool:      "trello",
		APIKeyEnv: "TRELLO_API_KEY",
		TokenEnv:  "TRELLO_TOKEN",
		BoardID:   "board",
		ListIDs: map[string]string{
			"todo":       "todo-list",
			"inProgress": "progress-list",
			"paused":     "paused-list",
			"done":       "done-list",
		},
	})
	if err == nil {
		t.Fatal("expected not-implemented error")
	}
	if !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}
