package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mattrandles/wtproj/internal/core"
)

// recoverPendingJournals is the one recovery entry point used by store
// initialization and all mutating load paths. Callers already hold the global
// lock. It validates every pending journal and every cross-journal target
// conflict before changing a single endpoint. That keeps a retained recovery
// journal from being silently overwritten by another transaction.
//
// The recovery order is part of the on-disk protocol: batch-update,
// reusable-update, then planning-promote. Do not derive it from filenames or
// journal timestamps.
func (p *Provider) recoverPendingJournals() error {
	pending, err := p.preflightPendingJournals()
	if err != nil {
		return err
	}

	if pending.batch != nil {
		if err := p.recoverBatchUpdate(); err != nil {
			return err
		}
	}
	if pending.reusable != nil {
		if err := p.recoverReusableUpdate(); err != nil {
			return err
		}
	}
	if pending.planningPromote != nil {
		if err := p.recoverPlanningPromote(); err != nil {
			return err
		}
	}
	return nil
}

type pendingRecoveryJournals struct {
	batch           *batchUpdateJournal
	reusable        *reusableUpdateJournal
	planningPromote *planningPromoteJournal
}

// preflightPendingJournals leaves all journal files in place on every error.
// In particular, planning-promotion live endpoint validation happens here,
// before an earlier recovery has touched any task or catalog target.
func (p *Provider) preflightPendingJournals() (pendingRecoveryJournals, error) {
	var pending pendingRecoveryJournals
	batchExists, err := journalExists(p.batchUpdateJournalPath())
	if err != nil {
		return pending, fmt.Errorf("inspect batch update journal %s: %w", p.batchUpdateJournalPath(), err)
	}
	if batchExists {
		batch := batchUpdateJournal{}
		if err := readJSON(p.batchUpdateJournalPath(), &batch); err != nil {
			return pending, fmt.Errorf("read batch update journal %s (journal retained): %w", p.batchUpdateJournalPath(), err)
		}
		if err := p.validateBatchUpdateJournal(batch); err != nil {
			return pending, fmt.Errorf("invalid batch update journal %s (journal retained): %w", p.batchUpdateJournalPath(), err)
		}
		pending.batch = &batch
	}

	reusableExists, err := journalExists(p.reusableUpdateJournalPath())
	if err != nil {
		return pending, fmt.Errorf("inspect reusable update journal %s: %w", p.reusableUpdateJournalPath(), err)
	}
	if reusableExists {
		reusable, err := p.readReusableUpdateJournal()
		if err != nil {
			return pending, fmt.Errorf("read reusable update journal %s (journal retained): %w", p.reusableUpdateJournalPath(), err)
		}
		pending.reusable = &reusable
	}

	planningExists, err := journalExists(p.planningPromoteJournalPath())
	if err != nil {
		return pending, fmt.Errorf("inspect planning promotion journal %s: %w", p.planningPromoteJournalPath(), err)
	}
	if planningExists {
		planning, err := p.readPlanningPromoteJournal()
		if err != nil {
			return pending, fmt.Errorf("read planning promotion journal %s (journal retained): %w", p.planningPromoteJournalPath(), err)
		}
		if err := p.validatePlanningPromoteJournalForRecovery(planning); err != nil {
			return pending, fmt.Errorf("planning promotion recovery preflight failed (journal retained): %w", err)
		}
		pending.planningPromote = &planning
	}

	if pending.batch != nil && pending.reusable != nil {
		if target, conflict := p.pendingJournalTargetConflict(*pending.batch, *pending.reusable); conflict {
			return pending, fmt.Errorf("cannot recover batch update and reusable update journals: shared target %s; both journals retained", target)
		}
	}
	if pending.batch != nil && pending.planningPromote != nil {
		if target, conflict := p.pendingBatchPlanningPromoteTargetConflict(*pending.batch, *pending.planningPromote); conflict {
			return pending, fmt.Errorf("cannot recover batch update and planning promotion journals: shared target %s; both journals retained", target)
		}
	}
	if pending.reusable != nil && pending.planningPromote != nil {
		if target, conflict := p.pendingReusablePlanningPromoteTargetConflict(*pending.reusable, *pending.planningPromote); conflict {
			return pending, fmt.Errorf("cannot recover reusable update and planning promotion journals: shared target %s; both journals retained", target)
		}
	}
	return pending, nil
}

func (p *Provider) pendingBatchPlanningPromoteTargetConflict(batch batchUpdateJournal, planning planningPromoteJournal) (string, bool) {
	batchTargets := p.pendingBatchJournalTargets(batch)
	return p.pendingPlanningPromoteTargetConflict(batchTargets, planning)
}

