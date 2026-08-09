---
name: task-management
description: Use wtp as the repo-local source of truth for task state in this repository. Break plans into concrete tasks, claim work safely, record meaningful progress, pause blocked work, and only mark tasks done after verification.
---

Use `wtp` as the repo-local source of truth for task state.

Apply this skill whenever work in this repository needs to be planned, claimed, updated, paused, or completed.

## Core Rules

1. Break plans into concrete tasks before starting implementation.
2. Run `wtp` from the intended repository, branch, and worktree; its store and branch scope come from that invocation context.
3. Use the exact short ID returned by `wtp task create`; never predict a sequence or substitute a legacy ID for a scoped ID.
4. Use dependencies for ordering, not for loose notes.
5. Revise task metadata with `wtp task update` or `wtp task edit` when scope, dependencies, ownership, or the suggested model changes.
6. Use task comments for audit and progress when the task state materially changes.
7. Use retained handoffs for context that must cross a worker boundary.
8. Pause blocked work instead of leaving it in progress.
9. Mark a task done only after implementation and verification are both complete.

## Decision Points

- If you need to inspect the next claimable task without changing state, use `wtp task ready --agent <name>`.
- If you need to inspect a specific task without claiming it, use `wtp task show <task-id> [--agent <name>]`.
- If you need a task selected safely in a multi-agent repo, use `wtp task next --agent <name>`.
- If you already know the exact task to claim, use `wtp task start <task-id> --agent <name>`.
- If work depends on another task finishing first, encode that as a dependency instead of describing it informally.
- If you need to inspect dependency structure across related work, use `wtp graph` with the appropriate `--status` filter.
- If the task itself needs to change, edit the task record instead of relying on comments alone.
- If work cannot proceed, move it to `paused` and leave a comment describing the blocker.
- If implementation is done but verification is not, keep the task out of `done`.
- If you need the complete CLI surface or file contract, use `wtp help` and `wtp schema`.

## Claiming Work

Read-only inspection:

```sh
wtp task ready --agent Tony
```

That command uses the same eligibility rules as `task next` but does not move the task to `inProgress`. It is useful when you need to preview what would be claimed next. The current supported backend for this behavior is the repo-local flat-file backend.

Preferred pattern:

```sh
wtp task next --agent Tony
```

That command claims work atomically by moving the returned task to `inProgress`. It is the safest way for multiple agents on the same filesystem to pull work without colliding.

Explicit claim is still available when needed:

```sh
wtp task start "$task_id" --agent Tony
```

Use explicit start when you already know which task should be claimed.

## Task IDs and Branch-Aware Selection

Accept both short-ID forms:

- `wtp-NNNN` is the legacy, unscoped form. Existing legacy tasks keep it, and
  tasks created on detached `HEAD` or outside Git use it.
- `wtp-BBBBBBBB-NNNN` is the scoped form created on a named Git branch. The
  middle part is eight lowercase hexadecimal characters derived from the exact
  branch name; the final part is a decimal sequence with at least four digits.

Do not construct IDs or assume the next number. Capture the short ID from the
first line of human `task create` output and reuse that value:

```sh
task_id="$(wtp task create --title "Implement parser" --priority high | sed -n '1s/ .*//p')"
wtp task show "$task_id" --agent Tony
wtp task start "$task_id" --agent Tony
```

When using `--json`, read `shortId` from the returned JSON instead of guessing
its format. Use the actual ID shown by `task list` or `task show` for existing
tasks.

On a named branch, `task ready` and `task next` automatically consider tasks
from the current branch scope first, then legacy `wtp-NNNN` tasks. They never
automatically select a scoped task belonging to another branch, though
`task list` may show current, legacy, and foreign tasks. On detached `HEAD` or
outside Git there is no current scope: creation and automatic selection use
the legacy namespace only. Explicit `task start <task-id>` is the intentional
path for an older or foreign scoped task; dependency and lifecycle checks still
apply. Explicit `--git-branch` metadata does not create a runtime branch scope.

