package flatfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
	"github.com/mattrandles/wtproj/internal/provider"
)

var _ provider.PlanningPromoter = (*Provider)(nil)

// PromotePlanningItems publishes one dependency-closed planning group as a
// single journaled transaction. The lock deliberately covers the selection,
// timestamp, journal construction, publication, cleanup, and result snapshot:
// preview is not a reservation and another update must not fit between any of
// those decisions.
func (p *Provider) PromotePlanningItems(grouping core.GroupingFilter) (provider.PlanningPromotionResult[core.TaskView], error) {
	if !grouping.HasSelector() {
		return provider.PlanningPromotionResult[core.TaskView]{}, errors.New("planning promotion requires at least one grouping selector")
	}

	var result provider.PlanningPromotionResult[core.TaskView]
	err := p.withGlobalLock(func() error {
		snapshot, catalog, err := p.loadPlanningValidationSnapshot()
		if err != nil {
			return err
		}
		// This is intentionally repeated inside the lock even if a caller first
		// ran preview: the locked snapshot is the only authoritative selection.
		selected, err := core.SelectPlanningPromotion(snapshot.planningItems, snapshot.tasks, grouping)
		if err != nil {
			return err
		}

		promotionAt := planningPromotionTimestamp(selected)
		journal, promoted, err := p.preparePlanningPromotion(selected, promotionAt)
		if err != nil {
			return err
		}

		faultPoint("planning-promote-before-journal")
		if err := p.writePlanningPromoteJournal(journal); err != nil {
			return fmt.Errorf("prepare planning promotion journal: %w", err)
		}
		faultPoint("planning-promote-prepared")

		for index, entry := range journal.Entries {
			path, err := p.resolvePlanningPromoteTarget(entry.After.Path)
			if err != nil {
				return p.recoverAfterPlanningPromoteError(fmt.Errorf("resolve promoted task %s: %w", promoted[index].ShortID, err))
			}
			if err := writeBytesAtomicWithFileSystem(path, entry.After.Data, p.fs); err != nil {
				return p.recoverAfterPlanningPromoteError(fmt.Errorf("publish promoted task %s: %w", promoted[index].ShortID, err))
			}
			faultPoint(fmt.Sprintf("planning-promote-replacement-%d", index+1))
			faultPoint("planning-promote-publication")
		}

		if err := transitionPlanningPromoteJournal(&journal, planningPromoteCommitted); err != nil {
			return p.recoverAfterPlanningPromoteError(fmt.Errorf("commit planning promotion journal: %w", err))
		}
		if err := p.writePlanningPromoteJournal(journal); err != nil {
			// A failed atomic replacement can leave either the prepared or the
			// committed marker on disk. Recovery reads that marker and chooses
			// rollback versus roll-forward without guessing.
			return p.recoverAfterPlanningPromoteError(fmt.Errorf("commit planning promotion journal: %w", err))
		}
		faultPoint("planning-promote-committed")
		faultPoint("planning-promote-cleanup")

		for index, entry := range journal.Entries {
			path, err := p.resolvePlanningPromoteTarget(entry.Before.Path)
			if err != nil {
				return fmt.Errorf("remove promoted planning source %s: %w (committed journal retained)", promoted[index].ShortID, err)
			}
			if err := p.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove promoted planning source %s: %w (committed journal retained)", promoted[index].ShortID, err)
			}
			faultPoint(fmt.Sprintf("planning-promote-removal-%d", index+1))
			faultPoint(fmt.Sprintf("planning-promote-source-removal-%d", index+1))
		}

		targets, err := p.planningPromoteRecoveryTargets(journal)
		if err != nil {
			return fmt.Errorf("planning promotion did not converge (committed journal retained): %w", err)
		}
		if err := p.confirmPlanningPromoteRecoveryConvergence(journal, targets); err != nil {
			return fmt.Errorf("planning promotion did not converge (committed journal retained): %w", err)
		}
		if err := p.removeRecoveredPlanningPromoteJournal(journal); err != nil {
			return err
		}

		// The in-memory snapshot is still the same locked snapshot used to
		// construct the journal. Replace the promoted planning vertices with
		// their resulting todo tasks before calculating readiness, otherwise a
		// promoted dependency would incorrectly remain a planning blocker.
		finalTasks := append([]core.Task(nil), snapshot.tasks...)
		finalTasks = append(finalTasks, promoted...)
		selectedIDs := make(map[string]struct{}, len(promoted))
		for _, task := range promoted {
			selectedIDs[task.ID] = struct{}{}
		}
		finalPlanning := make([]core.PlanningItem, 0, len(snapshot.planningItems)-len(selected))
		for _, item := range snapshot.planningItems {
			if _, promoted := selectedIDs[item.ID]; !promoted {
				finalPlanning = append(finalPlanning, item)
			}
		}

		result = provider.PlanningPromotionResult[core.TaskView]{
			DryRun: false,
			Count:  len(promoted),
			Items:  make([]core.TaskView, 0, len(promoted)),
		}
		for _, task := range promoted {
			view, err := p.decorateTaskWithCatalog(task, finalTasks, finalPlanning, "", &catalog)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, view)
		}
		return nil
	})
	if err != nil {
		return provider.PlanningPromotionResult[core.TaskView]{}, err
	}
	return result, nil
}

