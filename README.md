# wtp

`wtp` is a self-contained Go CLI for agent-oriented task workflow management.

It is designed for repos where agents need a narrow, scriptable task interface without depending on Jira, Trello, or a web UI. The default backend is local flat-file storage under `.wtp/`, and the CLI is structured so remote providers can be added later without changing the task model.

## Current Status

Implemented today:

- Go CLI with `task` subcommands
- local flat-file backend under `.wtp/`
- canonical task model with dependency validation
- comments, status transitions, export, and JSON output
- compatibility translation for the legacy README-style action flags
- repo-local locking for concurrent writers on the same filesystem
- atomic `task next` claim behavior

The broader scope and roadmap remain in [PLAN.md](/home/matty/dev/wtproj/PLAN.md).

## Install

Build a local binary:

```sh
go build -o wtp ./cmd/wtp
```

Or run it directly during development:

```sh
go run ./cmd/wtp task list
```

`wtp` assumes the current working directory is the repo or worktree it should manage.

## Quick Start

Create a task:

```sh
wtp task create \
  --title "Implement parser" \
  --description "Add provider selection parsing"
```

List tasks:

```sh
wtp task list
wtp task list --status todo
```

Claim the next eligible task for an agent:

```sh
wtp task next --agent Tony
```

That command is not read-only. It selects the next eligible task and immediately moves it to `inProgress` under the repo lock so two agents do not claim the same task at the same time.

Work a task explicitly:

```sh
wtp task start wtp-0002 --agent Tony
wtp task comment wtp-0002 --agent Tony --message "Implemented parser"
wtp task pause wtp-0002
wtp task done wtp-0002
```

Inspect one task:

```sh
wtp task get wtp-0002
wtp --json task get wtp-0002
```

## Command Surface

Primary command style:

```sh
wtp task next --agent Tony
wtp task list --status todo
wtp task get <task-id>
wtp task start <task-id> --agent Tony
wtp task pause <task-id>
wtp task done <task-id>
wtp task comment <task-id> --message "Implemented parser"
wtp task create --title "New Task" --description "..." --depends-on wtp-0001,wtp-0002
wtp export --out .wtp-export
```

Legacy compatibility mode:

```sh
wtp --agent Tony --get-next-task
wtp --agent Tony --get-tasks --status todo
wtp --agent Tony --get-task --task-id wtp-0001
wtp --agent Tony --set-task-in-progress --task-id wtp-0001
wtp --agent Tony --set-task-paused --task-id wtp-0001
wtp --agent Tony --set-task-done --task-id wtp-0001
wtp --agent Tony --add-comment --task-id wtp-0001 --comment "Implemented parser"
wtp --agent Tony --create-task --title "..." --description "..." --dependencies wtp-0001
wtp --export-tasks=.wtp-export
```

Compatibility mode accepts exactly one legacy action flag per invocation.

## Task Semantics

Canonical statuses:

- `todo`
- `inProgress`
- `paused`
- `done`

Identifier rules:

- each task has a canonical UUID in the JSON payload
- each task also has a stable short ID such as `wtp-0005`
- CLI input accepts either UUID or short ID
- flat-file task filenames use the short ID, for example `.wtp/todo/wtp-0005.json`

Dependency rules:

- dependencies are stored as canonical UUIDs
- create rejects missing dependencies
- create rejects cyclic dependencies
- a task cannot be started or claimed while any dependency is not `done`

`task next` ordering:

- eligible tasks are only `paused` or `todo`
- blocked tasks are excluded
- `paused` tasks are preferred before `todo`
- within a status bucket, older tasks win
- if `--agent` is supplied, already-assigned matching tasks are preferred first
- if none match, eligible unassigned tasks are the fallback

## Local Storage

Default layout:

```text
.wtp/
  todo/
  inProgress/
  paused/
  done/
  meta/
```

Task files live in the directory for their current status:

```text
.wtp/todo/wtp-0005.json
.wtp/inProgress/wtp-0004.json
```

Metadata:

- `.wtp/meta/index.json` stores the next short-ID counter
- `.wtp/meta/wtp.lock` is the repo-local lock file used for serialized writes and atomic claiming

The storage is intentionally human-readable and git-friendly, but it is not a database transaction engine. The lock file protects concurrent local agents from racing on create, claim, status update, and comment operations.

## Configuration

If `.wtp.json` is absent, `wtp` uses the local flat-file backend.

Example Trello-oriented config:

```json
{
  "tool": "trello",
  "apiKeyEnv": "TRELLO_API_KEY",
  "tokenEnv": "TRELLO_TOKEN",
  "boardId": "your-trello-board-id",
  "listIds": {
    "todo": "your-trello-todo-list-id",
    "inProgress": "your-trello-in-progress-list-id",
    "paused": "your-trello-paused-list-id",
    "done": "your-trello-done-list-id"
  }
}
```

Current provider behavior:

- `flatfile`: fully implemented
- `trello`: config validation exists, but the provider is not implemented yet

## JSON Output

Use `--json` on the root command to emit canonical JSON to stdout:

```sh
wtp --json task list
wtp --json task get wtp-0005
wtp --json task next --agent Tony
```

Errors remain on stderr.

## Concurrency Notes

`wtp` is intended to be safe for multiple agents working in the same repo on the same filesystem, within the limits of a flat-file backend:

- mutating operations acquire a repo-local lock
- `task next` claims work under the same lock instead of returning a merely advisory result
- short IDs are allocated under lock so concurrent creates do not reuse numbers

What this does not provide:

- multi-file rollback
- database-style isolation across machines or unreliable shared filesystems
- protection against manual edits that bypass the lock

## Repository Layout

The codebase is being built toward:

```text
cmd/wtp/
internal/cli/
internal/config/
internal/core/
internal/provider/
internal/provider/flatfile/
internal/provider/trello/
docs/
SKILL.md
```

The repo-local `.wtp/` backlog is also part of the intended workflow.
