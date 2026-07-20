//go:build windows

package flatfile

import (
	"strings"
	"testing"
)

func TestExportCanonicalRejectsCaseVariantOfStorageOnWindows(t *testing.T) {
	p, _ := newInternalProviderWithTask(t)

	err := p.ExportCanonical(strings.ToUpper(p.root))
	if err == nil || !strings.Contains(err.Error(), "overlaps active storage") {
		t.Fatalf("ExportCanonical(case-variant storage) error = %v, want overlap error", err)
	}
}
