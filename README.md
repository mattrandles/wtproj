# wtp

`wtp` is a small command-line task manager for repositories. Keep work close to the code, let agents coordinate safely, and get on with the good bit: making things.

It stores tasks as readable files in the repository, so there is no hosted service or account to set up.

![wtp demo](demo.gif)

## Install

[GitHub Releases](https://github.com/mattrandles/wtproj/releases) are the sole
supported distribution channel. Releases contain standalone, uncompressed
executables and `checksums.txt`; there are no package-manager or installer
releases. Source builds are for contributors, not a supported install or
upgrade path.

Choose the exact asset for your platform:

| Platform | Asset |
| --- | --- |
| macOS AMD64 | `wtp_darwin_amd64` |
| macOS Apple silicon | `wtp_darwin_arm64` |
| Linux AMD64 | `wtp_linux_amd64` |
| Linux ARM64 | `wtp_linux_arm64` |
| Windows AMD64 | `wtp_windows_amd64.exe` |
| Windows ARM64 | `wtp_windows_arm64.exe` |

Download the executable and `checksums.txt` from the same release, verify its
SHA-256 checksum, then place it on your `PATH`. The examples use GitHub's
`latest/download` URL; replace `latest` with a release tag to pin a version.

### macOS and Linux

This example is for Linux AMD64. Set `asset` to the matching Unix filename
from the table for another platform.

```sh
asset=wtp_linux_amd64
base=https://github.com/mattrandles/wtproj/releases/latest/download
curl --fail --location --remote-name "$base/$asset"
curl --fail --location --remote-name "$base/checksums.txt"
grep -F "  $asset" checksums.txt | sha256sum --check --status -
chmod 755 "$asset"
mkdir -p "$HOME/.local/bin"
install -m 755 "$asset" "$HOME/.local/bin/wtp"
```

On macOS, use this checksum command instead:

```sh
grep -F "  $asset" checksums.txt | shasum --algorithm 256 --check --status -
```

Add `~/.local/bin` to your `PATH` if needed, then confirm the installation:

```sh
wtp version
```

### Windows (PowerShell)

This example is for AMD64. Change `$asset` to `wtp_windows_arm64.exe` on
Windows ARM64.

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

$userBin = Join-Path $HOME "bin"
New-Item -ItemType Directory -Force $userBin | Out-Null
Move-Item $asset (Join-Path $userBin "wtp.exe")
[Environment]::SetEnvironmentVariable("Path", "$userBin;" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
$env:Path = "$userBin;$env:Path"
wtp version
```

Open a new terminal after changing a persistent `PATH`. A global install or
update needs permission to write both the executable and its directory.

## Updating

Run `wtp update` to install a newer stable GitHub Release over the executable
that is actually running. It selects only your matching platform asset and
checks its `checksums.txt` SHA-256 entry before replacement. Equal or older
releases are a no-op, development builds cannot self-update, and prereleases
must be installed manually from their release page.

If release discovery, download, or validation fails, the installed executable
is left untouched. Unix replacement is atomic and follows a launch symlink to
its target; Windows completes its verified replacement with a helper after
`wtp.exe` exits and reports any deferred failure beside the executable.

For the complete asset, checksum, API lookup, and updater contract, see the
[GitHub Release asset contract](docs/release-assets.md). Maintainers can use
the [direct-download release QA guide](docs/release-qa.md) for the release
verification harness.

## Everyday workflow

Run `wtp` from the repository or worktree you want to manage. A typical loop
looks like this:

```sh
# Add work, then inspect what is ready without changing it.
wtp task create --title "Implement parser" --priority high --estimate m --lane cli
wtp task ready --agent Tony

# Claim a task atomically, record progress, and finish it.
wtp task next --agent Tony
wtp task comment wtp-0001 --agent Tony --message "Parser implemented"
wtp task done wtp-0001

# Explore work and export a portable snapshot when useful.
wtp task list
wtp task show wtp-0001
wtp export --out .wtp-export
```

`task next` is deliberately not read-only: it claims the next eligible task
under a repository-local lock, preventing two local agents from claiming the
same work. Use `task ready` to preview eligible tasks. `wtp help` lists every
command; `wtp schema` documents the task-file and interoperability contract.

The canonical `task` commands are the public interface. Existing legacy
single-action flags remain supported for compatibility.

## CLI reference

Use `wtp --json <command>` for machine-readable output from commands that
return task, graph, version, or update data. The root `--json` flag comes
before the command, for example `wtp --json task list`.

| Command | Available options |
| --- | --- |
| `wtp help` | none |
| `wtp schema` | none |
| `wtp version` | `--json` |
| `wtp update` | `--json` |
| `wtp task create` | `--title` (required), `--description`, `--priority`, `--estimate`, `--lane`, `--model`, `--git-repo`, `--git-branch`, `--worktree-name`, `--worktree-dir`, `--depends-on`, `--agent` |
| `wtp task update <task-id>` | `--title`, `--description`, `--priority`, `--estimate`, `--lane`, `--model`, `--git-repo`, `--git-branch`, `--worktree-name`, `--worktree-dir`, `--depends-on`, `--agent`; supply at least one |
| `wtp task edit <task-id>` | same options as `task update` |
| `wtp task list` | `--status`, `--agent` |
| `wtp task show <task-id>` | `--agent` |
| `wtp task get <task-id>` | `--agent` (`get` is an alias for `show`) |
| `wtp task start <task-id>` | `--agent` |
| `wtp task pause <task-id>` | `--agent` |
| `wtp task done <task-id>` | `--agent` |
| `wtp task comment <task-id>` | `--message` (required), `--agent` |
| `wtp task ready` | `--agent`, `--limit` |
| `wtp task next` | `--agent` |
| `wtp graph` | `--status` |
| `wtp export` | `--out` |

`--priority` accepts `low`, `medium`, `high`, or `urgent`; `--estimate`
accepts `xs`, `s`, `m`, `l`, or `xl`. `--status` accepts `todo`,
`inProgress`, `paused`, or `done`; `graph --status` also accepts `all`.
`--depends-on` takes comma-separated task IDs. `--git-repo` and
`--worktree-dir` require absolute paths. During `task update` or `task edit`,
an empty `--model=`, `--git-repo=`, `--git-branch=`, `--worktree-name=`, or
`--worktree-dir=` clears the corresponding field.

When a task is created inside Git, omitted Git and worktree fields default
independently from the current repository, branch, and worktree. An explicitly
supplied value, including `--git-branch=` or another intentional empty value,
overrides only that field. Detached HEAD therefore defaults to an empty branch,
and omitted metadata stays empty outside Git. Task update and edit never refresh
these values from the current invocation context.

For older automation, pass exactly one legacy action flag: `--get-next-task`,
`--get-tasks`, `--get-task`, `--set-task-in-progress`,
`--set-task-paused`, `--set-task-done`, `--add-comment`, `--create-task`, or
`--export-tasks=<directory>`. The legacy forms accept their matching options:
`--agent`, `--task-id`, `--comment`, `--title`, `--description`, `--priority`,
`--estimate`, `--lane`, `--model`, `--git-repo`, `--git-branch`,
`--worktree-name`, `--worktree-dir`, `--dependencies`, and `--status`.

## Storage and compatibility

The implemented, supported backend is local flat-file storage.
Inside Git, `wtp` reads `.wtp.json` only from the root of the current worktree,
including when invoked from a nested directory or linked worktree. Outside Git,
it reads `.wtp.json` only from the invocation directory. With no configuration,
flat-file tasks remain under `.wtp/` at that same location.

Set the optional `wtpDir` property to keep flat-file tasks elsewhere. Relative
values are resolved from the directory containing `.wtp.json`; absolute paths
are also accepted. The shared-store version is also available as
[`examples/wtp.json`](examples/wtp.json):

```json
{
  "wtpDir": "../shared/wtp-tasks"
}
```

This makes a shared store practical for several projects. Put the same absolute
directory in each project's root `.wtp.json`, then create tasks normally; the
recorded Git/worktree fields identify where each task originated:

```json
{
  "wtpDir": "/srv/wtp/engineering-tasks"
}
```

```sh
# Run from each project or linked worktree. Both write to the shared store.
cd ~/src/api && wtp task create --title "Add API health check"
cd ~/src/web && wtp task create --title "Render health status"
```

The four optional origin fields are `gitRepo` (absolute primary repository
root), `gitBranch`, `worktreeName`, and `worktreeDir` (absolute current
worktree root). On `task create`, each omitted field is discovered separately;
an explicit flag overrides only that field. `gitRepo` and `worktreeDir` must be
absolute paths. For a detached HEAD, `gitBranch` is empty while the other Git
fields are still recorded. In a non-Git directory all four fields are empty
unless you pass overrides. `task update` and `task edit` preserve existing
origin fields unless their corresponding flag is supplied, so moving between
worktrees does not rewrite a task's provenance. Clear an individual field with
an empty assignment:

```sh
wtp task create --title "Imported work" \
  --git-repo /srv/src/api --git-branch main \
  --worktree-name api --worktree-dir /srv/src/api
wtp task update wtp-0001 --git-branch= --worktree-name=
```

Task files are human-readable and friendly to version control; task status is
represented by `todo`, `inProgress`, `paused`, and `done` directories. `wtp`
recognizes legacy UUID-named task files and safely migrates valid files to the
current stable short-ID filenames when storage is opened.

### Migrating an existing store

Adding `.wtp.json` is opt-in and does not move data. Installing or updating
`wtp` likewise never moves, deletes, or migrates a project's `.wtp/` storage.
To centralize an existing project, stop concurrent writers, commit or back up
the current `.wtp/`, move it deliberately, then point the project at its new
location:

```sh
mkdir -p ~/wtp-stores
mv .wtp ~/wtp-stores/api
printf '{\n  "wtpDir": "%s"\n}\n' "$HOME/wtp-stores/api" > .wtp.json
wtp task list
```

Use a distinct destination for each existing store unless you intentionally
want one combined task list. Do not merge task files by hand: stable short IDs
are allocated per store and must remain unique. Keep the original backup until
`wtp task list` and the task metadata look correct. Removing `.wtp.json`
later returns that worktree to its default local `.wtp/`; it does not delete
the external store.

## Verification

The unified verification gate has a fast `commit` mode and a complete
`release` mode. Both modes run formatting validation, `go test ./...`,
`go vet ./...`, and the platform's isolated smoke test. The commit gate is the
normal pre-commit check:

```sh
./scripts/verify.sh commit
```

```powershell
./scripts/verify.ps1 commit
```

The mode defaults to `commit`, and `check.sh`/`check.ps1` remain compatible
aliases. Before tagging a release, run the full gate:

```sh
./scripts/verify.sh release
```

```powershell
./scripts/verify.ps1 release
```

Release mode requires Go, Git, GoReleaser 2.17.0 (the pinned workflow
version), and the shell-specific tools used by release QA.
On Unix those are PowerShell 7 (`pwsh`), `curl`, `file`, and `sha256sum` or
`shasum`; on Windows the PowerShell gate needs PowerShell and the standard
Go/Git tools. It runs GoReleaser config validation, a non-publishing
snapshot, the release-asset contract test, and direct-download release QA.
Snapshots and fixtures are written below a temporary directory; the gate does
not publish, alter the checkout, or install into a user directory.

Commit verification normally takes under a minute with a warm Go cache; allow
several minutes for release verification because it builds multiple snapshots.
The Unix gate cross-compiles and validates Windows assets but executes the
Unix updater workflow. The PowerShell gate executes the Windows workflow only
on Windows; on other hosts it still validates all generated asset checksums
and formats.
