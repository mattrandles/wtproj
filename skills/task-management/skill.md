---
name: task-management
description: Use wtp as the repo-local source of truth for task state in this repository. Break plans into concrete tasks, claim work safely, record meaningful progress, pause blocked work, and only mark tasks done after verification.
---

Use `wtp` as the repo-local source of truth for task state.

Apply this skill whenever work in this repository needs to be planned, claimed, updated, paused, or completed.

## Core Rules

1. Break plans into concrete tasks before starting implementation.
2. Prefer short IDs like `wtp-0005` in human discussion and CLI usage.
3. Use dependencies for ordering, not for loose notes.
4. Revise task metadata with `wtp task update` or `wtp task edit` when scope, dependencies, ownership, or the suggested model changes.
5. Add progress comments when the task state materially changes.
6. Pause blocked work instead of leaving it in progress.
7. Mark a task done only after implementation and verification are both complete.

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
wtp task start wtp-0005 --agent Tony
```

Use explicit start when you already know which task should be claimed.

## Breaking Down Plans

When starting from a plan document:

1. Identify the smallest useful implementation slices.
2. Create one task per slice.
3. Add dependency links where ordering matters.
4. Put finish-line details in `description`.

Example:

```sh
wtp task create \
  --title "Add config validation" \
  --description "Validate provider-specific env vars and fail with actionable messages" \
  --model gpt-5.2-codex

wtp task create \
  --title "Add provider tests" \
  --description "Cover unsupported tool and missing env-var cases" \
  --depends-on wtp-0007
```

## Day-to-Day Loop

Claim or start work:

```sh
wtp task ready --agent Tony
wtp task show wtp-0008 --agent Tony
wtp task next --agent Tony
wtp task start wtp-0008 --agent Tony
wtp graph --status todo
```

Record progress when state or understanding changes materially:

```sh
wtp task comment wtp-0008 --agent Tony --message "Added provider fixture coverage"
```

Revise the task itself when requirements, dependencies, or ownership move:

```sh
wtp task update wtp-0008 --depends-on wtp-0007 --priority high
wtp task edit wtp-0008 --description "Cover provider config validation and CLI wiring" --model gpt-5.2-codex
wtp task update wtp-0008 --model=
```

Pause when blocked:

```sh
wtp task pause wtp-0008
wtp task comment wtp-0008 --agent Tony --message "Paused on missing provider config contract"
```

Finish after verification:

```sh
wtp task done wtp-0008
```

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
- Blocked work is paused instead of silently lingering.
- Tasks move to `done` only after implementation and verification are complete.

## Manual Inspection

The flat-file backend is intentionally inspectable:

- `.wtp/todo/`
- `.wtp/inProgress/`
- `.wtp/paused/`
- `.wtp/done/`

Task filenames use short IDs such as `wtp-0005.json`, so agents can inspect status directories directly even if they are not using the CLI at that moment.

If another tool or agent needs the exact storage contract, prefer `wtp schema` over reverse-engineering the directory contents.

If you need a dependency-oriented view instead of raw files, prefer `wtp graph` over manually reconstructing task trees.

## Notes on Safety

- `wtp` uses a repo-local lock file for mutating operations.
- The lock only helps if agents go through `wtp`.
- Manual edits to `.wtp/` should be rare and deliberate.
- If manual repair is needed, keep `status` in the JSON aligned with the containing directory.
