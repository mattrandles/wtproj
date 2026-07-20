package trello

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

type runtimeConfig struct {
	APIKey string
	Token  string
	Board  string
	Lists  map[core.Status]string
}

func New(cfg config.Config) (provider.Provider, error) {
	if _, err := resolveConfig(cfg); err != nil {
		return nil, err
	}
	return nil, errors.New("trello provider configuration is valid, but the trello provider is not implemented yet")
}

func resolveConfig(cfg config.Config) (runtimeConfig, error) {
	requiredStatuses := []core.Status{
		core.StatusTodo,
		core.StatusInProgress,
		core.StatusPaused,
		core.StatusDone,
	}
	if cfg.APIKeyEnv == "" {
		return runtimeConfig{}, errors.New("trello provider requires apiKeyEnv in .wtp.json")
	}
	if cfg.TokenEnv == "" {
		return runtimeConfig{}, errors.New("trello provider requires tokenEnv in .wtp.json")
	}
	if cfg.BoardID == "" {
		return runtimeConfig{}, errors.New("trello provider requires boardId in .wtp.json")
	}

	apiKey, err := config.ResolveEnv(cfg.APIKeyEnv)
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("trello provider api key: %w", err)
	}
	token, err := config.ResolveEnv(cfg.TokenEnv)
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("trello provider token: %w", err)
	}

	lists := make(map[core.Status]string, len(requiredStatuses))
	missing := []string{}
	for _, status := range requiredStatuses {
		value := cfg.ListIDs[string(status)]
		if value == "" {
			missing = append(missing, string(status))
			continue
		}
		lists[status] = value
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return runtimeConfig{}, fmt.Errorf("trello provider requires listIds for statuses: %s", strings.Join(missing, ", "))
	}

	return runtimeConfig{
		APIKey: apiKey,
		Token:  token,
		Board:  cfg.BoardID,
		Lists:  lists,
	}, nil
}