Branch scope follows the exact branch name, not a branch object. After a branch
rename, new tasks use the new name's scope while old IDs and files remain
unchanged and are not automatically adopted.

Run task commands and implementation work from the same intended branch and
worktree. Otherwise `wtp` can discover a different `.wtp.json`, use a different
store, choose a different automatic scope, or record the wrong Git/worktree
origin for a newly created task.

## Breaking Down Plans

When starting from a plan document:

1. Identify the smallest useful implementation slices.
2. Create one task per slice.
3. Add dependency links where ordering matters.
4. Put finish-line details in `description`.

Example:

```sh
parser_task_id="$(wtp task create \
  --title "Add config validation" \
  --description "Validate provider-specific env vars and fail with actionable messages" \
  --model gpt-5.2-codex | sed -n '1s/ .*//p')"

test_task_id="$(wtp task create \
  --title "Add provider tests" \
  --description "Cover unsupported tool and missing env-var cases" \
  --depends-on "$parser_task_id" | sed -n '1s/ .*//p')"
```

## Day-to-Day Loop

Claim or start work:

```sh
wtp task ready --agent Tony
wtp task next --agent Tony
wtp graph --status todo
```

Record progress when state or understanding changes materially:

```sh
wtp task comment "$task_id" --agent Tony --message "Added provider fixture coverage"
```

Revise the task itself when requirements, dependencies, or ownership move:

```sh
wtp task update "$task_id" --depends-on "$parser_task_id" --priority high
wtp task edit "$task_id" --description "Cover provider config validation and CLI wiring" --model gpt-5.2-codex
wtp task update "$task_id" --model=
```

Pause when blocked:

```sh
wtp task pause "$task_id"
wtp task comment "$task_id" --agent Tony --message "Paused on missing provider config contract"
```

Finish after verification:

```sh
wtp task done "$task_id"
```

## Comments and Handoffs

Use a task comment to record audit-friendly progress, decisions, or blockers:

```sh
wtp task comment "$task_id" --agent Tony --message "Added provider fixture coverage"
```

Use a retained handoff for cross-worker context. Append a global handoff for
the next queued worker, or attach one to a known future task with `--task`:

```sh
wtp handoff write --agent Tony --message "Parser context for the next queued worker"
wtp handoff write --task "$next_task_id" --agent Tony --message "Use the existing tokenizer tests"
```

Writes append by default. Reads and claim attachment do not consume records;
task-scoped handoffs are attached by `task next` or `task start` in newest-first
order. Retain records until their context is deliberately retired, then purge
exactly one selected scope, such as `wtp handoff purge --global --older-than
72h` or `wtp handoff purge --task "$next_task_id"`. Use `--replace` only when
intentionally replacing the selected scope.

Before running `wtp task done` or `wtp task pause`, append one concise global
handoff summarizing the implementation state, verification, and any useful
next-worker context. Keep the audit trail in comments and the reusable context
in handoffs; do not duplicate long status reports in both.

## Writing Good Tasks

- Keep tasks small enough to finish in one focused pass.
- Use clear, imperative titles.
- Keep descriptions concrete and testable.
- Use the optional free-form `model` field when a particular execution model is a useful recommendation; it is advisory and does not change readiness or ordering.
- Clear a stale model recommendation with `--model=`.
- Avoid combining unrelated changes into one task.
- Create follow-up tasks instead of stretching a task beyond its original scope.

## Completion Checks

- The work is represented as one or more concrete tasks.
- Task ordering is captured through dependencies where needed.
- Claimed work is in `inProgress`, not left in `todo`.
- Progress comments explain meaningful changes or blockers.
- Comments record audit/progress; handoffs preserve context across workers.
- Global handoffs are appended for the next queued worker, while known future
  work uses task-scoped handoffs.
- Retain handoffs until deliberate retirement and purge them by scope or cutoff.
- Blocked work is paused instead of silently lingering.
- Tasks move to `done` only after implementation and verification are complete.

