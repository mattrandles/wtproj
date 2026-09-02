package prerelease

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planning"
	"github.com/mattrandles/wtproj/internal/stats"
)

// scenarioPlanningEndToEnd exercises the versioned planning workflow through
// the candidate process boundary. Unit and integration tests cover individual
// layers; this scenario keeps lifecycle, grouping, reporting, promotion,
// planning isolation, and canonical export connected in one disposable store.
func scenarioPlanningEndToEnd(r *scenarioRunner) error {
	project, err := r.newGitProject("planning end-to-end project")
	if err != nil {
		return err
	}
	if err = runGit(project, "branch", "-M", "main"); err != nil {
		return err
	}
	r.setCWD(project)

	const (
		projectID = "Apollo"
		version   = "v2.0"
		milestone = "MVP"
		featureID = "FEAT-PLAN"
		feature   = "Planning"
	)

	createArgs := func(title string) []string {
		return []string{"planning", "create", "--title", title, "--project", projectID, "--version", version, "--milestone", milestone, "--feature-id", featureID, "--feature", feature}
	}

	var foundation core.PlanningItemView
	if err = r.json(&foundation, createArgs("Planning foundation")...); err != nil {
		return err
	}
	if foundation.Status != core.PlanningStatusToplan || foundation.Project != projectID || foundation.Version != version || foundation.Milestone != milestone {
		return fmt.Errorf("planning create metadata = %#v", foundation.PlanningItem)
	}
	if err = r.json(&foundation, "planning", "set-status", foundation.ShortID, "researched"); err != nil {
		return err
	}
	if err = r.json(&foundation, "planning", "set-status", foundation.ShortID, "planned"); err != nil {
		return err
	}

	var dependent core.PlanningItemView
	if err = r.json(&dependent, append(createArgs("Planning dependent"), "--depends-on", foundation.ShortID)...); err != nil {
		return err
	}
	if err = r.json(&dependent, "planning", "set-status", dependent.ShortID, "researched"); err != nil {
		return err
	}
	if err = r.json(&dependent, "planning", "set-status", dependent.ShortID, "planned"); err != nil {
		return err
	}
	if len(dependent.Dependencies) != 1 || dependent.Dependencies[0] != foundation.ID {
		return fmt.Errorf("planning dependency = %#v, want %s", dependent.Dependencies, foundation.ID)
	}
	r.assert("planning create, metadata, dependency resolution, and revisable lifecycle work through the candidate")

	selectors := []string{"--project", projectID, "--version", version, "--milestone", milestone}
	var listed []core.PlanningItemView
	if err = r.json(&listed, append([]string{"planning", "list"}, selectors...)...); err != nil {
		return err
	}
	if len(listed) != 2 || listed[0].ID != foundation.ID || listed[1].ID != dependent.ID {
		return fmt.Errorf("planning list = %#v, want foundation then dependent", listed)
	}
	var shown core.PlanningItemView
	if err = r.json(&shown, "planning", "show", foundation.ShortID); err != nil {
		return err
	}
	if shown.ID != foundation.ID || shown.FeatureID != featureID || shown.Feature != feature {
		return fmt.Errorf("planning show = %#v", shown.PlanningItem)
	}
	var report planning.Report
	if err = r.json(&report, append([]string{"planning", "report"}, selectors...)...); err != nil {
		return err
	}
	if report.TotalItems != 2 || len(report.Projects) != 1 || report.Projects[0].Value != projectID || len(report.Projects[0].Versions) != 1 || report.Projects[0].Versions[0].Value != version {
		return fmt.Errorf("planning report = %#v", report)
	}
	r.assert("planning list, show, and project/version/milestone report honor grouping selectors")

	var tasks []core.TaskView
	if err = r.json(&tasks, append([]string{"task", "list"}, selectors...)...); err != nil {
		return err
	}
	if len(tasks) != 0 {
		return fmt.Errorf("ordinary task list exposed planning items: %#v", tasks)
	}
	var statsReport stats.Report
	if err = r.json(&statsReport, append([]string{"stats"}, selectors...)...); err != nil {
		return err
	}
	if statsReport.TotalTasks != 0 {
		return fmt.Errorf("ordinary stats exposed planning items: %#v", statsReport)
	}
	var graph []graphEvidenceNode
	if err = r.json(&graph, append([]string{"graph", "--status", "all"}, selectors...)...); err != nil {
		return err
	}
	if len(graph) != 0 {
		return fmt.Errorf("ordinary graph exposed planning items: %#v", graph)
	}
	r.assert("ordinary task, stats, and graph commands remain planning-blind")

	export := filepath.Join(r.root, "planning export with spaces")
	if _, err = r.command("export", "--out", export); err != nil {
		return err
	}
	if err = validateExport(export, 0); err != nil {
		return err
	}
	planningEntries, err := os.ReadDir(filepath.Join(export, planningExportDirectory))
	if err != nil {
		return err
	}
	if len(planningEntries) != 2 {
		return fmt.Errorf("canonical planning export contains %d records, want 2", len(planningEntries))
	}
	r.assert("canonical export contains a validated planning directory with both records")

	type promotionResult[T any] struct {
		DryRun bool `json:"dryRun"`
		Count  int  `json:"count"`
		Items  []T  `json:"items"`
	}
	var preview promotionResult[core.PlanningItemView]
	if err = r.json(&preview, append([]string{"planning", "promote", "--dry-run"}, selectors[:2]...)...); err != nil {
		return err
	}
	if !preview.DryRun || preview.Count != 2 || len(preview.Items) != 2 {
		return fmt.Errorf("planning promotion preview = %#v", preview)
	}
	var promoted promotionResult[core.TaskView]
	if err = r.json(&promoted, append([]string{"planning", "promote"}, selectors[:2]...)...); err != nil {
		return err
	}
	if promoted.DryRun || promoted.Count != 2 || len(promoted.Items) != 2 {
		return fmt.Errorf("planning promotion = %#v", promoted)
	}
	if promoted.Items[1].Dependencies == nil || len(promoted.Items[1].Dependencies) != 1 || promoted.Items[1].Dependencies[0] != promoted.Items[0].ID {
		return fmt.Errorf("promoted dependency graph = %#v", promoted.Items)
	}
	r.assert("dependency-closed planning dry-run and atomic promotion return executable tasks")

	if err = r.expectFailureContaining("planning item", "planning", "show", foundation.ShortID); err != nil {
		return err
	}
	listed = nil
	if err = r.json(&listed, append([]string{"planning", "list"}, selectors...)...); err != nil {
		return err
	}
	if len(listed) != 0 {
		return fmt.Errorf("promoted planning records remain: %#v", listed)
	}
	tasks = nil
	if err = r.json(&tasks, append([]string{"task", "list"}, selectors...)...); err != nil {
		return err
	}
	if len(tasks) != 2 {
		return fmt.Errorf("promoted task list = %#v, want 2", tasks)
	}
	if err = validateStore(filepath.Join(project, ".wtp")); err != nil {
		return err
	}
	r.assert("promotion removes planning records and exposes only the resulting executable tasks")
	return nil
}
