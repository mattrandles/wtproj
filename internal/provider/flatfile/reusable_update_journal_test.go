package flatfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/reusablejson"
)

func TestReusableUpdateJournalCodecAndValidationAcceptEmptyDetachSet(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, nil)
	if err := p.validateReusableUpdateJournal(journal); err != nil {
		t.Fatalf("validateReusableUpdateJournal() error = %v", err)
	}
	encoded, err := encodeReusableUpdateJournal(journal)
	if err != nil {
		t.Fatalf("encodeReusableUpdateJournal() error = %v", err)
	}
	decoded, err := decodeReusableUpdateJournal(encoded)
	if err != nil {
		t.Fatalf("decodeReusableUpdateJournal() error = %v", err)
	}
	if err := p.validateReusableUpdateJournal(decoded); err != nil {
		t.Fatalf("validate decoded journal error = %v", err)
	}
	encodedAgain, err := encodeReusableUpdateJournal(decoded)
	if err != nil {
		t.Fatalf("encode reusable update journal again error = %v", err)
	}
	if string(encodedAgain) != string(encoded) {
		t.Fatalf("codec was not deterministic:\nfirst:  %s\nsecond: %s", encoded, encodedAgain)
	}
	if err := p.writeReusableUpdateJournal(journal); err != nil {
		t.Fatalf("writeReusableUpdateJournal() error = %v", err)
	}
	read, err := p.readReusableUpdateJournal()
	if err != nil {
		t.Fatalf("readReusableUpdateJournal() error = %v", err)
	}
	if read.ReusableTaskID != journal.ReusableTaskID || len(read.AffectedTasks) != 0 {
		t.Fatalf("read journal = %#v, want empty-detach journal %#v", read, journal)
	}
}

func TestReusableUpdateJournalValidationAcceptsMultiStatusDetaches(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusPaused, core.StatusDone})
	if err := p.validateReusableUpdateJournal(journal); err != nil {
		t.Fatalf("validateReusableUpdateJournal(multi-status) error = %v", err)
	}
	if got, want := len(journal.AffectedTasks), 4; got != want {
		t.Fatalf("affected task count = %d, want %d", got, want)
	}
}

func TestReusableUpdateJournalTransitionsOnlyPreparedToCommitted(t *testing.T) {
	_, journal := reusableUpdateJournalFixture(t, nil)
	if err := transitionReusableUpdateJournal(&journal, reusableUpdateJournalCommitted); err != nil {
		t.Fatalf("prepared -> committed error = %v", err)
	}
	if journal.State != reusableUpdateJournalCommitted {
		t.Fatalf("state = %q, want committed", journal.State)
	}
	if err := transitionReusableUpdateJournal(&journal, reusableUpdateJournalCommitted); err == nil {
		t.Fatal("committed -> committed transition unexpectedly succeeded")
	}
	journal.State = reusableUpdateJournalPrepared
	if err := transitionReusableUpdateJournal(&journal, reusableUpdateJournalPrepared); err == nil {
		t.Fatal("prepared -> prepared transition unexpectedly succeeded")
	}
}

func TestReusableUpdateJournalRejectsCorruptAndInconsistentInput(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	encoded, err := encodeReusableUpdateJournal(journal)
	if err != nil {
		t.Fatalf("encodeReusableUpdateJournal() error = %v", err)
	}
	for _, corrupt := range [][]byte{
		[]byte(`{"version":1,"version":1}`),
		[]byte(`{"version":1,"state":"prepared","reusableTaskId":"x","catalog":{},"affectedTasks":[],"unknown":true}`),
		[]byte(`{"version":1,"state":"prepared","reusableTaskId":"x","catalog":{},"affectedTasks":[]}{}`),
		[]byte{0xff, 0xfe},
	} {
		if _, err := decodeReusableUpdateJournal(corrupt); err == nil {
			t.Fatalf("decodeReusableUpdateJournal(%q) unexpectedly succeeded", corrupt)
		}
	}

	duplicate := journal
	duplicate.AffectedTasks = append(duplicate.AffectedTasks, duplicate.AffectedTasks[0])
	if err := p.validateReusableUpdateJournal(duplicate); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate target validation error = %v, want duplicate failure", err)
	}
	inconsistent := journal
	inconsistent.AffectedTasks[0].After.Data = append([]byte(nil), inconsistent.AffectedTasks[0].Before.Data...)
	if err := p.validateReusableUpdateJournal(inconsistent); err == nil || !strings.Contains(err.Error(), "updatedAt") {
		t.Fatalf("inconsistent task validation error = %v, want updatedAt failure", err)
	}
	if string(encoded) == "" {
		t.Fatal("encoded fixture unexpectedly empty")
	}
}

