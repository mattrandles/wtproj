package flatfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

func TestBatchPublicationFailureRollsBackPreparedStatusMove(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	originalRemove := p.fs.remove
	failed := false
	p.fs.remove = func(path string) error {
		if !failed && path == p.taskPath(core.StatusTodo, before.ShortID) {
			failed = true
			return errors.New("injected old status cleanup failure")
		}
		return originalRemove(path)
	}

	_, batchErr := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID:           before.ShortID,
		ExpectedUpdatedAt: before.UpdatedAt,
		Status:            core.OptionalStatus{Set: true, Value: core.StatusInProgress},
	}}})
	if batchErr == nil || !strings.Contains(batchErr.Error(), "injected old status cleanup failure") {
		t.Fatalf("BatchUpdate() error = %v", batchErr)
	}
	assertStoredTask(t, p.taskPath(before.Status, before.ShortID), before)
	if _, err := os.Stat(p.taskPath(core.StatusInProgress, before.ShortID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back target status file remains: %v (batch error: %v)", err, batchErr)
	}
	if _, err := os.Stat(filepath.Join(p.root, "meta", batchUpdateJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back journal remains: %v", err)
	}
}

func TestPreparedBatchJournalRollsBackBeforeProviderLoading(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	after := before
	after.Title = "Partially published"
	after.UpdatedAt = before.UpdatedAt.Add(1)
	journal := batchUpdateJournal{
		Version: batchUpdateJournalVersion,
		State:   batchJournalPrepared,
		Entries: []batchUpdateJournalEntry{{Before: before, After: after}},
	}
	if err := p.writeBatchUpdateJournal(journal); err != nil {
		t.Fatalf("writeBatchUpdateJournal() error = %v", err)
	}
	if err := p.writeTask(after); err != nil {
		t.Fatalf("write partially published task: %v", err)
	}

	reopened, err := New(p.root, nil)
	if err != nil {
		t.Fatalf("New() recovery error = %v", err)
	}
	view, err := reopened.GetTask(before.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if view.Title != before.Title || !view.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("prepared recovery task = %#v, want before snapshot %#v", view.Task, before)
	}
	if _, err := os.Stat(p.batchUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared recovery journal remains: %v", err)
	}
}

func TestCommittedBatchJournalRollsForwardBeforeProviderLoading(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	after := before
	after.Title = "Committed title"
	after.UpdatedAt = before.UpdatedAt.Add(1)
	journal := batchUpdateJournal{
		Version: batchUpdateJournalVersion,
		State:   batchJournalCommitted,
		Entries: []batchUpdateJournalEntry{{Before: before, After: after}},
	}
	if err := p.writeBatchUpdateJournal(journal); err != nil {
		t.Fatalf("writeBatchUpdateJournal() error = %v", err)
	}

	reopened, err := New(p.root, nil)
	if err != nil {
		t.Fatalf("New() recovery error = %v", err)
	}
	view, err := reopened.GetTask(before.ShortID, "")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if view.Title != after.Title || !view.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatalf("committed recovery task = %#v, want after snapshot %#v", view.Task, after)
	}
	if _, err := os.Stat(p.batchUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed recovery journal remains: %v", err)
	}
}

func TestBatchRecoveryFailureRetainsJournal(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	after := before
	after.Title = "Committed title"
	after.UpdatedAt = before.UpdatedAt.Add(1)
	journal := batchUpdateJournal{
		Version: batchUpdateJournalVersion,
		State:   batchJournalCommitted,
		Entries: []batchUpdateJournalEntry{{Before: before, After: after}},
	}
	if err := p.writeBatchUpdateJournal(journal); err != nil {
		t.Fatalf("writeBatchUpdateJournal() error = %v", err)
	}
	p.fs.replace = func(_, _ string) error { return errors.New("injected recovery replacement failure") }

	err := p.recoverBatchUpdate()
	if err == nil || !strings.Contains(err.Error(), "injected recovery replacement failure") {
		t.Fatalf("recoverBatchUpdate() error = %v", err)
	}
	if _, err := os.Stat(p.batchUpdateJournalPath()); err != nil {
		t.Fatalf("recovery failure removed journal: %v", err)
	}
}

