package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/reusablejson"
)

func TestReusableTasksEmptyCatalogReadDoesNotCreateFile(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	definitions, err := p.ListReusableTasks()
	if err != nil {
		t.Fatalf("ListReusableTasks() error = %v", err)
	}
	if definitions == nil || len(definitions) != 0 {
		t.Fatalf("ListReusableTasks() = %#v, want non-nil empty slice", definitions)
	}
	if _, err := p.GetReusableTask("unknown"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("GetReusableTask(unknown) error = %v, want not found", err)
	}
	if _, err := os.Stat(p.reusableTaskCatalogPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only reusable calls created catalog: stat error = %v", err)
	}
}

func TestReusableTasksListAndGetUseStableSelectorsAndOrdering(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	zeta, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Zeta", Title: "Z title", Instructions: "Z instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask(Zeta) error = %v", err)
	}
	alpha, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "alpha", Title: "A title", Instructions: "A instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask(alpha) error = %v", err)
	}

	definitions, err := p.ListReusableTasks()
	if err != nil {
		t.Fatalf("ListReusableTasks() error = %v", err)
	}
	if got, want := reusableNames(definitions), []string{"alpha", "Zeta"}; !slices.Equal(got, want) {
		t.Fatalf("ListReusableTasks() names = %v, want %v", got, want)
	}
	byName, err := p.GetReusableTask("  ALPHA  ")
	if err != nil {
		t.Fatalf("GetReusableTask(name) error = %v", err)
	}
	if byName != alpha {
		t.Fatalf("GetReusableTask(name) = %#v, want %#v", byName, alpha)
	}
	byID, err := p.GetReusableTask(zeta.ID)
	if err != nil {
		t.Fatalf("GetReusableTask(id) error = %v", err)
	}
	if byID != zeta {
		t.Fatalf("GetReusableTask(id) = %#v, want %#v", byID, zeta)
	}
}

func TestCreateReusableTaskRejectsMixedCaseNamesAndKeepsExistingCatalog(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Tests", Title: "First", Instructions: "First instructions"}); err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	before, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}
	if _, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "  tEsTs  ", Title: "Second", Instructions: "Second instructions"}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("CreateReusableTask(mixed case duplicate) error = %v, want duplicate", err)
	}
	after, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(after) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("catalog changed after rejected create:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestCreateReusableTaskRegeneratesCollidingUUIDAndAdvancesCatalogTime(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Nanosecond)
	existing := reusableDefinitionForTest("7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "Existing", future)
	writeReusableCatalogForTest(t, p, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{existing}})

	originalGenerator := generateReusableTaskID
	t.Cleanup(func() { generateReusableTaskID = originalGenerator })
	calls := 0
	const freshID = "e5c3806a-bd1b-424d-889b-29e5b06679b8"
	generateReusableTaskID = func() (string, error) {
		calls++
		if calls == 1 {
			return existing.ID, nil
		}
		return freshID, nil
	}

	created, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Created", Title: "Created title", Instructions: "Created instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	if created.ID != freshID || calls != 2 {
		t.Fatalf("CreateReusableTask() ID/calls = %q/%d, want %q/2", created.ID, calls, freshID)
	}
	if !created.CreatedAt.After(existing.UpdatedAt) || created.UpdatedAt != created.CreatedAt {
		t.Fatalf("created timestamps = %s/%s, want equal and after %s", created.CreatedAt, created.UpdatedAt, existing.UpdatedAt)
	}
}

