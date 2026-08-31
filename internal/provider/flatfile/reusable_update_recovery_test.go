package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/reusablejson"
)

func TestPreparedReusableUpdateRecoveryRestoresEverySnapshotAndIsIdempotent(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusDone})
	writeReusableRecoveryBeforeState(t, p, journal)
	// A previous recovery attempt may have converged one task before the
	// process stopped. Each target is allowed to be either complete endpoint.
	writeReusableUpdateSnapshotForTest(t, p, journal.AffectedTasks[0].After)
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("writeReusableUpdateJournal() error = %v", err)
	}

	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(prepared recovery) error = %v", err)
	}
	assertReusableRecoveryState(t, p, journal, reusableUpdateJournalPrepared)
	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(repeated prepared recovery) error = %v", err)
	}
	assertReusableJournalAbsent(t, p)
}

func TestCommittedReusableUpdateRecoveryFinishesEverySnapshotAndIsIdempotent(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusDone})
	writeReusableRecoveryAfterState(t, p, journal)
	// Leave the first task at its before endpoint to exercise roll-forward.
	writeReusableUpdateSnapshotForTest(t, p, journal.AffectedTasks[0].Before)
	if err := p.writeReusableUpdateJournal(journalWithState(journal, reusableUpdateJournalCommitted)); err != nil {
		t.Fatalf("write committed reusable journal error = %v", err)
	}

	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(committed recovery) error = %v", err)
	}
	assertReusableRecoveryState(t, p, journal, reusableUpdateJournalCommitted)
	if _, err := New(p.root, nil); err != nil {
		t.Fatalf("New(repeated committed recovery) error = %v", err)
	}
	assertReusableJournalAbsent(t, p)
}

func TestReusableUpdateRecoverySupportsCatalogOnlyAndCustomStatusDirectories(t *testing.T) {
	catalog, err := core.NewStatusCatalog([]core.StatusDefinition{{Name: "review", Category: core.StatusCategoryWaiting}})
	if err != nil {
		t.Fatalf("NewStatusCatalog() error = %v", err)
	}
	p, err := NewWithCatalog(t.TempDir(), nil, catalog)
	if err != nil {
		t.Fatalf("NewWithCatalog() error = %v", err)
	}
	when := mustRFC3339Time(t, "2026-03-24T14:10:04Z")
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Deleted", when)
	retained := reusableDefinitionForTest("b5c3806a-bd1b-424d-889b-29e5b06679b8", "Retained", when)
	beforeCatalog := core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted, retained}}
	afterCatalog := core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{retained}}
	beforeCatalogBytes := mustReusableCatalogBytes(t, beforeCatalog)
	afterCatalogBytes := mustReusableCatalogBytes(t, afterCatalog)
	journal := reusableUpdateJournal{
		Version: reusableUpdateJournalVersion, State: reusableUpdateJournalPrepared, ReusableTaskID: deleted.ID,
		Catalog: reusableUpdateJournalChange{
			Before: reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: beforeCatalogBytes},
			After:  reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: afterCatalogBytes},
		},
		AffectedTasks: []reusableUpdateJournalChange{},
	}
	writeReusableUpdateSnapshotForTest(t, p, journal.Catalog.Before)
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("write catalog-only journal error = %v", err)
	}
	if _, err := NewWithCatalog(p.root, nil, catalog); err != nil {
		t.Fatalf("NewWithCatalog(catalog-only recovery) error = %v", err)
	}
	assertReusableJournalAbsent(t, p)
	stored, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("read recovered catalog error = %v", err)
	}
	if string(stored) != string(beforeCatalogBytes) {
		t.Fatalf("prepared catalog-only bytes = %q, want %q", stored, beforeCatalogBytes)
	}

	// A custom status directory is an ordinary journal target; recovery must
	// not assume the four legacy directory names.
	task := validTask("25c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0001", core.Status("review"))
	started := task.CreatedAt
	task.StartedAt = &started
	task.ReusableTaskIDs = []string{deleted.ID, retained.ID}
	if err := p.writeTask(task); err != nil {
		t.Fatalf("write custom-status task error = %v", err)
	}
	after := task
	after.ReusableTaskIDs = []string{retained.ID}
	after.UpdatedAt = task.UpdatedAt.Add(1)
	beforeData := mustJSON(t, task)
	afterData := mustJSON(t, after)
	journal = reusableUpdateJournal{
		Version: reusableUpdateJournalVersion, State: reusableUpdateJournalCommitted, ReusableTaskID: deleted.ID,
		Catalog: reusableUpdateJournalChange{
			Before: reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: beforeCatalogBytes},
			After:  reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: afterCatalogBytes},
		},
		AffectedTasks: []reusableUpdateJournalChange{{
			Before: reusableUpdateJournalSnapshot{Path: filepath.ToSlash(filepath.Join("review", task.ShortID+".json")), Exists: true, Data: beforeData},
			After:  reusableUpdateJournalSnapshot{Path: filepath.ToSlash(filepath.Join("review", task.ShortID+".json")), Exists: true, Data: afterData},
		}},
	}
	writeReusableUpdateSnapshotForTest(t, p, journal.Catalog.After)
	writeReusableUpdateSnapshotForTest(t, p, journal.AffectedTasks[0].Before)
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("write custom-status journal error = %v", err)
	}
	if _, err := NewWithCatalog(p.root, nil, catalog); err != nil {
		t.Fatalf("NewWithCatalog(custom-status recovery) error = %v", err)
	}
	assertStoredTask(t, p.taskPath(core.Status("review"), task.ShortID), after)
	assertReusableJournalAbsent(t, p)
}