func TestBatchPublicationFailureRestoresEveryOriginalTaskFile(t *testing.T) {
	p, first := newInternalProviderWithTask(t)
	second := validTask("35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.StatusTodo)
	if err := p.writeTask(second); err != nil {
		t.Fatalf("writeTask(second) error = %v", err)
	}
	want := snapshotStatusFiles(t, p)
	failTarget := p.taskPath(core.StatusInProgress, second.ShortID)
	realReplace := p.fs.replace
	failed := false
	p.fs.replace = func(source, target string) error {
		if !failed && target == failTarget {
			failed = true
			return errors.New("injected second replacement failure")
		}
		return realReplace(source, target)
	}

	_, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{
		{ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Title: core.OptionalString{Set: true, Value: "changed first"}},
		{ShortID: second.ShortID, ExpectedUpdatedAt: second.UpdatedAt, Status: core.OptionalStatus{Set: true, Value: core.StatusInProgress}},
	}})
	if err == nil || !strings.Contains(err.Error(), "injected second replacement failure") {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	assertStatusFilesEqual(t, p, want)
	if _, err := os.Stat(p.batchUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back journal = %v, want absent", err)
	}
}

func TestBatchJournalPublicationFailureLeavesTasksByteAndPathUnchanged(t *testing.T) {
	p, first := newInternalProviderWithTask(t)
	second := validTask("35c3806a-bd1b-424d-889b-29e5b06679b8", "wtp-0002", core.StatusTodo)
	if err := p.writeTask(second); err != nil {
		t.Fatalf("writeTask(second) error = %v", err)
	}
	want := snapshotStatusFiles(t, p)
	realReplace := p.fs.replace
	p.fs.replace = func(source, target string) error {
		if target == p.batchUpdateJournalPath() {
			return errors.New("injected journal publication failure")
		}
		return realReplace(source, target)
	}
	_, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: first.ShortID, ExpectedUpdatedAt: first.UpdatedAt, Title: core.OptionalString{Set: true, Value: "must not publish"},
	}}})
	if err == nil || !strings.Contains(err.Error(), "injected journal publication failure") {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	assertStatusFilesEqual(t, p, want)
	if _, err := os.Stat(p.batchUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed journal remains: %v", err)
	}
}

func TestBatchRecoveryFailureReturnsActionableErrorAndRetainsJournal(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	target := p.taskPath(core.StatusTodo, before.ShortID)
	realReplace := p.fs.replace
	p.fs.replace = func(source, path string) error {
		if path == target {
			return errors.New("injected task publication and rollback failure")
		}
		return realReplace(source, path)
	}
	_, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: before.ShortID, ExpectedUpdatedAt: before.UpdatedAt, Title: core.OptionalString{Set: true, Value: "unpublished"},
	}}})
	if err == nil || !strings.Contains(err.Error(), "batch recovery failed") || !strings.Contains(err.Error(), "journal was retained") {
		t.Fatalf("BatchUpdate() error = %v, want actionable retained-journal error", err)
	}
	if _, statErr := os.Stat(p.batchUpdateJournalPath()); statErr != nil {
		t.Fatalf("retained journal stat error = %v", statErr)
	}
	p.fs.replace = realReplace
	if err := p.recoverBatchUpdate(); err != nil {
		t.Fatalf("recoverBatchUpdate() after restoring filesystem error = %v", err)
	}
	assertStoredTask(t, target, before)
}

