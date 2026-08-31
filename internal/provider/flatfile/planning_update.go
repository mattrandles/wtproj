package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

var _ provider.PlanningEditor = (*Provider)(nil)

// UpdatePlanningItem applies the complete mutable planning metadata surface.
// Status, identity, comments, and lifecycle timestamps are intentionally not
// part of the patch. All selectors resolve under the same lock as the store
// snapshot, so a published record cannot contain a stale dependency or
// reusable assignment reference.
func (p *Provider) UpdatePlanningItem(idOrShortID string, input core.UpdatePlanningItemInput) (core.PlanningItemView, error) {
	var view core.PlanningItemView
	err := p.withGlobalLock(func() error {
		snapshot, catalog, err := p.loadPlanningValidationSnapshot()
		if err != nil {
			return err
		}
		before, err := resolvePlanningItem(idOrShortID, snapshot.planningItems)
		if err != nil {
			return err
		}
		candidate := before

		if input.Title.Set {
			candidate.Title = strings.TrimSpace(input.Title.Value)
		}
		if input.Description.Set {
			candidate.Description = strings.TrimSpace(input.Description.Value)
		}
		if input.Priority.Set {
			candidate.Priority = input.Priority.Value
		}
		if input.Estimate.Set {
			candidate.Estimate = input.Estimate.Value
		}
		if input.Lane.Set {
			candidate.Lane = strings.TrimSpace(input.Lane.Value)
		}
		if input.Model.Set {
			candidate.Model = strings.TrimSpace(input.Model.Value)
		}
		if input.IssueID.Set {
			candidate.IssueID = strings.TrimSpace(input.IssueID.Value)
		}
		if input.Project.Set {
			candidate.Project = strings.TrimSpace(input.Project.Value)
		}
		if input.Milestone.Set {
			candidate.Milestone = strings.TrimSpace(input.Milestone.Value)
		}
		if input.Version.Set {
			candidate.Version = strings.TrimSpace(input.Version.Value)
		}
		if input.FeatureID.Set {
			candidate.FeatureID = strings.TrimSpace(input.FeatureID.Value)
		}
		if input.Feature.Set {
			candidate.Feature = strings.TrimSpace(input.Feature.Value)
		}
		if input.GitRepo.Set {
			candidate.GitRepo = strings.TrimSpace(input.GitRepo.Value)
		}
		if input.GitBranch.Set {
			candidate.GitBranch = strings.TrimSpace(input.GitBranch.Value)
		}
		if input.WorktreeName.Set {
			candidate.WorktreeName = strings.TrimSpace(input.WorktreeName.Value)
		}
		if input.WorktreeDir.Set {
			candidate.WorktreeDir = strings.TrimSpace(input.WorktreeDir.Value)
		}
		if input.Assignee.Set {
			candidate.Assignee = strings.TrimSpace(input.Assignee.Value)
		}

		if input.Dependencies.Set {
			resolved, err := resolveDependencyIDs(input.Dependencies.Value, snapshot.tasks, snapshot.planningItems)
			if err != nil {
				return err
			}
			if err := core.ValidateDependenciesAcrossLifecycles(candidate.ID, resolved, snapshot.tasks, snapshot.planningItems); err != nil {
				return err
			}
			candidate.Dependencies = resolved
		}

		if input.ReusableTasks.Set {
			resolved, err := resolveReusableTaskIDs(input.ReusableTasks.Value, catalog)
			if err != nil {
				return err
			}
			candidate.ReusableTaskIDs = resolved
		}

		if reflect.DeepEqual(candidate, before) {
			view, err = p.planningView(candidate, catalog)
			return err
		}

		candidate.UpdatedAt = nextPlanningMutationTime(before)
		if err := candidate.Validate(); err != nil {
			return err
		}
		if err := p.replacePlanningItem(before, candidate); err != nil {
			return err
		}
		view, err = p.planningView(candidate, catalog)
		return err
	})
	if err != nil {
		return core.PlanningItemView{}, err
	}
	return view, nil
}

// SetPlanningStatus performs one direct move in the fixed planning lifecycle.
// Planning status changes never call execution lifecycle normalization and
// therefore never synthesize startedAt or completedAt.
func (p *Provider) SetPlanningStatus(idOrShortID string, target core.PlanningStatus) (core.PlanningItemView, error) {
	var view core.PlanningItemView
	err := p.withGlobalLock(func() error {
		snapshot, catalog, err := p.loadPlanningValidationSnapshot()
		if err != nil {
			return err
		}
		before, err := resolvePlanningItem(idOrShortID, snapshot.planningItems)
		if err != nil {
			return err
		}
		if !core.AllowedPlanningTransition(before.Status, target) {
			return fmt.Errorf("invalid planning status transition from %s to %s", before.Status, target)
		}

		candidate := before
		candidate.Status = target
		candidate.UpdatedAt = nextPlanningMutationTime(before)
		candidate.StartedAt = nil
		candidate.CompletedAt = nil
		if err := candidate.Validate(); err != nil {
			return err
		}
		if err := p.replacePlanningItem(before, candidate); err != nil {
			return err
		}
		view, err = p.planningView(candidate, catalog)
		return err
	})
	if err != nil {
		return core.PlanningItemView{}, err
	}
	return view, nil
}

func (p *Provider) planningView(item core.PlanningItem, catalog core.ReusableTaskCatalog) (core.PlanningItemView, error) {
	resolved, err := core.ResolveReusableTasks(item.ReusableTaskIDs, catalog)
	if err != nil {
		return core.PlanningItemView{}, fmt.Errorf("resolve reusable tasks for %s: %w", item.ShortID, err)
	}
	return core.PlanningItemView{PlanningItem: item, ReusableTasks: resolved}, nil
}

func nextPlanningMutationTime(item core.PlanningItem) time.Time {
	latest := item.CreatedAt
	if item.UpdatedAt.After(latest) {
		latest = item.UpdatedAt
	}
	now := time.Now().UTC()
	if now.After(latest) {
		return now
	}
	return latest.Add(time.Nanosecond).UTC()
}

// replacePlanningItem publishes the new endpoint atomically before removing
// old filename/status endpoints. A failed replacement leaves the old record
// untouched; a cleanup interruption leaves the same status-move residue that
// loadPlanningItems already validates and repairs on the next locked read.
func (p *Provider) replacePlanningItem(before, after core.PlanningItem) error {
	paths, err := p.planningItemPaths(before)
	if err != nil {
		return err
	}
	target := p.planningStatusDir(after.Status)
	if after.Status == before.Status && len(paths) == 1 {
		target = paths[0]
	} else {
		target = filepath.Join(target, after.ShortID+".json")
	}
	if err := p.writeJSONAtomic(target, after); err != nil {
		return err
	}
	faultPoint("planning-update-publication")

	for _, status := range core.PlanningStatuses() {
		for _, filename := range planningItemFilenames(after) {
			path := filepath.Join(p.planningStatusDir(status), filename+".json")
			if path == target {
				continue
			}
			if err := p.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}
	return nil
}

func (p *Provider) planningItemPaths(item core.PlanningItem) ([]string, error) {
	paths := make([]string, 0, 1)
	for _, status := range core.PlanningStatuses() {
		for _, filename := range planningItemFilenames(item) {
			path := filepath.Join(p.planningStatusDir(status), filename+".json")
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("stat planning record %s: %w", path, err)
			}
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("planning record %s has no storage endpoint", item.ShortID)
	}
	if len(paths) > 1 {
		return nil, fmt.Errorf("planning record %s has multiple storage endpoints: %s", item.ShortID, strings.Join(paths, ", "))
	}
	return paths, nil
}