## Manual Inspection

The flat-file backend is intentionally inspectable:

- `.wtp/todo/`
- `.wtp/inProgress/`
- `.wtp/paused/`
- `.wtp/done/`

Inspect with `wtp task list`, `wtp task show`, `wtp graph`, and `wtp schema`
first. If raw inspection or repair is necessary, list the status directories
with `rg --files .wtp/todo .wtp/inProgress .wtp/paused .wtp/done` and read the
JSON carefully. Task filenames use the actual short ID, such as
`wtp-0001.json` or `wtp-0d6e4079-0001.json`; older canonical-UUID filenames may
be migrated when storage opens. The containing directory and JSON `status`
must agree. Do not infer branch selection from filenames or hand-merge stores.

If another tool or agent needs the exact storage contract, prefer `wtp schema` over reverse-engineering the directory contents.

If you need a dependency-oriented view instead of raw files, prefer `wtp graph` over manually reconstructing task trees.

## Storage, Context, and Shared Stores

`wtp` discovers `.wtp.json` at the root of the current Git worktree, even
when invoked from a nested directory. A linked worktree has its own root and
therefore its own configuration lookup. Outside Git, discovery is limited to
the invocation directory; parent directories are not searched.

Without configuration, task storage is `.wtp/` beside that discovery point.
Use `wtpDir` in `.wtp.json` to select another flat-file store. A relative path
is resolved from the configuration file's directory; an absolute path is used
as written. Multiple projects can intentionally use one absolute directory:

```json
{
  "wtpDir": "/srv/wtp/engineering-tasks"
}
```

For task history that should be version-controlled without appearing in source
branches, use a dedicated orphan history branch in one linked worktree. Add
the history-worktree path to the source checkout's local `.git/info/exclude`,
create it once, and point each source checkout's `.wtp.json` at its store:

```sh
project_root="$(git rev-parse --show-toplevel)"
history_worktree="$project_root/.wtp-task-history"
git -C "$project_root" worktree add --orphan -b wtp-task-history "$history_worktree"
printf '%s\n' '.wtp/meta/wtp.lock' > "$history_worktree/.gitignore"
```

```json
{
  "wtpDir": ".wtp-task-history/.wtp"
}
```

Run `wtp` from the source checkout that contains `.wtp.json`; use the history
worktree only to commit and sync `.wtp` (and ignore `.wtp/meta/wtp.lock` there):

```sh
git -C "$history_worktree" add .wtp
git -C "$history_worktree" commit -m "Update wtp task history"
git -C "$history_worktree" push -u origin wtp-task-history
```

Synchronize before another writer uses the store, keep one writer at a time,
and back up the history branch or run `wtp export`. The WTP lock does not
coordinate Git commits, pulls, pushes, or concurrent writers. Changing
`.wtp.json` changes lookup only; it does not move or delete an existing store.

When a task is created in Git, `gitRepo`, `gitBranch`, `worktreeName`, and
`worktreeDir` are filled from the current context. `gitRepo` and `worktreeDir`
are absolute paths. A detached HEAD has an empty `gitBranch`; a non-Git
invocation leaves all four fields empty unless explicitly overridden. Supply
any one of `--git-repo`, `--git-branch`, `--worktree-name`, or
`--worktree-dir` to override just that field. Update and edit preserve these
values unless explicitly changed; clear one with an empty assignment:

```sh
wtp task update "$task_id" --git-branch= --worktree-name=
```

Adding or removing `.wtp.json` never moves or deletes a store. To migrate an
existing `.wtp/`, back it up, move it deliberately, configure `wtpDir`, and
run `wtp task list` to confirm it. Do not hand-merge stores because short IDs
must be unique within a shared store.

## Notes on Safety

- `wtp` uses a repo-local lock file for mutating operations.
- The lock only helps if agents go through `wtp`.
- Manual edits to `.wtp/` should be rare and deliberate.
- If manual repair is needed, keep `status` in the JSON aligned with the containing directory.
