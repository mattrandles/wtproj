package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
)

func TestPreparedPlanningPromoteRecoveryRestoresPlanningBeforeRemovingTodoAndIsIdempotent(t *testing.T) {
	p, journal := planningPromoteRecoveryFixture(t, 2)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].After)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[1].Before)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[1].After)
	if err := p.writePlanningPromoteJournal(journal); err != nil {
		t.Fatalf("write prepared planning journal: %v", err)
	}

	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(prepared planning recovery): %v", err)
	}
	assertPlanningPromoteRecoveryState(t, p, journal, planningPromotePrepared)
	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(repeated prepared planning recovery): %v", err)
	}
	assertPlanningPromoteJournalAbsent(t, p)
}

func TestCommittedPlanningPromoteRecoveryFinishesTodoBeforeRemovingPlanningAndIsIdempotent(t *testing.T) {
	p, journal := planningPromoteRecoveryFixture(t, 2)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].Before)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[1].Before)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[1].After)
	journal.State = planningPromoteCommitted
	if err := p.writePlanningPromoteJournal(journal); err != nil {
		t.Fatalf("write committed planning journal: %v", err)
	}

	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(committed planning recovery): %v", err)
	}
	assertPlanningPromoteRecoveryState(t, p, journal, planningPromoteCommitted)
	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(repeated committed planning recovery): %v", err)
	}
	assertPlanningPromoteJournalAbsent(t, p)
}

func TestPlanningPromoteRecoveryPreflightsEveryJournalBeforeChangingEarlierEndpoints(t *testing.T) {
	p, journal := planningPromoteRecoveryFixture(t, 1)
	task := validTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.StatusTodo)
	if err := p.writeTask(task); err != nil {
		t.Fatalf("write batch fixture task: %v", err)
	}
	after := task
	after.Title = "would be recovered only after complete preflight"
	after.UpdatedAt = after.UpdatedAt.Add(time.Nanosecond)
	if err := p.writeBatchUpdateJournal(batchUpdateJournal{
		Version: batchUpdateJournalVersion,
		State:   batchJournalCommitted,
		Entries: []batchUpdateJournalEntry{{Before: task, After: after}},
	}); err != nil {
		t.Fatalf("write batch journal: %v", err)
	}
	staleBefore, err := p.resolvePlanningPromoteTarget(journal.Entries[0].Before.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleBefore, []byte("not a journal endpoint"), 0o644); err != nil {
		t.Fatalf("write stale planning endpoint: %v", err)
	}
	if err := p.writePlanningPromoteJournal(journal); err != nil {
		t.Fatalf("write planning journal: %v", err)
	}

	if _, err := New(p.root, nil); err == nil || !strings.Contains(err.Error(), "planning promotion recovery preflight failed") || !strings.Contains(err.Error(), "journal retained") {
		t.Fatalf("New(invalid planning journal alongside batch) error = %v", err)
	}
	assertStoredTask(t, p.taskPath(core.StatusTodo, task.ShortID), task)
	assertPlanningPromoteJournalPresent(t, p)
	if _, err := os.Stat(p.batchUpdateJournalPath()); err != nil {
		t.Fatalf("batch journal was changed before complete preflight: %v", err)
	}
}

func TestPlanningPromoteRecoveryRetainsJournalForReplacementRemovalAndConvergenceFailures(t *testing.T) {
	t.Run("replacement", func(t *testing.T) {
		p, journal := planningPromoteRecoveryFixture(t, 1)
		writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].After)
		if err := p.writePlanningPromoteJournal(journal); err != nil {
			t.Fatal(err)
		}
		p.fs.replace = func(_, _ string) error { return errors.New("injected planning replacement failure") }
		if err := p.recoverPlanningPromote(); err == nil || !strings.Contains(err.Error(), "replacement 1") || !strings.Contains(err.Error(), "journal retained") {
			t.Fatalf("replacement recovery error = %v", err)
		}
		assertPlanningPromoteJournalPresent(t, p)
	})

	t.Run("removal", func(t *testing.T) {
		p, journal := planningPromoteRecoveryFixture(t, 1)
		writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].After)
		if err := p.writePlanningPromoteJournal(journal); err != nil {
			t.Fatal(err)
		}
		realRemove := p.fs.remove
		p.fs.remove = func(path string) error {
			if path == filepath.Join(p.root, filepath.FromSlash(journal.Entries[0].After.Path)) {
				return errors.New("injected planning removal failure")
			}
			return realRemove(path)
		}
		if err := p.recoverPlanningPromote(); err == nil || !strings.Contains(err.Error(), "removal 1") || !strings.Contains(err.Error(), "journal retained") {
			t.Fatalf("removal recovery error = %v", err)
		}
		assertPlanningPromoteJournalPresent(t, p)
	})

	t.Run("convergence sync", func(t *testing.T) {
		p, journal := planningPromoteRecoveryFixture(t, 1)
		writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].Before)
		writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].After)
		if err := p.writePlanningPromoteJournal(journal); err != nil {
			t.Fatal(err)
		}
		realSync := p.fs.syncDirectory
		calls := 0
		p.fs.syncDirectory = func(path string) error {
			calls++
			if calls == 3 {
				return errors.New("injected converged-directory sync failure")
			}
			return realSync(path)
		}
		if err := p.recoverPlanningPromote(); err == nil || !strings.Contains(err.Error(), "did not converge") || !strings.Contains(err.Error(), "journal retained") {
			t.Fatalf("convergence recovery error = %v", err)
		}
		assertPlanningPromoteJournalPresent(t, p)
	})
}

