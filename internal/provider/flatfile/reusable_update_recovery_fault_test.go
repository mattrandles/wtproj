//go:build wtp_fault_injection

package flatfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestReusableUpdateRecoveryFaultInjectionAtEveryReplacement(t *testing.T) {
	for _, state := range []string{reusableUpdateJournalPrepared, reusableUpdateJournalCommitted} {
		t.Run(state, func(t *testing.T) {
			const taskCount = 2
			for replacement := 1; replacement <= taskCount+1; replacement++ {
				t.Run(string(rune('0'+replacement)), func(t *testing.T) {
					root, journal := reusableRecoveryFaultFixture(t, state, taskCount)
					runReusableRecoveryFaultChild(t, root, replacement)
					if _, err := New(root, nil); err != nil {
						t.Fatalf("recovery after interrupted replacement %d: %v", replacement, err)
					}
					assertReusableRecoveryState(t, mustProvider(t, root), journal, state)
				})
			}
		})
	}
}

func TestReusableUpdateRecoveryFaultInjectionChild(t *testing.T) {
	root := os.Getenv("WTP_REUSABLE_RECOVERY_ROOT")
	if root == "" {
		t.Skip("fault-injection child")
	}
	if _, err := New(root, nil); err != nil {
		t.Fatalf("New() in recovery fault child: %v", err)
	}
}

func reusableRecoveryFaultFixture(t *testing.T, state string, taskCount int) (string, reusableUpdateJournal) {
	t.Helper()
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo, core.StatusInProgress}[:taskCount])
	if state == reusableUpdateJournalPrepared {
		writeReusableRecoveryBeforeState(t, p, journal)
	} else {
		writeReusableRecoveryAfterState(t, p, journal)
		// Leave every task at the before endpoint so each recovery replacement
		// is observable while the catalog is already committed.
		for _, change := range journal.AffectedTasks {
			writeReusableUpdateSnapshotForTest(t, p, change.Before)
		}
	}
	journal.State = state
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("write fault fixture journal: %v", err)
	}
	return p.root, journal
}

func runReusableRecoveryFaultChild(t *testing.T, root string, replacement int) {
	t.Helper()
	point := "reusable-update-replacement-" + string(rune('0'+replacement))
	cmd := exec.Command(os.Args[0], "-test.run=^TestReusableUpdateRecoveryFaultInjectionChild$")
	cmd.Env = append(os.Environ(), "WTP_FAULT_POINT="+point, "WTP_REUSABLE_RECOVERY_ROOT="+root)
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 97 {
		t.Fatalf("fault child replacement %d exit = %v, code=%d", replacement, err, cmd.ProcessState.ExitCode())
	}
	if err := os.Remove(filepath.Join(root, "meta", "wtp.lock")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove crash lock marker: %v", err)
	}
}

func mustProvider(t *testing.T, root string) *Provider {
	t.Helper()
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(provider assertion) error: %v", err)
	}
	return p
}
