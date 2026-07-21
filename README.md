# wtp

`wtp` is a small, cheerful command-line task board for repositories. Keep work
close to the code, let agents coordinate safely, and get on with the good bit:
making things.

It stores tasks as readable files in the repository, so there is no hosted
service or account to set up.

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
| `wtp task create` | `--title` (required), `--description`, `--priority`, `--estimate`, `--lane`, `--model`, `--depends-on`, `--agent` |
| `wtp task update <task-id>` | `--title`, `--description`, `--priority`, `--estimate`, `--lane`, `--model`, `--depends-on`, `--agent`; supply at least one |
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
`--depends-on` takes comma-separated task IDs. An empty `--model=` clears a
previous model suggestion during `task update` or `task edit`.

For older automation, pass exactly one legacy action flag: `--get-next-task`,
`--get-tasks`, `--get-task`, `--set-task-in-progress`,
`--set-task-paused`, `--set-task-done`, `--add-comment`, `--create-task`, or
`--export-tasks=<directory>`. The legacy forms accept their matching options:
`--agent`, `--task-id`, `--comment`, `--title`, `--description`, `--priority`,
`--estimate`, `--lane`, `--model`, `--dependencies`, and `--status`.

## Storage and compatibility

The implemented, supported backend is local flat-file storage under `.wtp/`.
Task files are human-readable and friendly to version control; task status is
represented by `todo`, `inProgress`, `paused`, and `done` directories. `wtp`
recognizes legacy UUID-named task files and safely migrates valid files to the
current stable short-ID filenames when storage is opened.

Installing or updating `wtp` never moves, deletes, or migrates a project's
`.wtp/` storage. Commit or back up task files before upgrading so ordinary
changes made by a newer release remain reviewable.

## Verification

Contributors can run the same checks used for Unix and PowerShell workflows:

```sh
./scripts/check.sh
```

```powershell
./scripts/check.ps1
```
