---
name: setup-wtp
description: Install or update the released WTP CLI and configure its local flat-file storage. Use when needed to set up WTP for an end user, create or merge .wtp.json, choose repository-local, dedicated-history-worktree, external, or shared task storage, safely migrate an existing .wtp store, or quiz the user and add custom task states. Do not use for routine WTP task management.
---

# Set Up WTP

Set up the released `wtp` executable and its storage without assuming access to
the WTP source repository, Go tooling, or contributor documentation. Limit this
workflow to installation, updates, configuration, migration, and setup
verification. Do not teach or perform routine task creation, claiming,
commenting, handoffs, or lifecycle management.

## 1. Inspect before changing anything

Establish the target project directory. Run discovery commands without creating
or opening task storage:

- On macOS or Linux, inspect `uname -s`, `uname -m`, `command -v wtp`, and
  `wtp version` when the command exists.
- On Windows PowerShell, inspect
  `[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture`,
  `Get-Command wtp -ErrorAction SilentlyContinue`, and `wtp version` when found.
- In Git, resolve the configuration directory with
  `git -C TARGET rev-parse --show-toplevel`. WTP reads `.wtp.json` only from
  that current worktree root, including for nested invocations and linked
  worktrees.
- Outside Git, use the intended invocation directory. WTP reads `.wtp.json`
  only from that directory and does not search parents.
- Read any existing `.wtp.json` and preserve every property. If `wtpDir` is
  absent or blank, the effective store is `.wtp` under the configuration
  directory. Resolve a relative `wtpDir` from the directory containing
  `.wtp.json`; use an absolute value as written.
- Inspect whether the effective store, a default `.wtp`, or another proposed
  source store already exists. Record exact canonical paths and whether task
  files are present.

Do not run `wtp task list`, `wtp stats`, or another storage-opening command
against an existing store before backing it up. Opening a store may perform a
safe compatibility rename of legacy UUID-named task files, so it is not a
strictly read-only discovery operation.

## 2. Install or update the released executable

Use GitHub Releases from `mattrandles/wtproj` as the only supported binary
source. Do not build from source or use package-manager packages.

### Update an installed release

Run:

```sh
wtp version
wtp update
wtp version
```

`wtp update` checks the latest stable release, verifies its published checksum,
and replaces the executable only when a newer version exists. An equal or older
release is a no-op. It requires network access and write access to both the
running executable and its directory. Do not retry with elevated privileges
without the user's approval; offer a user-writable installation directory
instead.

If `wtp version` reports a non-release build that cannot self-update, replace it
through the verified latest-release installation below. Treat this as an
exception, not as a development workflow.

### Select the latest-release asset

Map the detected platform exactly:

| Platform | Architecture | Asset |
| --- | --- | --- |
| macOS | x86_64/AMD64 | `wtp_darwin_amd64` |
| macOS | arm64 | `wtp_darwin_arm64` |
| Linux | x86_64/AMD64 | `wtp_linux_amd64` |
| Linux | arm64/aarch64 | `wtp_linux_arm64` |
| Windows | AMD64/x64 | `wtp_windows_amd64.exe` |
| Windows | ARM64 | `wtp_windows_arm64.exe` |

Stop and explain the unsupported platform when no asset matches. Never guess an
asset.

### Install on macOS or Linux

Ask the user to approve the installation directory, defaulting to
`$HOME/.local/bin`. Download into a fresh temporary directory, not the project:

```sh
wtp_asset=wtp_linux_amd64  # replace with the exact detected asset
wtp_release_base=https://github.com/mattrandles/wtproj/releases/latest/download
wtp_stage_dir="$(mktemp -d)"
curl --fail --location --output "$wtp_stage_dir/$wtp_asset" "$wtp_release_base/$wtp_asset"
curl --fail --location --output "$wtp_stage_dir/checksums.txt" "$wtp_release_base/checksums.txt"
```

On Linux, verify with:

```sh
(cd "$wtp_stage_dir" &&
  grep -F "  $wtp_asset" checksums.txt | sha256sum --check --status -)
```

On macOS, verify with:

```sh
(cd "$wtp_stage_dir" &&
  grep -F "  $wtp_asset" checksums.txt |
    shasum --algorithm 256 --check --status -)
```

Proceed only when checksum verification succeeds. Install and verify:

```sh
mkdir -p "$HOME/.local/bin"
install -m 755 "$wtp_stage_dir/$wtp_asset" "$HOME/.local/bin/wtp"
"$HOME/.local/bin/wtp" version
command -v wtp
wtp version
```

If `command -v wtp` does not find that path, help the user add
`$HOME/.local/bin` to the appropriate shell `PATH`, then verify in a new shell.
Do not delete the staged files until installation succeeds.

### Install on Windows PowerShell

Ask the user to approve the installation directory, defaulting to
`$HOME\bin`. Use the exact detected asset:

