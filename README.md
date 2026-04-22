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
- Windows-compatible build and verification flow with PowerShell scripts and CI coverage

The broader scope and roadmap remain in [PLAN.md](/home/matty/dev/wtproj/PLAN.md).

## Install

For day-to-day local development, install `wtp` into your user `PATH`:

```sh
./scripts/install_local.sh
```

That builds the current source tree and installs the binary to `~/.local/bin/wtp` by default. In this repo, `~/.local/bin` is already on `PATH`, so future shells can run `wtp` directly.

To update the installed binary after making code changes, rerun the same script:

```sh
./scripts/install_local.sh
```

Override the target directory or binary name if needed:

```sh
WTP_INSTALL_DIR="$HOME/bin" ./scripts/install_local.sh
WTP_INSTALL_NAME=wtp-dev ./scripts/install_local.sh
```

On Windows / PowerShell:

```powershell
./scripts/install_local.ps1
```

That installs `wtp.exe` to `$HOME/.local/bin` by default, with the same `WTP_INSTALL_DIR` and `WTP_INSTALL_NAME` overrides available through environment variables.

If you only want a repo-local build artifact instead of installing into `PATH`, build it directly:

```sh
go build -o wtp ./cmd/wtp
```

On Windows, build:

```powershell
go build -o wtp.exe ./cmd/wtp
```

Or run it directly during development:

```sh
go run ./cmd/wtp task list
```

`wtp` assumes the current working directory is the repo or worktree it should manage.

Supported today:

- macOS
- Linux
- Windows

## Quick Start

Create a task:

```sh
wtp task create \
  --title "Implement parser" \
  --description "Add provider selection parsing" \
  --priority high \
  --estimate m \
  --lane cli
```

List tasks:

```sh
wtp task list
wtp task list --status todo
wtp task list --status todo --agent Tony
```

Claim the next eligible task for an agent:

```sh
wtp task next --agent Tony
```

That command is not read-only. It selects the next eligible task and immediately moves it to `inProgress` under the repo lock so two agents do not claim the same task at the same time.

Inspect the next eligible task without claiming it:

```sh
wtp task ready --agent Tony
wtp task ready --agent Tony --limit 3
```

Work a task explicitly:

```sh
wtp task start wtp-0002 --agent Tony
wtp task update wtp-0002 --depends-on wtp-0001 --priority high
wtp task edit wtp-0002 --description "Parser now handles provider selection"
wtp task comment wtp-0002 --agent Tony --message "Implemented parser"
wtp task pause wtp-0002
wtp task done wtp-0002
```

Inspect one task without claiming it:

```sh
wtp task show wtp-0002
wtp task show wtp-0002 --agent Tony
wtp --json task show wtp-0002
```

Discover usage or inspect the flat-file contract:

```sh
wtp help
wtp schema
wtp graph
```

## Command Surface

Primary command style:

```sh
wtp task next --agent Tony
wtp task list --status todo --agent Tony
wtp task show <task-id> [--agent Tony]
wtp task get <task-id> [--agent Tony]
wtp task start <task-id> --agent Tony
wtp task update <task-id> [--title "..."] [--description "..."] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--depends-on wtp-0001,wtp-0002] [--agent Tony]
wtp task edit <task-id> [same options as update]
wtp task pause <task-id>
wtp task done <task-id>
wtp task comment <task-id> --message "Implemented parser"
wtp task ready --agent Tony
wtp task ready --agent Tony --limit 3
wtp task create --title "New Task" --description "..." --priority high --estimate m --lane backend --depends-on wtp-0001,wtp-0002
wtp graph [--status todo|inProgress|paused|done|all]
wtp export --out .wtp-export
wtp help
wtp schema
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
wtp --agent Tony --create-task --title "..." --description "..." --priority high --estimate m --lane backend --dependencies wtp-0001
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
- tasks may optionally include scheduling metadata: `priority`, `estimate`, and `lane`
- CLI input accepts either UUID or short ID
- flat-file task filenames use the short ID, for example `.wtp/todo/wtp-0005.json`

Dependency rules:

- dependencies are stored as canonical UUIDs
- create rejects missing dependencies
- update/edit resolve short IDs or UUIDs and then store canonical UUIDs
- create rejects cyclic dependencies
- update/edit reject cyclic dependencies
- update/edit preserve status history while changing mutable task fields
- a task cannot be started or claimed while any dependency is not `done`

Help and schema output:

- `wtp help` prints the full command surface and a short usage guide
- `wtp schema` prints the flat-file layout, JSON field contract, behavioral rules, and interoperability notes for other tools

Graph output:

- `wtp graph` prints an ASCII tree of tasks and their dependencies
- `wtp graph` defaults to `todo` tasks
- `wtp graph --status ...` accepts `todo`, `inProgress`, `paused`, `done`, or `all`
- `wtp --json graph --status all` emits the same graph as structured JSON

Derived readiness metadata:

- read commands return a `readiness` object in JSON output
- `readiness.claimable` reflects current eligibility for the supplied `--agent` context
- `readiness.blocked` and `readiness.blockedReason` explain unresolved dependency blockers
- `readiness.dependencyCount` and `readiness.reverseDependencyCount` summarize graph position

`task next` ordering:

- eligible tasks are only `paused` or `todo`
- blocked tasks are excluded
- `paused` tasks are preferred before `todo`
- within a status bucket, higher priority wins before age
- priority values are `low`, `medium`, `high`, `urgent`
- estimate values are `xs`, `s`, `m`, `l`, `xl`
- within the same priority, older tasks win
- if `--agent` is supplied, already-assigned matching tasks are preferred first
- if none match, eligible unassigned tasks are the fallback
- tasks assigned to a different agent are not claimable via `task next --agent ...`

`task ready` uses the same eligibility rules and output shape as `task next`, but it does not change task state. Use `task ready --limit N` to inspect multiple ready tasks in priority order without claiming them. When no work is eligible, `task ready` succeeds and reports an empty result instead of treating the empty queue as an operational error. The current supported backend for batch read-only selection is the local `flatfile` backend.

`task show` prints a specific task without changing task state. `task get` remains available as an alias.

`task list --agent ...` and `task show ... --agent ...` use the same assignee-safety rule when reporting `claimable`.

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
wtp --json task show wtp-0005
wtp --json task show wtp-0005 --agent Tony
wtp --json task ready --agent Tony
wtp --json task ready --agent Tony --limit 3
wtp --json task next --agent Tony
wtp --json graph --status all
```

Errors remain on stderr.

## Verification

For a full local verification pass before committing:

```sh
./scripts/check.sh
```

On Windows or from PowerShell:

```powershell
./scripts/check.ps1
```

The Unix and PowerShell entrypoints run the same verification flow:

- `gofmt -l ./cmd ./internal`
- `go test ./...`
- a compiled-binary smoke test (`./scripts/e2e_smoke.sh` or `./scripts/e2e_smoke.ps1`)

The smoke test builds the CLI, creates a temporary repo, exercises create/list/claim/comment/pause/done/export flows, and verifies legacy compatibility mode against the compiled binary.

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
skills/task-management/skill.md
```

The repo-local `.wtp/` backlog is also part of the intended workflow.