// planningPromotionTimestamp gives every selected record one common UTC
// timestamp while preserving monotonicity when the wall clock goes backwards
// or has insufficient resolution for an existing future updatedAt.
func planningPromotionTimestamp(selected []core.PlanningItem) time.Time {
	latest := time.Time{}
	for _, item := range selected {
		if item.UpdatedAt.After(latest) {
			latest = item.UpdatedAt
		}
	}
	now := time.Now().UTC()
	if now.After(latest) {
		return now
	}
	return latest.Add(time.Nanosecond).UTC()
}

// preparePlanningPromotion validates the exact live endpoints and builds the
// before/after byte snapshots before the prepared journal is published. The
// planning decoder has already validated semantics; the raw bytes are retained
// so formatting, key order, escaping, and omitted optional fields survive.
func (p *Provider) preparePlanningPromotion(selected []core.PlanningItem, promotionAt time.Time) (planningPromoteJournal, []core.Task, error) {
	journal := planningPromoteJournal{
		Version:     planningPromoteJournalVersion,
		State:       planningPromotePrepared,
		SelectedIDs: make([]string, 0, len(selected)),
		Entries:     make([]planningPromoteJournalEntry, 0, len(selected)),
	}
	promoted := make([]core.Task, 0, len(selected))
	seenAfter := make(map[string]struct{}, len(selected))
	for _, item := range selected {
		paths, err := p.planningItemPaths(item)
		if err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("snapshot planned item %s: %w", item.ShortID, err)
		}
		beforePath := paths[0]
		beforeRelative, err := filepath.Rel(p.root, beforePath)
		if err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("relativize planned item %s: %w", item.ShortID, err)
		}
		beforeRelative = filepath.ToSlash(beforeRelative)
		if _, err := p.resolvePlanningPromoteTarget(beforeRelative); err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("resolve planned item %s: %w", item.ShortID, err)
		}
		beforeData, err := os.ReadFile(beforePath)
		if err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("read planned item %s: %w", item.ShortID, err)
		}
		decoded, err := decodePlanningPromotionSource(beforeData)
		if err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("validate planned item %s: %w", item.ShortID, err)
		}
		if !reflect.DeepEqual(decoded, item) {
			return planningPromoteJournal{}, nil, fmt.Errorf("planned item %s changed while preparing promotion", item.ShortID)
		}

		task := planningPromoteTaskFromPlanning(item, promotionAt)
		afterData, err := rewritePlanningPromotionSource(beforeData, promotionAt)
		if err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("prepare promoted task %s: %w", item.ShortID, err)
		}
		// Validate the byte-preserving projection against the same task model
		// used by journal recovery before anything durable is written.
		var after core.Task
		if err := json.Unmarshal(afterData, &after); err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("decode promoted task %s: %w", item.ShortID, err)
		}
		if !reflect.DeepEqual(after, task) {
			return planningPromoteJournal{}, nil, fmt.Errorf("promoted task %s differs outside allowed lifecycle fields", item.ShortID)
		}

		afterPath := filepath.ToSlash(filepath.Join(string(core.StatusTodo), item.ShortID+".json"))
		if _, duplicate := seenAfter[afterPath]; duplicate {
			return planningPromoteJournal{}, nil, fmt.Errorf("promotion target %s is duplicated", afterPath)
		}
		seenAfter[afterPath] = struct{}{}

		afterHost, err := p.resolvePlanningPromoteTarget(afterPath)
		if err != nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("resolve promotion target %s: %w", afterPath, err)
		}
		if existing, err := os.ReadFile(afterHost); err == nil {
			return planningPromoteJournal{}, nil, fmt.Errorf("promotion target %s already exists (%d bytes)", afterPath, len(existing))
		} else if !errors.Is(err, os.ErrNotExist) {
			return planningPromoteJournal{}, nil, fmt.Errorf("inspect promotion target %s: %w", afterPath, err)
		}

		journal.SelectedIDs = append(journal.SelectedIDs, item.ID)
		journal.Entries = append(journal.Entries, planningPromoteJournalEntry{
			Before: planningPromoteSnapshot{Path: beforeRelative, Data: append([]byte(nil), beforeData...)},
			After:  planningPromoteSnapshot{Path: afterPath, Data: afterData},
		})
		promoted = append(promoted, task)
	}
	if err := p.validatePlanningPromoteJournal(journal); err != nil {
		return planningPromoteJournal{}, nil, fmt.Errorf("prepare planning promotion: %w", err)
	}
	return journal, promoted, nil
}

