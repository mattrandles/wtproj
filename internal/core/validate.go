package core

import (
	"fmt"
	"sort"
	"strings"
)

func NormalizeDependencies(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func ValidateDependencies(taskID string, dependencies []string, allTasks []Task) error {
	return ValidateDependenciesWithCatalog(DefaultStatusCatalog(), taskID, dependencies, allTasks)
}

// ValidateDependenciesWithCatalog validates dependency references and cycles
// for an invocation's catalog. The catalog also supplies the dependency
// resolution predicate used by DependenciesResolved.
func ValidateDependenciesWithCatalog(_ StatusCatalog, taskID string, dependencies []string, allTasks []Task) error {
	return ValidateDependenciesAcrossLifecycles(taskID, dependencies, allTasks, nil)
}

// ValidateDependenciesAcrossLifecycles validates one dependency graph shared
// by executable tasks and planning items. The two input slices remain
// lifecycle-partitioned for callers' query results, but identifiers and edges
// have one global namespace.
func ValidateDependenciesAcrossLifecycles(taskID string, dependencies []string, tasks []Task, planningItems []PlanningItem) error {
	graph := make(map[string][]string, len(tasks)+len(planningItems)+1)
	shortIDs := make(map[string]string, len(tasks)+len(planningItems))
	for _, task := range tasks {
		if existing, ok := graph[task.ID]; ok {
			return fmt.Errorf("duplicate canonical id %s has dependencies %v and %v", task.ID, existing, task.Dependencies)
		}
		if existing, ok := shortIDs[task.ShortID]; ok && existing != task.ID {
			return fmt.Errorf("shortId %s is used by both %s and %s", task.ShortID, existing, task.ID)
		}
		graph[task.ID] = append([]string(nil), task.Dependencies...)
		shortIDs[task.ShortID] = task.ID
	}
	for _, item := range planningItems {
		if _, ok := graph[item.ID]; ok {
			return fmt.Errorf("canonical id %s is used by both executable and planning records", item.ID)
		}
		if existing, ok := shortIDs[item.ShortID]; ok && existing != item.ID {
			return fmt.Errorf("shortId %s is used by both %s and %s", item.ShortID, existing, item.ID)
		}
		graph[item.ID] = append([]string(nil), item.Dependencies...)
		shortIDs[item.ShortID] = item.ID
	}

	for id, itemDependencies := range graph {
		for _, dependency := range itemDependencies {
			if dependency == id {
				return fmt.Errorf("task cannot depend on itself: %s", dependency)
			}
			if _, ok := graph[dependency]; !ok {
				return fmt.Errorf("dependency %q does not exist", dependency)
			}
		}
	}
	for _, dependency := range dependencies {
		if dependency == taskID {
			return fmt.Errorf("task cannot depend on itself: %s", dependency)
		}
		if _, ok := graph[dependency]; !ok {
			return fmt.Errorf("dependency %q does not exist", dependency)
		}
	}
	if taskID != "" {
		graph[taskID] = append([]string(nil), dependencies...)
	}

	visited := map[string]bool{}
	onStack := map[string]bool{}
	path := []string{}

	var walk func(string) error
	walk = func(id string) error {
		if onStack[id] {
			cycle := append(path, id)
			return fmt.Errorf("cyclic dependency detected: %s", strings.Join(cycle, " -> "))
		}
		if visited[id] {
			return nil
		}
		visited[id] = true
		onStack[id] = true
		path = append(path, id)
		for _, dep := range graph[id] {
			if err := walk(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		onStack[id] = false
		return nil
	}

	for id := range graph {
		if err := walk(id); err != nil {
			return err
		}
	}
	return nil
}

// DependenciesResolved reports whether every dependency of task is resolved
// according to the catalog. Done is the only resolving status by default.
func DependenciesResolved(catalog StatusCatalog, task Task, allTasks []Task) bool {
	return DependenciesResolvedAcrossLifecycles(catalog, task, allTasks, nil)
}

// DependenciesResolvedAcrossLifecycles applies execution readiness semantics
// to a shared dependency graph. Planning records deliberately never resolve a
// dependency: promotion changes their lifecycle, and only the resulting
// executable record can later reach a resolving execution status.
func DependenciesResolvedAcrossLifecycles(catalog StatusCatalog, task Task, allTasks []Task, planningItems []PlanningItem) bool {
	byID := make(map[string]Task, len(allTasks))
	for _, candidate := range allTasks {
		byID[candidate.ID] = candidate
	}
	planningByID := make(map[string]PlanningItem, len(planningItems))
	for _, item := range planningItems {
		planningByID[item.ID] = item
	}
	for _, dependencyID := range task.Dependencies {
		candidate, ok := byID[dependencyID]
		if !ok || !catalog.DependencyResolved(candidate.Status) {
			return false
		}
		if _, planning := planningByID[dependencyID]; planning {
			return false
		}
	}
	return true
}
