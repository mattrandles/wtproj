package cli

import (
	stdcontext "context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/app"
	"github.com/mattrandles/wtproj/internal/buildinfo"
	"github.com/mattrandles/wtproj/internal/config"
	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
	"github.com/mattrandles/wtproj/internal/runtimecontext"
	"github.com/mattrandles/wtproj/internal/stats"
	"github.com/mattrandles/wtproj/internal/updater"
)

type context struct {
	provider   provider.Provider
	invocation runtimecontext.Context
	stdout     io.Writer
	stderr     io.Writer
	jsonOut    bool
}

type legacyParseResult struct {
	args  []string
	found bool
}

func Run(args []string, stdout, stderr io.Writer) error {
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
		stdout:  stdout,
		stderr:  stderr,
		jsonOut: *jsonOut,
	}

	rest := rootFlags.Args()
	if len(rest) == 0 {
		return usage(stderr)
	}
	switch rest[0] {
	case "help":
		if err := requireNoArgs("help", rest[1:]); err != nil {
			return err
		}
		return help(stdout)
	case "schema":
		if err := requireNoArgs("schema", rest[1:]); err != nil {
			return err
		}
		return schema(stdout)
	case "version":
		return runVersion(ctx, rest[1:])
	case "update":
		return runSelfUpdate(ctx, rest[1:], updater.Run)
	}

	p, invocation, err := providerAndContextForInvocation(".")
	if err != nil {
		return err
	}
	ctx.provider = p
	ctx.invocation = invocation

	switch rest[0] {
	case "task":
		return runTask(ctx, rest[1:])
	case "handoff":
		return runHandoff(ctx, rest[1:])
	case "graph":
		return runGraph(ctx, rest[1:])
	case "stats":
		return runStats(ctx, rest[1:])
	case "export":
		return runExport(ctx, rest[1:])
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

const statsUsage = "usage: wtp stats [todo|inProgress|paused|done] [model|lane|priority|estimate|assignee|comments|dependencies]"

// runStats accepts only positional status and attribute arguments. Keeping
// this parser separate from flag.FlagSet makes reversed and extra arguments
// unambiguous, while root --json remains the sole output switch.
func runStats(ctx context, args []string) error {
	status, attribute, focused, err := parseStatsArgs(args)
	if err != nil {
		return err
	}

	report, err := stats.Aggregate(ctx.provider, stats.Options{Status: status})
	if err != nil {
		return err
	}
	if focused {
		return printStatsFocused(ctx, report.Focus(attribute))
	}
	return printStatsOverview(ctx, report)
}

func parseStatsArgs(args []string) (*core.Status, stats.Attribute, bool, error) {
	if len(args) > 2 {
		return nil, "", false, errors.New(statsUsage)
	}
	if len(args) == 0 {
		return nil, "", false, nil
	}

	if status, err := core.ParseStatus(args[0]); err == nil {
		if len(args) == 1 {
			return &status, "", false, nil
		}
		attribute, ok := parseStatsAttribute(args[1])
		if !ok {
			return nil, "", false, fmt.Errorf("unknown stats attribute %q; %s", args[1], statsUsage)
		}
		return &status, attribute, true, nil
	}

	attribute, ok := parseStatsAttribute(args[0])
	if !ok {
		return nil, "", false, fmt.Errorf("stats argument %q must be a status or attribute; %s", args[0], statsUsage)
	}
	if len(args) == 2 {
		return nil, "", false, fmt.Errorf("stats status must precede attribute %q; %s", attribute, statsUsage)
	}
	return nil, attribute, true, nil
}

func parseStatsAttribute(value string) (stats.Attribute, bool) {
	attribute := stats.Attribute(value)
	switch attribute {
	case stats.AttributeModel, stats.AttributeLane, stats.AttributePriority,
		stats.AttributeEstimate, stats.AttributeAssignee, stats.AttributeComments,
		stats.AttributeDependencies:
		return attribute, true
	default:
		return "", false
	}
}

func printStatsOverview(ctx context, report stats.Report) error {
	if ctx.jsonOut {
		return encodeJSON(ctx.stdout, report)
	}
	if _, err := fmt.Fprintln(ctx.stdout, "stats"); err != nil {
		return err
	}
	status := report.Status
	if status == "" {
		status = "all"
	}
	if err := printStatsLine(ctx.stdout, "status", status); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "totalTasks", report.TotalTasks); err != nil {
		return err
	}
	if err := printStatsBuckets(ctx.stdout, "statusCounts", report.StatusCounts); err != nil {
		return err
	}
	for _, item := range []struct {
		name    string
		buckets []stats.Bucket
	}{
		{name: "model", buckets: report.Attributes.Model},
		{name: "lane", buckets: report.Attributes.Lane},
		{name: "priority", buckets: report.Attributes.Priority},
		{name: "estimate", buckets: report.Attributes.Estimate},
		{name: "assignee", buckets: report.Attributes.Assignee},
	} {
		if err := printStatsBuckets(ctx.stdout, item.name, item.buckets); err != nil {
			return err
		}
	}
	if err := printStatsLine(ctx.stdout, "comments.tasksWithComments", report.Comments.TasksWithComments); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "comments.totalRecords", report.Comments.TotalRecords); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "dependencies.tasksWithDependencies", report.Dependencies.TasksWithDependencies); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "dependencies.independentTasks", report.Dependencies.IndependentTasks); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "dependencies.directDependencyTotal", report.Dependencies.DirectDependencyTotal); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "handoffs.total", report.Handoffs.Total); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "handoffs.allStatusTotal", report.Handoffs.AllStatusTotal); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "handoffs.global", report.Handoffs.Global); err != nil {
		return err
	}
	return printStatsLine(ctx.stdout, "handoffs.taskScoped", report.Handoffs.TaskScoped)
}

