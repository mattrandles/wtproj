package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
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
	case "export":
		return runExport(ctx, rest[1:])
	case "help":
		return usage(stdout)
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

func runTask(ctx context, args []string) error {
	if len(args) == 0 {
		return errors.New("task subcommand is required")
	}
	switch args[0] {
	case "create":
		return runTaskCreate(ctx, args[1:])
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
	agent := flags.String("agent", "", "assignee")
	dependsOn := flags.String("depends-on", "", "comma-separated task identifiers")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task create --title \"...\" [--description \"...\"] [--depends-on a,b] [--agent Tony]")
	}
	if strings.TrimSpace(*title) == "" {
		return errors.New("task title is required")
	}
	task, err := ctx.provider.CreateTask(core.CreateTaskInput{
		Title:        *title,
		Description:  *description,
		Assignee:     *agent,
		Dependencies: splitCSV(*dependsOn),
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: wtp task list [--status todo|inProgress|paused|done]")
	}
	filter := provider.TaskFilter{}
	if strings.TrimSpace(*statusValue) != "" {
		status, err := core.ParseStatus(*statusValue)
		if err != nil {
			return err
		}
		filter.Status = &status
	}
	tasks, err := ctx.provider.ListTasks(filter)
	if err != nil {
		return err
	}
	return printValue(ctx, tasks)
}

func runTaskGet(ctx context, args []string) error {
	args = trimEmptyArgs(args)
	if len(args) != 1 {
		return errors.New("usage: wtp task get <task-id>")
	}
	task, err := ctx.provider.GetTask(args[0])
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
	agent := flags.String("agent", "", "preferred assignee")
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
	case core.Task:
		return printTask(ctx.stdout, typed)
	case []core.Task:
		for _, task := range typed {
			if _, err := fmt.Fprintf(ctx.stdout, "%s\t%s\t%s\t%s\n", task.ShortID, task.Status, displayAssignee(task.Assignee), task.Title); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported output type %T", value)
	}
}

func printTask(w io.Writer, task core.Task) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n", task.ShortID, task.ID); err != nil {
		return err
	}
	lines := []string{
		fmt.Sprintf("title: %s", task.Title),
		fmt.Sprintf("status: %s", task.Status),
		fmt.Sprintf("assignee: %s", displayAssignee(task.Assignee)),
		fmt.Sprintf("created: %s", task.CreatedAt.Format(timeLayout)),
		fmt.Sprintf("updated: %s", task.UpdatedAt.Format(timeLayout)),
	}
	if task.Description != "" {
		lines = append(lines, fmt.Sprintf("description: %s", task.Description))
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

func usage(w io.Writer) error {
	_, err := io.WriteString(w, `wtp

Commands:
  wtp task create --title "..." [--description "..."] [--depends-on a,b] [--agent Tony]
  wtp task list [--status todo|inProgress|paused|done]
  wtp task get <task-id>
  wtp task start <task-id> [--agent Tony]
  wtp task pause <task-id>
  wtp task done <task-id>
  wtp task comment <task-id> --message "..."
  wtp task next [--agent Tony]
  wtp export --out .wtp-export

task next claims the returned task by moving it to inProgress.

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

func rewriteLegacyArgs(args []string) (legacyParseResult, error) {
	actions := []string{}
	agent := ""
	taskID := ""
	comment := ""
	title := ""
	description := ""
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
			args:  append(append(rest, "task", "list"), withValue("--status", status)...),
			found: true,
		}, nil
	case "get":
		if strings.TrimSpace(taskID) == "" {
			return legacyParseResult{}, errors.New("legacy --get-task requires --task-id")
		}
		return legacyParseResult{args: append(rest, "task", "get", taskID), found: true}, nil
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
