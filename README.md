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

Remaining work and release planning are tracked in the repo-local `.wtp/` backlog.

## Install

Published [GitHub Releases](https://github.com/mattrandles/wtproj/releases)
are the only supported distribution channel. There are no package-manager,
or installer releases. Contributor source builds are for development only, not
a supported installation or upgrade channel. Release assets are standalone,
uncompressed executables; use the exact filename for your operating system and
architecture:

| Platform | Asset |
| --- | --- |
| macOS AMD64 | `wtp_darwin_amd64` |
| macOS Apple silicon | `wtp_darwin_arm64` |
| Linux AMD64 | `wtp_linux_amd64` |
| Linux ARM64 | `wtp_linux_arm64` |
| Windows AMD64 | `wtp_windows_amd64.exe` |
| Windows ARM64 | `wtp_windows_arm64.exe` |

Each release also contains `checksums.txt`. Download it with the executable
and verify the SHA-256 digest before installing. The filenames are stable, so
the following commands use GitHub's `latest/download` URL; replace `latest`
with a specific release tag when you need to pin an earlier version.

### Unix (macOS and Linux)

Set `asset` to one of the Unix asset names above. This example selects Linux
AMD64; macOS users should substitute the matching `darwin` asset.

```sh
asset=wtp_linux_amd64
base=https://github.com/mattrandles/wtproj/releases/latest/download
curl --fail --location --remote-name "$base/$asset"
curl --fail --location --remote-name "$base/checksums.txt"
grep -F "  $asset" checksums.txt | sha256sum --check --status -
chmod 755 "$asset"
```

On macOS, replace the checksum command with:

```sh
grep -F "  $asset" checksums.txt | shasum --algorithm 256 --check --status -
```

Only continue when the checksum command exits successfully. Choose one of
these installation scopes after verification:

```sh
# Project-local: keep the binary in this checkout and use it in this shell.
mkdir -p .tools/bin
install -m 755 "$asset" .tools/bin/wtp
export PATH="$PWD/.tools/bin:$PATH"

# User-local: persist ~/.local/bin in your shell startup file if it is not already on PATH.
mkdir -p "$HOME/.local/bin"
install -m 755 "$asset" "$HOME/.local/bin/wtp"

# Global: requires administrator permission and a globally writable PATH directory.
sudo install -m 755 "$asset" /usr/local/bin/wtp
```

Run `wtp version` to confirm the installed release. `wtp` uses the current
directory as the project or worktree it manages, not its installation
directory.

### Windows (PowerShell)

Set `$asset` to the exact Windows asset for your processor. The following is
the AMD64 example. It downloads and verifies the release before copying it to
the chosen location.

```powershell
$asset = "wtp_windows_amd64.exe"
$base = "https://github.com/mattrandles/wtproj/releases/latest/download"
Invoke-WebRequest "$base/$asset" -OutFile $asset
Invoke-WebRequest "$base/checksums.txt" -OutFile checksums.txt
$line = @(Get-Content checksums.txt | Where-Object { $_ -match "  $([regex]::Escape($asset))$" })
if ($line.Count -ne 1) { throw "checksums.txt must contain one entry for $asset" }
$expected = $line[0].Substring(0, 64)
$actual = (Get-FileHash $asset -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "SHA-256 verification failed for $asset" }
```

Then choose one scope (the first two commands add the location to the current
PowerShell session; add the user path permanently if desired):

```powershell
# Project-local
$projectBin = Join-Path $PWD ".tools\\bin"
New-Item -ItemType Directory -Force $projectBin | Out-Null
Move-Item $asset (Join-Path $projectBin "wtp.exe")
$env:Path = "$projectBin;$env:Path"

# User-local
$userBin = Join-Path $HOME "bin"
New-Item -ItemType Directory -Force $userBin | Out-Null
Move-Item $asset (Join-Path $userBin "wtp.exe")
[Environment]::SetEnvironmentVariable("Path", "$userBin;" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
$env:Path = "$userBin;$env:Path"

# Global: run an elevated PowerShell session.
$globalBin = Join-Path $env:ProgramFiles "wtp"
New-Item -ItemType Directory -Force $globalBin | Out-Null
Move-Item $asset (Join-Path $globalBin "wtp.exe")
[Environment]::SetEnvironmentVariable("Path", "$globalBin;" + [Environment]::GetEnvironmentVariable("Path", "Machine"), "Machine")
```

Open a new terminal after changing a persistent Windows `PATH`, then run
`wtp version`. The global install and `wtp update` require permission to write
both the executable and its containing directory; use an elevated PowerShell
session or a user-local installation when that is not appropriate.

### Updating, stability, and storage

Released binaries report their embedded version, commit, and build date with
`wtp version` (`wtp --json version` is machine-readable). Use `wtp update` to
check the latest published stable GitHub Release and update the executable that
is actually running. It accepts strict Semantic Versioning: an equal or older
release is a no-op, and development builds cannot self-update. Prereleases are
not selected by the GitHub latest-release endpoint, so install a prerelease
manually from its release page if you intentionally want one.

The updater selects only the matching platform asset, checks its `checksums.txt`
SHA-256 entry, and leaves the installed executable untouched if discovery,
download, or validation fails. On Unix it atomically replaces the resolved
target while preserving its permission bits; a symlink used to launch `wtp`
continues to point at that target. On Windows it stages a verified executable,
then uses a detached helper after `wtp.exe` exits; a failed deferred replacement
restores the old binary and records the error as
`wtp.exe.wtp-update-error.txt` next to the executable.

Installing or updating never moves, deletes, or migrates a project's `.wtp/`
storage. The flat-file provider is the implemented and supported provider; it
also recognizes legacy UUID-named task files and safely migrates them to the
current short-ID filenames when the storage is opened. Commit or back up
`.wtp/` before upgrading so you can review any normal task-storage changes made
by a newer binary. A `.wtp.json` configuration with `"tool": "trello"` is
validated, but the Trello provider itself is not implemented and cannot manage
tasks.

The exact release assets, checksum format, API lookup, and updater contract are
defined in the [GitHub Release asset contract](docs/release-assets.md).
The reproducible direct-download release validation harness is documented in
[Direct-download release QA](docs/release-qa.md).

## Quick Start

Create a task:

```sh
wtp task create \
  --title "Implement parser" \
  --description "Add provider selection parsing" \
  --priority high \
  --estimate m \
  --lane cli \
  --model gpt-5.2-codex
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
wtp task update wtp-0002 --depends-on wtp-0001 --priority high --model gpt-5.2-codex
wtp task edit wtp-0002 --description "Parser now handles provider selection"
wtp task edit wtp-0002 --model=
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
wtp version
wtp update
```

## Command Surface

Primary command style:

```sh
wtp task next --agent Tony
wtp task list --status todo --agent Tony
wtp task show <task-id> [--agent Tony]
wtp task get <task-id> [--agent Tony]
wtp task start <task-id> --agent Tony
wtp task update <task-id> [--title "..."] [--description "..."] [--priority low|medium|high|urgent] [--estimate xs|s|m|l|xl] [--lane backend] [--model gpt-5] [--depends-on wtp-0001,wtp-0002] [--agent Tony]
wtp task edit <task-id> [same options as update]
wtp task pause <task-id> [--agent Tony]
wtp task done <task-id> [--agent Tony]
wtp task comment <task-id> [--agent Tony] --message "Implemented parser"
wtp task ready --agent Tony
wtp task ready --agent Tony --limit 3
wtp task create --title "New Task" --description "..." --priority high --estimate m --lane backend --model gpt-5 --depends-on wtp-0001,wtp-0002
wtp graph [--status todo|inProgress|paused|done|all]
wtp export --out .wtp-export
wtp version
wtp update
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
wtp --agent Tony --create-task --title "..." --description "..." --priority high --estimate m --lane backend --model gpt-5 --dependencies wtp-0001
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

- each task has a unique canonical lowercase UUID in the JSON payload
- each task also has a unique stable short ID such as `wtp-0005` (`wtp-` plus at least four digits)
- tasks may optionally include scheduling metadata (`priority`, `estimate`, and `lane`) plus a free-form suggested `model`
- CLI input accepts either UUID or short ID
- flat-file task filenames use the short ID, for example `.wtp/todo/wtp-0005.json`

Dependency rules:

- dependencies are stored as canonical UUIDs
- create rejects missing dependencies
- update/edit resolve short IDs or UUIDs and then store canonical UUIDs
- create rejects cyclic dependencies
- update/edit reject cyclic dependencies
- update/edit preserve status history while changing mutable task fields
- `model` is advisory metadata only; it does not affect task ordering or claimability
- clear a suggested model with `wtp task update <task-id> --model=`
- a task cannot be started or claimed while any dependency is not `done`

Lifecycle and comment rules:

- `createdAt` cannot be after `updatedAt`; comment and lifecycle timestamps must fall within that range
- `todo` has neither lifecycle timestamp; `inProgress` and `paused` require `startedAt`; `done` requires both `startedAt` and `completedAt`
- comment IDs are unique canonical lowercase UUIDs and messages are non-empty; an empty author represents a legacy anonymous comment

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

## Export Snapshots

`wtp export --out <directory>` treats its destination as a dedicated exact snapshot. After a successful export, the directory contains exactly one `<canonical-task-uuid>.json` file for every current task. Existing files for current tasks are atomically replaced, then stale canonical UUID-named JSON files are removed in lexical order.

Export preflights an existing destination before changing it. A directory, symlink, special file, or filename outside the canonical UUID JSON format is unmanaged; all unmanaged names are reported in sorted order and nothing in the destination is changed. Use a new, empty, or previously generated export directory rather than a directory containing other data.

The destination and active `.wtp` storage paths are resolved through symlinks before comparison. Export rejects the storage directory itself, any destination inside it, and any destination that contains it. Cleanup is limited to preflighted stale files directly inside the resolved export directory.

The policy is the same on Unix and Windows. Individual JSON files use durable atomic replacement (`rename` plus directory sync on Unix and replace-existing/write-through on Windows). The directory as a whole is not a database transaction: an I/O failure can leave a partially refreshed snapshot, but it never triggers broad directory deletion; rerunning export converges it to the exact current snapshot.

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
wtp --json update
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