func printStatsFocused(ctx context, report stats.FocusedReport) error {
	if ctx.jsonOut {
		return encodeJSON(ctx.stdout, report)
	}
	if _, err := fmt.Fprintln(ctx.stdout, "stats"); err != nil {
		return err
	}
	status := report.Status
	if status == "" {
		status = "all"
	}
	if err := printStatsLine(ctx.stdout, "status", status); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "totalTasks", report.TotalTasks); err != nil {
		return err
	}
	if err := printStatsLine(ctx.stdout, "attribute", report.Attribute); err != nil {
		return err
	}
	if report.Buckets != nil {
		return printStatsBuckets(ctx.stdout, "buckets", *report.Buckets)
	}
	if report.Comments != nil {
		if err := printStatsLine(ctx.stdout, "comments.tasksWithComments", report.Comments.TasksWithComments); err != nil {
			return err
		}
		return printStatsLine(ctx.stdout, "comments.totalRecords", report.Comments.TotalRecords)
	}
	if report.Dependencies != nil {
		if err := printStatsLine(ctx.stdout, "dependencies.tasksWithDependencies", report.Dependencies.TasksWithDependencies); err != nil {
			return err
		}
		if err := printStatsLine(ctx.stdout, "dependencies.independentTasks", report.Dependencies.IndependentTasks); err != nil {
			return err
		}
		return printStatsLine(ctx.stdout, "dependencies.directDependencyTotal", report.Dependencies.DirectDependencyTotal)
	}
	return nil
}

func printStatsBuckets(w io.Writer, name string, buckets []stats.Bucket) error {
	if _, err := fmt.Fprintf(w, "%s:\n", name); err != nil {
		return err
	}
	for _, bucket := range buckets {
		value := bucket.Value
		if value == "" {
			value = "(unset)"
		}
		if _, err := fmt.Fprintf(w, "  %s: %d\n", value, bucket.Count); err != nil {
			return err
		}
	}
	return nil
}

func printStatsLine(w io.Writer, name string, value any) error {
	_, err := fmt.Fprintf(w, "%s: %v\n", name, value)
	return err
}

func providerForInvocation(invocationDir string) (provider.Provider, error) {
	p, _, err := providerAndContextForInvocation(invocationDir)
	return p, err
}

func providerAndContextForInvocation(invocationDir string) (provider.Provider, runtimecontext.Context, error) {
	runtime, err := runtimecontext.Discover(invocationDir)
	if err != nil {
		return nil, runtimecontext.Context{}, err
	}

	configAnchor := runtime.InvocationDir
	if runtime.InGit {
		configAnchor = runtime.WorktreeRoot
	}
	cfg, err := config.Discover(configAnchor)
	if err != nil {
		return nil, runtimecontext.Context{}, err
	}
	p, err := app.NewProvider(cfg.WTPDir, cfg, runtime.Scope())
	if err != nil {
		return nil, runtimecontext.Context{}, err
	}
	return p, runtime, nil
}

func requireNoArgs(command string, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: wtp %s", command)
	}
	return nil
}

func runVersion(ctx context, args []string) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	jsonOut := flags.Bool("json", false, "emit JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp [--json] version")
	}

	info := buildinfo.Current()
	if ctx.jsonOut || *jsonOut {
		encoder := json.NewEncoder(ctx.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	}
	_, err := fmt.Fprintf(ctx.stdout, "wtp %s\ncommit: %s\nbuildDate: %s\n", info.Version, info.Commit, info.BuildDate)
	return err
}

type updateRunner func(stdcontext.Context, string) (updater.Result, error)

func runSelfUpdate(ctx context, args []string, runner updateRunner) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	jsonOut := flags.Bool("json", false, "emit JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp [--json] update")
	}

	result, err := runner(stdcontext.Background(), buildinfo.Version)
	if err != nil {
		return err
	}
	if ctx.jsonOut || *jsonOut {
		encoder := json.NewEncoder(ctx.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	switch {
	case result.Updated:
		_, err = fmt.Fprintf(ctx.stdout, "updated wtp from %s to %s at %s\n", result.CurrentVersion, result.LatestVersion, result.Path)
	case result.Scheduled:
		_, err = fmt.Fprintf(ctx.stdout, "downloaded and verified wtp %s; Windows will replace %s after this process exits (failures are written to %s.wtp-update-error.txt)\n", result.LatestVersion, result.Path, result.Path)
	default:
		_, err = fmt.Fprintf(ctx.stdout, "no update available (current %s, latest %s)\n", result.CurrentVersion, result.LatestVersion)
	}
	return err
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
	case "get", "show":
		return runTaskGet(ctx, args[1:], args[0])
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

func runHandoff(ctx context, args []string) error {
	if len(args) == 0 {
		return errors.New("handoff subcommand is required")
	}
	switch args[0] {
	case "write":
		return runHandoffWrite(ctx, args[1:])
	case "get":
		return runHandoffGet(ctx, args[1:])
	case "purge":
		return runHandoffPurge(ctx, args[1:])
	default:
		return fmt.Errorf("unknown handoff subcommand %q", args[0])
	}
}

func runHandoffWrite(ctx context, args []string) error {
	flags := flag.NewFlagSet("handoff write", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	message := flags.String("message", "", "handoff message")
	agent := flags.String("agent", "", "handoff author")
	task := flags.String("task", "", "task scope")
	replace := flags.Bool("replace", false, "replace handoffs in the selected scope")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp handoff write --message \"...\" [--agent Tony] [--task <task-id>] [--replace]")
	}
	if strings.TrimSpace(*message) == "" {
		return errors.New("handoff message is required")
	}
	result, err := ctx.provider.WriteHandoff(provider.HandoffWriteRequest{
		Task:    *task,
		Author:  *agent,
		Message: *message,
		Replace: *replace,
	})
	if err != nil {
		return err
	}
	if ctx.jsonOut {
		return encodeJSON(ctx.stdout, result)
	}
	if err := printHandoff(ctx.stdout, result.Handoff); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ctx.stdout, "scopeCount: %d\n", result.ScopeCount); err != nil {
		return err
	}
	_, err = fmt.Fprintf(ctx.stdout, "purge: %s\n", handoffPurgeCommand(result.Handoff))
	return err
}

