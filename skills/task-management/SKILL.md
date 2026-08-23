---
name: task-management
description: Use WTP for repository-local task and project management. Apply it when Codex needs to plan and prioritize work, inspect project health and remaining counts, use custom statuses, coordinate branch-scoped tasks and retained handoffs, claim and update work safely, or verify completion.
---

# WTP Project Management

Use `wtp` as the source of truth for project work. Run it from the intended
repository, branch, and worktree so it uses the correct configuration, task
scope, and provider.

## Set up the trusted CLI when needed

If `wtp` is unavailable or its provenance cannot be established, install the
official `setup-wtp` skill from `mattrandles/wtproj`:

```sh
npx skills add mattrandles/wtproj --skill setup-wtp
```

Then invoke `$setup-wtp`; it installs released WTP binaries only from this
repository's GitHub Releases and verifies the published SHA-256 checksum. Do
not perform installation or updates through this task-management skill.

## 1. Inspect project health

Inspect the project before planning or claiming work:

```sh
wtp stats
wtp stats todo
wtp stats todo priority
wtp task list
wtp graph --status todo
```

- Use `wtp stats` for the complete project overview and status counts.
- Use `wtp stats STATUS` for tasks in one configured status; `wtp stats todo`
  is the direct way to count remaining todo work.
- Use `wtp stats ATTRIBUTE` or `wtp stats STATUS ATTRIBUTE` for a focused
  breakdown. Attributes include `model`, `lane`, `priority`, `estimate`,
  `assignee`, `comments`, and `dependencies`.
- Use `wtp graph` to inspect dependency structure. Do not infer ordering from
  counts alone.

Use `wtp task ready --agent NAME` to preview claimable work without changing
state. Use `wtp task show TASK_ID --agent NAME` to inspect one task.

## 2. Plan and create tasks

Break a plan into small, testable tasks. Encode ordering with dependencies and
put the finish line in each description.

```sh
parser_task_id="$(wtp task create \
  --title "Add config validation" \
  --description "Validate provider settings and cover errors with tests" \
  --priority high | sed -n '1s/ .*//p')"

test_task_id="$(wtp task create \
  --title "Add integration coverage" \
  --depends-on "$parser_task_id" | sed -n '1s/ .*//p')"
```

Always capture and reuse the exact returned short ID or the JSON `shortId`.
Treat IDs as opaque; never predict the next sequence or rebuild a scoped ID.

Use `--model` only as an optional, free-form execution recommendation. Labels
such as `GPT 5.6 Sol High` and `GPT 5.6 Terra High` are examples, not promises
that a model is available. WTP does not route models, and this skill assumes no
specific model provider, task provider, dispatcher, or execution harness. The
system consuming the task decides whether and how to map the label. Clear a
stale recommendation with `wtp task update TASK_ID --model=`.

## 3. Claim branch-appropriate work

Prefer atomic automatic claiming in multi-worker projects:

```sh
wtp task next --agent NAME
```

Use `wtp task start TASK_ID --agent NAME` only when intentionally claiming a
known task. `task next` moves the selected task to `inProgress`; `task ready`
does not.

On a named Git branch, automatic selection checks that branch's scoped tasks
first and then legacy unscoped `wtp-NNNN` tasks. It never automatically selects
a task scoped to another branch. Foreign tasks may still appear in listings.
Starting one explicitly is allowed when intentional, including after a branch
rename. Detached HEAD and non-Git contexts automatically select only legacy
tasks.

Keep the returned `wtp-BBBBBBBB-NNNN` or `wtp-NNNN` ID unchanged in every
command, prompt, comment, and handoff.

## 4. Update status and progress

Record meaningful state changes, decisions, and blockers:

```sh
wtp task comment "$task_id" --agent NAME --message "Added validation coverage"
wtp task update "$task_id" --priority urgent --depends-on "$parser_task_id"
wtp task pause "$task_id" --agent NAME
wtp task done "$task_id" --agent NAME
```

Use `task update` or `task edit` when title, scope, dependencies, ownership, or
model guidance changes. Use comments for concise audit history, not as a
substitute for task metadata.

Projects may append statuses in `.wtp.json`:

```json
{
  "additionalStatuses": [
    {"name": "waitingForReview", "category": "waiting"},
    {"name": "vendorBlocked", "category": "blocked"},
    {"name": "verificationFailed", "category": "failed"}
  ]
}
```

Move a task to any configured status with:

```sh
wtp task set-status "$task_id" waitingForReview --agent NAME
```

Inspect `.wtp.json` or the catalog shown by `wtp stats` and replace
`waitingForReview` with the project's actual status name. Additional names use
lower camel case and categories are `waiting`, `blocked`, or `failed`. Custom
statuses are not eligible for `task ready` or `task next`. A `failed` status is
terminal but does not resolve dependencies; only `done` resolves them. Use the
project's configured blocked status when it expresses the workflow better than
built-in `paused`. The `start`, `pause`, and `done` commands remain convenient
aliases for built-in states.

## 5. Coordinate handoffs

Use comments for audit and handoffs for context that must cross a worker
boundary. Write task-scoped context for a known successor task and global
context for the next general worker:

```sh
wtp handoff write --task "$next_task_id" --agent NAME \
  --message "Reuse the validation fixtures added in the parser tests"

wtp handoff write --agent NAME \
  --message "Project-wide migration context for the next worker"
```

`task start` and `task next` attach retained task-scoped handoffs for the
claimed task in newest-first order. Read global context separately, or inspect
a task scope explicitly:

```sh
wtp handoff get --limit 1
wtp handoff get --task "$task_id" --all
```

Handoff reads and claim attachment are non-consuming. Writes append unless
`--replace` is explicitly used. Retain useful context until it is deliberately
retired, then purge only the intended record or scope. Do not duplicate long
status reports in both comments and handoffs.

## 6. Verify completion

Do not mark work `done` until implementation and proportionate verification
are complete. If verification fails, keep the task out of `done`; use a
configured failed or blocked status when appropriate and record the reason.

Before leaving claimed work, ensure its status is accurate, add an audit
comment for material outcomes, and write a handoff if another worker needs
context. Then confirm project state with `wtp task show`, `wtp stats`, or
`wtp graph` as appropriate.

Use `wtp help` for the current CLI surface, `wtp schema` for the interoperability
contract, and the project README for uncommon provider, storage, migration, or
repair details. Do not edit task storage directly when the CLI can perform the
operation.