func TestPlanningPromoteRecoveryRefusesConflictingLiveBatchTransaction(t *testing.T) {
	p, journal := planningPromoteRecoveryFixture(t, 1)
	writePlanningPromoteSnapshotForTest(t, p, journal.Entries[0].Before)
	if err := p.writePlanningPromoteJournal(journal); err != nil {
		t.Fatal(err)
	}
	var before core.Task
	if err := json.Unmarshal(journal.Entries[0].After.Data, &before); err != nil {
		t.Fatal(err)
	}
	after := before
	after.Title = "conflicting batch endpoint"
	after.UpdatedAt = after.UpdatedAt.Add(time.Nanosecond)
	if err := p.writeBatchUpdateJournal(batchUpdateJournal{
		Version: batchUpdateJournalVersion,
		State:   batchJournalCommitted,
		Entries: []batchUpdateJournalEntry{{Before: before, After: after}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := p.CreateTask(core.CreateTaskInput{Title: "must not start live transaction"}); err == nil || !strings.Contains(err.Error(), "shared target") || !strings.Contains(err.Error(), "both journals retained") {
		t.Fatalf("CreateTask(conflicting journals) error = %v", err)
	}
	assertPlanningPromoteJournalPresent(t, p)
	if _, err := os.Stat(p.batchUpdateJournalPath()); err != nil {
		t.Fatalf("conflicting batch journal was removed: %v", err)
	}
}

func planningPromoteRecoveryFixture(t *testing.T, count int) (*Provider, planningPromoteJournal) {
	t.Helper()
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]planningPromoteJournalEntry, count)
	selected := make([]string, count)
	promotedAt := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	for index := range entries {
		created := time.Date(2026, 8, 31, 12, 0, index, 0, time.UTC)
		item := core.PlanningItem{
			ID:           []string{"15c3806a-bd1b-424d-889b-29e5b06679b8", "25c3806a-bd1b-424d-889b-29e5b06679b8"}[index],
			ShortID:      []string{"wtp-0d6e4079-0082", "wtp-0d6e4079-0083"}[index],
			Title:        "planning recovery fixture",
			Description:  "recover exact promotion endpoints",
			Status:       core.PlanningStatusPlanned,
			Dependencies: []string{},
			Comments:     []core.Comment{},
			CreatedAt:    created,
			UpdatedAt:    created.Add(time.Minute),
		}
		before, err := planningjson.Encode(item)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(planningPromoteTaskFromPlanning(item, promotedAt))
		if err != nil {
			t.Fatal(err)
		}
		entries[index] = planningPromoteJournalEntry{
			Before: planningPromoteSnapshot{Path: "planning/planned/" + item.ShortID + ".json", Data: before},
			After:  planningPromoteSnapshot{Path: "todo/" + item.ShortID + ".json", Data: after},
		}
		selected[index] = item.ID
	}
	return p, planningPromoteJournal{Version: planningPromoteJournalVersion, State: planningPromotePrepared, SelectedIDs: selected, Entries: entries}
}

func writePlanningPromoteSnapshotForTest(t *testing.T, p *Provider, snapshot planningPromoteSnapshot) {
	t.Helper()
	path, err := p.resolvePlanningPromoteTarget(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, snapshot.Data, 0o644); err != nil {
		t.Fatalf("write %s: %v", snapshot.Path, err)
	}
}

func assertPlanningPromoteRecoveryState(t *testing.T, p *Provider, journal planningPromoteJournal, state string) {
	t.Helper()
	for _, entry := range journal.Entries {
		want, absent := entry.Before, entry.After
		if state == planningPromoteCommitted {
			want, absent = entry.After, entry.Before
		}
		path, err := p.resolvePlanningPromoteTarget(want.Path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want.Data) {
			t.Fatalf("recovered %s = %q, %v; want %q", want.Path, got, err, want.Data)
		}
		absentPath, err := p.resolvePlanningPromoteTarget(absent.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(absentPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("opposite endpoint %s stat = %v, want absent", absent.Path, err)
		}
	}
}

func assertPlanningPromoteJournalPresent(t *testing.T, p *Provider) {
	t.Helper()
	if _, err := os.Stat(p.planningPromoteJournalPath()); err != nil {
		t.Fatalf("planning promotion journal stat = %v, want present", err)
	}
}

func assertPlanningPromoteJournalAbsent(t *testing.T, p *Provider) {
	t.Helper()
	if _, err := os.Stat(p.planningPromoteJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning promotion journal stat = %v, want absent", err)
	}
}
