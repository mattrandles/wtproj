package app

import (
	"fmt"
	"path/filepath"

	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	flatfileprovider "github.com/mattrandles/wtproj/internal/provider/flatfile"
	trelloprovider "github.com/mattrandles/wtproj/internal/provider/trello"
)

func NewProvider(wtpDir string, cfg config.Config, invocationScope *core.BranchScope) (provider.Provider, error) {
	return newProvider(wtpDir, cfg, invocationScope, false)
}

// NewReadOnlyProvider constructs a provider without initializing or repairing
// flat-file storage. It is used by commands whose read-only contract includes
// byte-identical storage, such as planning promotion previews.
func NewReadOnlyProvider(wtpDir string, cfg config.Config, invocationScope *core.BranchScope) (provider.Provider, error) {
	return newProvider(wtpDir, cfg, invocationScope, true)
}

func newProvider(wtpDir string, cfg config.Config, invocationScope *core.BranchScope, readOnly bool) (provider.Provider, error) {
	catalog, err := cfg.StatusCatalog()
	if err != nil {
		return nil, fmt.Errorf("validate status configuration: %w", err)
	}
	switch cfg.EffectiveTool() {
	case "flatfile":
		if !filepath.IsAbs(wtpDir) {
			return nil, fmt.Errorf("flat-file storage directory must be absolute: %q", wtpDir)
		}
		constructor := flatfileprovider.NewWithCatalog
		if readOnly {
			constructor = flatfileprovider.NewReadOnlyWithCatalog
		}
		flatfile, err := constructor(filepath.Clean(wtpDir), invocationScope, catalog)
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
