package flatfile

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/planningjson"
	"github.com/mattrandles/wtproj/internal/provider"
)

var _ provider.PlanningCreator = (*Provider)(nil)

// CreatePlanningItem creates one planning record in the store-wide identity
// and dependency namespace. The lock covers the complete validation snapshot,
// reusable resolution, allocation, and publication so concurrent task and
// planning creators cannot share either identity or short ID.
func (p *Provider) CreatePlanningItem(input core.CreatePlanningItemInput) (core.PlanningItemView, error) {
	var view core.PlanningItemView
	err := p.withGlobalLock(func() error {
		snapshot, catalog, err := p.loadPlanningValidationSnapshot()
		if err != nil {
			return err
		}
		resolvedDependencies, err := resolveDependencyIDs(input.Dependencies, snapshot.tasks, snapshot.planningItems)
		if err != nil {
			return err
		}
		resolvedReusableTaskIDs, err := resolveReusableTaskIDs(input.ReusableTasks, catalog)
		if err != nil {
			return err
		}

		id, err := newAvailableID(snapshot.tasks, snapshot.planningItems)
		if err != nil {
			return err
		}
		if err := core.ValidateDependenciesAcrossLifecycles(id, resolvedDependencies, snapshot.tasks, snapshot.planningItems); err != nil {
			return err
		}
		status := input.Status
		if status == "" {
			status = core.PlanningStatusToplan
		}
		if _, err := core.ParsePlanningStatus(string(status)); err != nil {
			return err
		}

		item := core.PlanningItem{
			ID:              id,
			Title:           strings.TrimSpace(input.Title),
			Description:     strings.TrimSpace(input.Description),
			Priority:        input.Priority,
			Estimate:        input.Estimate,
			Lane:            strings.TrimSpace(input.Lane),
			Model:           strings.TrimSpace(input.Model),
			IssueID:         strings.TrimSpace(input.IssueID),
			Project:         strings.TrimSpace(input.Project),
			Milestone:       strings.TrimSpace(input.Milestone),
			Version:         strings.TrimSpace(input.Version),
			FeatureID:       strings.TrimSpace(input.FeatureID),
			Feature:         strings.TrimSpace(input.Feature),
			GitRepo:         strings.TrimSpace(input.GitRepo),
			GitBranch:       strings.TrimSpace(input.GitBranch),
			WorktreeName:    strings.TrimSpace(input.WorktreeName),
			WorktreeDir:     strings.TrimSpace(input.WorktreeDir),
			Status:          status,
			Assignee:        strings.TrimSpace(input.Assignee),
			Dependencies:    resolvedDependencies,
			Comments:        []core.Comment{},
			StartedAt:       nil,
			CompletedAt:     nil,
			ReusableTaskIDs: resolvedReusableTaskIDs,
		}

		index, err := p.readIndex()
		if err != nil {
			return err
		}
		index, item.ShortID, err = p.nextAvailableShortID(index, snapshot.tasks, snapshot.planningItems)
		if err != nil {
			return err
		}
		createdAt := nextPlanningCreationTime(snapshot.planningItems)
		item.CreatedAt = createdAt
		item.UpdatedAt = createdAt

		// Validate and encode before publishing the allocation index. This keeps
		// all input, graph, reusable, lifecycle, and codec failures allocation-free.
		if err := item.Validate(); err != nil {
			return err
		}
		if _, err := planningjson.Encode(item); err != nil {
			return fmt.Errorf("encode planning record: %w", err)
		}

		index.Next++
		if err := p.writeIndex(index); err != nil {
			return err
		}
		if err := p.writeJSONAtomic(filepath.Join(p.planningStatusDir(item.Status), item.ShortID+".json"), item); err != nil {
			return err
		}
		faultPoint("create-planning-publication")

		view, err = p.planningView(item, catalog)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return core.PlanningItemView{}, err
	}
	return view, nil
}

// nextPlanningCreationTime keeps creation timestamps UTC and strictly after
// every timestamp already present in the planning namespace when the wall
// clock moves backwards or has insufficient resolution.
func nextPlanningCreationTime(items []core.PlanningItem) time.Time {
	latest := time.Time{}
	for _, item := range items {
		if item.CreatedAt.After(latest) {
			latest = item.CreatedAt
		}
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
