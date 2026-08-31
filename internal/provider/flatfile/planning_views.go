package flatfile

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

var _ provider.PlanningReader = (*Provider)(nil)

// loadPlanningValidationSnapshot loads the reusable catalog before either
// lifecycle partition and carries that exact validated catalog through the
// complete locked snapshot. Planning records are checked against it even when
// a later filter would exclude them from the returned views.
func (p *Provider) loadPlanningValidationSnapshot() (validationSnapshot, core.ReusableTaskCatalog, error) {
	catalog, err := p.loadReusableTaskCatalog()
	if err != nil {
		return validationSnapshot{}, core.ReusableTaskCatalog{}, err
	}
	snapshot, err := p.loadValidationSnapshot(&catalog)
	if err != nil {
		return validationSnapshot{}, core.ReusableTaskCatalog{}, err
	}
	return snapshot, catalog, nil
}

// ListPlanningItems returns planning records from every branch scope in the
// store. The execution partition is loaded only for the locked validation
// snapshot; it is never included in the returned views or used for selection.
// Reusable definitions are resolved after filtering, from the same locked
// catalog snapshot, in each record's stored assignment order.
func (p *Provider) ListPlanningItems(filter provider.PlanningFilter) ([]core.PlanningItemView, error) {
	var views []core.PlanningItemView
	err := p.withGlobalLock(func() error {
		snapshot, catalog, err := p.loadPlanningValidationSnapshot()
		if err != nil {
			return err
		}

		items := make([]core.PlanningItem, 0, len(snapshot.planningItems))
		for _, item := range snapshot.planningItems {
			if filter.Status != nil && item.Status != *filter.Status {
				continue
			}
			if !matchesPlanningGrouping(item, filter.Grouping) {
				continue
			}
			items = append(items, item)
		}
		sortPlanningItems(items)
		views, err = p.decoratePlanningItems(items, catalog)
		return err
	})
	if err != nil {
		return nil, err
	}
	if views == nil {
		views = []core.PlanningItemView{}
	}
	return views, nil
}

// GetPlanningItem resolves only the planning partition. In particular, an
// executable task with the same selector can never satisfy this lookup.
func (p *Provider) GetPlanningItem(idOrShortID string) (core.PlanningItemView, error) {
	var view core.PlanningItemView
	err := p.withGlobalLock(func() error {
		snapshot, catalog, err := p.loadPlanningValidationSnapshot()
		if err != nil {
			return err
		}
		item, err := resolvePlanningItem(idOrShortID, snapshot.planningItems)
		if err != nil {
			return err
		}
		views, err := p.decoratePlanningItems([]core.PlanningItem{item}, catalog)
		if err != nil {
			return err
		}
		view = views[0]
		return nil
	})
	if err != nil {
		return core.PlanningItemView{}, err
	}
	return view, nil
}

func matchesPlanningGrouping(item core.PlanningItem, filter core.GroupingFilter) bool {
	return core.MatchesGroupingFilter(core.Task{
		IssueID:   item.IssueID,
		Project:   item.Project,
		Milestone: item.Milestone,
		Version:   item.Version,
		FeatureID: item.FeatureID,
		Feature:   item.Feature,
	}, filter)
}

func sortPlanningItems(items []core.PlanningItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ShortID < items[j].ShortID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
}

func (p *Provider) decoratePlanningItems(items []core.PlanningItem, catalog core.ReusableTaskCatalog) ([]core.PlanningItemView, error) {
	views := make([]core.PlanningItemView, 0, len(items))
	for _, item := range items {
		resolved, err := core.ResolveReusableTasks(item.ReusableTaskIDs, catalog)
		if err != nil {
			return nil, fmt.Errorf("resolve reusable tasks for %s: %w", item.ShortID, err)
		}
		views = append(views, core.PlanningItemView{PlanningItem: item, ReusableTasks: resolved})
	}
	return views, nil
}

func resolvePlanningItem(idOrShortID string, items []core.PlanningItem) (core.PlanningItem, error) {
	selector := strings.TrimSpace(idOrShortID)
	if selector == "" {
		return core.PlanningItem{}, errors.New("planning item identifier is required")
	}

	matches := make([]core.PlanningItem, 0, 1)
	for _, item := range items {
		if item.ID == selector || item.ShortID == selector {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return core.PlanningItem{}, fmt.Errorf("planning item %q not found", selector)
	}
	if len(matches) > 1 {
		identifiers := make([]string, 0, len(matches))
		for _, item := range matches {
			identifiers = append(identifiers, fmt.Sprintf("%s (%s)", item.ShortID, item.ID))
		}
		sort.Strings(identifiers)
		return core.PlanningItem{}, fmt.Errorf("planning item identifier %q is ambiguous: %s", selector, strings.Join(identifiers, ", "))
	}
	return matches[0], nil
}
