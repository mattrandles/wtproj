package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wtp/internal/app"
	"wtp/internal/config"
	"wtp/internal/core"
	"wtp/internal/provider"
)

type context struct {
	provider provider.Provider
	stdout   io.Writer
	stderr   io.Writer
	jsonOut  bool
}

type legacyParseResult struct {
	args  []string
	found bool
}

func Run(args []string, stdout, stderr io.Writer) error {
	cwd, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}

	cfg, err := config.Discover(cwd)
	if err != nil {
		return err
	}
	p, err := app.NewProvider(cwd, cfg)
	if err != nil {
		return err
	}

	legacy, err := rewriteLegacyArgs(args)
	if err != nil {
		return err
	}
	args = legacy.args
	rootFlags := flag.NewFlagSet("wtp", flag.ContinueOnError)
	rootFlags.SetOutput(stderr)
	jsonOut := rootFlags.Bool("json", false, "emit JSON output")
	if err := rootFlags.Parse(args); err != nil {
		return err
	}

	ctx := context{
		provider: p,
		stdout:   stdout,
		stderr:   stderr,
		jsonOut:  *jsonOut,
	}

	rest := rootFlags.Args()
	if len(rest) == 0 {
		return usage(stderr)
	}

	switch rest[0] {
	case "task":
		return runTask(ctx, rest[1:])
	case "graph":
		return runGraph(ctx, rest[1:])
	case "export":
		return runExport(ctx, rest[1:])
	case "help":
		return help(stdout)
	case "schema":
		return schema(stdout)
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

type graphNode struct {
	Task         core.TaskView `json:"task"`
	Dependencies []graphNode   `json:"dependencies,omitempty"`
}

func runTask(ctx context, args []string) error {
	if len(args) == 0 {
		return errors.New("task subcommand is required")
	}
	switch args[0] {
	case "create":
		return runTaskCreate(ctx, args[1:])
	case "update", "edit":
		return runTaskUpdate(ctx, args[1:])
	case "list":
		return runTaskList(ctx, args[1:])
	case "get":
		return runTaskGet(ctx, args[1:])
	case "start":
		return runTaskTransition(ctx, "start", core.StatusInProgress, args[1:])
	case "pause":
		return runTaskTransition(ctx, "pause", core.StatusPaused, args[1:])
	case "done":
		return runTaskTransition(ctx, "done", core.StatusDone, args[1:])
	case "comment":
		return runTaskComment(ctx, args[1:])
	case "ready":
		return runTaskReady(ctx, args[1:])
	case "next":
		return runTaskNext(ctx, args[1:])
	default:
		return fmt.Errorf("unknown task subcommand %q", args[0])
	}
}

func runTaskCreate(ctx context, args []string) error {
	flags := flag.NewFlagSet("task create", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	title := flags.String("title", "", "task title")
	description := flags.String("description", "", "task description")
	priorityValue := flags.String("priority", "", "task priority (low|medium|high|urgent)")
	estimateValue := flags.String("estimate", "", "task estimate (xs|s|m|l|xl)")
	lane := flags.String("lane", "", "task lane or area")
	agent := flags.String("agent", "", "assignee")
	dependsOn := flags.String("depends-on", "", "comma-separated task identifiers")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task create --title \"...\" [--description \"...\"] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--depends-on a,b] [--agent Tony]")
	}
	if strings.TrimSpace(*title) == "" {
		return errors.New("task title is required")
	}
	priority, err := core.ParsePriority(*priorityValue)
	if err != nil {
		return err
	}
	estimate, err := core.ParseEstimate(*estimateValue)
	if err != nil {
		return err
	}
	task, err := ctx.provider.CreateTask(core.CreateTaskInput{
		Title:        *title,
		Description:  *description,
		Priority:     priority,
		Estimate:     estimate,
		Lane:         *lane,
		Assignee:     *agent,
		Dependencies: splitCSV(*dependsOn),
	})
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskUpdate(ctx context, args []string) error {
	id, options, err := splitSinglePositionalArgs(args)
	if err != nil {
		return errors.New("usage: wtp task update <task-id> [--title \"...\"] [--description \"...\"] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--depends-on a,b] [--agent Tony]")
	}
	flags := flag.NewFlagSet("task update", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)

	var title optionString
	var description optionString
	var priorityValue optionString
	var estimateValue optionString
	var lane optionString
	var dependsOn optionString
	var agent optionString

	flags.Var(&title, "title", "task title")
	flags.Var(&description, "description", "task description")
	flags.Var(&priorityValue, "priority", "task priority (low|medium|high|urgent)")
	flags.Var(&estimateValue, "estimate", "task estimate (xs|s|m|l|xl)")
	flags.Var(&lane, "lane", "task lane or area")
	flags.Var(&dependsOn, "depends-on", "comma-separated task identifiers")
	flags.Var(&agent, "agent", "assignee")
	if err := flags.Parse(options); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task update <task-id> [--title \"...\"] [--description \"...\"] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--depends-on a,b] [--agent Tony]")
	}
	if !title.set && !description.set && !priorityValue.set && !estimateValue.set && !lane.set && !dependsOn.set && !agent.set {
		return errors.New("task update requires at least one field to change")
	}

	priority, err := core.ParsePriority(priorityValue.value)
	if err != nil {
		return err
	}
	estimate, err := core.ParseEstimate(estimateValue.value)
	if err != nil {
		return err
	}

	task, err := ctx.provider.UpdateTask(id, core.UpdateTaskInput{
		Title:        core.OptionalString{Set: title.set, Value: title.value},
		Description:  core.OptionalString{Set: description.set, Value: description.value},
		Priority:     core.OptionalPriority{Set: priorityValue.set, Value: priority},
		Estimate:     core.OptionalEstimate{Set: estimateValue.set, Value: estimate},
		Lane:         core.OptionalString{Set: lane.set, Value: lane.value},
		Assignee:     core.OptionalString{Set: agent.set, Value: agent.value},
		Dependencies: core.OptionalString{Set: dependsOn.set, Value: dependsOn.value},
	})
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskList(ctx context, args []string) error {
	flags := flag.NewFlagSet("task list", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	statusValue := flags.String("status", "", "status filter")
	agent := flags.String("agent", "", "agent context for claimability")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task list [--status todo|inProgress|paused|done] [--agent Tony]")
	}
	filter := provider.TaskFilter{}
	if strings.TrimSpace(*statusValue) != "" {
		status, err := core.ParseStatus(*statusValue)
		if err != nil {
			return err
		}
		filter.Status = &status
	}
	filter.Agent = *agent
	tasks, err := ctx.provider.ListTasks(filter)
	if err != nil {
		return err
	}
	return printValue(ctx, tasks)
}

func runTaskGet(ctx context, args []string) error {
	id, options, err := splitSinglePositional(args)
	if err != nil {
		return errors.New("usage: wtp task get <task-id> [--agent Tony]")
	}
	task, err := ctx.provider.GetTask(id, firstOption(options, "--agent"))
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskTransition(ctx context, name string, target core.Status, args []string) error {
	id, options, err := splitSinglePositional(args)
	if err != nil {
		return fmt.Errorf("usage: wtp task %s <task-id> [--agent Tony]", name)
	}
	agent := firstOption(options, "--agent")
	task, err := ctx.provider.UpdateTaskStatus(id, target, agent)
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskComment(ctx context, args []string) error {
	id, options, err := splitSinglePositional(args)
	if err != nil {
		return errors.New("usage: wtp task comment <task-id> --message \"...\"")
	}
	message := firstOption(options, "--message")
	if strings.TrimSpace(message) == "" {
		return errors.New("comment message is required")
	}
	task, err := ctx.provider.AddComment(id, firstOption(options, "--agent"), message)
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskNext(ctx context, args []string) error {
	flags := flag.NewFlagSet("task next", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	agent := flags.String("agent", "", "claiming agent")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task next [--agent Tony]")
	}
	task, err := ctx.provider.GetNextTask(*agent)
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskReady(ctx context, args []string) error {
	flags := flag.NewFlagSet("task ready", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	agent := flags.String("agent", "", "agent context")
	limit := flags.Int("limit", 1, "number of ready tasks to inspect")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task ready [--agent Tony] [--limit N]")
	}
	if *limit <= 0 {
		return errors.New("ready task limit must be greater than zero")
	}
	if *limit == 1 {
		task, err := ctx.provider.PeekNextTask(*agent)
		if err != nil {
			return handleNoEligibleReady(ctx, err, false)
		}
		return printValue(ctx, task)
	}
	tasks, err := ctx.provider.PeekNextTasks(*agent, *limit)
	if err != nil {
		return handleNoEligibleReady(ctx, err, true)
	}
	return printValue(ctx, tasks)
}

func runGraph(ctx context, args []string) error {
	flags := flag.NewFlagSet("graph", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	statusValue := flags.String("status", string(core.StatusTodo), "graph status filter (todo|inProgress|paused|done|all)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp graph [--status todo|inProgress|paused|done|all]")
	}
	statusFilter, err := parseGraphStatus(*statusValue)
	if err != nil {
		return err
	}
	tasks, err := ctx.provider.ListTasks(provider.TaskFilter{})
	if err != nil {
		return err
	}
	nodes := buildGraph(tasks, statusFilter)
	if ctx.jsonOut {
		encoder := json.NewEncoder(ctx.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(nodes)
	}
	return printGraph(ctx.stdout, nodes)
}

func handleNoEligibleReady(ctx context, err error, batch bool) error {
	if !errors.Is(err, provider.ErrNoEligibleTask) {
		return err
	}
	if ctx.jsonOut {
		encoder := json.NewEncoder(ctx.stdout)
		encoder.SetIndent("", "  ")
		if batch {
			return encoder.Encode([]core.TaskView{})
		}
		return encoder.Encode(nil)
	}
	_, writeErr := fmt.Fprintln(ctx.stdout, provider.ErrNoEligibleTask.Error())
	return writeErr
}

func parseGraphStatus(value string) (*core.Status, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		status := core.StatusTodo
		return &status, nil
	}
	if trimmed == "all" {
		return nil, nil
	}
	status, err := core.ParseStatus(trimmed)
	if err != nil {
		return nil, errors.New("graph status must be one of todo, inProgress, paused, done, or all")
	}
	return &status, nil
}

func buildGraph(tasks []core.TaskView, statusFilter *core.Status) []graphNode {
	byID := make(map[string]core.TaskView, len(tasks))
	selected := make([]core.TaskView, 0, len(tasks))
	selectedSet := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
		if statusFilter != nil && task.Status != *statusFilter {
			continue
		}
		selected = append(selected, task)
		selectedSet[task.ID] = struct{}{}
	}

	dependedOn := make(map[string]struct{}, len(selected))
	for _, task := range selected {
		for _, dependencyID := range task.Dependencies {
			if _, ok := selectedSet[dependencyID]; ok {
				dependedOn[dependencyID] = struct{}{}
			}
		}
	}

	roots := make([]core.TaskView, 0, len(selected))
	for _, task := range selected {
		if _, ok := dependedOn[task.ID]; ok {
			continue
		}
		roots = append(roots, task)
	}
	if len(roots) == 0 {
		roots = selected
	}

	graph := make([]graphNode, 0, len(roots))
	for _, root := range roots {
		graph = append(graph, buildGraphNode(root, byID, selectedSet))
	}
	return graph
}

func buildGraphNode(task core.TaskView, byID map[string]core.TaskView, selectedSet map[string]struct{}) graphNode {
	dependencyIDs := make([]string, 0, len(task.Dependencies))
	for _, dependencyID := range task.Dependencies {
		if _, ok := selectedSet[dependencyID]; !ok {
			continue
		}
		dependencyIDs = append(dependencyIDs, dependencyID)
	}
	sort.Slice(dependencyIDs, func(i, j int) bool {
		left := byID[dependencyIDs[i]]
		right := byID[dependencyIDs[j]]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ShortID < right.ShortID
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	node := graphNode{Task: task}
	for _, dependencyID := range dependencyIDs {
		node.Dependencies = append(node.Dependencies, buildGraphNode(byID[dependencyID], byID, selectedSet))
	}
	return node
}

func printGraph(w io.Writer, nodes []graphNode) error {
	if len(nodes) == 0 {
		_, err := fmt.Fprintln(w, "no tasks matched graph filter")
		return err
	}
	for index, node := range nodes {
		if err := printGraphNode(w, node, "", index == len(nodes)-1, true); err != nil {
			return err
		}
	}
	return nil
}

func printGraphNode(w io.Writer, node graphNode, prefix string, isLast bool, root bool) error {
	line := fmt.Sprintf("%s [%s] %s", node.Task.ShortID, node.Task.Status, node.Task.Title)
	if root {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	} else {
		branch := "|-- "
		nextPrefix := prefix + "|   "
		if isLast {
			branch = "\\-- "
			nextPrefix = prefix + "    "
		}
		if _, err := fmt.Fprintf(w, "%s%s%s\n", prefix, branch, line); err != nil {
			return err
		}
		prefix = nextPrefix
	}
	for index, child := range node.Dependencies {
		if err := printGraphNode(w, child, prefix, index == len(node.Dependencies)-1, false); err != nil {
			return err
		}
	}
	return nil
}

func runExport(ctx context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	outDir := flags.String("out", ".wtp-export", "export directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp export --out .wtp-export")
	}
	if err := ctx.provider.ExportCanonical(*outDir); err != nil {
		return err
	}
	if ctx.jsonOut {
		return json.NewEncoder(ctx.stdout).Encode(map[string]string{"out": *outDir})
	}
	_, err := fmt.Fprintf(ctx.stdout, "exported tasks to %s\n", *outDir)
	return err
}

func printValue(ctx context, value any) error {
	if ctx.jsonOut {
		encoder := json.NewEncoder(ctx.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}

	switch typed := value.(type) {
	case core.TaskView:
		return printTask(ctx.stdout, typed)
	case []core.TaskView:
		for _, task := range typed {
			if _, err := fmt.Fprintf(ctx.stdout, "%s\t%s\t%s\t%s\t%s\t%s", task.ShortID, task.Status, displayPriority(task.Priority), displayClaimable(task.Readiness.Claimable), displayAssignee(task.Assignee), task.Title); err != nil {
				return err
			}
			if task.Readiness.BlockedReason != "" {
				if _, err := fmt.Fprintf(ctx.stdout, "\t%s", task.Readiness.BlockedReason); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(ctx.stdout); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported output type %T", value)
	}
}

func printTask(w io.Writer, task core.TaskView) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n", task.ShortID, task.ID); err != nil {
		return err
	}
	lines := []string{
		fmt.Sprintf("title: %s", task.Title),
		fmt.Sprintf("status: %s", task.Status),
		fmt.Sprintf("priority: %s", displayPriority(task.Priority)),
		fmt.Sprintf("assignee: %s", displayAssignee(task.Assignee)),
		fmt.Sprintf("claimable: %s", displayClaimable(task.Readiness.Claimable)),
		fmt.Sprintf("blocked: %s", displayBool(task.Readiness.Blocked)),
		fmt.Sprintf("dependencyCount: %d", task.Readiness.DependencyCount),
		fmt.Sprintf("reverseDependencyCount: %d", task.Readiness.ReverseDependencyCount),
		fmt.Sprintf("created: %s", task.CreatedAt.Format(timeLayout)),
		fmt.Sprintf("updated: %s", task.UpdatedAt.Format(timeLayout)),
	}
	if task.Description != "" {
		lines = append(lines, fmt.Sprintf("description: %s", task.Description))
	}
	if task.Estimate != "" {
		lines = append(lines, fmt.Sprintf("estimate: %s", task.Estimate))
	}
	if task.Lane != "" {
		lines = append(lines, fmt.Sprintf("lane: %s", task.Lane))
	}
	if task.Readiness.BlockedReason != "" {
		lines = append(lines, fmt.Sprintf("blockedReason: %s", task.Readiness.BlockedReason))
	}
	if len(task.Dependencies) > 0 {
		lines = append(lines, fmt.Sprintf("dependencies: %s", strings.Join(task.Dependencies, ", ")))
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if len(task.Comments) > 0 {
		if _, err := fmt.Fprintln(w, "comments:"); err != nil {
			return err
		}
		for _, comment := range task.Comments {
			if _, err := fmt.Fprintf(w, "- %s [%s] %s\n", comment.Author, comment.CreatedAt.Format(timeLayout), comment.Message); err != nil {
				return err
			}
		}
	}
	return nil
}

func help(w io.Writer) error {
	_, err := io.WriteString(w, `wtp

Commands:
  wtp task create --title "..." [--description "..."] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--depends-on a,b] [--agent Tony]
	wtp task update <task-id> [--title "..."] [--description "..."] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--depends-on a,b] [--agent Tony]
	wtp task edit <task-id> [same options as update]
  wtp task list [--status todo|inProgress|paused|done] [--agent Tony]
  wtp task get <task-id> [--agent Tony]
  wtp task start <task-id> [--agent Tony]
  wtp task pause <task-id>
  wtp task done <task-id>
  wtp task comment <task-id> --message "..."
  wtp task ready [--agent Tony] [--limit N]
  wtp task next [--agent Tony]
	wtp graph [--status todo|inProgress|paused|done|all]
  wtp export --out .wtp-export
	wtp schema
	wtp help

task ready shows the next eligible task without changing task state.
Use --limit N to inspect multiple ready tasks in the same order; batch ready is currently implemented only for the flatfile backend.
task next claims the returned task by moving it to inProgress.
When --agent is supplied, list/get/ready/next compute claimability using that same assignee-safety rule.
task update edits mutable task fields in place; dependencies accept UUIDs or short IDs and are stored as canonical UUIDs.
graph prints dependency trees for matching tasks; it defaults to todo and accepts done, paused, inProgress, todo, or all.

Usage Guide:
	1. Create tasks with title and optional metadata.
	2. Inspect work with task list, task get, or task ready.
	3. Use graph to inspect dependency trees by status.
	4. Claim or move work with task next, start, pause, and done.
	5. Revise metadata or dependencies with task update or task edit.
	6. Add notes with task comment.
	7. Export canonical JSON snapshots with export.

Legacy Compatibility Mode:
  Exactly one legacy action flag is allowed per invocation.

  wtp --agent Tony --get-next-task
  wtp --agent Tony --get-tasks --status todo
  wtp --agent Tony --get-task --task-id wtp-0001
  wtp --agent Tony --set-task-in-progress --task-id wtp-0001
  wtp --agent Tony --set-task-paused --task-id wtp-0001
  wtp --agent Tony --set-task-done --task-id wtp-0001
  wtp --agent Tony --add-comment --task-id wtp-0001 --comment "..."
  wtp --agent Tony --create-task --title "..." --description "..." --dependencies wtp-0001
  wtp --export-tasks=.wtp-export
`)
	return err
}

func usage(w io.Writer) error {
	return help(w)
}

func schema(w io.Writer) error {
	_, err := io.WriteString(w, `wtp schema

Storage layout:
	.wtp/
		todo/
		inProgress/
		paused/
		done/
		meta/
			index.json
			locks/

Task file rules:
	- Each task is stored as JSON in the directory matching its status.
	- Flat-file task filenames use the short ID: .wtp/<status>/wtp-0001.json
	- The JSON payload includes both a canonical UUID id and a stable shortId.
	- Dependencies are stored as canonical UUID strings, even when CLI input uses short IDs.

Compatibility rule:
	- dependencies are stored as canonical UUID strings.

Task JSON schema:
	{
		"id": "25c3806a-bd1b-424d-889b-29e5b06679b8",
		"shortId": "wtp-0001",
		"title": "Implement parser",
		"description": "Add provider selection parsing",
		"priority": "high",
		"estimate": "m",
		"lane": "cli",
		"status": "todo",
		"assignee": "Tony",
		"dependencies": ["7f13f5e2-6d9d-4630-84e1-7aef10c637e4"],
		"comments": [
			{
				"id": "7a6e05a5-b5db-4d36-a1cf-4928cc5fd3e6",
				"author": "Tony",
				"message": "Implemented parser",
				"createdAt": "2026-04-21T12:34:56Z"
			}
		],
		"createdAt": "2026-04-21T12:00:00Z",
		"updatedAt": "2026-04-21T12:34:56Z",
		"startedAt": "2026-04-21T12:10:00Z",
		"completedAt": "2026-04-21T13:00:00Z"
	}

Field semantics:
	- id: required UUID string.
	- shortId: required stable human-friendly identifier, format wtp-NNNN.
	- title: required non-empty string.
	- description: optional string.
	- priority: optional enum low|medium|high|urgent.
	- estimate: optional enum xs|s|m|l|xl.
	- lane: optional string for area/team grouping.
	- status: required enum todo|inProgress|paused|done.
	- assignee: optional string.
	- dependencies: array of task UUIDs.
	- comments: array of comment objects with id, author, message, createdAt.
	- createdAt, updatedAt: required RFC3339 timestamps in UTC.
	- startedAt, completedAt: optional RFC3339 timestamps in UTC.

Behavioral rules:
	- A task cannot depend on itself.
	- Dependencies must reference existing task UUIDs.
	- Cyclic dependency graphs are invalid.
	- A task cannot start or be claimed until all dependencies are done.
	- Status determines the directory where the task file is stored.
	- task next prefers paused tasks before todo, then higher priority, then older tasks.

Interoperability guidance:
	- Programs that write wtp flat files should preserve unknown future fields when possible.
	- Writers must update updatedAt whenever a task changes.
	- Writers must keep the task file in the directory that matches status.
	- Writers creating new tasks should increment .wtp/meta/index.json to allocate the next shortId.
`)
	return err
}

type optionString struct {
	set   bool
	value string
}

func (o *optionString) String() string {
	return o.value
}

func (o *optionString) Set(value string) error {
	o.set = true
	o.value = value
	return nil
}

func rewriteLegacyArgs(args []string) (legacyParseResult, error) {
	actions := []string{}
	agent := ""
	taskID := ""
	comment := ""
	title := ""
	description := ""
	priority := ""
	estimate := ""
	lane := ""
	dependencies := ""
	status := ""
	exportOut := ""

	rest := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--get-next-task":
			actions = append(actions, "next")
		case "--get-tasks":
			actions = append(actions, "list")
		case "--get-task":
			actions = append(actions, "get")
		case "--set-task-in-progress":
			actions = append(actions, "start")
		case "--set-task-paused":
			actions = append(actions, "pause")
		case "--set-task-done":
			actions = append(actions, "done")
		case "--add-comment":
			actions = append(actions, "comment")
		case "--create-task":
			actions = append(actions, "create")
		case "--agent":
			i++
			if i < len(args) {
				agent = args[i]
			}
		case "--task-id":
			i++
			if i < len(args) {
				taskID = args[i]
			}
		case "--comment":
			i++
			if i < len(args) {
				comment = args[i]
			}
		case "--title":
			i++
			if i < len(args) {
				title = args[i]
			}
		case "--description":
			i++
			if i < len(args) {
				description = args[i]
			}
		case "--priority":
			i++
			if i < len(args) {
				priority = args[i]
			}
		case "--estimate":
			i++
			if i < len(args) {
				estimate = args[i]
			}
		case "--lane":
			i++
			if i < len(args) {
				lane = args[i]
			}
		case "--dependencies":
			i++
			if i < len(args) {
				dependencies = args[i]
			}
		case "--status":
			i++
			if i < len(args) {
				status = args[i]
			}
		default:
			if strings.HasPrefix(arg, "--export-tasks=") {
				exportOut = strings.TrimPrefix(arg, "--export-tasks=")
				actions = append(actions, "export")
				continue
			}
			rest = append(rest, arg)
		}
	}

	if len(actions) == 0 {
		return legacyParseResult{args: args, found: false}, nil
	}
	if len(actions) > 1 {
		return legacyParseResult{}, fmt.Errorf("legacy compatibility mode accepts exactly one action flag, got %s", strings.Join(actions, ", "))
	}

	action := actions[0]
	switch action {
	case "next":
		return legacyParseResult{
			args:  append(append(rest, "task", "next"), withValue("--agent", agent)...),
			found: true,
		}, nil
	case "list":
		return legacyParseResult{
			args:  append(append(rest, "task", "list"), append(withValue("--status", status), withValue("--agent", agent)...)...),
			found: true,
		}, nil
	case "get":
		if strings.TrimSpace(taskID) == "" {
			return legacyParseResult{}, errors.New("legacy --get-task requires --task-id")
		}
		return legacyParseResult{args: append(append(rest, "task", "get", taskID), withValue("--agent", agent)...), found: true}, nil
	case "start":
		if strings.TrimSpace(taskID) == "" {
			return legacyParseResult{}, errors.New("legacy --set-task-in-progress requires --task-id")
		}
		out := append(append(rest, "task", "start"), withValue("--agent", agent)...)
		return legacyParseResult{args: append(out, taskID), found: true}, nil
	case "pause":
		if strings.TrimSpace(taskID) == "" {
			return legacyParseResult{}, errors.New("legacy --set-task-paused requires --task-id")
		}
		return legacyParseResult{args: append(rest, "task", "pause", taskID), found: true}, nil
	case "done":
		if strings.TrimSpace(taskID) == "" {
			return legacyParseResult{}, errors.New("legacy --set-task-done requires --task-id")
		}
		out := append(append(rest, "task", "done"), withValue("--agent", agent)...)
		return legacyParseResult{args: append(out, taskID), found: true}, nil
	case "comment":
		if strings.TrimSpace(taskID) == "" {
			return legacyParseResult{}, errors.New("legacy --add-comment requires --task-id")
		}
		if strings.TrimSpace(comment) == "" {
			return legacyParseResult{}, errors.New("legacy --add-comment requires --comment")
		}
		out := append(append(rest, "task", "comment"), withValue("--agent", agent)...)
		out = append(out, withValue("--message", comment)...)
		return legacyParseResult{args: append(out, taskID), found: true}, nil
	case "create":
		if strings.TrimSpace(title) == "" {
			return legacyParseResult{}, errors.New("legacy --create-task requires --title")
		}
		out := append(append(rest, "task", "create"), withValue("--title", title)...)
		out = append(out, withValue("--description", description)...)
		out = append(out, withValue("--priority", priority)...)
		out = append(out, withValue("--estimate", estimate)...)
		out = append(out, withValue("--lane", lane)...)
		out = append(out, withValue("--depends-on", dependencies)...)
		return legacyParseResult{
			args:  append(out, withValue("--agent", agent)...),
			found: true,
		}, nil
	case "export":
		if strings.TrimSpace(exportOut) == "" {
			return legacyParseResult{}, errors.New("legacy --export-tasks requires a target path")
		}
		return legacyParseResult{args: append(rest, "export", "--out", exportOut), found: true}, nil
	default:
		return legacyParseResult{args: args, found: false}, nil
	}
}

func withValue(flagName, value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{flagName, value}
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func displayAssignee(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func displayPriority(value core.Priority) string {
	if strings.TrimSpace(string(value)) == "" {
		return "-"
	}
	return string(value)
}

func displayClaimable(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func displayBool(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func splitSinglePositional(args []string) (string, map[string]string, error) {
	options := map[string]string{}
	positionals := []string{}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				options[arg] = ""
				continue
			}
			options[arg] = args[i+1]
			i++
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 {
		return "", nil, errors.New("expected exactly one positional argument")
	}
	return positionals[0], options, nil
}

func splitSinglePositionalArgs(args []string) (string, []string, error) {
	positionals := []string{}
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			remaining = append(remaining, arg)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				remaining = append(remaining, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 {
		return "", nil, errors.New("expected exactly one positional argument")
	}
	return positionals[0], remaining, nil
}

func firstOption(options map[string]string, name string) string {
	if options == nil {
		return ""
	}
	return strings.TrimSpace(options[name])
}

func trimEmptyArgs(args []string) []string {
	out := args[:0]
	for _, arg := range args {
		if strings.TrimSpace(arg) != "" {
			out = append(out, arg)
		}
	}
	return out
}

const timeLayout = time.RFC3339
