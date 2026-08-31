//go:build !windows

package flatfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/reusablejson"
)

func TestExportCanonicalRejectsSymlinkAliasOfStorage(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)
	alias := filepath.Join(t.TempDir(), "storage-alias")
	if err := os.Symlink(p.root, alias); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}

	err := p.ExportCanonical(alias)
	if err == nil || !strings.Contains(err.Error(), "overlaps active storage") {
		t.Fatalf("ExportCanonical(symlink alias) error = %v, want overlap error", err)
	}
}

func TestExportCanonicalWritesVersionOneReusableCatalogOnUnix(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)
	exportDir := filepath.Join(t.TempDir(), "canonical export")
	if err := p.ExportCanonical(exportDir); err != nil {
		t.Fatalf("ExportCanonical() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(exportDir, reusableFilename))
	if err != nil {
		t.Fatalf("ReadFile(reusable.json) error = %v", err)
	}
	catalog, err := reusablejson.Decode(data)
	if err != nil {
		t.Fatalf("Decode(reusable.json) error = %v", err)
	}
	if catalog.Version != 1 || catalog.Definitions == nil || len(catalog.Definitions) != 0 {
		t.Fatalf("exported reusable catalog = %#v, want valid empty version-1 catalog", catalog)
	}
}

func TestExportCanonicalRejectsPlanningSymlinksOnUnix(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)

	t.Run("planning directory", func(t *testing.T) {
		exportDir := t.TempDir()
		target := filepath.Join(t.TempDir(), "planning-target")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatalf("Mkdir(target) error = %v", err)
		}
		if err := os.Symlink(target, filepath.Join(exportDir, planningDirectory)); err != nil {
			t.Fatalf("Symlink(planning) error = %v", err)
		}
		err := p.ExportCanonical(exportDir)
		if err == nil || !strings.Contains(err.Error(), "unmanaged entries: planning") {
			t.Fatalf("ExportCanonical() error = %v, want planning symlink rejection", err)
		}
	})

	t.Run("planning record", func(t *testing.T) {
		exportDir := t.TempDir()
		planningDir := filepath.Join(exportDir, planningDirectory)
		if err := os.Mkdir(planningDir, 0o755); err != nil {
			t.Fatalf("Mkdir(planning) error = %v", err)
		}
		target := filepath.Join(t.TempDir(), "planning-record")
		if err := os.WriteFile(target, []byte("not managed"), 0o644); err != nil {
			t.Fatalf("WriteFile(target) error = %v", err)
		}
		if err := os.Symlink(target, filepath.Join(planningDir, "15c3806a-bd1b-424d-889b-29e5b06679b8.json")); err != nil {
			t.Fatalf("Symlink(record) error = %v", err)
		}
		err := p.ExportCanonical(exportDir)
		if err == nil || !strings.Contains(err.Error(), "unmanaged entries") {
			t.Fatalf("ExportCanonical() error = %v, want planning record symlink rejection", err)
		}
	})
}
