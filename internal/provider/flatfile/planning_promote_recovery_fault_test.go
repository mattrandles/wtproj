//go:build wtp_fault_injection

package flatfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPlanningPromoteRecoveryFaultInjectionAtEveryReplacementAndRemoval(t *testing.T) {
	for _, state := range []string{planningPromotePrepared, planningPromoteCommitted} {
		t.Run(state, func(t *testing.T) {
			for _, action := range []string{"replacement", "removal"} {
				for index := 1; index <= 2; index++ {
					t.Run(action+"-"+strconv.Itoa(index), func(t *testing.T) {
						root, journal := planningPromoteRecoveryFaultFixture(t, state)
						runPlanningPromoteRecoveryFaultChild(t, root, "planning-promote-"+action+"-"+strconv.Itoa(index))
						if _, err := New(root, nil); err != nil {
							t.Fatalf("recovery after interrupted %s %d: %v", action, index, err)
						}
						p, err := New(root, nil)
						if err != nil {
							t.Fatal(err)
						}
						assertPlanningPromoteRecoveryState(t, p, journal, state)
						assertPlanningPromoteJournalAbsent(t, p)
					})
				}
			}
		})
	}
}

func TestPlanningPromoteRecoveryFaultInjectionChild(t *testing.T) {
	root := os.Getenv("WTP_PLANNING_PROMOTE_RECOVERY_ROOT")
	if root == "" {
		t.Skip("fault-injection child")
	}
	if _, err := New(root, nil); err != nil {
		t.Fatalf("New() in planning promotion recovery fault child: %v", err)
	}
}

func planningPromoteRecoveryFaultFixture(t *testing.T, state string) (string, planningPromoteJournal) {
	t.Helper()
	p, journal := planningPromoteRecoveryFixture(t, 2)
	journal.State = state
	for _, entry := range journal.Entries {
		if state == planningPromotePrepared {
			writePlanningPromoteSnapshotForTest(t, p, entry.After)
		} else {
			writePlanningPromoteSnapshotForTest(t, p, entry.Before)
		}
	}
	if err := p.writePlanningPromoteJournal(journal); err != nil {
		t.Fatalf("write planning recovery fault journal: %v", err)
	}
	return p.root, journal
}

func runPlanningPromoteRecoveryFaultChild(t *testing.T, root, point string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPlanningPromoteRecoveryFaultInjectionChild$")
	cmd.Env = append(os.Environ(), "WTP_FAULT_POINT="+point, "WTP_PLANNING_PROMOTE_RECOVERY_ROOT="+root)
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 97 {
		t.Fatalf("fault child %s exit = %v, code=%d", point, err, cmd.ProcessState.ExitCode())
	}
	if err := os.Remove(filepath.Join(root, "meta", "wtp.lock")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove crash lock marker: %v", err)
	}
}
