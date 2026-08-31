//go:build windows

package flatfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/reusablejson"
)

func TestExportCanonicalRejectsCaseVariantOfStorageOnWindows(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)

	err := p.ExportCanonical(strings.ToUpper(p.root))
	if err == nil || !strings.Contains(err.Error(), "overlaps active storage") {
		t.Fatalf("ExportCanonical(case-variant storage) error = %v, want overlap error", err)
	}
}

func TestExportCanonicalWritesVersionOneReusableCatalogOnWindows(t *testing.T) {
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

func TestExportCanonicalRejectsNonDirectoryPlanningEntryOnWindows(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)
	exportDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(exportDir, planningDirectory), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile(planning) error = %v", err)
	}
	err := p.ExportCanonical(exportDir)
	if err == nil || !strings.Contains(err.Error(), "unmanaged entries: planning") {
		t.Fatalf("ExportCanonical() error = %v, want planning entry rejection", err)
	}
}
