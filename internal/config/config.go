package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattrandles/wtproj/internal/core"
)

type AdditionalStatus = core.StatusDefinition

type Config struct {
	Tool               string             `json:"tool"`
	WTPDir             string             `json:"wtpDir"`
	APIKeyEnv          string             `json:"apiKeyEnv"`
	TokenEnv           string             `json:"tokenEnv"`
	BoardID            string             `json:"boardId"`
	ListIDs            map[string]string  `json:"listIds"`
	AdditionalStatuses []AdditionalStatus `json:"additionalStatuses"`
}

// Discover loads .wtp.json from anchor. It does not search parent directories.
// WTPDir is always returned as an absolute path, defaulting to anchor/.wtp.
func Discover(anchor string) (Config, error) {
	path, err := filepath.Abs(filepath.Join(anchor, ".wtp.json"))
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration path: %w", err)
	}
	path = filepath.Clean(path)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{WTPDir: filepath.Join(filepath.Dir(path), ".wtp")}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.normalize()
	if _, err := cfg.StatusCatalog(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	cfg.WTPDir, err = resolveWTPDir(filepath.Dir(path), cfg.WTPDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve wtpDir in %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) normalize() {
	c.Tool = strings.ToLower(strings.TrimSpace(c.Tool))
	c.WTPDir = strings.TrimSpace(c.WTPDir)
	c.APIKeyEnv = strings.TrimSpace(c.APIKeyEnv)
	c.TokenEnv = strings.TrimSpace(c.TokenEnv)
	c.BoardID = strings.TrimSpace(c.BoardID)
	for index := range c.AdditionalStatuses {
		c.AdditionalStatuses[index].Name = core.Status(strings.TrimSpace(string(c.AdditionalStatuses[index].Name)))
		c.AdditionalStatuses[index].Category = core.StatusCategory(strings.TrimSpace(string(c.AdditionalStatuses[index].Category)))
	}
	if c.ListIDs == nil {
		c.ListIDs = map[string]string{}
	}
	for key, value := range c.ListIDs {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		delete(c.ListIDs, key)
		if trimmedKey != "" && trimmedValue != "" {
			c.ListIDs[trimmedKey] = trimmedValue
		}
	}
}

// StatusCatalog constructs the immutable status catalog for one invocation.
func (c Config) StatusCatalog() (core.StatusCatalog, error) {
	return core.NewStatusCatalog(c.AdditionalStatuses)
}

// BuildStatusCatalog is a descriptive alias for StatusCatalog.
func (c Config) BuildStatusCatalog() (core.StatusCatalog, error) { return c.StatusCatalog() }

// Validate checks project configuration, including additional status
// definitions, without touching the filesystem.
func (c Config) Validate() error {
	_, err := c.StatusCatalog()
	return err
}

func resolveWTPDir(configDir, value string) (string, error) {
	if value == "" {
		value = ".wtp"
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(configDir, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func (c Config) EffectiveTool() string {
	if c.Tool == "" {
		return "flatfile"
	}
	return c.Tool
}

func ResolveEnv(envName string) (string, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return "", errors.New("environment variable name is required")
	}
	value, ok := os.LookupEnv(envName)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("required environment variable %s is not set", envName)
	}
	return value, nil
}