```powershell
$wtpAsset = "wtp_windows_amd64.exe" # use ARM64 when detected
$wtpReleaseBase = "https://github.com/mattrandles/wtproj/releases/latest/download"
$wtpStageDir = Join-Path ([System.IO.Path]::GetTempPath()) ("wtp-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $wtpStageDir | Out-Null
Invoke-WebRequest "$wtpReleaseBase/$wtpAsset" -OutFile (Join-Path $wtpStageDir $wtpAsset)
Invoke-WebRequest "$wtpReleaseBase/checksums.txt" -OutFile (Join-Path $wtpStageDir "checksums.txt")

$wtpLines = @(Get-Content (Join-Path $wtpStageDir "checksums.txt") |
  Where-Object { $_ -match "  $([regex]::Escape($wtpAsset))$" })
if ($wtpLines.Count -ne 1) { throw "checksums.txt must contain one entry for $wtpAsset" }
$wtpExpected = $wtpLines[0].Substring(0, 64).ToLowerInvariant()
$wtpActual = (Get-FileHash (Join-Path $wtpStageDir $wtpAsset) -Algorithm SHA256).Hash.ToLowerInvariant()
if ($wtpActual -ne $wtpExpected) { throw "SHA-256 verification failed for $wtpAsset" }

$wtpUserBin = Join-Path $HOME "bin"
New-Item -ItemType Directory -Force $wtpUserBin | Out-Null
Copy-Item (Join-Path $wtpStageDir $wtpAsset) (Join-Path $wtpUserBin "wtp.exe")
```

Add the directory to the user `PATH` only if absent, update the current session,
then verify:

```powershell
$wtpUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($wtpUserPath -split ';') -notcontains $wtpUserBin) {
  [Environment]::SetEnvironmentVariable("Path", "$wtpUserBin;$wtpUserPath", "User")
}
$env:Path = "$wtpUserBin;$env:Path"
& (Join-Path $wtpUserBin "wtp.exe") version
Get-Command wtp
wtp version
```

Explain that a new terminal may be required after a persistent `PATH` change.

## 3. Define the storage setup with the user

Use structured questions when available. Ask only after inspecting discoverable
facts, and resolve these decisions before writing configuration or moving data:

1. **Storage topology:** choose one of:
   - Repository/worktree-local `.wtp` at the configuration root. This is the
     default and needs no `wtpDir` property.
   - A dedicated orphan task-history branch checked out in one linked worktree.
   - A distinct central directory elsewhere on disk for this project.
   - One intentionally shared central directory for multiple projects.
2. **Existing data:** confirm whether the discovered store should be migrated,
   retained in place, or whether this is a new empty setup.
3. **Version control:** ask whether `.wtp` task records and `.wtp.json` should be
   committed, local-only, or split so config is committed but task data is not.
   For local-only Git exclusions, prefer `.git/info/exclude`; change a tracked
   `.gitignore` only when the team should share the ignore rule.
4. **Location details:** obtain the exact external path, or the history branch
   name and linked-worktree path. Recommend a distinct external destination per
   existing store.
5. **Coordination:** identify other users, agents, projects, clones, or
   worktrees that may write concurrently. Require writers to stop during a
   migration. Explain that the local WTP lock does not coordinate Git
   commit/pull/push operations or writers on different machines.
6. **Custom states:** ask whether the four built-ins fully describe the user's
   workflow. Run the state quiz below only when more states are requested.

Make the distinction between location and tracking explicit: a repository-local
`.wtp` can be committed or ignored, and an external store can separately be
version-controlled in its own worktree or repository.

## 4. Create or merge `.wtp.json`

Edit `.wtp.json` only at the discovered configuration root. Parse the existing
JSON and preserve all unrelated keys. Change only `wtpDir` and/or
`additionalStatuses` approved in this setup. Never replace an existing config
with a minimal object that discards other settings.

Use no `wtpDir` for the default local store. For another location, use either:

```json
{
  "wtpDir": "../shared/wtp-tasks"
}
```

or:

```json
{
  "wtpDir": "/absolute/path/to/wtp-tasks"
}
```

Relative paths resolve from `.wtp.json`, not from the caller's current working
directory. Use an absolute path when several projects or worktrees must point
to exactly the same store. Ensure JSON remains valid on the user's platform.

Changing, creating, or removing `.wtp.json` only changes where WTP looks. It
never copies, moves, merges, or deletes a store. Installing or updating WTP also
never migrates project data.

## 5. Migrate an existing store safely

Apply this sequence for any destination:

1. Stop every known writer.
2. Resolve and show the exact source store, destination store, config file, and
   backup path. Obtain confirmation before any copy, move, config edit, branch
   creation, or worktree creation not already explicitly authorized.
3. Require a new, empty destination. If it exists, inspect it and stop rather
   than combining files.
4. Back up the complete source directory, including `meta`, handoffs, status
   directories, and hidden files. Use a fresh backup path on another durable
   location when practical.
5. Prefer copying the complete store to the new destination, then switching
   `.wtp.json`. This leaves the original as a recoverable fallback. Move only
   when the user specifically requests it and a separate backup is verified.
6. Write or merge `.wtp.json` only after the destination copy succeeds.
7. From the intended project/worktree, run `wtp task list` and
   `wtp --json stats`.
   Compare counts, configured statuses, handoffs, and representative task
   metadata with the source or backup.