func TestCreateReusableTaskConcurrentNameCollisionPublishesOneDefinition(t *testing.T) {
	root := t.TempDir()
	const workers = 20
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			p, err := New(root, nil)
			if err == nil {
				_, err = p.CreateReusableTask(core.CreateReusableTaskInput{
					Name:         []string{"Shared", "sHaReD"}[index%2],
					Title:        fmt.Sprintf("title %d", index),
					Instructions: fmt.Sprintf("instructions %d", index),
				})
			}
			errs <- err
		}(index)
	}
	group.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !strings.Contains(err.Error(), "duplicated") {
			t.Fatalf("concurrent CreateReusableTask() error = %v, want duplicate", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent creates succeeded %d times, want 1", successes)
	}
	p, err := New(root, nil)
	if err != nil {
		t.Fatalf("New(reopen) error = %v", err)
	}
	definitions, err := p.ListReusableTasks()
	if err != nil {
		t.Fatalf("ListReusableTasks() error = %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("published definitions = %#v, want exactly one", definitions)
	}
}

func TestReusableTasksAreSharedAcrossBranchScopedProviders(t *testing.T) {
	root := t.TempDir()
	first, err := New(root, core.NewBranchScope("feature/first"))
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	second, err := New(root, core.NewBranchScope("feature/second"))
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	created, err := first.CreateReusableTask(core.CreateReusableTaskInput{Name: "Shared", Title: "Shared title", Instructions: "Shared instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	got, err := second.GetReusableTask(created.ID)
	if err != nil {
		t.Fatalf("second.GetReusableTask() error = %v", err)
	}
	if got != created {
		t.Fatalf("second.GetReusableTask() = %#v, want %#v", got, created)
	}
}

func TestReusableTaskReadAndWriteFailuresDoNotPublishPartialCatalog(t *testing.T) {
	t.Run("read failure", func(t *testing.T) {
		p, err := New(t.TempDir(), nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if err := os.Mkdir(p.reusableTaskCatalogPath(), 0o755); err != nil {
			t.Fatalf("os.Mkdir(catalog path) error = %v", err)
		}
		if _, err := p.ListReusableTasks(); err == nil || !strings.Contains(err.Error(), "read reusable catalog") {
			t.Fatalf("ListReusableTasks() error = %v, want read failure", err)
		}
	})

	t.Run("atomic replace failure", func(t *testing.T) {
		p, err := New(t.TempDir(), nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if _, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "First", Title: "First title", Instructions: "First instructions"}); err != nil {
			t.Fatalf("CreateReusableTask(first) error = %v", err)
		}
		before, err := os.ReadFile(p.reusableTaskCatalogPath())
		if err != nil {
			t.Fatalf("os.ReadFile(before) error = %v", err)
		}
		p.fs.replace = func(_, _ string) error { return errors.New("injected reusable replace failure") }
		if _, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Second", Title: "Second title", Instructions: "Second instructions"}); err == nil || !strings.Contains(err.Error(), "injected reusable replace failure") {
			t.Fatalf("CreateReusableTask(second) error = %v, want injected failure", err)
		}
		after, err := os.ReadFile(p.reusableTaskCatalogPath())
		if err != nil {
			t.Fatalf("os.ReadFile(after) error = %v", err)
		}
		if string(after) != string(before) {
			t.Fatalf("catalog changed after failed replacement:\nbefore: %s\nafter:  %s", before, after)
		}
	})
}

func TestUpdateReusableTaskRenamesWithoutBreakingUUIDReferences(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	created, err := p.CreateReusableTask(core.CreateReusableTaskInput{
		Name:         "Tests",
		Title:        "Run tests",
		Instructions: "Keep the UUID reference",
	})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}

	updated, err := p.UpdateReusableTask("tests", core.UpdateReusableTaskInput{Name: core.OptionalString{Set: true, Value: "Verification"}})
	if err != nil {
		t.Fatalf("UpdateReusableTask(rename) error = %v", err)
	}
	if updated.ID != created.ID || !updated.CreatedAt.Equal(created.CreatedAt) || updated.Name != "Verification" {
		t.Fatalf("renamed definition = %#v, want same ID/createdAt and new name", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("renamed updatedAt = %s, want after %s", updated.UpdatedAt, created.UpdatedAt)
	}

	catalog, err := reusablejson.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("reusablejson.ReadFile() error = %v", err)
	}
	resolved, err := core.ResolveReusableTasks([]string{created.ID}, catalog)
	if err != nil {
		t.Fatalf("ResolveReusableTasks() error = %v", err)
	}
	if len(resolved) != 1 || resolved[0].ID != created.ID || resolved[0].Name != "Verification" {
		t.Fatalf("resolved UUID reference = %#v, want renamed definition with stable ID", resolved)
	}
}

func TestUpdateReusableTaskSupportsPartialAndCasingOnlyEdits(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	created, err := p.CreateReusableTask(core.CreateReusableTaskInput{
		Name:         "Checks",
		Title:        "Original title",
		Instructions: "Original instructions",
	})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}

	titleOnly, err := p.UpdateReusableTask(created.ID, core.UpdateReusableTaskInput{Title: core.OptionalString{Set: true, Value: "  Updated title  "}})
	if err != nil {
		t.Fatalf("UpdateReusableTask(title) error = %v", err)
	}
	if titleOnly.Name != created.Name || titleOnly.Title != "Updated title" || titleOnly.Instructions != created.Instructions {
		t.Fatalf("partial title update = %#v, want only title changed", titleOnly)
	}
	if !titleOnly.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("partial title updatedAt = %s, want after %s", titleOnly.UpdatedAt, created.UpdatedAt)
	}

	casingOnly, err := p.UpdateReusableTask("checks", core.UpdateReusableTaskInput{Name: core.OptionalString{Set: true, Value: "cHeCkS"}})
	if err != nil {
		t.Fatalf("UpdateReusableTask(casing-only) error = %v", err)
	}
	if casingOnly.ID != created.ID || casingOnly.Name != "cHeCkS" || casingOnly.Title != titleOnly.Title || casingOnly.Instructions != created.Instructions {
		t.Fatalf("casing-only update = %#v, want same definition with updated casing", casingOnly)
	}
	if !casingOnly.UpdatedAt.After(titleOnly.UpdatedAt) {
		t.Fatalf("casing-only updatedAt = %s, want after %s", casingOnly.UpdatedAt, titleOnly.UpdatedAt)
	}
}

