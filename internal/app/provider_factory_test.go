package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/core"
)

func TestNewProviderRejectsUnknownTool(t *testing.T) {
	_, err := NewProvider(t.TempDir(), config.Config{Tool: "unknown"}, nil)
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if !strings.Contains(err.Error(), "supported tools are flatfile and trello") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewProviderValidatesTrelloConfig(t *testing.T) {
	_, err := NewProvider(t.TempDir(), config.Config{Tool: "trello"}, nil)
	if err == nil {
		t.Fatal("expected trello validation error")
	}
	if !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewProviderUsesExplicitFlatfileStorageDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "external storage")

	if _, err := NewProvider(root, config.Config{}, nil); err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "meta", "index.json")); err != nil {
		t.Fatalf("flat-file storage was not initialized at %s: %v", root, err)
	}
}

func TestNewProviderReturnsActionableInvalidStorageErrors(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		_, err := NewProvider("relative/storage", config.Config{}, nil)
		if err == nil || !strings.Contains(err.Error(), "must be absolute") {
			t.Fatalf("NewProvider() error = %v", err)
		}
	})

	t.Run("existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "storage-file")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		_, err := NewProvider(path, config.Config{}, nil)
		if err == nil || !strings.Contains(err.Error(), "initialize flat-file storage at "+path) {
			t.Fatalf("NewProvider() error = %v", err)
		}
	})
}

func TestNewProviderDoesNotApplyFlatfilePathValidationToOtherTools(t *testing.T) {
	_, err := NewProvider("relative/storage", config.Config{Tool: "trello"}, core.NewBranchScope("feature/runtime-scope"))
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

func TestNewProviderCopiesInvocationScopeIntoFlatfileProvider(t *testing.T) {
	scope := core.NewBranchScope("feature/runtime-scope")
	p, err := NewProvider(t.TempDir(), config.Config{}, scope)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	scoped, ok := p.(interface{ InvocationScope() *core.BranchScope })
	if !ok {
		t.Fatalf("NewProvider() returned %T, which does not expose its invocation scope", p)
	}
	got := scoped.InvocationScope()
	if got == nil || *got != *scope {
		t.Fatalf("InvocationScope() = %#v, want %#v", got, scope)
	}
	scope.Branch = "changed-after-construction"
	if got := scoped.InvocationScope(); got == nil || got.Branch != "feature/runtime-scope" {
		t.Fatalf("InvocationScope() changed after caller mutation: %#v", got)
	}
}

func TestNewProviderPropagatesStatusCatalog(t *testing.T) {
	p, err := NewProvider(t.TempDir(), config.Config{
		AdditionalStatuses: []config.AdditionalStatus{{Name: "needsReview", Category: core.StatusCategoryWaiting}},
	}, nil)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	withCatalog, ok := p.(interface{ StatusCatalog() core.StatusCatalog })
	if !ok {
		t.Fatalf("provider %T does not expose its status catalog", p)
	}
	if !withCatalog.StatusCatalog().Contains("needsReview") {
		t.Fatal("provider did not receive configured additional status")
	}
}
