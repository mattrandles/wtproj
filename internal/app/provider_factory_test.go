package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/config"
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

func TestNewProviderUsesExplicitFlatfileStorageDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "external storage")

	if _, err := NewProvider(root, config.Config{}); err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "meta", "index.json")); err != nil {
		t.Fatalf("flat-file storage was not initialized at %s: %v", root, err)
	}
}

func TestNewProviderReturnsActionableInvalidStorageErrors(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		_, err := NewProvider("relative/storage", config.Config{})
		if err == nil || !strings.Contains(err.Error(), "must be absolute") {
			t.Fatalf("NewProvider() error = %v", err)
		}
	})

	t.Run("existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "storage-file")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		_, err := NewProvider(path, config.Config{})
		if err == nil || !strings.Contains(err.Error(), "initialize flat-file storage at "+path) {
			t.Fatalf("NewProvider() error = %v", err)
		}
	})
}

func TestNewProviderDoesNotApplyFlatfilePathValidationToOtherTools(t *testing.T) {
	_, err := NewProvider("relative/storage", config.Config{Tool: "trello"})
	if err == nil {
		t.Fatal("expected trello configuration error")
	}
	if strings.Contains(err.Error(), "storage directory") {
		t.Fatalf("trello provider was affected by flat-file storage validation: %v", err)
	}
	if !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("unexpected trello error: %v", err)
	}
}
