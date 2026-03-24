package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverNormalizesConfig(t *testing.T) {
	dir := t.TempDir()
	content := `{
  "tool": " TRELLO ",
  "apiKeyEnv": " API_KEY ",
  "tokenEnv": " TOKEN ",
  "boardId": " board ",
  "listIds": {
    " todo ": " list-a ",
    "done": " list-b "
  }
}`
	if err := os.WriteFile(filepath.Join(dir, ".wtp.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if cfg.EffectiveTool() != "trello" {
		t.Fatalf("EffectiveTool() = %q, want trello", cfg.EffectiveTool())
	}
	if cfg.APIKeyEnv != "API_KEY" || cfg.TokenEnv != "TOKEN" || cfg.BoardID != "board" {
		t.Fatalf("normalized config = %#v", cfg)
	}
	if cfg.ListIDs["todo"] != "list-a" || cfg.ListIDs["done"] != "list-b" {
		t.Fatalf("normalized list IDs = %#v", cfg.ListIDs)
	}
}

func TestResolveEnvRequiresValue(t *testing.T) {
	t.Setenv("WTP_CONFIG_TEST_SET", "present")

	value, err := ResolveEnv("WTP_CONFIG_TEST_SET")
	if err != nil {
		t.Fatalf("ResolveEnv() error = %v", err)
	}
	if value != "present" {
		t.Fatalf("ResolveEnv() = %q, want present", value)
	}

	if _, err := ResolveEnv("WTP_CONFIG_TEST_MISSING"); err == nil {
		t.Fatal("expected missing env var error")
	}
}
