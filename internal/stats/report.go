// Package stats contains the read-only aggregation model used by the stats
// command. It deliberately depends on the provider interface rather than on
// a storage implementation so that every provider can expose the same report.
package stats

import (
	"fmt"
	"sort"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

// Bucket is one categorical value and its number of tasks. Value is kept as
// the original string for JSON consumers; an empty Value is the unset bucket
// and can be rendered as "unset" by human-facing callers.
type Bucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Attribute identifies a supported focused stats breakdown.
type Attribute string

const (
	AttributeModel        Attribute = "model"
	AttributeLane         Attribute = "lane"
	AttributePriority     Attribute = "priority"
	AttributeEstimate     Attribute = "estimate"
	AttributeAssignee     Attribute = "assignee"
	AttributeComments     Attribute = "comments"
	AttributeDependencies Attribute = "dependencies"
)

// Attributes contains the supported categorical task breakdowns. Each slice
// is ordered by its attribute's canonical order or, for free text, lexical
// order. Empty values are represented by a bucket whose Value is "".
type Attributes struct {
	Model    []Bucket `json:"model"`
	Lane     []Bucket `json:"lane"`
	Priority []Bucket `json:"priority"`
	Estimate []Bucket `json:"estimate"`
	Assignee []Bucket `json:"assignee"`
}

// CommentMetrics describes comments on the selected tasks.
type CommentMetrics struct {
	TasksWithComments int `json:"tasksWithComments"`
	TotalRecords      int `json:"totalRecords"`
}

// DependencyMetrics describes direct dependencies on the selected tasks.
type DependencyMetrics struct {
	TasksWithDependencies int `json:"tasksWithDependencies"`
	IndependentTasks      int `json:"independentTasks"`
	DirectDependencyTotal int `json:"directDependencyTotal"`
}

// HandoffMetrics describes retained handoffs relevant to the selected task
// set. AllStatusTotal is the total before applying a status filter; it is
// equal to Total when the report is unfiltered.
type HandoffMetrics struct {
	Total          int `json:"total"`
	AllStatusTotal int `json:"allStatusTotal"`
	Global         int `json:"global"`
	TaskScoped     int `json:"taskScoped"`
}

// Report is the renderer-neutral stats result. StatusCounts always contains
// todo, inProgress, paused, and done in that order, including zero-count
// buckets. Other categorical slices contain observed values only.
type Report struct {
	Status       string            `json:"status,omitempty"`
	TotalTasks   int               `json:"totalTasks"`
	StatusCounts []Bucket          `json:"statusCounts"`
	Attributes   Attributes        `json:"attributes"`
	Comments     CommentMetrics    `json:"comments"`
	Dependencies DependencyMetrics `json:"dependencies"`
	Handoffs     HandoffMetrics    `json:"handoffs"`
}

// FocusedReport is the stable JSON shape for an attribute-specific query.
// Categorical queries populate Buckets; comments and dependencies populate
// their matching metric field. Only the requested metric is present.
type FocusedReport struct {
	Status       string             `json:"status,omitempty"`
	TotalTasks   int                `json:"totalTasks"`
	Attribute    Attribute          `json:"attribute"`
	Buckets      *[]Bucket          `json:"buckets,omitempty"`
	Comments     *CommentMetrics    `json:"comments,omitempty"`
	Dependencies *DependencyMetrics `json:"dependencies,omitempty"`
}

// Options controls aggregation. A nil Status selects every task. The status
// is passed through the provider's existing TaskFilter; provider and storage
// interfaces are intentionally unchanged.
type Options struct {
	Status *core.Status
}

// Aggregate loads tasks and retained handoffs from p and returns their
// deterministic aggregate. Handoffs are loaded with AllScopes so the report
// can include global records and task-scoped records belonging to selected
// tasks without changing Provider or storage contracts.
func Aggregate(p provider.Provider, options Options) (Report, error) {
	if p == nil {
		return Report{}, fmt.Errorf("stats provider is nil")
	}

	filter := provider.TaskFilter{}
	if options.Status != nil {
		status, err := core.ParseStatus(string(*options.Status))
		if err != nil {
			return Report{}, err
		}
		filter.Status = &status
	}

	tasks, err := p.ListTasks(filter)
	if err != nil {
		return Report{}, fmt.Errorf("list tasks for stats: %w", err)
	}
	handoffResult, err := p.ListHandoffs(provider.HandoffFilter{AllScopes: true})
	if err != nil {
		return Report{}, fmt.Errorf("list handoffs for stats: %w", err)
	}

	report := Report{
		TotalTasks:   len(tasks),
		StatusCounts: statusBuckets(tasks),
		Attributes: Attributes{
			Model:    textBuckets(tasks, func(task core.TaskView) string { return task.Model }),
			Lane:     textBuckets(tasks, func(task core.TaskView) string { return task.Lane }),
			Priority: priorityBuckets(tasks),
			Estimate: estimateBuckets(tasks),
			Assignee: textBuckets(tasks, func(task core.TaskView) string { return task.Assignee }),
		},
		Comments: commentMetrics(tasks),
		Dependencies: DependencyMetrics{
			TasksWithDependencies: countTasks(tasks, func(task core.TaskView) bool { return len(task.Dependencies) > 0 }),
			IndependentTasks:      countTasks(tasks, func(task core.TaskView) bool { return len(task.Dependencies) == 0 }),
			DirectDependencyTotal: countDependencies(tasks),
		},
	}
	if options.Status != nil {
		report.Status = string(*options.Status)
	}

	report.Handoffs = handoffMetrics(tasks, handoffResult.Handoffs, options.Status != nil)
	return report, nil
}

// Build is a descriptive alias for Aggregate for callers constructing a
// report as part of a command or another renderer.
func Build(p provider.Provider, options Options) (Report, error) {
	return Aggregate(p, options)
}

// Buckets returns the categorical buckets for a supported task attribute.
// Comments and dependencies are scalar metrics and therefore return nil.
func (r Report) Buckets(attribute Attribute) []Bucket {
	switch attribute {
	case AttributeModel:
		return r.Attributes.Model
	case AttributeLane:
		return r.Attributes.Lane
	case AttributePriority:
		return r.Attributes.Priority
	case AttributeEstimate:
		return r.Attributes.Estimate
	case AttributeAssignee:
		return r.Attributes.Assignee
	default:
		return nil
	}
}

// Focus returns the stable focused report for attribute. Callers should pass
// one of the Attribute constants; unsupported attributes return an empty
// categorical report and are rejected by the CLI parser.
func (r Report) Focus(attribute Attribute) FocusedReport {
	focused := FocusedReport{
		Status:     r.Status,
		TotalTasks: r.TotalTasks,
		Attribute:  attribute,
	}
	switch attribute {
	case AttributeModel, AttributeLane, AttributePriority, AttributeEstimate, AttributeAssignee:
		buckets := r.Buckets(attribute)
		focused.Buckets = &buckets
	case AttributeComments:
		focused.Comments = &r.Comments
	case AttributeDependencies:
		focused.Dependencies = &r.Dependencies
	}
	return focused
}

func statusBuckets(tasks []core.TaskView) []Bucket {
	counts := map[core.Status]int{}
	for _, task := range tasks {
		counts[task.Status]++
	}
	statuses := []core.Status{core.StatusTodo, core.StatusInProgress, core.StatusPaused, core.StatusDone}
	result := make([]Bucket, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, Bucket{Value: string(status), Count: counts[status]})
	}
	return result
}

