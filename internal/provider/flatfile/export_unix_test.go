//go:build !windows

package flatfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