func decodePlanningPromotionSource(data []byte) (core.PlanningItem, error) {
	return planningjson.Decode(data)
}

// rewritePlanningPromotionSource changes only the JSON values owned by the
// promotion lifecycle. All bytes before, between, and after those values are
// copied verbatim, including whitespace, field order, and string spellings of
// every preserved field.
func rewritePlanningPromotionSource(data []byte, promotionAt time.Time) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("planning record must be an object")
	}
	type replacement struct {
		start int
		end   int
		data  []byte
	}
	var replacements []replacement
	foundStatus, foundUpdatedAt := false, false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("planning record property name must be a string")
		}
		valueStart := int(decoder.InputOffset())
		for valueStart < len(data) && (data[valueStart] == ' ' || data[valueStart] == '\t' || data[valueStart] == '\r' || data[valueStart] == '\n') {
			valueStart++
		}
		if valueStart >= len(data) || data[valueStart] != ':' {
			return nil, errors.New("planning record property is missing a colon")
		}
		valueStart++
		for valueStart < len(data) && (data[valueStart] == ' ' || data[valueStart] == '\t' || data[valueStart] == '\r' || data[valueStart] == '\n') {
			valueStart++
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		valueEnd := valueStart + len(raw)
		if valueStart < 0 || valueEnd > len(data) {
			return nil, errors.New("planning record property value offset is invalid")
		}
		switch key {
		case "status":
			foundStatus = true
			replacements = append(replacements, replacement{start: valueStart, end: valueEnd, data: []byte(`"todo"`)})
		case "updatedAt":
			foundUpdatedAt = true
			encoded, err := json.Marshal(promotionAt.UTC().Format(time.RFC3339Nano))
			if err != nil {
				return nil, err
			}
			replacements = append(replacements, replacement{start: valueStart, end: valueEnd, data: encoded})
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := closing.(json.Delim); !ok || delim != '}' {
		return nil, errors.New("planning record object is not closed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("planning record has trailing JSON data")
		}
		return nil, err
	}
	if !foundStatus || !foundUpdatedAt {
		return nil, errors.New("planning record is missing status or updatedAt")
	}
	var out bytes.Buffer
	previous := 0
	for _, item := range replacements {
		out.Write(data[previous:item.start])
		out.Write(item.data)
		previous = item.end
	}
	out.Write(data[previous:])
	return out.Bytes(), nil
}

func (p *Provider) recoverAfterPlanningPromoteError(original error) error {
	recoveryErr := p.recoverPlanningPromote()
	if recoveryErr == nil {
		return original
	}
	return fmt.Errorf("%v; planning promotion recovery failed (journal retained): %w", original, recoveryErr)
}