func TestCommittedRecoveryCleansDuplicateStatusCopies(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	after := before
	after.Status = core.StatusInProgress
	after.Title = "committed"
	after.UpdatedAt = before.UpdatedAt.Add(time.Second)
	after.StartedAt = &after.UpdatedAt
	if err := p.writeTask(after); err != nil {
		t.Fatalf("writeTask(after) error = %v", err)
	}
	if err := p.writeBatchUpdateJournal(batchUpdateJournal{
		Version: batchUpdateJournalVersion,
		State:   batchJournalCommitted,
		Entries: []batchUpdateJournalEntry{{Before: before, After: after}},
	}); err != nil {
		t.Fatalf("writeBatchUpdateJournal() error = %v", err)
	}
	if err := p.recoverBatchUpdate(); err != nil {
		t.Fatalf("recoverBatchUpdate() error = %v", err)
	}
	assertStoredTask(t, p.taskPath(after.Status, after.ShortID), after)
	if _, err := os.Stat(p.taskPath(before.Status, before.ShortID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old status copy remains: %v", err)
	}
	if _, err := os.Stat(p.batchUpdateJournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed journal remains: %v", err)
	}
}

func TestBatchRollbackRestoresLegacyUUIDPathAndBytes(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	shortPath := p.taskPath(before.Status, before.ShortID)
	legacyPath := p.taskPath(before.Status, before.ID)
	data, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	original := append([]byte(" \n"), append(data, '\n', '\n')...)
	if err := os.Remove(shortPath); err != nil {
		t.Fatalf("remove short path error = %v", err)
	}
	if err := os.WriteFile(legacyPath, original, 0o644); err != nil {
		t.Fatalf("write legacy path error = %v", err)
	}
	realRemove := p.fs.remove
	failed := false
	p.fs.remove = func(path string) error {
		if !failed && path == legacyPath {
			failed = true
			return errors.New("injected legacy cleanup failure")
		}
		return realRemove(path)
	}
	_, err = p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
		ShortID: before.ShortID, ExpectedUpdatedAt: before.UpdatedAt, Status: core.OptionalStatus{Set: true, Value: core.StatusInProgress},
	}}})
	if err == nil || !strings.Contains(err.Error(), "injected legacy cleanup failure") {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("read restored legacy path error = %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored legacy bytes = %q, want %q", got, original)
	}
	if _, err := os.Stat(shortPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("short path after rollback = %v, want absent", err)
	}
	if _, err := os.Stat(p.taskPath(core.StatusInProgress, before.ShortID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new status path after rollback = %v, want absent", err)
	}
}

func TestBatchReaderWaitsForGlobalPublicationLock(t *testing.T) {
	p, before := newInternalProviderWithTask(t)
	oldPath := p.taskPath(before.Status, before.ShortID)
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	realRemove := p.fs.remove
	var once sync.Once
	p.fs.remove = func(path string) error {
		if path == oldPath {
			once.Do(func() { close(cleanupStarted) })
			<-allowCleanup
		}
		return realRemove(path)
	}
	batchDone := make(chan error, 1)
	go func() {
		_, err := p.BatchUpdate(provider.BatchUpdateRequest{Tasks: []core.BatchTaskUpdateInput{{
			ShortID: before.ShortID, ExpectedUpdatedAt: before.UpdatedAt, Status: core.OptionalStatus{Set: true, Value: core.StatusInProgress},
		}}})
		batchDone <- err
	}()
	select {
	case <-cleanupStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not reach status cleanup")
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := p.GetTask(before.ShortID, "")
		readDone <- err
	}()
	select {
	case err := <-readDone:
		t.Fatalf("reader returned while batch was publishing: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(allowCleanup)
	if err := <-batchDone; err != nil {
		t.Fatalf("BatchUpdate() error = %v", err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("reader after batch error = %v", err)
	}
}

func snapshotStatusFiles(t *testing.T, p *Provider) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	for _, status := range p.statuses() {
		entries, err := os.ReadDir(p.statusDir(status))
		if err != nil {
			t.Fatalf("ReadDir(%s) error = %v", status, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(p.statusDir(status), entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", path, err)
			}
			files[path] = append([]byte(nil), data...)
		}
	}
	return files
}

func assertStatusFilesEqual(t *testing.T, p *Provider, want map[string][]byte) {
	t.Helper()
	got := snapshotStatusFiles(t, p)
	if !slices.EqualFunc(mapsKeys(got), mapsKeys(want), func(left, right string) bool { return left == right }) {
		t.Fatalf("status file paths = %v, want %v", mapsKeys(got), mapsKeys(want))
	}
	for path, expected := range want {
		if string(got[path]) != string(expected) {
			t.Fatalf("status file %s = %q, want %q", path, got[path], expected)
		}
	}
}

func mapsKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
