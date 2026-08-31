package prerelease

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/stats"
)

// scenarioGroupingEndToEnd exercises grouping through the candidate process
// boundary. The unit and integration suites cover individual layers; this
// matrix keeps the public lifecycle, wire formats, branch scope, ownership,
// ordering, and claim transaction behavior connected in one disposable store.
func scenarioGroupingEndToEnd(r *scenarioRunner) error {
	project, err := r.newGitProject("grouping end-to-end project")
	if err != nil {
		return err
	}
	if err = runGit(project, "branch", "-M", "main"); err != nil {
		return err
	}
	r.setCWD(project)

	const (
		issueID   = "Issue-42"
		projectID = "Apollo"
		milestone = "MVP"
		version   = "v1"
		featureID = "FEAT-7"
		feature   = "Search"
		renamed   = "Search Renamed"
	)

	createArgs := func(title, display string, agent string) []string {
		args := []string{"task", "create", "--title", title, "--issue-id", issueID, "--project", projectID, "--milestone", milestone, "--version", version, "--feature-id", featureID, "--feature", display}
		if agent != "" {
			args = append(args, "--agent", agent)
		}
		return args
	}

	var grouped, claimA, claimB, assigned, clearable core.TaskView
	for _, item := range []struct {
		target *core.TaskView
		args   []string
	}{
		{target: &grouped, args: createArgs("grouped first", feature, "")},
		{target: &claimA, args: createArgs("grouped claim A", renamed, "")},
		{target: &claimB, args: createArgs("grouped claim B", renamed, "")},
		{target: &assigned, args: createArgs("assigned elsewhere", renamed, "Alice")},
		{target: &clearable, args: createArgs("clear every field", "Clear me", "")},
	} {
		if err = r.json(item.target, item.args...); err != nil {
			return err
		}
	}
	if grouped.IssueID != issueID || grouped.Project != projectID || grouped.Milestone != milestone || grouped.Version != version || grouped.FeatureID != featureID || grouped.Feature != feature {
		return fmt.Errorf("create grouping metadata mismatch: %#v", grouped.Task)
	}
	groupedID, groupedShortID := grouped.ID, grouped.ShortID
	r.assert("create persists all six grouping fields and captures process-returned IDs")

	if err = r.json(&grouped, "task", "edit", grouped.ShortID, "--feature", renamed); err != nil {
		return err
	}
	if grouped.FeatureID != featureID || grouped.Feature != renamed {
		return fmt.Errorf("edit changed stable feature key or display name: %#v", grouped.Task)
	}
	if err = r.json(&clearable, "task", "edit", clearable.ShortID, "--feature", "Clear me renamed"); err != nil {
		return err
	}
	if clearable.FeatureID != featureID || clearable.Feature != "Clear me renamed" {
		return fmt.Errorf("edit clearable task mismatch: %#v", clearable.Task)
	}
	clearableID := clearable.ShortID
	clearable = core.TaskView{}
	if err = r.json(&clearable, "task", "update", clearableID, "--issue-id=", "--project=", "--milestone=", "--version=", "--feature-id=", "--feature="); err != nil {
		return err
	}
	if clearable.IssueID != "" || clearable.Project != "" || clearable.Milestone != "" || clearable.Version != "" || clearable.FeatureID != "" || clearable.Feature != "" {
		return fmt.Errorf("explicit grouping clears left values behind: %#v", clearable.Task)
	}
	r.assert("edit preserves featureId during display rename and explicit empty assignments clear only requested metadata")

	// Create a task in a foreign branch with the same grouping scope. Ordinary
	// list/graph/export views may show it, but automatic ready/next may not.
	if err = runGit(project, "checkout", "-qb", "feature/foreign"); err != nil {
		return err
	}
	var foreign core.TaskView
	if err = r.json(&foreign, createArgs("foreign branch", renamed, "Foreign")...); err != nil {
		return err
	}
	if err = runGit(project, "checkout", "main"); err != nil {
		return err
	}
	r.setCWD(project)

	// Create an unscoped task while detached, then replace its short-ID file
	// with the old canonical UUID filename. The next candidate invocation must
	// migrate it without losing its grouping metadata.
	if err = runGit(project, "checkout", "--detach", "HEAD"); err != nil {
		return err
	}
	var legacy core.TaskView
	if err = r.json(&legacy, createArgs("legacy grouped file", "Legacy", "")...); err != nil {
		return err
	}
	legacyShortPath := filepath.Join(project, ".wtp", "todo", legacy.ShortID+".json")
	legacyUUIDPath := filepath.Join(project, ".wtp", "todo", legacy.ID+".json")
	if err = os.Rename(legacyShortPath, legacyUUIDPath); err != nil {
		return fmt.Errorf("prepare legacy UUID filename: %w", err)
	}
	if err = runGit(project, "checkout", "main"); err != nil {
		return err
	}
	if err = r.json(&legacy, "task", "show", legacy.ID); err != nil {
		return err
	}
	if legacy.FeatureID != featureID || legacy.Feature != "Legacy" {
		return fmt.Errorf("legacy filename migration lost grouping metadata: %#v", legacy.Task)
	}
	if _, statErr := os.Stat(legacyUUIDPath); !errors.Is(statErr, os.ErrNotExist) {
		return errors.New("legacy UUID filename was not migrated away")
	}
	if _, statErr := os.Stat(legacyShortPath); statErr != nil {
		return fmt.Errorf("migrated legacy short-ID filename missing: %w", statErr)
	}
	r.assert("legacy UUID-named grouped task migrates to its short-ID file without data loss")

	allScope := groupingScopeArgs(issueID, projectID, milestone, version, featureID, renamed)
	var listed []core.TaskView
	if err = r.json(&listed, append([]string{"task", "list"}, allScope...)...); err != nil {
		return err
	}
	if len(listed) != 5 || listed[0].ID != grouped.ID || listed[1].ID != claimA.ID || listed[2].ID != claimB.ID || listed[3].ID != assigned.ID || listed[4].ID != foreign.ID {
		return fmt.Errorf("mixed-case AND list/order = %#v, want current creation order plus foreign task", listed)
	}
	var unmatched []core.TaskView
	if err = r.json(&unmatched, "task", "list", "--feature", "FEAT-7"); err != nil {
		return err
	}
	if len(unmatched) != 0 {
		return fmt.Errorf("feature key/name fallback unexpectedly matched display filter: %#v", unmatched)
	}
	if err = r.json(&unmatched, "task", "list", "--feature-id", "does-not-exist"); err != nil {
		return err
	}
	if len(unmatched) != 0 {
		return fmt.Errorf("unmatched grouping filter returned tasks: %#v", unmatched)
	}
	r.assert("list applies trimmed case-insensitive exact AND matching, deterministic ordering, and empty unmatched results")

	var graph []graphEvidenceNode
	if err = r.json(&graph, append([]string{"graph", "--status", "all"}, allScope...)...); err != nil {
		return err
	}
	graphIDs := make(map[string]bool)
	for _, node := range graph {
		if node.Task != nil {
			graphIDs[node.Task.ID] = true
		}
	}
	for _, task := range []core.TaskView{grouped, claimA, claimB, assigned, foreign} {
		if !graphIDs[task.ID] {
			return fmt.Errorf("grouped graph omitted %s", task.ShortID)
		}
	}
	if err = r.json(&graph, "graph", "--feature-id", "does-not-exist"); err != nil {
		return err
	}
	if len(graph) != 0 {
		return fmt.Errorf("unmatched graph filter returned nodes: %#v", graph)
	}
	r.assert("graph applies all-field grouping filters and does not invent unmatched nodes")

	var overview stats.Report
	if err = r.json(&overview, append([]string{"stats"}, allScope...)...); err != nil {
		return err
	}
	if overview.TotalTasks != 5 {
		return fmt.Errorf("grouped stats total = %d, want 5", overview.TotalTasks)
	}
	var focused stats.FocusedReport
	if err = r.json(&focused, append(append([]string{"stats"}, allScope...), "model")...); err != nil {
		return err
	}
	if focused.TotalTasks != 5 || focused.Attribute != stats.AttributeModel {
		return fmt.Errorf("grouped focused stats = %#v", focused)
	}
	if err = r.json(&overview, "stats", "--feature-id", "does-not-exist"); err != nil {
		return err
	}
	if overview.TotalTasks != 0 {
		return fmt.Errorf("unmatched stats total = %d, want 0", overview.TotalTasks)
	}
	r.assert("stats scopes overview and focused aggregation with the same six-field AND filter")

	// Export all grouping fields, then import JSON v1 with omission preservation,
	// explicit null clearing, and a display-name rename that keeps featureId.
	jsonPath := filepath.Join(r.root, "grouping-v1.json")
	if _, err = r.command(append([]string{"batch", "export", "--out", jsonPath}, allScope...)...); err != nil {
		return err
	}
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}
	var exported struct {
		Version int                          `json:"version"`
		Tasks   []map[string]json.RawMessage `json:"tasks"`
	}
	if err = json.Unmarshal(jsonData, &exported); err != nil {
		return fmt.Errorf("decode grouped JSON export: %w", err)
	}
	if exported.Version != 1 || len(exported.Tasks) != 5 {
		return fmt.Errorf("grouped JSON export = %#v, want version 1 and five rows", exported)
	}
	for index, row := range exported.Tasks {
		for key, want := range map[string]string{"issueId": issueID, "project": projectID, "milestone": milestone, "version": version, "featureId": featureID} {
			if got := groupingRawString(row, key); got != want {
				return fmt.Errorf("JSON export row %d %s = %q, want %q", index+1, key, got, want)
			}
		}
	}
	row := map[string]any{"id": groupedID, "shortId": groupedShortID, "updatedAt": grouped.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), "issueId": nil, "feature": "Search Batch"}
	batchData, err := json.Marshal(map[string]any{"version": 1, "tasks": []map[string]any{row}})
	if err != nil {
		return err
	}
	jsonImportPath := filepath.Join(r.root, "grouping-v1-edit.json")
	if err = os.WriteFile(jsonImportPath, batchData, 0o644); err != nil {
		return err
	}
	var batchResult struct {
		Updated   []core.TaskView `json:"updated"`
		Unchanged []core.TaskView `json:"unchanged"`
	}
	if err = r.json(&batchResult, "batch", "import", "--in", jsonImportPath); err != nil {
		return err
	}
	if len(batchResult.Updated) != 1 || batchResult.Updated[0].ID != grouped.ID {
		return fmt.Errorf("JSON v1 import result = %#v", batchResult)
	}
	grouped = core.TaskView{}
	if err = r.json(&grouped, "task", "show", groupedID); err != nil {
		return err
	}
	if grouped.IssueID != "" || grouped.Project != projectID || grouped.FeatureID != featureID || grouped.Feature != "Search Batch" {
		return fmt.Errorf("JSON v1 omission/null/stable-key result = %#v", grouped.Task)
	}
	r.assert("batch export carries all six fields and JSON version 1 preserves omissions, clears nulls, and retains featureId")

	// CSV carries the same six fields and uses a normal quoted round-trip. Edit
	// the assigned row only so the claim set remains two unassigned tasks.
	csvPath := filepath.Join(r.root, "grouping.csv")
	if _, err = r.command("batch", "export", "--out", csvPath, "--format", "csv", "--feature-id", featureID, "--feature", renamed); err != nil {
		return err
	}
	csvData, err := os.ReadFile(csvPath)
	if err != nil {
		return err
	}
	editedCSV, err := rewriteGroupingCSV(csvData, assigned.ID, "Search CSV")
	if err != nil {
		return err
	}
	csvEditedPath := filepath.Join(r.root, "grouping-edit.csv")
	if err = os.WriteFile(csvEditedPath, editedCSV, 0o644); err != nil {
		return err
	}
	if err = r.json(&batchResult, "batch", "import", "--in", csvEditedPath, "--format", "csv"); err != nil {
		return err
	}
	if len(batchResult.Updated) != 1 || batchResult.Updated[0].ID != assigned.ID {
		return fmt.Errorf("CSV import result = %#v", batchResult)
	}
	if err = r.json(&assigned, "task", "show", assigned.ID); err != nil {
		return err
	}
	if assigned.FeatureID != featureID || assigned.Feature != "Search CSV" {
		return fmt.Errorf("CSV stable-key result = %#v", assigned.Task)
	}
	for _, field := range []string{"issueId", "project", "milestone", "version", "featureId", "feature"} {
		if !strings.Contains(string(csvData), field) {
			return fmt.Errorf("CSV export header missing %s", field)
		}
	}
	r.assert("CSV export/import carries all six grouping columns and preserves stable featureId on display rename")

	var ready []core.TaskView
	if err = r.json(&ready, append([]string{"task", "ready", "--limit", "10", "--agent", "Bob"}, groupingScopeArgs(issueID, projectID, milestone, version, featureID, renamed)...)...); err != nil {
		return err
	}
	if len(ready) != 2 || ready[0].ID != claimA.ID || ready[1].ID != claimB.ID {
		return fmt.Errorf("assignee/order ready = %#v, want claim A then B only", ready)
	}
	claimArgs := groupingScopeArgs(issueID, projectID, milestone, version, featureID, renamed)
	results, err := r.batch([]batchSpec{
		{args: append([]string{"--json", "task", "next", "--agent", "Bob"}, claimArgs...), cwd: project, kind: "grouping-next"},
		{args: append([]string{"--json", "task", "next", "--agent", "Carol"}, claimArgs...), cwd: project, kind: "grouping-next"},
	})
	if err != nil {
		return err
	}
	if err = requireBatchSuccess(results, "grouped next"); err != nil {
		return err
	}
	claimed := make(map[string]bool)
	for index, result := range results {
		var task core.TaskView
		if err = decodeJSONOutput(result, &task); err != nil {
			return fmt.Errorf("decode grouped claim %d: %w", index+1, err)
		}
		if task.ID != claimA.ID && task.ID != claimB.ID || claimed[task.ID] || (task.Assignee != "Bob" && task.Assignee != "Carol") || task.Status != core.StatusInProgress {
			return fmt.Errorf("grouped claim %d = %#v, want unique A/B claim", index+1, task)
		}
		claimed[task.ID] = true
	}
	if len(claimed) != 2 {
		return fmt.Errorf("atomic grouped claim set = %#v", claimed)
	}
	var noReady any
	if err = r.json(&noReady, append([]string{"task", "ready", "--agent", "Bob"}, claimArgs...)...); err != nil {
		return err
	}
	if noReady != nil {
		return fmt.Errorf("ready after atomic claims = %#v, want null", noReady)
	}
	if err = r.expectFailureContaining("no eligible task found", append([]string{"task", "next", "--agent", "Bob"}, claimArgs...)...); err != nil {
		return err
	}
	r.assert("ready/next reuse one exact grouping scope, honor assignee and branch rules, and atomically claim each match once")

	if err = validateStore(filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	return nil
}

