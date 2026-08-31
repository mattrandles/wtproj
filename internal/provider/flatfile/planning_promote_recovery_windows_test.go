//go:build windows

package flatfile

import (
	"path/filepath"
	"testing"
)

func TestPlanningPromoteRecoveryUsesNativeWindowsEndpoints(t *testing.T) {
	p, journal := planningPromoteRecoveryFixture(t, 1)
	path, err := p.resolvePlanningPromoteTarget(journal.Entries[0].After.Path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(p.root, "todo", "wtp-0d6e4079-0082.json"); path != want {
		t.Fatalf("resolved todo endpoint = %q, want %q", path, want)
	}
}