func textBuckets(tasks []core.TaskView, value func(core.TaskView) string) []Bucket {
	counts := make(map[string]int)
	for _, task := range tasks {
		counts[value(task)]++
	}
	return sortedBuckets(counts, nil)
}

func priorityBuckets(tasks []core.TaskView) []Bucket {
	counts := make(map[string]int)
	for _, task := range tasks {
		counts[string(task.Priority)]++
	}
	order := []string{"", string(core.PriorityLow), string(core.PriorityMedium), string(core.PriorityHigh), string(core.PriorityUrgent)}
	return sortedBuckets(counts, order)
}

func estimateBuckets(tasks []core.TaskView) []Bucket {
	counts := make(map[string]int)
	for _, task := range tasks {
		counts[string(task.Estimate)]++
	}
	order := []string{"", string(core.EstimateXS), string(core.EstimateS), string(core.EstimateM), string(core.EstimateL), string(core.EstimateXL)}
	return sortedBuckets(counts, order)
}

func sortedBuckets(counts map[string]int, canonical []string) []Bucket {
	values := make([]string, 0, len(counts))
	for value := range counts {
		values = append(values, value)
	}
	if canonical == nil {
		sort.Strings(values)
	} else {
		positions := make(map[string]int, len(canonical))
		for index, value := range canonical {
			positions[value] = index
		}
		sort.Slice(values, func(i, j int) bool {
			left, leftOK := positions[values[i]]
			right, rightOK := positions[values[j]]
			switch {
			case leftOK && rightOK:
				return left < right
			case leftOK:
				return true
			case rightOK:
				return false
			default:
				return values[i] < values[j]
			}
		})
	}
	result := make([]Bucket, 0, len(values))
	for _, value := range values {
		result = append(result, Bucket{Value: value, Count: counts[value]})
	}
	return result
}

func commentMetrics(tasks []core.TaskView) CommentMetrics {
	result := CommentMetrics{}
	for _, task := range tasks {
		if len(task.Comments) > 0 {
			result.TasksWithComments++
		}
		result.TotalRecords += len(task.Comments)
	}
	return result
}

func countDependencies(tasks []core.TaskView) int {
	total := 0
	for _, task := range tasks {
		total += len(task.Dependencies)
	}
	return total
}

func countTasks(tasks []core.TaskView, predicate func(core.TaskView) bool) int {
	count := 0
	for _, task := range tasks {
		if predicate(task) {
			count++
		}
	}
	return count
}

func handoffMetrics(tasks []core.TaskView, handoffs []core.Handoff, statusFiltered bool) HandoffMetrics {
	selectedIDs := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		selectedIDs[task.ID] = struct{}{}
	}

	result := HandoffMetrics{AllStatusTotal: len(handoffs)}
	for _, handoff := range handoffs {
		if handoff.TaskID != "" {
			if _, selected := selectedIDs[handoff.TaskID]; !selected && statusFiltered {
				continue
			}
			if !statusFiltered {
				result.TaskScoped++
			}
		}
		result.Total++
		if handoff.TaskID == "" {
			result.Global++
		} else if statusFiltered {
			result.TaskScoped++
		}
	}
	if !statusFiltered {
		result.AllStatusTotal = result.Total
	}
	return result
}