func TestReusableUpdateJournalRejectsEscapingAndWindowsPaths(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	if got, err := p.resolveReusableUpdateTarget(journal.AffectedTasks[0].Before.Path); err != nil || got != filepath.Join(p.root, "todo", "wtp-0001.json") {
		t.Fatalf("resolve Unix contract path = %q, %v", got, err)
	}
	for _, target := range []string{"../todo/wtp-0001.json", "todo\\wtp-0001.json", "/tmp/outside.json", "todo/../todo/wtp-0001.json"} {
		changed := journal
		changed.AffectedTasks = append([]reusableUpdateJournalChange(nil), journal.AffectedTasks...)
		changed.AffectedTasks[0].Before.Path = target
		if err := p.validateReusableUpdateJournal(changed); err == nil {
			t.Fatalf("validate journal with path %q unexpectedly succeeded", target)
		}
	}
}

func TestReusableUpdateJournalRejectsSymlinkEscapingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink setup depends on host privileges; separator handling is platform-independent")
	}
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	targetDir := p.statusDir(core.StatusTodo)
	if err := os.Remove(targetDir); err != nil {
		t.Fatalf("remove empty status directory: %v", err)
	}
	if err := os.Symlink(t.TempDir(), targetDir); err != nil {
		t.Fatalf("create escaping status symlink: %v", err)
	}
	if err := p.validateReusableUpdateJournal(journal); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("symlink target validation error = %v, want escape failure", err)
	}
}