func TestReusableUpdateRecoveryRetainsJournalAndDiagnosticsForMissingTarget(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	writeReusableRecoveryBeforeState(t, p, journal)
	target, err := p.resolveReusableUpdateTarget(journal.AffectedTasks[0].Before.Path)
	if err != nil {
		t.Fatalf("resolve task target: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove task target: %v", err)
	}
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("writeReusableUpdateJournal() error = %v", err)
	}

	if _, err := New(p.root, nil); err == nil || !strings.Contains(err.Error(), "journal retained") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("New(missing reusable target) error = %v, want retained stale diagnostic", err)
	}
	if _, err := os.Stat(p.reusableUpdateJournalPath()); err != nil {
		t.Fatalf("missing-target recovery removed journal: %v", err)
	}
}

func TestReusableUpdateRecoveryRetainsJournalWhenReplacementOrCleanupFails(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	writeReusableRecoveryBeforeState(t, p, journal)
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("writeReusableUpdateJournal() error = %v", err)
	}

	p.fs.replace = func(_, _ string) error { return errors.New("injected reusable recovery replacement failure") }
	if err := p.recoverReusableUpdate(); err == nil || !strings.Contains(err.Error(), "journal retained") || !strings.Contains(err.Error(), "replacement 1") {
		t.Fatalf("replacement failure error = %v, want retained diagnostic", err)
	}
	if _, err := os.Stat(p.reusableUpdateJournalPath()); err != nil {
		t.Fatalf("replacement failure removed journal: %v", err)
	}

	p.fs = defaultFileSystem()
	if err := p.recoverReusableUpdate(); err != nil {
		t.Fatalf("recovery after replacement failure = %v", err)
	}

	// Cleanup is intentionally last and its failure must leave a converged
	// store plus the journal for a later retry.
	p, journal = reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	writeReusableRecoveryBeforeState(t, p, journal)
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("write cleanup fixture journal = %v", err)
	}
	realRemove := p.fs.remove
	p.fs.remove = func(path string) error {
		if path == p.reusableUpdateJournalPath() {
			return errors.New("injected reusable recovery cleanup failure")
		}
		return realRemove(path)
	}
	if err := p.recoverReusableUpdate(); err == nil || !strings.Contains(err.Error(), "cleanup") || !strings.Contains(err.Error(), "journal retained") {
		t.Fatalf("cleanup failure error = %v, want retained diagnostic", err)
	}
	assertReusableJournalPresent(t, p)
	p.fs = defaultFileSystem()
	if err := p.recoverReusableUpdate(); err != nil {
		t.Fatalf("recovery after cleanup failure = %v", err)
	}
	assertReusableJournalAbsent(t, p)
}

