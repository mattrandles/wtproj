package app

import (
	"fmt"
	"path/filepath"

	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/provider"
	flatfileprovider "github.com/mattrandles/wtproj/internal/provider/flatfile"
	trelloprovider "github.com/mattrandles/wtproj/internal/provider/trello"
)

func NewProvider(wtpDir string, cfg config.Config) (provider.Provider, error) {
	switch cfg.EffectiveTool() {
	case "flatfile":
		if !filepath.IsAbs(wtpDir) {
			return nil, fmt.Errorf("flat-file storage directory must be absolute: %q", wtpDir)
		}
		flatfile, err := flatfileprovider.New(filepath.Clean(wtpDir))
		if err != nil {
			return nil, fmt.Errorf("initialize flat-file storage at %s: %w", wtpDir, err)
		}
		return flatfile, nil
	case "trello":
		return trelloprovider.New(cfg)
	default:
		return nil, fmt.Errorf("unsupported provider tool %q; supported tools are flatfile and trello", cfg.Tool)
	}
}
