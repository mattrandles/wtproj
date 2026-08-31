package flatfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type planningPromoteRecoveryTarget struct {
	beforePath string
	afterPath  string
	entry      planningPromoteJournalEntry
}

// recoverPlanningPromote restores a prepared journal to its planning endpoint
// or completes a committed journal at its todo endpoint. The complete journal
// and every live endpoint are validated before the first write, then all
// copies are published before any opposite endpoint is removed. This phase
// separation is what makes partial source/target sets safe to retry.
func (p *Provider) recoverPlanningPromote() error {
	path := p.planningPromoteJournalPath()
	journal, err := p.readPlanningPromoteJournal()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read planning promotion journal %s (journal retained): %w", path, err)
	}
	if err := p.validatePlanningPromoteJournalForRecovery(journal); err != nil {
		return fmt.Errorf("planning promotion recovery preflight failed (journal retained): %w", err)
	}
	targets, err := p.planningPromoteRecoveryTargets(journal)
	if err != nil {
		return fmt.Errorf("planning promotion recovery preflight failed (journal retained): %w", err)
	}

	for index, target := range targets {
		snapshot := target.entry.Before
		destination := target.beforePath
		if journal.State == planningPromoteCommitted {
			snapshot = target.entry.After
			destination = target.afterPath
		}
		if err := writeBytesAtomicWithFileSystem(destination, snapshot.Data, p.fs); err != nil {
			return fmt.Errorf("planning promotion recovery replacement %d for %s failed (journal retained): %w", index+1, snapshot.Path, err)
		}
		faultPoint(fmt.Sprintf("planning-promote-replacement-%d", index+1))
		faultPoint(fmt.Sprintf("planning-promote-recovery-replacement-%d", index+1))
	}

	for index, target := range targets {
		source := target.afterPath
		if journal.State == planningPromoteCommitted {
			source = target.beforePath
		}
		if err := p.removeFile(source); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("planning promotion recovery removal %d for %s failed (journal retained): %w", index+1, source, err)
		}
		faultPoint(fmt.Sprintf("planning-promote-removal-%d", index+1))
		faultPoint(fmt.Sprintf("planning-promote-recovery-removal-%d", index+1))
	}

	if err := p.confirmPlanningPromoteRecoveryConvergence(journal, targets); err != nil {
		return fmt.Errorf("planning promotion recovery did not converge (journal retained): %w", err)
	}
	if err := p.removeRecoveredPlanningPromoteJournal(journal); err != nil {
		return err
	}
	return nil
}

func (p *Provider) planningPromoteRecoveryTargets(journal planningPromoteJournal) ([]planningPromoteRecoveryTarget, error) {
	targets := make([]planningPromoteRecoveryTarget, len(journal.Entries))
	for index, entry := range journal.Entries {
		beforePath, err := p.resolvePlanningPromoteTarget(entry.Before.Path)
		if err != nil {
			return nil, fmt.Errorf("entry %d before target: %w", index, err)
		}
		afterPath, err := p.resolvePlanningPromoteTarget(entry.After.Path)
		if err != nil {
			return nil, fmt.Errorf("entry %d after target: %w", index, err)
		}
		targets[index] = planningPromoteRecoveryTarget{beforePath: beforePath, afterPath: afterPath, entry: entry}
	}
	return targets, nil
}

func (p *Provider) confirmPlanningPromoteRecoveryConvergence(journal planningPromoteJournal, targets []planningPromoteRecoveryTarget) error {
	directories := make(map[string]struct{}, len(targets)*2)
	for index, target := range targets {
		want := target.entry.Before
		presentPath, absentPath := target.beforePath, target.afterPath
		if journal.State == planningPromoteCommitted {
			want = target.entry.After
			presentPath, absentPath = target.afterPath, target.beforePath
		}
		data, err := os.ReadFile(presentPath)
		if err != nil {
			return fmt.Errorf("entry %d expected endpoint %s: %w", index, want.Path, err)
		}
		if !bytes.Equal(data, want.Data) {
			return fmt.Errorf("entry %d expected endpoint %s does not match its journal snapshot", index, want.Path)
		}
		if _, err := os.Stat(absentPath); err == nil {
			return fmt.Errorf("entry %d opposite endpoint %s remains", index, absentPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("entry %d inspect opposite endpoint %s: %w", index, absentPath, err)
		}
		directories[filepath.Dir(presentPath)] = struct{}{}
		directories[filepath.Dir(absentPath)] = struct{}{}
	}

	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Strings(ordered)
	for _, directory := range ordered {
		if err := p.fs.syncDirectory(directory); err != nil {
			return fmt.Errorf("sync converged endpoint directory %s: %w", directory, err)
		}
	}
	return nil
}

// removeRecoveredPlanningPromoteJournal retries publication of the journal if
// removal reports an error after unlinking it (for example a directory-sync
// failure). The recovery record remains available for a subsequent open
// rather than turning an unconfirmed cleanup into an unjournaled state.
func (p *Provider) removeRecoveredPlanningPromoteJournal(journal planningPromoteJournal) error {
	if err := p.removePlanningPromoteJournal(); err != nil {
		if _, statErr := os.Stat(p.planningPromoteJournalPath()); errors.Is(statErr, os.ErrNotExist) {
			if restoreErr := p.writePlanningPromoteJournal(journal); restoreErr != nil {
				return fmt.Errorf("remove recovered planning promotion journal failed and restoring it failed: remove=%v restore=%w", err, restoreErr)
			}
		}
		return fmt.Errorf("remove recovered planning promotion journal (journal retained): %w", err)
	}
	return nil
}