func TestPendingJournalRecoveryRejectsOverlappingBatchAndReusableTargets(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	writeReusableRecoveryBeforeState(t, p, journal)
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("write reusable journal error = %v", err)
	}
	before := journal.AffectedTasks[0]
	afterTask := mustTaskFromSnapshot(t, before.Before)
	afterTask.Title = "batch update"
	afterTask.UpdatedAt = afterTask.UpdatedAt.Add(1)
	if err := p.writeBatchUpdateJournal(batchUpdateJournal{
		Version: batchUpdateJournalVersion, State: batchJournalCommitted,
		Entries: []batchUpdateJournalEntry{{Before: mustTaskFromSnapshot(t, before.Before), After: afterTask}},
	}); err != nil {
		t.Fatalf("write overlapping batch journal: %v", err)
	}
	if _, err := New(p.root, nil); err == nil || !strings.Contains(err.Error(), "shared target") || !strings.Contains(err.Error(), "both journals retained") {
		t.Fatalf("New(overlapping journals) error = %v, want shared-target retained diagnostic", err)
	}
	if _, err := os.Stat(p.batchUpdateJournalPath()); err != nil {
		t.Fatalf("batch journal was removed after overlap refusal: %v", err)
	}
	if _, err := os.Stat(p.reusableUpdateJournalPath()); err != nil {
		t.Fatalf("reusable journal was removed after overlap refusal: %v", err)
	}
}

func writeReusableRecoveryBeforeState(t *testing.T, p *Provider, journal reusableUpdateJournal) {
	t.Helper()
	writeReusableUpdateSnapshotForTest(t, p, journal.Catalog.Before)
	for _, change := range journal.AffectedTasks {
		writeReusableUpdateSnapshotForTest(t, p, change.Before)
	}
}

func writeReusableRecoveryAfterState(t *testing.T, p *Provider, journal reusableUpdateJournal) {
	t.Helper()
	writeReusableUpdateSnapshotForTest(t, p, journal.Catalog.After)
	for _, change := range journal.AffectedTasks {
		writeReusableUpdateSnapshotForTest(t, p, change.After)
	}
}

func assertReusableRecoveryState(t *testing.T, p *Provider, journal reusableUpdateJournal, state string) {
	t.Helper()
	wantCatalog := journal.Catalog.Before
	if state == reusableUpdateJournalCommitted {
		wantCatalog = journal.Catalog.After
	}
	assertSnapshotBytes(t, p, wantCatalog)
	for _, change := range journal.AffectedTasks {
		want := change.Before
		if state == reusableUpdateJournalCommitted {
			want = change.After
		}
		assertSnapshotBytes(t, p, want)
	}
}

func assertSnapshotBytes(t *testing.T, p *Provider, snapshot reusableUpdateJournalSnapshot) {
	t.Helper()
	path, err := p.resolveReusableUpdateTarget(snapshot.Path)
	if err != nil {
		t.Fatalf("resolve recovered snapshot: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovered snapshot %s: %v", snapshot.Path, err)
	}
	if string(got) != string(snapshot.Data) {
		t.Fatalf("recovered %s bytes = %q, want %q", snapshot.Path, got, snapshot.Data)
	}
}

func assertReusableJournalAbsent(t *testing.T, p *Provider) {
	t.Helper()
	if _, err := os.Stat(p.reusableUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reusable update journal stat = %v, want absent", err)
	}
}

func assertReusableJournalPresent(t *testing.T, p *Provider) {
	t.Helper()
	if _, err := os.Stat(p.reusableUpdateJournalPath()); err != nil {
		t.Fatalf("reusable update journal stat = %v, want present", err)
	}
}

func journalWithState(journal reusableUpdateJournal, state string) reusableUpdateJournal {
	journal.State = state
	return journal
}

func mustReusableCatalogBytes(t *testing.T, catalog core.ReusableTaskCatalog) []byte {
	t.Helper()
	data, err := reusablejson.Encode(catalog)
	if err != nil {
		t.Fatalf("encode reusable catalog: %v", err)
	}
	return data
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON fixture: %v", err)
	}
	return data
}

func mustTaskFromSnapshot(t *testing.T, snapshot reusableUpdateJournalSnapshot) core.Task {
	t.Helper()
	var task core.Task
	if err := json.Unmarshal(snapshot.Data, &task); err != nil {
		t.Fatalf("decode task snapshot: %v", err)
	}
	return task
}