func TestUpdateReusableTaskNoOpReturnsUnchangedWithoutWriting(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	created, err := p.CreateReusableTask(core.CreateReusableTaskInput{
		Name:         "Checks",
		Title:        "Run checks",
		Instructions: "Keep the output deterministic",
	})
	if err != nil {
		t.Fatalf("CreateReusableTask() error = %v", err)
	}
	before, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}
	p.fs.replace = func(_, _ string) error { return errors.New("no-op must not replace") }

	got, err := p.UpdateReusableTask(created.ID, core.UpdateReusableTaskInput{
		Name:         core.OptionalString{Set: true, Value: "  Checks  "},
		Title:        core.OptionalString{Set: true, Value: "Run checks"},
		Instructions: core.OptionalString{Set: true, Value: "Keep the output deterministic"},
	})
	if err != nil {
		t.Fatalf("UpdateReusableTask(no-op) error = %v", err)
	}
	if got != created {
		t.Fatalf("UpdateReusableTask(no-op) = %#v, want %#v", got, created)
	}
	after, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(after) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("catalog changed after no-op:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestUpdateReusableTaskRejectsDuplicateNameAndEmptyInputAtomically(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Checks", Title: "First", Instructions: "First instructions"})
	if err != nil {
		t.Fatalf("CreateReusableTask(first) error = %v", err)
	}
	if _, err := p.CreateReusableTask(core.CreateReusableTaskInput{Name: "Review", Title: "Second", Instructions: "Second instructions"}); err != nil {
		t.Fatalf("CreateReusableTask(second) error = %v", err)
	}
	before, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(before) error = %v", err)
	}

	if _, err := p.UpdateReusableTask(first.ID, core.UpdateReusableTaskInput{Name: core.OptionalString{Set: true, Value: " review "}}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("UpdateReusableTask(duplicate) error = %v, want duplicate", err)
	}
	if _, err := p.UpdateReusableTask(first.ID, core.UpdateReusableTaskInput{}); err == nil || !strings.Contains(err.Error(), "at least one field") {
		t.Fatalf("UpdateReusableTask(empty) error = %v, want missing-field error", err)
	}
	after, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(after) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("catalog changed after rejected update:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestUpdateReusableTaskUsesMonotonicTimestampAndAtomicFailure(t *testing.T) {
	p, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Nanosecond)
	existing := reusableDefinitionForTest("7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6", "Checks", future)
	writeReusableCatalogForTest(t, p, core.ReusableTaskCatalog{Version: core.ReusableTaskCatalogVersion, Definitions: []core.ReusableTaskDefinition{existing}})

	updated, err := p.UpdateReusableTask(existing.ID, core.UpdateReusableTaskInput{Instructions: core.OptionalString{Set: true, Value: "Updated instructions"}})
	if err != nil {
		t.Fatalf("UpdateReusableTask(future timestamp) error = %v", err)
	}
	if !updated.UpdatedAt.After(existing.UpdatedAt) || !updated.UpdatedAt.After(future) {
		t.Fatalf("updatedAt = %s, want strictly after %s", updated.UpdatedAt, future)
	}

	before, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(before failure) error = %v", err)
	}
	p.fs.replace = func(_, _ string) error { return errors.New("injected reusable update replace failure") }
	if _, err := p.UpdateReusableTask(updated.ID, core.UpdateReusableTaskInput{Title: core.OptionalString{Set: true, Value: "unpublished"}}); err == nil || !strings.Contains(err.Error(), "injected reusable update replace failure") {
		t.Fatalf("UpdateReusableTask(failure) error = %v, want injected failure", err)
	}
	after, err := os.ReadFile(p.reusableTaskCatalogPath())
	if err != nil {
		t.Fatalf("os.ReadFile(after failure) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("catalog changed after failed update:\nbefore: %s\nafter:  %s", before, after)
	}
	still, err := p.GetReusableTask(updated.ID)
	if err != nil {
		t.Fatalf("GetReusableTask(after failure) error = %v", err)
	}
	if still != updated {
		t.Fatalf("definition after failed update = %#v, want %#v", still, updated)
	}
}

func reusableNames(definitions []core.ReusableTaskDefinition) []string {
	names := make([]string, len(definitions))
	for index, definition := range definitions {
		names[index] = definition.Name
	}
	return names
}

func reusableDefinitionForTest(id, name string, timestamp time.Time) core.ReusableTaskDefinition {
	return core.ReusableTaskDefinition{
		ID:           id,
		Name:         name,
		Title:        name + " title",
		Instructions: name + " instructions",
		CreatedAt:    timestamp,
		UpdatedAt:    timestamp,
	}
}

func writeReusableCatalogForTest(t *testing.T, p *Provider, catalog core.ReusableTaskCatalog) {
	t.Helper()
	data, err := reusablejson.Encode(catalog)
	if err != nil {
		t.Fatalf("reusablejson.Encode() error = %v", err)
	}
	if err := os.WriteFile(filepath.Clean(p.reusableTaskCatalogPath()), data, 0o644); err != nil {
		t.Fatalf("os.WriteFile(reusable catalog) error = %v", err)
	}
}
