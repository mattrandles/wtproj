package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

func TestBatchCLIProcessJSONRoundTripSelectionAndLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "json batch repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	first := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "First", "--description", "preserve me", "--lane", "keep-lane", "--model", "model-a")
	dependency := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Dependency")
	target := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Target", "--depends-on", dependency.ShortID)

	allPath := filepath.Join(root, "all.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", allPath); err != nil {
		t.Fatalf("batch export all: %v\n%s", err, output)
	}
	allBefore, err := os.ReadFile(allPath)
	if err != nil {
		t.Fatalf("ReadFile(all export): %v", err)
	}
	var allDocument struct {
		Tasks []struct {
			ID           string   `json:"id"`
			ShortID      string   `json:"shortId"`
			Dependencies []string `json:"dependencies"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(allBefore, &allDocument); err != nil {
		t.Fatalf("decode all batch export: %v", err)
	}
	if got, want := len(allDocument.Tasks), 3; got != want {
		t.Fatalf("all export task count = %d, want %d", got, want)
	}
	for index, want := range []string{first.ShortID, dependency.ShortID, target.ShortID} {
		if allDocument.Tasks[index].ShortID != want {
			t.Fatalf("all export order[%d] = %q, want %q", index, allDocument.Tasks[index].ShortID, want)
		}
	}
	if got := allDocument.Tasks[2].Dependencies; !slices.Equal(got, []string{dependency.ShortID}) {
		t.Fatalf("exported dependency identifiers = %v, want short ID %s", got, dependency.ShortID)
	}

	allRepeat := filepath.Join(root, "all-repeat.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", allRepeat); err != nil {
		t.Fatalf("repeat batch export all: %v\n%s", err, output)
	}
	allAfter, err := os.ReadFile(allRepeat)
	if err != nil {
		t.Fatalf("ReadFile(repeated export): %v", err)
	}
	if !bytes.Equal(allBefore, allAfter) {
		t.Fatalf("repeated all export changed bytes:\nfirst %q\nsecond %q", allBefore, allAfter)
	}

	selectedPath := filepath.Join(root, "selected.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", selectedPath, "--task", target.ID, "--task", first.ShortID); err != nil {
		t.Fatalf("batch export explicit IDs: %v\n%s", err, output)
	}
	selected := decodeBatchTaskRows(t, selectedPath)
	if got, want := batchRowShortIDs(selected), []string{target.ShortID, first.ShortID}; !slices.Equal(got, want) {
		t.Fatalf("explicit selection order = %v, want %v", got, want)
	}

	// A JSON null clears a field while an omitted field preserves it. The
	// import is intentionally process-level; the codec tests own syntax.
	firstEdit := batchRow(first, map[string]any{
		"title":       "First edited",
		"description": nil,
	})
	targetUnchanged := batchRow(target, map[string]any{"title": target.Title})
	editPath := filepath.Join(root, "edit.json")
	writeBatchDocument(t, editPath, firstEdit, targetUnchanged)
	result := runBatchImportJSON(t, root, editPath)
	if got, want := batchResultIDs(result.Updated), []string{first.ID}; !slices.Equal(got, want) {
		t.Fatalf("updated result IDs = %v, want %v", got, want)
	}
	if got, want := batchResultIDs(result.Unchanged), []string{target.ID}; !slices.Equal(got, want) {
		t.Fatalf("unchanged result IDs = %v, want %v", got, want)
	}
	firstAfter := runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	if firstAfter.Title != "First edited" {
		t.Fatalf("first after JSON null = %#v", firstAfter.Task)
	}
	if firstAfter.Description != "" || firstAfter.Lane != "keep-lane" {
		t.Fatalf("JSON null/omission semantics = description %q lane %q", firstAfter.Description, firstAfter.Lane)
	}

	third := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Canonical dependency target")
	thirdPath := filepath.Join(root, "dependency-import.json")
	writeBatchDocument(t, thirdPath, batchRow(third, map[string]any{"dependencies": []string{dependency.ShortID}}))
	thirdResult := runBatchImportJSON(t, root, thirdPath)
	if got, want := batchResultIDs(thirdResult.Updated), []string{third.ID}; !slices.Equal(got, want) {
		t.Fatalf("dependency import updates = %v, want %v", got, want)
	}
	thirdAfter := runCLIJSONTask(t, root, "--json", "task", "show", third.ShortID)
	if !slices.Equal(thirdAfter.Dependencies, []string{dependency.ID}) {
		t.Fatalf("resolved dependency = %v, want canonical UUID %s", thirdAfter.Dependencies, dependency.ID)
	}
	storedThird := readStoredBatchTask(t, root, thirdAfter)
	if !slices.Equal(storedThird.Dependencies, []string{dependency.ID}) {
		t.Fatalf("stored dependency = %v, want canonical UUID %s", storedThird.Dependencies, dependency.ID)
	}

	// Lifecycle transitions are exercised through import, including the
	// provider's status-directory move and generated timestamps.
	firstForStart := runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	writeBatchDocument(t, filepath.Join(root, "start.json"), batchRow(firstForStart, map[string]any{"status": "inProgress"}))
	runBatchImportJSON(t, root, filepath.Join(root, "start.json"))
	started := runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	if started.Status != core.StatusInProgress || started.StartedAt == nil || started.CompletedAt != nil {
		t.Fatalf("started task lifecycle = %#v", started.Task)
	}
	writeBatchDocument(t, filepath.Join(root, "done.json"), batchRow(started, map[string]any{"status": "done"}))
	runBatchImportJSON(t, root, filepath.Join(root, "done.json"))
	completed := runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	if completed.Status != core.StatusDone || completed.StartedAt == nil || completed.CompletedAt == nil {
		t.Fatalf("completed task lifecycle = %#v", completed.Task)
	}

	// Re-exporting current tokens and importing that file produces only
	// unchanged results, proving the result split across processes.
	currentPath := filepath.Join(root, "current.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", currentPath); err != nil {
		t.Fatalf("current batch export: %v\n%s", err, output)
	}
	currentResult := runBatchImportJSON(t, root, currentPath)
	if len(currentResult.Updated) != 0 || len(currentResult.Unchanged) != 4 {
		t.Fatalf("current import result = %#v, want four unchanged tasks", currentResult)
	}

	// JSON stdout is the raw editable document, while file export with
	// --json remains a metadata response.
	stdoutExport, err := runCLIProcess(root, "batch", "export", "--out", "-", "--format", "json")
	if err != nil {
		t.Fatalf("batch export stdout: %v\n%s", err, stdoutExport)
	}
	var stdoutDocument struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(stdoutExport), &stdoutDocument); err != nil || len(stdoutDocument.Tasks) != 4 {
		t.Fatalf("raw JSON stdout = %q, error = %v", stdoutExport, err)
	}
	rejectedOutput, rejectedErr := runCLIProcess(root, "--json", "batch", "export", "--out", "-", "--format", "json")
	if rejectedErr == nil || !strings.Contains(rejectedOutput, "cannot be combined with --json") {
		t.Fatalf("JSON summary with raw stdout error = %v output = %q", rejectedErr, rejectedOutput)
	}
}

func TestBatchCLIProcessCSVFormatsAndStdin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "csv batch repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfigFile(t, root, `{"additionalStatuses":[{"name":"waitingForReview","category":"waiting"}]}`)

	task := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "CSV task", "--description", "old description", "--lane", "old lane", "--status", "waitingForReview")
	statusPath := filepath.Join(root, "waiting.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", statusPath, "--status", "waitingForReview"); err != nil {
		t.Fatalf("configured status export: %v\n%s", err, output)
	}
	statusRows := decodeBatchTaskRows(t, statusPath)
	if got, want := batchRowShortIDs(statusRows), []string{task.ShortID}; !slices.Equal(got, want) {
		t.Fatalf("configured status selection = %v, want %v", got, want)
	}

	// Explicit format overrides the .json suffix and is then used again on
	// import, covering both direction's format resolution.
	overridePath := filepath.Join(root, "override.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", overridePath, "--format", "csv", "--task", task.ID); err != nil {
		t.Fatalf("CSV format override export: %v\n%s", err, output)
	}
	overrideData, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("ReadFile(format override): %v", err)
	}
	if !bytes.Contains(overrideData, []byte("id,shortId,updatedAt")) {
		t.Fatalf("format override did not write CSV: %q", overrideData)
	}

	// Round-trip an edited CRLF+BOM document with a quoted multiline value and
	// an explicit _clear operation. Standard encoding/csv only prepares the
	// process input; parser behavior remains covered by batchcsv tests.
	records := readCSVRecords(t, overrideData)
	header := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		header[name] = index
	}
	record := records[1]
	record[header["description"]] = "line one\nline two"
	record[header["lane"]] = ""
	record[header["_clear"]] = "lane"
	editedCSV := encodeCRLFBOMCSV(t, records[0], record)
	editedPath := filepath.Join(root, "edited.csv")
	if err := os.WriteFile(editedPath, editedCSV, 0o644); err != nil {
		t.Fatalf("WriteFile(edited CSV): %v", err)
	}
	result := runBatchImportJSONWithArgs(t, root, editedPath, "--format", "csv")
	if got, want := batchResultIDs(result.Updated), []string{task.ID}; !slices.Equal(got, want) {
		t.Fatalf("CSV updated result = %v, want %v", got, want)
	}
	updated := runCLIJSONTask(t, root, "--json", "task", "show", task.ID)
	if updated.Description != "line one\nline two" || updated.Lane != "" || updated.Status != "waitingForReview" {
		t.Fatalf("CSV edit result = %#v", updated.Task)
	}

	stdoutCSV, err := runCLIProcess(root, "batch", "export", "--out", "-", "--format", "csv", "--status", "waitingForReview")
	if err != nil {
		t.Fatalf("CSV stdout export: %v\n%s", err, stdoutCSV)
	}
	if !strings.Contains(stdoutCSV, "id,shortId,updatedAt") || !strings.Contains(stdoutCSV, "line one") {
		t.Fatalf("raw CSV stdout = %q", stdoutCSV)
	}
	stdinResult := runBatchImportJSONWithInput(t, root, []byte(stdoutCSV), "--in", "-", "--format", "csv")
	if len(stdinResult.Updated) != 0 || len(stdinResult.Unchanged) != 1 {
		t.Fatalf("CSV stdin result = %#v, want unchanged task", stdinResult)
	}
}

func TestBatchCLIProcessReusableAssignmentsAcrossJSONCSVAndCatalogChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "reusable batch repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	checks := runCLIJSONReusableDefinition(t, root, "--json", "reusable", "create", "--name", "Checks", "--title", "Run checks", "--instructions", "run focused checks")
	review := runCLIJSONReusableDefinition(t, root, "--json", "reusable", "create", "--name", "Review", "--title", "Review work", "--instructions", "review the result")
	transient := runCLIJSONReusableDefinition(t, root, "--json", "reusable", "create", "--name", "Transient", "--title", "Temporary", "--instructions", "temporary definition")

	first := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "First", "--reusable", review.Name, "--reusable", checks.ID)
	second := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Second")

	exportPath := filepath.Join(root, "reusable-export.json")
	if output, err := runCLIProcess(root, "batch", "export", "--out", exportPath, "--task", first.ShortID, "--task", second.ID); err != nil {
		t.Fatalf("batch export reusable tasks: %v\n%s", err, output)
	}
	var exported struct {
		Tasks []struct {
			ShortID       string   `json:"shortId"`
			ReusableTasks []string `json:"reusableTasks"`
		} `json:"tasks"`
	}
	exportedData, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("ReadFile(reusable export) error = %v", err)
	}
	if err := json.Unmarshal(exportedData, &exported); err != nil {
		t.Fatalf("decode reusable export: %v", err)
	}
	if len(exported.Tasks) != 2 || exported.Tasks[0].ShortID != first.ShortID || !slices.Equal(exported.Tasks[0].ReusableTasks, []string{review.ID, checks.ID}) || exported.Tasks[1].ReusableTasks != nil {
		t.Fatalf("reusable export rows = %#v", exported.Tasks)
	}

	// Names are resolved against the catalog at import time, while exported
	// UUIDs remain stable across a definition rename.
	renamed := runCLIJSONReusableDefinition(t, root, "--json", "reusable", "update", checks.ID, "--name", "Checks renamed")
	first = runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	second = runCLIJSONTask(t, root, "--json", "task", "show", second.ID)
	jsonPath := filepath.Join(root, "reusable-edit.json")
	writeBatchDocument(t, jsonPath,
		batchRow(first, map[string]any{"title": "First JSON edit", "reusableTasks": []string{renamed.Name, review.ID}}),
		batchRow(second, map[string]any{"title": "Second JSON edit", "reusableTasks": []string{review.Name}}),
	)
	jsonResult := runBatchImportJSON(t, root, jsonPath)
	if got, want := batchResultIDs(jsonResult.Updated), []string{first.ID, second.ID}; !slices.Equal(got, want) {
		t.Fatalf("JSON reusable updates = %v, want %v", got, want)
	}
	if !slices.Equal(jsonResult.Updated[0].ReusableTaskIDs, []string{checks.ID, review.ID}) || jsonResult.Updated[0].ReusableTasks[0].Name != renamed.Name {
		t.Fatalf("JSON reusable response = %#v", jsonResult.Updated[0])
	}

	// Omitting reusableTasks preserves assignments. CSV then replaces them with
	// canonical UUIDs, and its explicit clear form reports a human result.
	first = runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	preservePath := filepath.Join(root, "reusable-preserve.json")
	writeBatchDocument(t, preservePath, batchRow(first, map[string]any{"description": "preserved assignments"}))
	preserve := runBatchImportJSON(t, root, preservePath)
	if len(preserve.Updated) != 1 || !slices.Equal(preserve.Updated[0].ReusableTaskIDs, []string{checks.ID, review.ID}) {
		t.Fatalf("omitted reusableTasks did not preserve assignments: %#v", preserve)
	}

	csvPath := filepath.Join(root, "reusable.csv")
	if output, err := runCLIProcess(root, "batch", "export", "--out", csvPath, "--format", "csv", "--task", first.ID); err != nil {
		t.Fatalf("CSV reusable export: %v\n%s", err, output)
	}
	records := readCSVRecords(t, mustReadBatchFile(t, csvPath))
	positions := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		positions[name] = index
	}
	records[1][positions["reusableTasks"]] = review.ID
	records[1][positions["title"]] = "First CSV edit"
	if err := os.WriteFile(csvPath, encodeCSVRecords(t, records), 0o644); err != nil {
		t.Fatalf("WriteFile(CSV replacement) error = %v", err)
	}
	csvResult := runBatchImportJSONWithArgs(t, root, csvPath, "--format", "csv")
	if len(csvResult.Updated) != 1 || !slices.Equal(csvResult.Updated[0].ReusableTaskIDs, []string{review.ID}) {
		t.Fatalf("CSV reusable replacement = %#v", csvResult)
	}

	first = runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	clearPath := filepath.Join(root, "reusable-clear.csv")
	if output, err := runCLIProcess(root, "batch", "export", "--out", clearPath, "--format", "csv", "--task", first.ID); err != nil {
		t.Fatalf("CSV reusable clear export: %v\n%s", err, output)
	}
	records = readCSVRecords(t, mustReadBatchFile(t, clearPath))
	positions = make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		positions[name] = index
	}
	records[1][positions["reusableTasks"]] = ""
	records[1][positions["_clear"]] = "reusableTasks"
	if err := os.WriteFile(clearPath, encodeCSVRecords(t, records), 0o644); err != nil {
		t.Fatalf("WriteFile(CSV clear) error = %v", err)
	}
	output, err := runCLIProcess(root, "batch", "import", "--in", clearPath, "--format", "csv")
	if err != nil || output != "updated: 1\nunchanged: 0\n" {
		t.Fatalf("human CSV reusable clear = %q, error = %v", output, err)
	}
	cleared := runCLIJSONTask(t, root, "--json", "task", "show", first.ID)
	if len(cleared.ReusableTaskIDs) != 0 || len(cleared.ReusableTasks) != 0 {
		t.Fatalf("CSV reusable clear task = %#v", cleared)
	}

	// A definition deleted after export is not silently re-created or resolved
	// from stale names/IDs. The whole batch stays unpublished.
	second = runCLIJSONTask(t, root, "--json", "task", "show", second.ID)
	deletePath := filepath.Join(root, "reusable-deleted-definition.json")
	writeBatchDocument(t, deletePath, batchRow(second, map[string]any{"title": "must not publish", "reusableTasks": []string{transient.ID}}))
	if output, err := runCLIProcess(root, "--json", "reusable", "delete", transient.ID); err != nil {
		t.Fatalf("delete reusable definition: %v\n%s", err, output)
	}
	output, err = runCLIProcess(root, "batch", "import", "--in", deletePath)
	if err == nil || !strings.Contains(output, "not found") {
		t.Fatalf("deleted reusable import error = %v output = %q", err, output)
	}
	secondAfter := runCLIJSONTask(t, root, "--json", "task", "show", second.ID)
	if secondAfter.Title != second.Title || !slices.Equal(secondAfter.ReusableTaskIDs, []string{review.ID}) {
		t.Fatalf("deleted reusable import published task = %#v", secondAfter)
	}
}

func TestBatchCLIProcessRejectsInvalidBatchesWithoutMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "invalid batch repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	a := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "A")
	b := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "B")
	c := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "C")
	baseline := storageManifest(t, filepath.Join(root, ".wtp"))

	cases := []struct {
		name string
		data []byte
		want string
	}{
		{name: "malformed", data: []byte(`{"version":`), want: "EOF"},
		{name: "duplicate", data: batchDocument(batchRow(a, map[string]any{"title": "one"}), batchRow(a, map[string]any{"title": "two"})), want: "duplicate"},
		{name: "stale updatedAt", data: batchDocument(batchRowWithUpdatedAt(a, a.UpdatedAt.Add(-time.Second), map[string]any{"title": "stale"})), want: "stale task update"},
		{name: "invalid transition", data: batchDocument(batchRow(a, map[string]any{"status": "done"})), want: "invalid status transition"},
		{name: "missing dependency", data: batchDocument(batchRow(c, map[string]any{"dependencies": []string{"wtp-9999"}})), want: "not found"},
		{name: "cyclic dependency", data: batchDocument(batchRow(a, map[string]any{"dependencies": []string{b.ShortID}}), batchRow(b, map[string]any{"dependencies": []string{a.ShortID}})), want: "cyclic dependency"},
		{name: "mixed validity", data: batchDocument(batchRow(c, map[string]any{"title": "must not publish"}), batchRow(b, map[string]any{"dependencies": []string{"wtp-9999"}})), want: "not found"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, "invalid-"+strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(path, test.data, 0o644); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}
			output, err := runCLIProcess(root, "batch", "import", "--in", path)
			if err == nil || !strings.Contains(output, test.want) {
				t.Fatalf("invalid import error = %v output = %q, want %q", err, output, test.want)
			}
			if got := storageManifest(t, filepath.Join(root, ".wtp")); got != baseline {
				t.Fatalf("invalid %s changed store:\nbefore %s\nafter %s", test.name, baseline, got)
			}
		})
	}
}

func TestBatchCLIProcessPreservesCanonicalAndLegacyExportContracts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "export contract repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	task := runCLIJSONTask(t, root, "--json", "task", "create", "--title", "Export contract task")
	planningOutput, err := runCLIProcess(root, "--json", "planning", "create", "--title", "Export contract planning", "--status", "planned")
	if err != nil {
		t.Fatalf("planning create: %v\n%s", err, planningOutput)
	}
	var planning core.PlanningItemView
	if err := json.Unmarshal([]byte(planningOutput), &planning); err != nil {
		t.Fatalf("decode planning create: %v\n%s", err, planningOutput)
	}
	runCLIJSONHandoff(t, root, "--json", "handoff", "write", "--agent", "Test", "--message", "retained export context")

	canonicalDir := filepath.Join(root, "canonical export")
	legacyDir := filepath.Join(root, "legacy export")
	if output, err := runCLIProcess(root, "export", "--out", canonicalDir); err != nil {
		t.Fatalf("canonical root export: %v\n%s", err, output)
	}
	if output, err := runCLIProcess(root, "--export-tasks="+legacyDir); err != nil {
		t.Fatalf("legacy export alias: %v\n%s", err, output)
	}
	wantEntries := []string{task.ID + ".json", "handoffs.json", "planning", "reusable.json"}
	for _, directory := range []string{canonicalDir, legacyDir} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", directory, err)
		}
		if got := entryNames(entries); !slices.Equal(got, wantEntries) {
			t.Fatalf("%s entries = %v, want %v", directory, got, wantEntries)
		}
	}
	for _, name := range []string{task.ID + ".json", "handoffs.json", "reusable.json"} {
		canonical, err := os.ReadFile(filepath.Join(canonicalDir, name))
		if err != nil {
			t.Fatalf("ReadFile(canonical %s): %v", name, err)
		}
		legacy, err := os.ReadFile(filepath.Join(legacyDir, name))
		if err != nil {
			t.Fatalf("ReadFile(legacy %s): %v", name, err)
		}
		if !bytes.Equal(canonical, legacy) {
			t.Fatalf("export contract changed for %s:\ncanonical %q\nlegacy %q", name, canonical, legacy)
		}
	}
	canonicalPlanning, err := os.ReadFile(filepath.Join(canonicalDir, "planning", planning.ID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(canonical planning): %v", err)
	}
	legacyPlanning, err := os.ReadFile(filepath.Join(legacyDir, "planning", planning.ID+".json"))
	if err != nil {
		t.Fatalf("ReadFile(legacy planning): %v", err)
	}
	if !bytes.Equal(canonicalPlanning, legacyPlanning) {
		t.Fatalf("export contract changed for planning record:\ncanonical %q\nlegacy %q", canonicalPlanning, legacyPlanning)
	}
}

func batchRow(task core.TaskView, fields map[string]any) map[string]any {
	return batchRowWithUpdatedAt(task, task.UpdatedAt, fields)
}

func batchRowWithUpdatedAt(task core.TaskView, updatedAt time.Time, fields map[string]any) map[string]any {
	row := map[string]any{
		"id":        task.ID,
		"shortId":   task.ShortID,
		"updatedAt": updatedAt.UTC().Format(time.RFC3339Nano),
	}
	for key, value := range fields {
		row[key] = value
	}
	return row
}

func batchDocument(rows ...map[string]any) []byte {
	document := map[string]any{"version": 1, "tasks": rows}
	data, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return data
}

func writeBatchDocument(t *testing.T, path string, rows ...map[string]any) {
	t.Helper()
	if err := os.WriteFile(path, batchDocument(rows...), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func runCLIJSONReusableDefinition(t *testing.T, root string, args ...string) core.ReusableTaskDefinition {
	t.Helper()
	output, err := runCLIProcess(root, args...)
	if err != nil {
		t.Fatalf("wtp %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var definition core.ReusableTaskDefinition
	if err := json.Unmarshal([]byte(output), &definition); err != nil {
		t.Fatalf("decode reusable output %q: %v", output, err)
	}
	return definition
}

func mustReadBatchFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return data
}

func encodeCSVRecords(t *testing.T, records [][]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.WriteAll(records); err != nil {
		t.Fatalf("encode CSV records: %v", err)
	}
	return output.Bytes()
}

func runBatchImportJSON(t *testing.T, root, path string) struct {
	Updated   []core.TaskView `json:"updated"`
	Unchanged []core.TaskView `json:"unchanged"`
} {
	t.Helper()
	return runBatchImportJSONWithArgs(t, root, path)
}

func runBatchImportJSONWithArgs(t *testing.T, root, path string, extra ...string) struct {
	Updated   []core.TaskView `json:"updated"`
	Unchanged []core.TaskView `json:"unchanged"`
} {
	t.Helper()
	args := append([]string{"--json", "batch", "import", "--in", path}, extra...)
	output, err := runCLIProcess(root, args...)
	if err != nil {
		t.Fatalf("batch import %s: %v\n%s", path, err, output)
	}
	var result struct {
		Updated   []core.TaskView `json:"updated"`
		Unchanged []core.TaskView `json:"unchanged"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode batch import output %q: %v", output, err)
	}
	return result
}

func runBatchImportJSONWithInput(t *testing.T, root string, input []byte, args ...string) struct {
	Updated   []core.TaskView `json:"updated"`
	Unchanged []core.TaskView `json:"unchanged"`
} {
	t.Helper()
	commandArgs := append([]string{"--json", "batch", "import"}, args...)
	output, err := runCLIProcessWithInput(root, input, commandArgs...)
	if err != nil {
		t.Fatalf("batch import stdin: %v\n%s", err, output)
	}
	var result struct {
		Updated   []core.TaskView `json:"updated"`
		Unchanged []core.TaskView `json:"unchanged"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode batch stdin output %q: %v", output, err)
	}
	return result
}

func decodeBatchTaskRows(t *testing.T, path string) []struct {
	ID           string   `json:"id"`
	ShortID      string   `json:"shortId"`
	Dependencies []string `json:"dependencies"`
} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var document struct {
		Tasks []struct {
			ID           string   `json:"id"`
			ShortID      string   `json:"shortId"`
			Dependencies []string `json:"dependencies"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode batch rows %s: %v", path, err)
	}
	return document.Tasks
}

func batchRowShortIDs(rows []struct {
	ID           string   `json:"id"`
	ShortID      string   `json:"shortId"`
	Dependencies []string `json:"dependencies"`
}) []string {
	ids := make([]string, len(rows))
	for index, row := range rows {
		ids[index] = row.ShortID
	}
	return ids
}

func batchResultIDs(tasks []core.TaskView) []string {
	ids := make([]string, len(tasks))
	for index, task := range tasks {
		ids[index] = task.ID
	}
	return ids
}

func readStoredBatchTask(t *testing.T, root string, task core.TaskView) core.Task {
	t.Helper()
	path := filepath.Join(root, ".wtp", string(task.Status), task.ShortID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(stored task): %v", err)
	}
	var stored core.Task
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode stored task: %v", err)
	}
	return stored
}

func readCSVRecords(t *testing.T, data []byte) [][]string {
	t.Helper()
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	reader := csv.NewReader(bytes.NewReader(data))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("decode CSV records: %v", err)
	}
	return records
}

func encodeCRLFBOMCSV(t *testing.T, header, record []string) []byte {
	t.Helper()
	var output bytes.Buffer
	output.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(&output)
	writer.UseCRLF = true
	if err := writer.Write(header); err != nil {
		t.Fatalf("write CSV header: %v", err)
	}
	if err := writer.Write(record); err != nil {
		t.Fatalf("write CSV record: %v", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatalf("flush CSV: %v", err)
	}
	return output.Bytes()
}

func runCLIProcessWithInput(dir string, input []byte, args ...string) (string, error) {
	commandArgs := append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)
	command := exec.Command(os.Args[0], commandArgs...)
	command.Dir = dir
	command.Env = append(os.Environ(), "WTP_CLI_PROCESS=1")
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	return string(output), err
}
