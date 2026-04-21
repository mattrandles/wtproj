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
	byID := make(map[string]Task, len(allTasks))
	for _, task := range allTasks {
		byID[task.ID] = task
	}

	for _, task := range allTasks {
		for _, dependency := range task.Dependencies {
			if dependency == task.ID {
				return fmt.Errorf("task cannot depend on itself: %s", dependency)
			}
			if _, ok := byID[dependency]; !ok {
				return fmt.Errorf("dependency %q does not exist", dependency)
			}
		}
	}

	for _, dependency := range dependencies {
		if dependency == taskID {
			return fmt.Errorf("task cannot depend on itself: %s", dependency)
		}
		if _, ok := byID[dependency]; !ok {
			return fmt.Errorf("dependency %q does not exist", dependency)
		}
	}

	graph := make(map[string][]string, len(allTasks)+1)
	for _, task := range allTasks {
		graph[task.ID] = append([]string(nil), task.Dependencies...)
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
