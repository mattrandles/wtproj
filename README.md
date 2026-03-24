# wtp

`wtp` is a planned self-contained Go CLI for agent-oriented task workflow management.

The project goal is simple: give coding agents a scriptable task interface inside the current repository without turning the agent runner into a project-management UI. The first release is designed to work locally with no external service by default, while keeping a clean provider abstraction for future integrations such as Trello.

## Status

This repository is currently in the planning stage.

There is no shipped CLI yet. The current source of truth for scope and implementation direction is [PLAN.md](/home/matty/dev/wtproj/PLAN.md).

## Why This Exists

Existing tools like Jira, Trello, and similar project-management systems already solve the human-facing side of task tracking. Agents need something narrower:

- easy to invoke from scripts and terminals
- usable inside the current worktree
- independent of any specific SaaS in the first release
- extensible enough to add remote backends later

`wtp` is intended to fill that gap.

## Planned UX

The primary interface is planned as subcommands:

```sh
wtp task next --agent Tony
wtp task list --status todo
wtp task get <task-id>
wtp task start <task-id> --agent Tony
wtp task pause <task-id>
wtp task done <task-id>
wtp task comment <task-id> --message "Implemented parser"
wtp task create --title "New Task" --description "..." --depends-on id1,id2
wtp export --out .wtp
```

Compatibility aliases are also planned:

```sh
wtp --agent Tony --get-next-task
wtp --agent Tony --get-tasks --status todo
wtp --agent Tony --get-task --task-id 123
wtp --agent Tony --set-task-in-progress --task-id 123
wtp --agent Tony --set-task-paused --task-id 123
wtp --agent Tony --set-task-done --task-id 123
wtp --agent Tony --add-comment --task-id 123 --comment "Implemented parser"
wtp --agent Tony --create-task --title "New Task" --description "..." --dependencies 123,124
wtp --export-tasks=.wtp
```

## Planned Behavior

- If `.wtp.json` exists, `wtp` will load the configured provider.
- If `.wtp.json` does not exist, `wtp` will default to a local flat-file backend rooted at `.wtp/`.
- Tasks will use a minimal canonical model so storage and provider integrations share one contract.
- Dependency validation, cycle rejection, and deterministic next-task selection are core v1 requirements.
- `--json` output is planned from the start for machine-readable automation.

## Local Backend Design

The default backend is planned as a flat-file store:

```text
.wtp/
  todo/
  inProgress/
  paused/
  done/
  meta/
```

Each task will live as a single JSON file in the directory that matches its status. The local format is intentionally simple so it can be inspected manually and later imported or exported to other systems.

## Configuration

Remote providers are planned to be configured through `.wtp.json`. For example:

```json
{
  "tool": "trello",
  "apiKeyEnv": "TRELLO_API_KEY",
  "boardId": "your-trello-board-id",
  "listIds": {
    "todo": "your-trello-list-id-for-todo",
    "inProgress": "your-trello-list-id-for-in-progress",
    "paused": "your-trello-list-id-for-paused",
    "done": "your-trello-list-id-for-done"
  }
}
```

In v1, omitting `.wtp.json` will mean "use the local flat-file provider."

## Scope

Planned v1 goals:

- self-contained Go CLI
- local flat-file backend under `.wtp/`
- canonical task model with stable statuses
- task creation, listing, lookup, comments, status transitions, and next-task selection
- provider abstraction that allows future integrations without reshaping the CLI

Explicit non-goals for v1:

- full Trello integration
- rich PM features such as labels, due dates, sprints, attachments, or permissions
- multi-user locking and conflict resolution beyond safe file-writing conventions
- web UI or background service

## Repository Direction

The plan currently calls for a structure along these lines:

```text
cmd/wtp/
internal/cli/
internal/config/
internal/core/
internal/provider/
internal/provider/flatfile/
internal/provider/trello/
internal/store/
docs/
SKILL.md
```

That structure is not implemented yet; it is the intended shape for the first build-out.

## Next Steps

The implementation plan in [PLAN.md](/home/matty/dev/wtproj/PLAN.md) breaks the work into:

1. Foundation and CLI scaffolding
2. Flat-file provider
3. Compatibility aliases and JSON output
4. Documentation and agent workflow guidance
5. Future-ready provider scaffolding

## Contributing

At this stage, the most useful contributions are clarifying the plan, tightening the CLI contract, and keeping the canonical task model stable before implementation begins.
