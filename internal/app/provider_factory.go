package app

import (
	"fmt"
	"path/filepath"

	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/provider"
	flatfileprovider "github.com/mattrandles/wtproj/internal/provider/flatfile"
	trelloprovider "github.com/mattrandles/wtproj/internal/provider/trello"
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