func TestReusableUpdateJournalRejectsStaleEndpointsBeforeRecovery(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo, core.StatusDone})
	writeReusableUpdateSnapshotForTest(t, p, journal.Catalog.Before)
	for _, change := range journal.AffectedTasks {
		writeReusableUpdateSnapshotForTest(t, p, change.Before)
	}
	if err := p.validateReusableUpdateJournalForRecovery(journal); err != nil {
		t.Fatalf("validateReusableUpdateJournalForRecovery(before endpoints) error = %v", err)
	}
	path, err := p.resolveReusableUpdateTarget(journal.AffectedTasks[0].Before.Path)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"corrupt":"stale"}`), 0o644); err != nil {
		t.Fatalf("write stale endpoint: %v", err)
	}
	if err := p.validateReusableUpdateJournalForRecovery(journal); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale recovery validation error = %v, want stale failure", err)
	}
}

func TestReusableUpdateJournalRecoveryValidationRejectsOmittedLiveReference(t *testing.T) {
	p, journal := reusableUpdateJournalFixture(t, []core.Status{core.StatusTodo})
	writeReusableUpdateSnapshotForTest(t, p, journal.Catalog.Before)
	for _, change := range journal.AffectedTasks {
		writeReusableUpdateSnapshotForTest(t, p, change.Before)
	}
	omitted := validTask("65c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0099", core.StatusPaused)
	omitted.ReusableTaskIDs = []string{journal.ReusableTaskID}
	started := omitted.CreatedAt
	omitted.StartedAt = &started
	if err := p.writeTask(omitted); err != nil {
		t.Fatalf("write omitted task: %v", err)
	}
	if err := p.validateReusableUpdateJournalForRecovery(journal); err == nil || !strings.Contains(err.Error(), "not represented") {
		t.Fatalf("omitted live reference validation error = %v, want represented failure", err)
	}
}

func reusableUpdateJournalFixture(t *testing.T, statuses []core.Status) (*Provider, reusableUpdateJournal) {
	t.Helper()
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	when := mustRFC3339Time(t, "2026-03-24T14:10:04Z")
	deleted := reusableDefinitionForTest("a5c3806a-bd1b-424d-889b-29e5b06679b8", "Deleted", when)
	retained := reusableDefinitionForTest("b5c3806a-bd1b-424d-889b-29e5b06679b8", "Retained", when)
	beforeCatalog := core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{deleted, retained}}
	afterCatalog := core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{retained}}
	beforeCatalogBytes, err := reusablejson.Encode(beforeCatalog)
	if err != nil {
		t.Fatalf("encode before catalog: %v", err)
	}
	afterCatalogBytes, err := reusablejson.Encode(afterCatalog)
	if err != nil {
		t.Fatalf("encode after catalog: %v", err)
	}
	journal := reusableUpdateJournal{
		Version:        reusableUpdateJournalVersion,
		State:          reusableUpdateJournalPrepared,
		ReusableTaskID: deleted.ID,
		Catalog: reusableUpdateJournalChange{
			Before: reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: beforeCatalogBytes},
			After:  reusableUpdateJournalSnapshot{Path: reusableUpdateCatalogTarget, Exists: true, Data: afterCatalogBytes},
		},
		AffectedTasks: make([]reusableUpdateJournalChange, 0, len(statuses)),
	}
	for index, status := range statuses {
		before := reusableUpdateTaskForTest(index, status, deleted.ID, retained.ID)
		after := before
		after.ReusableTaskIDs = []string{retained.ID}
		after.UpdatedAt = before.UpdatedAt.Add(time.Second)
		beforeBytes, err := json.Marshal(before)
		if err != nil {
			t.Fatalf("marshal before task: %v", err)
		}
		afterBytes, err := json.Marshal(after)
		if err != nil {
			t.Fatalf("marshal after task: %v", err)
		}
		target := filepath.ToSlash(filepath.Join(string(status), before.ShortID+".json"))
		journal.AffectedTasks = append(journal.AffectedTasks, reusableUpdateJournalChange{
			Before: reusableUpdateJournalSnapshot{Path: target, Exists: true, Data: beforeBytes},
			After:  reusableUpdateJournalSnapshot{Path: target, Exists: true, Data: afterBytes},
		})
	}
	return p, journal
}

func reusableUpdateTaskForTest(index int, status core.Status, deletedID, retainedID string) core.Task {
	ids := []string{
		"25c3806a-bd1b-424d-889b-29e5b06679b8",
		"35c3806a-bd1b-424d-889b-29e5b06679b8",
		"45c3806a-bd1b-424d-889b-29e5b06679b8",
		"55c3806a-bd1b-424d-889b-29e5b06679b8",
	}
	task := validTask(ids[index], "wtp-000"+string(rune('1'+index)), status)
	task.ReusableTaskIDs = []string{deletedID, retainedID}
	if status == core.StatusInProgress || status == core.StatusPaused || status == core.StatusDone {
		started := task.CreatedAt
		task.StartedAt = &started
	}
	if status == core.StatusDone {
		completed := task.CreatedAt
		task.CompletedAt = &completed
	}
	return task
}

func writeReusableUpdateSnapshotForTest(t *testing.T, p *Provider, snapshot reusableUpdateJournalSnapshot) {
	t.Helper()
	path, err := p.resolveReusableUpdateTarget(snapshot.Path)
	if err != nil {
		t.Fatalf("resolve snapshot target: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create snapshot parent: %v", err)
	}
	if err := os.WriteFile(path, snapshot.Data, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}