func runHandoffGet(ctx context, args []string) error {
	flags := flag.NewFlagSet("handoff get", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	usage := "usage: wtp handoff get [--task <task-id> | --all-scopes] [--limit N | --all]"
	task := flags.String("task", "", "task scope")
	allScopes := flags.Bool("all-scopes", false, "include every scope")
	limit := flags.Int("limit", 1, "maximum records to show")
	all := flags.Bool("all", false, "show all matching records")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(usage)
	}
	if *allScopes && strings.TrimSpace(*task) != "" {
		return fmt.Errorf("handoff get accepts either --task or --all-scopes, not both\n%s", usage)
	}
	if *all && wasFlagSet(flags, "limit") {
		return fmt.Errorf("handoff get accepts either --limit or --all, not both\n%s", usage)
	}
	if wasFlagSet(flags, "limit") && *limit <= 0 {
		return fmt.Errorf("handoff limit must be greater than zero\n%s", usage)
	}

	filter := provider.HandoffFilter{Task: *task, AllScopes: *allScopes, Limit: *limit}
	if *all {
		filter.Limit = 0
	}
	result, err := ctx.provider.ListHandoffs(filter)
	if err != nil {
		return err
	}
	if ctx.jsonOut {
		return encodeJSON(ctx.stdout, result)
	}
	if len(result.Handoffs) == 0 {
		if _, err := fmt.Fprintln(ctx.stdout, "no handoffs found"); err != nil {
			return err
		}
	} else {
		for index, handoff := range result.Handoffs {
			if index > 0 {
				if _, err := fmt.Fprintln(ctx.stdout); err != nil {
					return err
				}
			}
			if err := printHandoff(ctx.stdout, handoff); err != nil {
				return err
			}
		}
	}
	if result.HasMore {
		if _, err := fmt.Fprintf(ctx.stdout, "more matching handoffs: %s\n", handoffGetAllCommand(*task, *allScopes)); err != nil {
			return err
		}
	}
	if result.OtherScopesAvailable {
		_, err := fmt.Fprintln(ctx.stdout, "other scopes: wtp handoff get --all-scopes --all")
		return err
	}
	return nil
}

func runHandoffPurge(ctx context, args []string) error {
	flags := flag.NewFlagSet("handoff purge", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	id := flags.String("id", "", "handoff ID")
	task := flags.String("task", "", "task scope")
	global := flags.Bool("global", false, "global scope")
	allScopes := flags.Bool("all-scopes", false, "every scope")
	beforeValue := flags.String("before", "", "delete records before this RFC3339 time")
	olderThan := flags.String("older-than", "", "delete records older than this duration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp handoff purge (--id <handoff-id> | --global | --task <task-id> | --all-scopes) [--before RFC3339 | --older-than DURATION]")
	}
	selectors := 0
	if strings.TrimSpace(*id) != "" {
		selectors++
	}
	if *global {
		selectors++
	}
	if strings.TrimSpace(*task) != "" {
		selectors++
	}
	if *allScopes {
		selectors++
	}
	if selectors != 1 {
		return errors.New("handoff purge requires exactly one selector: --id, --global, --task, or --all-scopes")
	}
	if wasFlagSet(flags, "before") && wasFlagSet(flags, "older-than") {
		return errors.New("handoff purge accepts either --before or --older-than, not both")
	}
	if strings.TrimSpace(*id) != "" && (wasFlagSet(flags, "before") || wasFlagSet(flags, "older-than")) {
		return errors.New("handoff purge --id cannot be combined with --before or --older-than")
	}

	request := provider.HandoffPurgeRequest{ID: *id, Task: *task, Global: *global, AllScopes: *allScopes}
	if wasFlagSet(flags, "before") {
		before, err := time.Parse(time.RFC3339, *beforeValue)
		if err != nil {
			return fmt.Errorf("handoff purge --before must be RFC3339: %w", err)
		}
		before = before.UTC()
		request.Before = &before
	}
	if wasFlagSet(flags, "older-than") {
		duration, err := time.ParseDuration(*olderThan)
		if err != nil || duration <= 0 {
			return errors.New("handoff purge --older-than must be a positive Go duration")
		}
		before := time.Now().UTC().Add(-duration)
		request.Before = &before
	}
	result, err := ctx.provider.PurgeHandoffs(request)
	if err != nil {
		return err
	}
	if ctx.jsonOut {
		return encodeJSON(ctx.stdout, result)
	}
	_, err = fmt.Fprintf(ctx.stdout, "purged: %d\n", result.Purged)
	return err
}

func wasFlagSet(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == name {
			found = true
		}
	})
	return found
}

func encodeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printHandoff(w io.Writer, handoff core.Handoff) error {
	if _, err := fmt.Fprintf(w, "%s\n", handoff.ID); err != nil {
		return err
	}
	scope := "global"
	if handoff.TaskID != "" {
		scope = "task " + handoff.TaskID
	}
	lines := []string{
		"scope: " + scope,
		"author: " + displayAssignee(handoff.Author),
		"message: " + handoff.Message,
		"created: " + handoff.CreatedAt.Format(timeLayout),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func handoffPurgeCommand(handoff core.Handoff) string {
	if handoff.TaskID == "" {
		return "wtp handoff purge --global"
	}
	return "wtp handoff purge --task " + handoff.TaskID
}

func handoffGetAllCommand(task string, allScopes bool) string {
	if allScopes {
		return "wtp handoff get --all-scopes --all"
	}
	if strings.TrimSpace(task) != "" {
		return "wtp handoff get --task " + task + " --all"
	}
	return "wtp handoff get --all"
}

func runTaskCreate(ctx context, args []string) error {
	flags := flag.NewFlagSet("task create", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)
	title := flags.String("title", "", "task title")
	description := flags.String("description", "", "task description")
	priorityValue := flags.String("priority", "", "task priority (low|medium|high|urgent)")
	estimateValue := flags.String("estimate", "", "task estimate (xs|s|m|l|xl)")
	lane := flags.String("lane", "", "task lane or area")
	model := flags.String("model", "", "suggested model for completing the task")
	var gitRepo optionString
	var gitBranch optionString
	var worktreeName optionString
	var worktreeDir optionString
	flags.Var(&gitRepo, "git-repo", "absolute path to the Git repository")
	flags.Var(&gitBranch, "git-branch", "Git branch name")
	flags.Var(&worktreeName, "worktree-name", "Git worktree name")
	flags.Var(&worktreeDir, "worktree-dir", "absolute path to the Git worktree")
	agent := flags.String("agent", "", "assignee")
	dependsOn := flags.String("depends-on", "", "comma-separated task identifiers")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task create --title \"...\" [--description \"...\"] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--model gpt-5] [--git-repo /path/to/repo] [--git-branch branch] [--worktree-name name] [--worktree-dir /path/to/worktree] [--depends-on a,b] [--agent Tony]")
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
		Model:        *model,
		GitRepo:      defaultedOption(gitRepo, ctx.invocation.RepositoryRoot),
		GitBranch:    defaultedOption(gitBranch, ctx.invocation.Branch),
		WorktreeName: defaultedOption(worktreeName, ctx.invocation.WorktreeName),
		WorktreeDir:  defaultedOption(worktreeDir, ctx.invocation.WorktreeRoot),
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
		return errors.New("usage: wtp task update <task-id> [--title \"...\"] [--description \"...\"] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--model gpt-5] [--git-repo /path/to/repo] [--git-branch branch] [--worktree-name name] [--worktree-dir /path/to/worktree] [--depends-on a,b] [--agent Tony]")
	}
	flags := flag.NewFlagSet("task update", flag.ContinueOnError)
	flags.SetOutput(ctx.stderr)

	var title optionString
	var description optionString
	var priorityValue optionString
	var estimateValue optionString
	var lane optionString
	var model optionString
	var gitRepo optionString
	var gitBranch optionString
	var worktreeName optionString
	var worktreeDir optionString
	var dependsOn optionString
	var agent optionString

	flags.Var(&title, "title", "task title")
	flags.Var(&description, "description", "task description")
	flags.Var(&priorityValue, "priority", "task priority (low|medium|high|urgent)")
	flags.Var(&estimateValue, "estimate", "task estimate (xs|s|m|l|xl)")
	flags.Var(&lane, "lane", "task lane or area")
	flags.Var(&model, "model", "suggested model for completing the task")
	flags.Var(&gitRepo, "git-repo", "absolute path to the Git repository")
	flags.Var(&gitBranch, "git-branch", "Git branch name")
	flags.Var(&worktreeName, "worktree-name", "Git worktree name")
	flags.Var(&worktreeDir, "worktree-dir", "absolute path to the Git worktree")
	flags.Var(&dependsOn, "depends-on", "comma-separated task identifiers")
	flags.Var(&agent, "agent", "assignee")
	if err := flags.Parse(options); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task update <task-id> [--title \"...\"] [--description \"...\"] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--model gpt-5] [--git-repo /path/to/repo] [--git-branch branch] [--worktree-name name] [--worktree-dir /path/to/worktree] [--depends-on a,b] [--agent Tony]")
	}
	if !title.set && !description.set && !priorityValue.set && !estimateValue.set && !lane.set && !model.set && !gitRepo.set && !gitBranch.set && !worktreeName.set && !worktreeDir.set && !dependsOn.set && !agent.set {
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
		Model:        core.OptionalString{Set: model.set, Value: model.value},
		GitRepo:      core.OptionalString{Set: gitRepo.set, Value: gitRepo.value},
		GitBranch:    core.OptionalString{Set: gitBranch.set, Value: gitBranch.value},
		WorktreeName: core.OptionalString{Set: worktreeName.set, Value: worktreeName.value},
		WorktreeDir:  core.OptionalString{Set: worktreeDir.set, Value: worktreeDir.value},
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

func runTaskGet(ctx context, args []string, name string) error {
	id, options, err := parseTaskTargetOptions(args, "agent")
	if err != nil {
		return fmt.Errorf("%w; usage: wtp task %s <task-id> [--agent Tony]", err, name)
	}
	task, err := ctx.provider.GetTask(id, optionValue(options, "agent"))
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskTransition(ctx context, name string, target core.Status, args []string) error {
	id, options, err := parseTaskTargetOptions(args, "agent")
	if err != nil {
		return fmt.Errorf("%w; usage: wtp task %s <task-id> [--agent Tony]", err, name)
	}
	agent := optionValue(options, "agent")
	task, err := ctx.provider.UpdateTaskStatus(id, target, agent)
	if err != nil {
		return err
	}
	return printValue(ctx, task)
}

func runTaskComment(ctx context, args []string) error {
	id, options, err := parseTaskTargetOptions(args, "agent", "message")
	if err != nil {
		return fmt.Errorf("%w; usage: wtp task comment <task-id> [--agent Tony] --message \"...\"", err)
	}
	message := optionValue(options, "message")
	if strings.TrimSpace(message) == "" {
		return errors.New("comment message is required")
	}
	task, err := ctx.provider.AddComment(id, optionValue(options, "agent"), message)
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
			if _, err := fmt.Fprintf(ctx.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s", task.ShortID, task.Status, displayPriority(task.Priority), displayClaimable(task.Readiness.Claimable), displayAssignee(task.Assignee), displayModel(task.Model), task.Title); err != nil {
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
	if task.Model != "" {
		lines = append(lines, fmt.Sprintf("model: %s", task.Model))
	}
	if task.GitRepo != "" {
		lines = append(lines, fmt.Sprintf("gitRepo: %s", task.GitRepo))
	}
	if task.GitBranch != "" {
		lines = append(lines, fmt.Sprintf("gitBranch: %s", task.GitBranch))
	}
	if task.WorktreeName != "" {
		lines = append(lines, fmt.Sprintf("worktreeName: %s", task.WorktreeName))
	}
	if task.WorktreeDir != "" {
		lines = append(lines, fmt.Sprintf("worktreeDir: %s", task.WorktreeDir))
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
	if len(task.Handoffs) > 0 {
		if _, err := fmt.Fprintln(w, "handoffs:"); err != nil {
			return err
		}
		for _, handoff := range task.Handoffs {
			if _, err := fmt.Fprintf(w, "- %s\n", handoff.ID); err != nil {
				return err
			}
			scope := "global"
			if handoff.TaskID != "" {
				scope = "task " + handoff.TaskID
			}
			lines := []string{
				"  scope: " + scope,
				"  author: " + displayAssignee(handoff.Author),
				"  created: " + handoff.CreatedAt.Format(timeLayout),
				"  message: " + handoff.Message,
			}
			for _, line := range lines {
				if _, err := fmt.Fprintln(w, line); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func help(w io.Writer) error {
	_, err := io.WriteString(w, `wtp

Commands:
  wtp task create --title "..." [--description "..."] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--model gpt-5] [--git-repo /path/to/repo] [--git-branch branch] [--worktree-name name] [--worktree-dir /path/to/worktree] [--depends-on a,b] [--agent Tony]
	wtp task update <task-id> [--title "..."] [--description "..."] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--model gpt-5] [--git-repo /path/to/repo] [--git-branch branch] [--worktree-name name] [--worktree-dir /path/to/worktree] [--depends-on a,b] [--agent Tony]
	wtp task edit <task-id> [same options as update]
  wtp task list [--status todo|inProgress|paused|done] [--agent Tony]
  wtp task show <task-id> [--agent Tony]
  wtp task get <task-id> [--agent Tony]
  wtp task start <task-id> [--agent Tony]
	wtp task pause <task-id> [--agent Tony]
	wtp task done <task-id> [--agent Tony]
	wtp task comment <task-id> [--agent Tony] --message "..."
  wtp task ready [--agent Tony] [--limit N]
  wtp task next [--agent Tony]
	wtp handoff write --message "..." [--agent Tony] [--task <task-id>] [--replace]
	wtp handoff get [--task <task-id> | --all-scopes] [--limit N | --all]
	wtp handoff purge (--id <handoff-id> | --global | --task <task-id> | --all-scopes) [--before RFC3339 | --older-than DURATION]
	wtp graph [--status todo|inProgress|paused|done|all]
	wtp stats
	wtp stats [todo|inProgress|paused|done]
	wtp stats [model|lane|priority|estimate|assignee|comments|dependencies]
	wtp stats [todo|inProgress|paused|done] [model|lane|priority|estimate|assignee|comments|dependencies]
	wtp export --out .wtp-export
	wtp version
	wtp update
	wtp schema
	wtp help

task ready shows the next eligible task without changing task state.
Use --limit N to inspect multiple ready tasks in the same order; batch ready is currently implemented only for the flatfile backend.
task show prints one specific task without claiming it; task get remains available as an alias.
task next claims the returned task by moving it to inProgress.
handoff write appends by default, requires --message, and retains a global record unless --task selects a task; --replace removes older records only in that scope.
handoff get defaults to the newest global record; --task selects one task scope and --all-scopes includes global and task scopes. Human output hints at more records and other scopes.
handoff purge requires exactly one of --id, --global, --task, or --all-scopes; --before and --older-than delete only records older than the cutoff.
task-scoped handoff records are retained after task next/start claims and are attached to those claim results in newest-first order; global records are not auto-attached and claiming never consumes anything.
The retained collection is .wtp/handoffs.json with a {"handoffs":[...]} wrapper; a missing file is treated as empty and is not created by reads.
With --json, handoff write returns {"handoff":...,"scopeCount":...}, get returns {"handoffs":...,"totalMatching":...,"hasMore":...,"otherScopesAvailable":...}, and purge returns {"purged":...}.
When --agent is supplied, list/get/ready/next compute claimability using that same assignee-safety rule.
task update edits mutable task fields in place; pass --model= or any Git/worktree metadata option with = to clear that field.
On task create, omitted Git/worktree fields default independently from the current Git context; explicit empty values override those defaults.
Configuration is read from .wtp.json at the current Git worktree root (or the invocation directory outside Git); wtpDir selects storage and is relative to that file when not absolute.
The optional free-form model field records a suggested execution model and does not affect task ordering or claimability.
Dependencies accept UUIDs or short IDs and are stored as canonical UUIDs.
graph prints dependency trees for matching tasks; it defaults to todo and accepts done, paused, inProgress, todo, or all.
stats supports exactly four forms: wtp stats and wtp stats STATUS print an overview; wtp stats ATTRIBUTE and wtp stats STATUS ATTRIBUTE print a focused report. STATUS is todo, inProgress, paused, or done. ATTRIBUTE is model, lane, priority, estimate, assignee, comments, or dependencies; a status must precede an attribute. Overview JSON has totalTasks, statusCounts, attributes, comments, dependencies, and handoffs, plus status when filtered. statusCounts always includes all four statuses. Focused JSON has totalTasks, attribute, and exactly one of buckets, comments, or dependencies, plus status when filtered. Categorical buckets use value and count; model, lane, and assignee are lexical, while priority and estimate use their canonical order. Empty categorical values are value "" in JSON and (unset) in text.
The comments metrics count selected tasks with at least one comment and all comment records. The dependency metrics count selected tasks with direct dependencies, independent selected tasks, and the total number of direct dependency entries; dependencies are not deduplicated or expanded transitively. Overview handoffs include every global retained handoff and every task-scoped handoff; a status-filtered report keeps global handoffs and task-scoped handoffs for selected tasks, while allStatusTotal remains the count before filtering. Focused reports do not include handoff metrics.
Examples:
  wtp stats
  wtp stats model
  wtp stats done model
export writes an exact canonical snapshot to a dedicated directory, including the retained handoffs.json collection; unmanaged entries and paths overlapping active .wtp storage are rejected.
version reports the binary's version, commit, and build date; use wtp --json version for machine-readable output.
update installs a newer checksum-verified GitHub release over the running executable; use wtp --json update for machine-readable output.

Task IDs and scoped storage:
  A short ID is either wtp-NNNN (the legacy namespace) or
  wtp-BBBBBBBB-NNNN (a named-branch scope); B is exactly eight lowercase
  hexadecimal characters and N is at least four decimal digits.
  For a named branch, branchId is the first eight lowercase hexadecimal
  characters of SHA-256(branch name encoded as UTF-8). The branch name is the
  exact, case-sensitive Git short name: it is not normalized. For example,
  main hashes to 0d6e4079 and its first task is wtp-0d6e4079-0001.
  Named-branch tasks are written as .wtp/<status>/wtp-<branchId>-NNNN.json.
  The allocation record is .wtp/meta/index-<branchId>.json, for example:
    {"branch":"main","next":1}
  Detached HEAD and non-Git invocations have no branch scope. They use the
  legacy ID and .wtp/meta/index.json, for example {"next":1}; named-branch
  creation never changes that legacy index.
  The index next value is the next candidate sequence, starts at 1, and is
  advanced past every already-used short ID before allocation. It is written
  before the task file, so a failed task publication may leave a gap; writers
  must not reuse a lower sequence. Sequences are zero-padded to at least four
  digits and grow beyond four digits as needed. Allocation is serialized by
  the store's .wtp/meta/wtp.lock global lock.
  A scoped index branch value must equal the exact branch name for its token.
  A mismatch is an error (for example, "branch index token <branchId> belongs
  to branch <other>, not <current>") so a 32-bit token collision or stale
  index never shares an allocation sequence.
  task list may show current, legacy, and foreign scoped tasks. On a named
  branch, task ready and task next automatically consider current-scope tasks
  first and legacy tasks second; they never select foreign scoped tasks.
  Detached and non-Git ready/next selection is legacy-only. A foreign task can
  be started intentionally with task start <task-id>; normal lifecycle and
  dependency checks still apply.
  Scope follows the exact current branch name, not a branch object. Renaming a
  branch changes the prefix used for new tasks; existing IDs and filenames keep
  their old prefix, are not migrated, and are foreign to the renamed branch
  until explicitly started.
  On storage open, a valid canonical-UUID-named task file is migrated to the
  exact shortId filename in the same status directory. This compatibility
  migration does not rename short IDs, alter task JSON, or migrate old branch
  scopes; conflicting or invalid files are rejected without partial migration.
  export remains canonical: it writes one UUID-named JSON snapshot per task,
  plus handoffs.json, and does not export scoped short-ID filenames or indexes.

Usage Guide:
	1. Create tasks with title and optional metadata, including --model when a particular execution model is suggested.
	2. Inspect work with task list, task show, or task ready.
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
  wtp --agent Tony --create-task --title "..." --description "..." --model gpt-5 --dependencies wtp-0001
  wtp --export-tasks=.wtp-export

Legacy task action flags remain supported, and legacy --export-tasks is an alias for export; both export forms include handoffs.json.
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
	 handoffs.json
	 meta/
			index.json
			index-<branchId>.json
			wtp.lock (transient global allocation lock)
			locks/

Configuration and discovery:
	- In Git, .wtp.json is read only from the current worktree root; nested directories and linked worktrees use that worktree's configuration.
	- Outside Git, .wtp.json is read only from the invocation directory. Parent directories are not searched.
	- Without configuration, storage is <discovery directory>/.wtp.
	- The optional .wtp.json wtpDir property selects storage. Relative wtpDir values are resolved from the configuration file; absolute values are accepted.
	- Adding, changing, or removing .wtp.json never moves or deletes an existing store.

Task file rules:
	- Each task is stored as JSON in the directory matching its status.
	- Flat-file task filenames use the exact short ID: .wtp/<status>/<shortId>.json.
	  There are no per-branch subdirectories; for example, a task on main is
	  .wtp/todo/wtp-0d6e4079-0001.json.
	- The JSON payload includes both a canonical UUID id and a stable shortId.
	- Dependencies are stored as canonical UUID strings, even when CLI input uses short IDs.
	- New tasks and status moves use the shortId filename in the destination
	  status directory. A filename must be either that exact shortId plus .json
	  or the task's exact canonical UUID plus .json for legacy migration input.

Short IDs, branch scopes, and allocation indexes:
	- Legacy IDs have the form wtp-NNNN. Named-branch IDs have the form
	  wtp-BBBBBBBB-NNNN, where B is exactly eight lowercase hexadecimal
	  characters and N is at least four decimal digits. Both forms are valid in
	  task JSON and as CLI identifiers; the .json suffix is only a filename
	  suffix, not part of shortId.
	- For a named Git branch, compute branchId by hashing the exact,
	  case-sensitive short branch name as its UTF-8 bytes with SHA-256, taking
	  the first four digest bytes, and encoding those bytes as lowercase hex.
	  Do not trim, lowercase, or otherwise normalize the name. main therefore
	  has branchId 0d6e4079.
	- Named-branch allocation uses .wtp/meta/index-<branchId>.json. Its exact
	  shape is {"branch":"<exact branch name>","next":<positive integer>}.
	  The legacy unscoped allocator uses .wtp/meta/index.json with
	  {"next":<positive integer>}; named creates never rewrite that file.
	  The index's next value is the next candidate sequence, initially 1.
	- Under the store-wide .wtp/meta/wtp.lock, a writer reads its index, scans
	  all existing task shortIds, skips occupied candidates, increments next,
	  persists the index, and then publishes the task. A missing or stale index
	  is safe: allocation starts at its stored (or initial) value and skips all
	  occupied IDs. A task publication failure can leave an unused sequence gap;
	  later writers continue from next rather than reusing it.
	- A scoped index's branch must exactly match the current branch name. A
	  mismatch is rejected with a collision/stale-index error naming the token,
	  stored branch, current branch, and index path. Never merge two branch names
	  into one token or allocation sequence.

Scoped readiness and branch changes:
	- Ordinary listing can include current-scope, legacy, and foreign scoped
	  tasks. Foreign tasks are not automatically claimable.
	- On a named branch, task ready and task next select current-scope tasks
	  before legacy tasks and never select a foreign scope. On detached HEAD or
	  outside Git, there is no current scope and automatic selection is legacy
	  only. Explicit --git-branch metadata does not create a runtime scope.
	- task start <task-id> is the explicit path for an older or foreign scoped
	  task. It still enforces dependency and lifecycle checks.
	- Scope is derived from the branch name, not a branch object. After a branch
	  rename, new tasks use the new name's branchId; old short IDs, filenames,
	  and records are unchanged and are not automatically migrated or adopted.

Filename compatibility migration:
	- When storage opens, valid files named exactly <canonical task UUID>.json
	  are migrated to .wtp/<status>/<shortId>.json in the same status directory.
	  The payload's id and shortId do not change, and scoped short IDs migrate to
	  their scoped target filename just like legacy short IDs.
	- Invalid, mismatched, or conflicting files are rejected before migration
	  writes. Existing short-ID files and old branch scopes are not renamed by
	  this UUID-filename compatibility step.

Compatibility rule:
	- dependencies are stored as canonical UUID strings.
	- task files created before model metadata remain valid; an omitted model means no suggestion.
	- legacy task files named by canonical UUID are validated before migration to short-ID filenames.
	- comments created without an agent remain valid with an empty author.
	- a missing .wtp/handoffs.json is a legacy store with no retained handoffs; reads do not create it.

Task JSON schema:
	{
		"id": "25c3806a-bd1b-424d-889b-29e5b06679b8",
		"shortId": "wtp-0001",
		"title": "Implement parser",
		"description": "Add provider selection parsing",
		"priority": "high",
		"estimate": "m",
		"lane": "cli",
		"model": "gpt-5",
		"gitRepo": "/workspace/repo",
		"gitBranch": "feature/parser",
		"worktreeName": "parser",
		"worktreeDir": "/workspace/repo-parser",
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

Retained handoff JSON schema (.wtp/handoffs.json):
	{
		"handoffs": [
			{
				"id": "8b1f1f55-6d6a-4f5a-9ca1-2e91e3a72d40",
				"taskId": "25c3806a-bd1b-424d-889b-29e5b06679b8",
				"author": "Tony",
				"message": "Parser context for the next worker",
				"createdAt": "2026-04-21T12:34:56Z"
			}
		]
	}

Handoff field semantics:
	- handoffs: required array; an empty array is valid.
	- id: required canonical lowercase UUID string and unique within the collection.
	- taskId: optional canonical lowercase task UUID; omitted identifies the global scope.
	- author: optional non-blank string.
	- message: required non-blank, already-trimmed string.
	- createdAt: required RFC3339 timestamp in UTC.

Handoff behavior and CLI conflicts:
	- handoff write appends by default. --replace removes all older records in the selected global or task scope before adding the new record; every other scope is preserved.
	- handoff get returns newest-first records. With no scope option it returns the newest global record; --task accepts a task short ID or canonical UUID, while --all-scopes includes every scope. --task and --all-scopes conflict, as do --limit and --all.
	- handoff purge requires exactly one selector: --id, --global, --task, or --all-scopes. --before accepts RFC3339; --older-than accepts a positive Go duration such as 72h. They conflict with each other, and either conflicts with --id. A cutoff is exclusive: records with createdAt before it are deleted, while records at or after it are retained.
	- Handoff reads and task claims are non-consuming. task start and task next attach all retained task-scoped records for the claimed task, newest first; global records are not auto-attached, and task show, task get, and task list do not attach them.
	- Human get output suggests --all when more matching records exist and --all-scopes --all when another scope has records.

Handoff JSON response shapes:
	- handoff write: {"handoff": { ... }, "scopeCount": 1}.
	- handoff get: {"handoffs": [ ... ], "totalMatching": 1, "hasMore": false, "otherScopesAvailable": false}.
	- handoff purge: {"purged": 1}.

Field semantics:
	- id: required canonical lowercase UUID string.
	- shortId: required stable human-friendly identifier, format wtp-NNNN or
	  wtp-BBBBBBBB-NNNN (B is exactly 8 lowercase hexadecimal characters and N
	  is at least four decimal digits). The .json filename suffix is not part of
	  this value.
	- title: required non-empty string.
	- description: optional string.
	- priority: optional enum low|medium|high|urgent.
	- estimate: optional enum xs|s|m|l|xl.
	- lane: optional string for area/team grouping.
	- model: optional free-form string naming the suggested model for completing the task.
	  Set it with --model VALUE on task create/update/edit, or clear it with --model=.
	- gitRepo: optional absolute path to the primary Git worktree root where the task was created.
	- gitBranch: optional branch name where the task was created; empty for a detached HEAD unless explicitly overridden.
	- worktreeName: optional name of the current Git worktree where the task was created.
	- worktreeDir: optional absolute path to the current Git worktree root where the task was created.
	- status: required enum todo|inProgress|paused|done.
	- assignee: optional string.
	- dependencies: array of canonical lowercase task UUIDs.
	- comments: array of comment objects with a canonical lowercase UUID id, optional non-blank author,
	  required non-blank message, and required UTC createdAt.
	- createdAt, updatedAt: required RFC3339 timestamps in UTC.
	- startedAt, completedAt: optional RFC3339 timestamps in UTC.

Behavioral rules:
	- Task UUIDs and short IDs must be unique. A newer, valid status-move copy may coexist temporarily
	  with its older copy after interrupted cleanup; readers select the newer copy.
	- Comment UUIDs must be unique within a task.
	- A task cannot depend on itself.
	- Dependencies must reference existing task UUIDs.
	- Cyclic dependency graphs are invalid.
	- A task cannot start or be claimed until all dependencies are done.
	- Status determines the directory where the task file is stored.
	- createdAt must not be after updatedAt; comments and lifecycle timestamps must fall within that range.
	- todo has no lifecycle timestamps; inProgress and paused require startedAt but no completedAt;
	  done requires both, with completedAt not before startedAt.
	- task next prefers paused tasks before todo, then higher priority, then older tasks.
	- model is advisory metadata and does not affect task ordering or claimability.
	- task create discovers each Git/worktree field independently when it is omitted; an explicit value, including an empty value, overrides only that field.
	- task update and task edit preserve origin fields unless their corresponding option is supplied; pass --git-repo=, --git-branch=, --worktree-name=, or --worktree-dir= to clear one.
	- Handoff IDs are unique within .wtp/handoffs.json; malformed or corrupt handoff storage is rejected.

Export rules:
	- A successful export directory contains exactly one canonical UUID-named JSON file per current task and a handoffs.json collection; scoped short-ID filenames and allocation indexes are not exported.
	- handoffs.json has the same {"handoffs":[...]} shape as .wtp/handoffs.json and contains the exact retained collection, including an empty array when no handoffs exist.
	- Current task and handoff snapshot files are atomically replaced; stale canonical UUID-named task files are removed.
	- Unmanaged entries are reported before changes, and export paths overlapping active .wtp storage are rejected.
	- Legacy --export-tasks=<directory> remains an alias for export and includes handoffs.json too.

Legacy task compatibility:
	- The legacy task action flags remain supported: --get-next-task, --get-tasks, --get-task, --set-task-in-progress, --set-task-paused, --set-task-done, --add-comment, and --create-task.

Interoperability guidance:
	- Programs that write wtp flat files should preserve unknown future fields when possible.
	- Writers must update updatedAt whenever a task changes.
	- Writers must keep the task file in the directory that matches status.
	- Writers must use the exact shortId filename in the matching status directory;
	  do not add a branch subdirectory or include .json in the shortId field.
	- Writers creating a task in a named branch should use the exact branch hash
	  algorithm above and its index-<branchId>.json record; detached/non-Git
	  writers should use the legacy index.json record. Keep each scoped index's
	  branch and next fields intact, and reject branch-token mismatches.
	- Writers should serialize allocation with .wtp/meta/wtp.lock, skip every
	  already-used shortId, advance next before publication, and tolerate gaps
	  after failed publication. Do not rewrite the legacy index for scoped work.
	- Writers migrating UUID-named files should validate every file and its
	  target before changing paths, preserving the task payload and unknown
	  fields. Do not migrate old short IDs when a branch is renamed.
	- Canonical export is unchanged: export task records under canonical UUID
	  filenames and preserve the task JSON shape; short-ID indexes stay in active
	  storage only.
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

func defaultedOption(option optionString, fallback string) string {
	if option.set {
		return option.value
	}
	return fallback
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
	model := ""
	var gitRepo optionString
	var gitBranch optionString
	var worktreeName optionString
	var worktreeDir optionString
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
		case "--model":
			i++
			if i < len(args) {
				model = args[i]
			}
		case "--git-repo":
			gitRepo.set = true
			i++
			if i < len(args) {
				gitRepo.value = args[i]
			}
		case "--git-branch":
			gitBranch.set = true
			i++
			if i < len(args) {
				gitBranch.value = args[i]
			}
		case "--worktree-name":
			worktreeName.set = true
			i++
			if i < len(args) {
				worktreeName.value = args[i]
			}
		case "--worktree-dir":
			worktreeDir.set = true
			i++
			if i < len(args) {
				worktreeDir.value = args[i]
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
			if value, found := strings.CutPrefix(arg, "--git-repo="); found {
				gitRepo = optionString{set: true, value: value}
				continue
			}
			if value, found := strings.CutPrefix(arg, "--git-branch="); found {
				gitBranch = optionString{set: true, value: value}
				continue
			}
			if value, found := strings.CutPrefix(arg, "--worktree-name="); found {
				worktreeName = optionString{set: true, value: value}
				continue
			}
			if value, found := strings.CutPrefix(arg, "--worktree-dir="); found {
				worktreeDir = optionString{set: true, value: value}
				continue
			}
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
		out := append(append(rest, "task", "pause"), withValue("--agent", agent)...)
		return legacyParseResult{args: append(out, taskID), found: true}, nil
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
		out = append(out, withValue("--model", model)...)
		out = append(out, withOptionalValue("--git-repo", gitRepo)...)
		out = append(out, withOptionalValue("--git-branch", gitBranch)...)
		out = append(out, withOptionalValue("--worktree-name", worktreeName)...)
		out = append(out, withOptionalValue("--worktree-dir", worktreeDir)...)
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

func withOptionalValue(flagName string, option optionString) []string {
	if !option.set {
		return nil
	}
	if option.value == "" {
		return []string{flagName + "="}
	}
	return []string{flagName, option.value}
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

func displayModel(value string) string {
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

// parseTaskTargetOptions accepts one task ID plus a deliberately small set of
// long options. It supports both --name value and --name=value so command
// handlers can keep the documented task-id-first spelling while still letting
// callers put options first.
func parseTaskTargetOptions(args []string, allowedNames ...string) (string, map[string]string, error) {
	allowed := make(map[string]struct{}, len(allowedNames))
	for _, name := range allowedNames {
		allowed[name] = struct{}{}
	}

	options := make(map[string]string, len(allowedNames))
	positionals := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			nameAndValue := strings.TrimPrefix(arg, "--")
			name, value, hasValue := strings.Cut(nameAndValue, "=")
			if _, ok := allowed[name]; !ok {
				return "", nil, fmt.Errorf("unknown option %q", arg)
			}
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return "", nil, fmt.Errorf("option %q requires a value", "--"+name)
				}
				value = args[i+1]
				i++
			}
			if strings.TrimSpace(value) == "" {
				return "", nil, fmt.Errorf("option %q requires a value", "--"+name)
			}
			options[name] = value
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", nil, fmt.Errorf("unknown option %q", arg)
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 {
		return "", nil, errors.New("expected exactly one task ID")
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

func optionValue(options map[string]string, name string) string {
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
