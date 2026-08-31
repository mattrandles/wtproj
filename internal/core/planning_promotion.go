package core

import (
	"fmt"
	"sort"
	"strings"
)

// SelectPlanningPromotion returns the planned planning records matching every
// supplied grouping selector. The returned records are in the same stable
// order used by planning list and promotion. Before returning, it verifies
// that every planning dependency reachable through executable or planning
// vertices is also selected and planned.
//
// The function is deliberately pure: it only reads the supplied snapshots and
// never resolves IDs, changes statuses, or mutates either input slice.
func SelectPlanningPromotion(planningItems []PlanningItem, tasks []Task, grouping GroupingFilter) ([]PlanningItem, error) {
	grouping = NormalizeGroupingFilter(grouping)
	if !grouping.HasSelector() {
		return nil, fmt.Errorf("planning promotion requires at least one grouping selector")
	}

	byID := make(map[string]planningPromotionNode, len(planningItems)+len(tasks))
	for _, item := range planningItems {
		byID[item.ID] = planningPromotionNode{shortID: item.ShortID, status: string(item.Status), planning: true, dependencies: append([]string(nil), item.Dependencies...)}
	}
	for _, task := range tasks {
		byID[task.ID] = planningPromotionNode{shortID: task.ShortID, status: string(task.Status), dependencies: append([]string(nil), task.Dependencies...)}
	}

	selected := make([]PlanningItem, 0, len(planningItems))
	selectedIDs := make(map[string]struct{}, len(planningItems))
	for _, item := range planningItems {
		if item.Status != PlanningStatusPlanned || !MatchesGroupingFilter(Task{
			IssueID: item.IssueID, Project: item.Project, Milestone: item.Milestone,
			Version: item.Version, FeatureID: item.FeatureID, Feature: item.Feature,
		}, grouping) {
			continue
		}
		selected = append(selected, item)
		selectedIDs[item.ID] = struct{}{}
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].CreatedAt.Equal(selected[j].CreatedAt) {
			return selected[i].ShortID < selected[j].ShortID
		}
		return selected[i].CreatedAt.Before(selected[j].CreatedAt)
	})
	if len(selected) == 0 {
		return nil, fmt.Errorf("no planned planning items match promotion filters")
	}

	visited := make(map[string]bool, len(byID))
	stack := make(map[string]bool, len(byID))
	for _, root := range selected {
		if err := walkPlanningPromotionDependencies(root.ID, nil, byID, selectedIDs, visited, stack); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

// HasSelector reports whether at least one grouping selector is non-empty
// after the same trimming used by grouping matching.
func (f GroupingFilter) HasSelector() bool {
	f = NormalizeGroupingFilter(f)
	return f.IssueID != "" || f.Project != "" || f.Milestone != "" || f.Version != "" || f.FeatureID != "" || f.Feature != ""
}

type planningPromotionNode struct {
	shortID      string
	status       string
	planning     bool
	dependencies []string
}

func walkPlanningPromotionDependencies(id string, path []string, nodes map[string]planningPromotionNode, selected map[string]struct{}, visited, stack map[string]bool) error {
	node, ok := nodes[id]
	if !ok {
		return fmt.Errorf("invalid dependency graph: dependency %q does not exist", id)
	}
	if stack[id] {
		cycle := append(append([]string(nil), path...), id)
		return fmt.Errorf("invalid dependency graph: cyclic dependency detected: %s", formatPlanningPromotionChain(cycle, nodes))
	}
	if visited[id] {
		return nil
	}
	stack[id] = true
	path = append(path, id)
	dependencies := append([]string(nil), node.dependencies...)
	sort.Strings(dependencies)
	for _, dependencyID := range dependencies {
		dependency, exists := nodes[dependencyID]
		if !exists {
			return fmt.Errorf("invalid dependency graph: dependency %q does not exist", dependencyID)
		}
		dependencyPath := append(append([]string(nil), path...), dependencyID)
		if dependency.planning {
			if dependency.status != string(PlanningStatusPlanned) {
				return missingPlanningPromotionDependency(dependencyPath, nodes)
			}
			if _, included := selected[dependencyID]; !included {
				return missingPlanningPromotionDependency(dependencyPath, nodes)
			}
		}
		if err := walkPlanningPromotionDependencies(dependencyID, path, nodes, selected, visited, stack); err != nil {
			return err
		}
	}
	delete(stack, id)
	visited[id] = true
	return nil
}

func missingPlanningPromotionDependency(path []string, nodes map[string]planningPromotionNode) error {
	return fmt.Errorf("planning promotion selection is not dependency-closed: missing planning dependency chain: %s", formatPlanningPromotionChain(path, nodes))
}

func formatPlanningPromotionChain(path []string, nodes map[string]planningPromotionNode) string {
	labels := make([]string, 0, len(path))
	for _, id := range path {
		node := nodes[id]
		label := node.shortID
		if label == "" {
			label = id
		}
		if node.planning {
			label = fmt.Sprintf("%s (planning %s)", label, node.status)
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, " -> ")
}
