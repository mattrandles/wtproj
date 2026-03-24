package app

import (
	"fmt"
	"path/filepath"

	"wtp/internal/config"
	"wtp/internal/provider"
	flatfileprovider "wtp/internal/provider/flatfile"
	trelloprovider "wtp/internal/provider/trello"
)

func NewProvider(cwd string, cfg config.Config) (provider.Provider, error) {
	switch cfg.EffectiveTool() {
	case "flatfile":
		return flatfileprovider.New(filepath.Join(cwd, ".wtp"))
	case "trello":
		return trelloprovider.New(cfg)
	default:
		return nil, fmt.Errorf("unsupported provider tool %q; supported tools are flatfile and trello", cfg.Tool)
	}
}