func groupingScopeArgs(issueID, project, milestone, version, featureID, feature string) []string {
	return []string{"--issue-id", " " + issueID + " ", "--project", strings.ToLower(project), "--milestone", strings.ToLower(milestone), "--version", strings.ToUpper(version), "--feature-id", strings.ToLower(featureID), "--feature", strings.ToLower(feature)}
}

func groupingRawString(row map[string]json.RawMessage, key string) string {
	var value string
	if err := json.Unmarshal(row[key], &value); err != nil {
		return ""
	}
	return value
}

func rewriteGroupingCSV(data []byte, taskID, feature string) ([]byte, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("decode grouping CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, errors.New("grouping CSV export has no task rows")
	}
	positions := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		positions[name] = index
	}
	idPosition, ok := positions["id"]
	if !ok {
		return nil, errors.New("grouping CSV export has no id column")
	}
	featurePosition, ok := positions["feature"]
	if !ok {
		return nil, errors.New("grouping CSV export has no feature column")
	}
	for index := 1; index < len(records); index++ {
		if records[index][idPosition] == taskID {
			records[index][featurePosition] = feature
			var output bytes.Buffer
			writer := csv.NewWriter(&output)
			for _, record := range records {
				if err := writer.Write(record); err != nil {
					return nil, err
				}
			}
			writer.Flush()
			if err := writer.Error(); err != nil {
				return nil, err
			}
			return output.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("task %s missing from grouping CSV", taskID)
}
