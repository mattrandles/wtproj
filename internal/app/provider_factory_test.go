package app

import (
	"strings"
	"testing"

	"wtp/internal/config"
)

func TestNewProviderRejectsUnknownTool(t *testing.T) {
	_, err := NewProvider(t.TempDir(), config.Config{Tool: "unknown"})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if !strings.Contains(err.Error(), "supported tools are flatfile and trello") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewProviderValidatesTrelloConfig(t *testing.T) {
	_, err := NewProvider(t.TempDir(), config.Config{Tool: "trello"})
	if err == nil {
		t.Fatal("expected trello validation error")
	}
	if !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("unexpected error: %v", err)
	}
}
