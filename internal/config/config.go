package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Tool      string            `json:"tool"`
	APIKeyEnv string            `json:"apiKeyEnv"`
	TokenEnv  string            `json:"tokenEnv"`
	BoardID   string            `json:"boardId"`
	ListIDs   map[string]string `json:"listIds"`
}

func Discover(cwd string) (Config, error) {
	path := filepath.Join(cwd, ".wtp.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	c.Tool = strings.ToLower(strings.TrimSpace(c.Tool))
	c.APIKeyEnv = strings.TrimSpace(c.APIKeyEnv)
	c.TokenEnv = strings.TrimSpace(c.TokenEnv)
	c.BoardID = strings.TrimSpace(c.BoardID)
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
