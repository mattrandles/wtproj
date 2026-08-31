//go:build windows

package flatfile

import (
	"path/filepath"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestPlanningStorageUsesNestedWindowsPaths(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	want := filepath.Join(p.root, planningDirectory, string(core.PlanningStatusRejected))
	if got := p.planningStatusDir(core.PlanningStatusRejected); got != want {
		t.Fatalf("planningStatusDir() = %q, want %q", got, want)
	}
}
