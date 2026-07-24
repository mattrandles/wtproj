package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if cfg.WTPDir != filepath.Join(dir, ".wtp") {
		t.Fatalf("WTPDir = %q, want %q", cfg.WTPDir, filepath.Join(dir, ".wtp"))
	}
	if cfg.APIKeyEnv != "API_KEY" || cfg.TokenEnv != "TOKEN" || cfg.BoardID != "board" {
		t.Fatalf("normalized config = %#v", cfg)
	}
	if cfg.ListIDs["todo"] != "list-a" || cfg.ListIDs["done"] != "list-b" {
		t.Fatalf("normalized list IDs = %#v", cfg.ListIDs)
	}
}

func TestDiscoverDefaultsMissingConfigToAnchorStorage(t *testing.T) {
	anchor := t.TempDir()

	cfg, err := Discover(anchor)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := cfg.WTPDir, filepath.Join(anchor, ".wtp"); got != want {
		t.Fatalf("WTPDir = %q, want %q", got, want)
	}
	if cfg.EffectiveTool() != "flatfile" {
		t.Fatalf("EffectiveTool() = %q, want flatfile", cfg.EffectiveTool())
	}
}

func TestDiscoverResolvesRelativeAndAbsoluteWTPDir(t *testing.T) {
	absoluteStorage := filepath.Join(t.TempDir(), "absolute task storage")
	for _, test := range []struct {
		name  string
		value string
		want  func(anchor string) string
	}{
		{
			name:  "relative",
			value: filepath.Join("..", "external task storage"),
			want: func(anchor string) string {
				return filepath.Join(filepath.Dir(anchor), "external task storage")
			},
		},
		{
			name:  "absolute",
			value: absoluteStorage,
			want: func(_ string) string {
				return absoluteStorage
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			anchor := filepath.Join(t.TempDir(), "configuration anchor")
			if err := os.MkdirAll(anchor, 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			content, err := json.Marshal(Config{WTPDir: test.value})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(anchor, ".wtp.json"), content, 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			cfg, err := Discover(anchor)
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			want := test.want(anchor)
			if cfg.WTPDir != filepath.Clean(want) {
				t.Fatalf("WTPDir = %q, want %q", cfg.WTPDir, filepath.Clean(want))
			}
		})
	}
}

func TestDiscoverReturnsActionableConfigErrors(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		anchor := t.TempDir()
		path := filepath.Join(anchor, ".wtp.json")
		if err := os.WriteFile(path, []byte(`{"wtpDir":`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		_, err := Discover(anchor)
		if err == nil || !strings.Contains(err.Error(), "parse "+path) {
			t.Fatalf("Discover() error = %v, want parse error naming config", err)
		}
	})

	t.Run("unreadable path", func(t *testing.T) {
		anchor := t.TempDir()
		path := filepath.Join(anchor, ".wtp.json")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("Mkdir() error = %v", err)
		}

		_, err := Discover(anchor)
		if err == nil || !strings.Contains(err.Error(), "read "+path) {
			t.Fatalf("Discover() error = %v, want read error naming config", err)
		}
	})
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