func (p *Provider) pendingReusablePlanningPromoteTargetConflict(reusable reusableUpdateJournal, planning planningPromoteJournal) (string, bool) {
	targets := make(map[string]struct{})
	changes := append([]reusableUpdateJournalChange{reusable.Catalog}, reusable.AffectedTasks...)
	for _, change := range changes {
		for _, snapshot := range []reusableUpdateJournalSnapshot{change.Before, change.After} {
			resolved, err := p.resolveReusableUpdateTarget(snapshot.Path)
			if err == nil {
				targets[normalizePathForComparison(resolved)] = struct{}{}
			}
		}
	}
	return p.pendingPlanningPromoteTargetConflict(targets, planning)
}

func (p *Provider) pendingPlanningPromoteTargetConflict(otherTargets map[string]struct{}, planning planningPromoteJournal) (string, bool) {
	var conflicts []string
	for _, entry := range planning.Entries {
		resolved, err := p.resolvePlanningPromoteTarget(entry.After.Path)
		if err != nil {
			continue // The complete planning preflight above reports this separately.
		}
		resolved = normalizePathForComparison(resolved)
		if _, shared := otherTargets[resolved]; shared {
			conflicts = append(conflicts, resolved)
		}
	}
	if len(conflicts) == 0 {
		return "", false
	}
	sort.Strings(conflicts)
	return conflicts[0], true
}

func (p *Provider) pendingBatchJournalTargets(batch batchUpdateJournal) map[string]struct{} {
	targets := make(map[string]struct{})
	for _, entry := range batch.Entries {
		for _, file := range entry.BeforeFiles {
			targets[normalizePathForComparison(filepath.Clean(file.Path))] = struct{}{}
		}
		for _, task := range []core.Task{entry.Before, entry.After} {
			for _, status := range p.statuses() {
				for _, filename := range taskFilenames(task) {
					targets[normalizePathForComparison(p.taskPath(status, filename))] = struct{}{}
				}
			}
		}
	}
	return targets
}

func journalExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// pendingJournalTargetConflict is intentionally conservative. Batch recovery
// may remove either task filename in every configured status directory while
// reusable recovery replaces one exact task target. Treating every possible
// batch task path as shared prevents a committed batch from erasing a
// successfully recovered reusable detachment, and keeps both diagnostics for
// an operator to resolve.
func (p *Provider) pendingJournalTargetConflict(batch batchUpdateJournal, reusable reusableUpdateJournal) (string, bool) {
	batchTargets := p.pendingBatchJournalTargets(batch)

	var conflicts []string
	changes := append([]reusableUpdateJournalChange{reusable.Catalog}, reusable.AffectedTasks...)
	for _, change := range changes {
		for _, snapshot := range []reusableUpdateJournalSnapshot{change.Before, change.After} {
			resolved, err := p.resolveReusableUpdateTarget(snapshot.Path)
			if err != nil {
				continue
			}
			resolved = normalizePathForComparison(resolved)
			if _, shared := batchTargets[resolved]; shared {
				conflicts = append(conflicts, resolved)
			}
		}
	}
	if len(conflicts) == 0 {
		return "", false
	}
	sort.Strings(conflicts)
	return conflicts[0], true
}

// recoverReusableUpdate restores every before snapshot from a prepared
// journal, or republishes every after snapshot from a committed journal. The
// journal is read and the complete live endpoint set is validated before the
// first replacement. Each replacement is atomic and cleanup is the final
// action, so a repeated open safely resumes a partially completed recovery.
func (p *Provider) recoverReusableUpdate() error {
	path := p.reusableUpdateJournalPath()
	journal, err := p.readReusableUpdateJournal()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read reusable update journal %s (journal retained): %w", path, err)
	}
	if err := p.validateReusableUpdateJournalForRecovery(journal); err != nil {
		return fmt.Errorf("reusable update recovery preflight failed (journal retained): %w", err)
	}

	changes := append([]reusableUpdateJournalChange{journal.Catalog}, journal.AffectedTasks...)
	for index, change := range changes {
		snapshot := change.Before
		if journal.State == reusableUpdateJournalCommitted {
			snapshot = change.After
		}
		resolved, err := p.resolveReusableUpdateTarget(snapshot.Path)
		if err != nil {
			return fmt.Errorf("reusable update recovery target %s failed (journal retained): %w", snapshot.Path, err)
		}
		if !snapshot.Exists {
			return fmt.Errorf("reusable update recovery target %s is absent (journal retained)", snapshot.Path)
		}
		if err := writeBytesAtomicWithFileSystem(resolved, snapshot.Data, p.fs); err != nil {
			return fmt.Errorf("reusable update recovery replacement %d for %s failed (journal retained): %w", index+1, snapshot.Path, err)
		}
		// Keep the point after publication, matching batch-update recovery's
		// fault semantics: an interrupted retry sees a complete endpoint and
		// can safely publish it again before removing the journal.
		faultPoint(fmt.Sprintf("reusable-update-replacement-%d", index+1))
		faultPoint(fmt.Sprintf("reusable-update-recovery-replacement-%d", index+1))
	}
	if err := p.removeReusableUpdateJournal(); err != nil {
		return fmt.Errorf("remove recovered reusable update journal (journal retained): %w", err)
	}
	return nil
}