8. On any failure, restore the previous `.wtp.json` selection and leave all
   stores intact. Keep the backup and original until the user accepts the
   migration.

Never merge independent stores by copying their task files together. Stable
short IDs and allocation indexes are scoped to a store and may collide. To
share one store across projects, start with one new store or choose one existing
store as canonical; keep every other existing store separate.

### Use an external or shared directory

Create a distinct destination directory, copy the entire existing `.wtp`
directory into it, and set `wtpDir` to the resulting store directory. For an
intentional multi-project store, put the same absolute `wtpDir` in each
project's root `.wtp.json`. Confirm that users expect one combined task list and
coordinate one writer at a time across machines unless external synchronization
provides stronger locking.

### Use a dedicated task-history branch and linked worktree

Use an orphan branch so task history does not appear in source branches.
Resolve these values with the user instead of assuming them:

```sh
wtp_project_root="$(git -C TARGET rev-parse --show-toplevel)"
wtp_history_branch=wtp-task-history
wtp_history_worktree="$wtp_project_root/.wtp-task-history"
wtp_exclude_file="$(git -C "$wtp_project_root" rev-parse --path-format=absolute --git-path info/exclude)"
```

Inspect `git -C "$wtp_project_root" worktree list` and
`git -C "$wtp_project_root" show-ref --verify
"refs/heads/$wtp_history_branch"` first. For a new branch and absent target,
create the linked worktree:

```sh
grep -qxF '.wtp-task-history/' "$wtp_exclude_file" ||
  printf '%s\n' '.wtp-task-history/' >> "$wtp_exclude_file"
git -C "$wtp_project_root" worktree add --orphan -b "$wtp_history_branch" "$wtp_history_worktree"
printf '%s\n' '.wtp/meta/wtp.lock' '.wtp/meta/batch-update.json' > "$wtp_history_worktree/.gitignore"
```

If the branch already exists, do not recreate it; update/synchronize it safely
and add a linked worktree only when it is not already checked out. Do not reuse
a nonempty target path.

For an existing store, back it up and copy the complete `.wtp` directory to
`$wtp_history_worktree/.wtp` before changing config. Set the project root's
`.wtp.json` to point to `.wtp-task-history/.wtp`, merging any existing config.
Then verify from the project root and commit from the history worktree:

```sh
git -C "$wtp_history_worktree" add .gitignore .wtp
git -C "$wtp_history_worktree" commit -m "Initialize wtp task history"
git -C "$wtp_history_worktree" push -u origin "$wtp_history_branch"
```

Commit subsequent task-file changes from the history worktree. Synchronize that
branch before the next writer, keep the branch checked out in only one worktree
per clone, and back up both its commits and current working store. The ignored
linked-worktree directory is not itself a backup.

## 6. Quiz the user about custom states

Keep the built-in states `todo`, `inProgress`, `paused`, and `done`; custom
states append to them. Begin with workflow language rather than configuration
jargon:

1. Ask which recurring situation cannot be expressed by the four built-ins.
2. For each situation, ask whether work has started and is waiting, cannot
   currently start or continue because it is blocked, or has ended in failure.
3. Propose a concise lower-camel-case name and one category:
   - `waiting`: work has started and awaits review, a response, or another
     event. It has `startedAt`, no `completedAt`, and is nonclaimable.
   - `blocked`: work is not active because a prerequisite or external condition
     prevents it. It has neither lifecycle timestamp and is nonclaimable.
   - `failed`: the attempt ended unsuccessfully. It has both timestamps and is
     terminal.
4. Explain that only built-in `done` resolves dependencies. A terminal `failed`
   state does not. Custom states are never selected by `task ready` or
   `task next`.
5. Confirm the exact names, categories, and JSON order before editing.

Require names matching `^[a-z][a-zA-Z0-9]*$`. Reject `all`, duplicates, built-in
names, hyphenated names, and categories other than `waiting`, `blocked`, or
`failed`. Example additions are:

```json
{
  "additionalStatuses": [
    {"name": "waitingForReview", "category": "waiting"},
    {"name": "vendorBlocked", "category": "blocked"},
    {"name": "verificationFailed", "category": "failed"}
  ]
}
```

Merge additions with existing definitions and retain their order. Never remove
or rename an existing custom status merely because the current quiz omitted it.
Before an explicitly requested removal or rename, inspect the corresponding
status directory and task files. Refuse the config change while any task still
uses that status; agree on a separate task-data transition first.

## 7. Verify and hand off the setup

Run setup-focused checks from the intended project or linked worktree:

```sh
wtp version
wtp task list
wtp --json stats
```

Confirm the executable path/version, configuration root, resolved store path,
status catalog, expected task counts, representative metadata, and whether the
chosen files appear in Git status as intended. On Windows, also report any
deferred updater error file beside `wtp.exe`.

Summarize what was installed or updated, which files and directories changed,
where the active store and backup live, what is committed or ignored, which
custom states were added, and any synchronization duties. Stop there; route
subsequent task-management requests to a WTP task-management skill.
