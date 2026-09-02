package prerelease

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/mattrandles/wtproj/internal/core"
)

// scenarioReusableEndToEnd exercises reusable advisory definitions through
// the candidate process boundary. It verifies that task views resolve the
// current catalog in assignment order and that catalog changes remain live.
func scenarioReusableEndToEnd(r *scenarioRunner) error {
	project, err := r.newGitProject("reusable end-to-end project")
	if err != nil {
		return err
	}
	r.setCWD(project)

	var checks, review core.ReusableTaskDefinition
	if err = r.json(&checks, "reusable", "create", "--name", "Checks", "--title", "Run checks", "--instructions", "Run focused checks before handoff."); err != nil {
		return err
	}
	if err = r.json(&review, "reusable", "create", "--name", "Review", "--title", "Review changes", "--instructions", "Review the completed change carefully."); err != nil {
		return err
	}
	if err = checks.Validate(); err != nil {
		return fmt.Errorf("created checks definition is invalid: %w", err)
	}
	if err = review.Validate(); err != nil {
		return fmt.Errorf("created review definition is invalid: %w", err)
	}
	r.assert("reusable create returns valid durable definitions")

	var task core.TaskView
	if err = r.json(&task, "task", "create", "--title", "task with ordered reusable advice", "--reusable", review.Name, "--reusable", checks.ID); err != nil {
		return err
	}
	if err = assertReusableTaskView(task, []string{review.ID, checks.ID}, []core.ReusableTaskDefinition{review, checks}); err != nil {
		return fmt.Errorf("created task reusable resolution: %w", err)
	}
	r.assert("task create resolves repeatable reusable selectors to stable UUIDs in caller order")

	var shown core.TaskView
	if err = r.json(&shown, "task", "show", task.ShortID); err != nil {
		return err
	}
	if err = assertReusableTaskView(shown, []string{review.ID, checks.ID}, []core.ReusableTaskDefinition{review, checks}); err != nil {
		return fmt.Errorf("task show reusable resolution: %w", err)
	}
	r.assert("task show JSON includes live reusable definitions in stored assignment order")

	var claimed core.TaskView
	if err = r.json(&claimed, "task", "next", "--agent", "Reusable worker"); err != nil {
		return err
	}
	if claimed.ID != task.ID || claimed.Status != core.StatusInProgress || claimed.Assignee != "Reusable worker" {
		return fmt.Errorf("task next claim = %#v, want task %s claimed by Reusable worker", claimed.Task, task.ShortID)
	}
	if err = assertReusableTaskView(claimed, []string{review.ID, checks.ID}, []core.ReusableTaskDefinition{review, checks}); err != nil {
		return fmt.Errorf("task next reusable resolution: %w", err)
	}
	r.assert("task next claim JSON preserves ordered live reusable definitions")

	var renamed core.ReusableTaskDefinition
	if err = r.json(&renamed, "reusable", "update", checks.ID, "--name", "Verification", "--title", "Verify changes"); err != nil {
		return err
	}
	if renamed.ID != checks.ID || renamed.Name != "Verification" || renamed.Title != "Verify changes" {
		return fmt.Errorf("reusable rename = %#v, want stable ID with updated live fields", renamed)
	}
	if err = r.json(&shown, "task", "show", task.ID); err != nil {
		return err
	}
	if err = assertReusableTaskView(shown, []string{review.ID, checks.ID}, []core.ReusableTaskDefinition{review, renamed}); err != nil {
		return fmt.Errorf("task show after reusable rename: %w", err)
	}
	r.assert("reusable rename is resolved live by existing task views without changing stored UUIDs")

	var deleted struct {
		Deleted           core.ReusableTaskDefinition `json:"deleted"`
		DetachedTaskCount int                         `json:"detachedTaskCount"`
	}
	if err = r.json(&deleted, "reusable", "delete", review.ID); err != nil {
		return err
	}
	if deleted.Deleted.ID != review.ID || deleted.DetachedTaskCount != 1 {
		return fmt.Errorf("reusable delete = %#v, want review definition detached from one task", deleted)
	}
	if err = r.json(&shown, "task", "show", task.ID); err != nil {
		return err
	}
	if err = assertReusableTaskView(shown, []string{checks.ID}, []core.ReusableTaskDefinition{renamed}); err != nil {
		return fmt.Errorf("task show after reusable delete: %w", err)
	}
	r.assert("reusable delete atomically detaches the definition while retaining other ordered assignments")

	exportPath := filepath.Join(r.root, "reusable canonical export")
	if _, err = r.command("export", "--out", exportPath); err != nil {
		return err
	}
	exported, err := os.ReadFile(filepath.Join(exportPath, "reusable.json"))
	if err != nil {
		return fmt.Errorf("read canonical reusable.json: %w", err)
	}
	var catalog core.ReusableTaskCatalog
	if err = json.Unmarshal(exported, &catalog); err != nil {
		return fmt.Errorf("decode canonical reusable.json: %w", err)
	}
	if err = catalog.Validate(); err != nil {
		return fmt.Errorf("validate canonical reusable.json: %w", err)
	}
	if len(catalog.Definitions) != 1 || catalog.Definitions[0].ID != renamed.ID || catalog.Definitions[0].Name != renamed.Name {
		return fmt.Errorf("canonical reusable.json = %#v, want only renamed checks definition", catalog.Definitions)
	}
	r.assert("canonical export contains a valid reusable.json catalog after live rename and delete")
	return nil
}

func assertReusableTaskView(view core.TaskView, wantIDs []string, wantDefinitions []core.ReusableTaskDefinition) error {
	if !slices.Equal(view.ReusableTaskIDs, wantIDs) {
		return fmt.Errorf("reusableTaskIds = %v, want %v", view.ReusableTaskIDs, wantIDs)
	}
	if len(view.ReusableTasks) != len(wantDefinitions) {
		return fmt.Errorf("reusableTasks length = %d, want %d (%#v)", len(view.ReusableTasks), len(wantDefinitions), view.ReusableTasks)
	}
	for index, want := range wantDefinitions {
		got := view.ReusableTasks[index]
		if got.ID != want.ID || got.Name != want.Name || got.Title != want.Title || got.Instructions != want.Instructions {
			return fmt.Errorf("reusableTasks[%d] = %#v, want %#v", index, got, want)
		}
	}
	return nil
}
