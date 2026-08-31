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
wtp --json stats
wtp --json stats status
wtp --json stats model
wtp --json stats done model
wtp --json stats created 7d-0d
wtp --json stats progressed 7d-0d
wtp task list
wtp graph --status todo
```

- Use `wtp --json stats` for the complete project overview and its status
  counts; JSON is the preferred interface for agents and scripts.
- Use `wtp --json stats STATUS` for an overview filtered to one configured
  status, or `wtp --json stats status` for focused counts of every status.
- Use `wtp --json stats ATTRIBUTE` or `wtp --json stats STATUS ATTRIBUTE` for
  one focused breakdown. Attributes include `status`, `model`, `lane`,
  `priority`, `estimate`, `assignee`, `issueId`, `project`, `milestone`,
  `version`, `featureId`, `feature`, `comments`, and `dependencies`; the status
  selector precedes the attribute. Any grouping selectors must precede these
  positional selectors.
- Use `wtp --json stats created STARTd-ENDd` or `progressed STARTd-ENDd` for
  rolling 24-hour series. Ranges are half-open UTC windows, and `progressed`
  uses each task's latest `UpdatedAt`.
- Charts are optional human-facing output: `wtp stats --chart model` or
  `wtp stats --chart done model`. Place `--chart` immediately after `stats`;
  it cannot be combined with root `--json`.
- Use `wtp graph` to inspect dependency structure. Do not infer ordering from
  counts alone.

Use `wtp task ready --agent NAME` to preview claimable work without changing
state. Use `wtp task show TASK_ID --agent NAME` to inspect one task.

### Grouping fields and filters

Tasks may use six independent optional grouping fields: `issueId`, `project`,
`milestone`, `version`, `featureId`, and `feature`. `featureId` is the stable
machine-facing feature key; `feature` is the human-readable display name. Keep
the key stable across a rename and change only the display name when the label
changes. WTP never treats the key and name as interchangeable.

Set them with `--issue-id`, `--project`, `--milestone`, `--version`,
`--feature-id`, and `--feature` on create, update, or edit. Create values must
not be blank after trimming. Update/edit preserves an omitted field; pass an
explicit empty assignment such as `--feature-id=` or `--project=` to clear
only that field. Older task files without these properties remain compatible
and expose them as unset.

The same selectors are supported by `task list`, `task ready`, `task next`,
`graph`, `batch export`, and `stats`. Selector values are trimmed and matched
case-insensitively as exact strings. Multiple selectors are ANDed, omitted
selectors are unrestricted, and wildcard, substring, version parsing, and
feature key/name fallback are not supported. For example:

```sh
wtp task list --project Apollo --feature-id FEAT-7
wtp task ready --project Apollo --feature-id FEAT-7 --agent NAME
wtp task next --project Apollo --feature-id FEAT-7 --agent NAME
wtp --json stats --project Apollo --feature-id FEAT-7 model
```

Use one fixed grouping scope for a targeted run. Apply that exact scope to
every automatic `task ready`/`task next` call and to model stats; do not inspect
one group and then claim from the unrestricted queue. A scope can contain any
non-empty subset of the six fields, and all supplied fields must match.

Stats grouping applies before aggregation to overview and focused reports,
comments, dependency metrics, and `created`/`progressed` series. Status remains
the first positional selector before a focused attribute, so the compatibility
form `wtp --json stats --project Apollo done model` remains valid. Grouping-only
overview handoff metrics include global handoffs and task-scoped handoffs for
the selected group.

## 2. Manage future work in planning

Keep ideas and research separate from executable queue work. Inspect planning
reports before deciding what future work to create or advance:

```sh
wtp planning report --project Apollo --version v2.0
wtp planning list --status toplan --project Apollo
wtp planning show PLANNING-ID
```

Planning is a store-global, flat-file workflow with the exact statuses
`toplan`, `researched`, `planned`, and `rejected`. Create future work as a
planning item, keeping the stable grouping fields (`issueId`, `project`,
`milestone`, `version`, `featureId`) and human-facing `feature` consistent
across the group:

```text
toplan     -> researched | rejected
researched -> toplan | planned | rejected
planned    -> researched | rejected
rejected   -> toplan
```

```sh
foundation_id=$(wtp --json planning create --title "Choose search index" \
  --project Apollo --version v2.0 --milestone Search-MVP \
  --feature-id SEARCH-1 --feature "Search" | jq -r .shortId)
wtp planning set-status "$foundation_id" researched
```

Research means using `planning update` to record the evidence and decisions in
the planning item, then moving it through the allowed transitions with
`planning set-status`. Do not create an executable `todo` task merely to track
an idea. Keep `featureId` stable when the display name changes, and use the same
exact grouping selectors when reviewing a future-work slice. Planning
list/show/report/promotion operate across branch scopes;
ordinary task commands, stats, batch operations, and graph output remain
planning-blind.

When a researched item is ready for implementation, move it to `planned` only
after its dependencies and scope are explicit. Preview promotion with the same
non-empty grouping selectors that define the intended group:

```sh
wtp --json planning promote --project Apollo --version v2.0 \
  --milestone Search-MVP --dry-run
wtp planning promote --project Apollo --version v2.0 --milestone Search-MVP
```

Review the dry-run's exact ordered items, then rerun it as needed before
publishing; a preview is not a reservation. Promote only dependency-closed
planned groups: every reachable planning dependency, including one reached
through an executable task, must also be selected and already be `planned`.
Executable dependencies are left unchanged. Promotion never auto-adds missing
planning dependencies, claims work, creates handoffs, executes reusable
instructions, or chooses an implicit all-items group. A failed closure check
rejects the entire group. `planning promote` requires at least one grouping
selector and accepts no status, task-ID, or agent selector.

Rejected work is revisable rather than deleted: use `rejected -> toplan` when
research resumes. Planning has no `delete`, `comment`, `start`, `next`,
`ready`, `done`, or `graph` command. Preserve the complete planning payload,
including dependencies, comments, and reusable references, and let the worker
interpret reusable advisory instructions only after the item is promoted and
claimed as an executable task.

## 3. Plan and create executable tasks

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

## 4. Define and attach reusable advisory tasks

Reusable definitions are store-global advisory instructions, not additional
queue tasks. They have no status, lifecycle, dependency, execution, or
completion-enforcement behavior, and WTP never infers the final task in a
group. Define them with the CRUD commands:

```sh
wtp reusable create --name "Tests" --title "Run focused tests" \
  --instructions "Run focused and full tests, then record the result."
wtp reusable list
wtp reusable show Tests
wtp reusable update Tests --title "Run focused and full tests"
wtp reusable delete Tests
```

Names are trimmed and unique under case-insensitive exact matching. A selector
may be a canonical UUID or an exact name; UUIDs are stable, and updating a
definition preserves its UUID and creation time. Deleting one atomically
detaches it from every task while preserving the order of remaining
assignments.

For a dependency chain, explicitly choose the final parent task from the
create response, then attach the reusable definitions to that exact task with
repeatable `--reusable NAME_OR_ID` flags in the intended order:

```sh
tests_id=$(wtp --json reusable create --name Tests \
  --title "Run tests" --instructions "Run focused and full tests." | jq -r .id)
review_id=$(wtp --json reusable create --name "Code review" \
  --title "Review code" --instructions "Review the implementation and tests." | jq -r .id)

tests_task=$(wtp --json task create --title "Run tests" | jq -r .shortId)
final_task_id=$(wtp --json task create --title "Commit release" \
  --depends-on "$tests_task" | jq -r .shortId)

# Choose this final parent explicitly; WTP never infers a group end.
wtp task update "$final_task_id" \
  --reusable "$tests_id" --reusable "$review_id"
wtp task show "$final_task_id"
```

On create, update, or edit, supplied reusable flags replace the complete
assignment list; they do not append. Duplicate definitions are rejected, and
one empty occurrence (`--reusable=`) is the explicit clear form and cannot be
mixed with non-empty occurrences. Reusable assignments are live references:
detailed task views and `task start`/`task next` claim results resolve the
current definitions in the exact stored order. A missing or malformed catalog,
or an assigned UUID missing from it, is an error rather than a silently
shortened view. WTP neither executes nor enforces the instructions; the worker
must interpret and carry them out.

The grouping flags on the example are ordinary task metadata and selectors,
not a reusable-task scope. Apply one fixed grouping scope to every automatic
claim in a targeted run as described above.

## 5. Claim branch-appropriate work

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

## 6. Update status and progress

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

For a focused edit across several existing tasks, use the batch contract:

```sh
wtp batch export --status todo --out task-edits.json
# Edit the patch fields, then import once.
wtp batch import --in task-edits.json
```

Prefer focused batch export/import over generated PowerShell scripts or repeated
`task update` calls. Preserve each row's `updatedAt`; a stale token rejects the
whole import safely. Use `wtp help` and `wtp schema` for the JSON/CSV field and
clearing rules.

Batch export may combine `--status STATUS` with any of the six grouping
selectors, but repeatable `--task ID` exact selection cannot be combined with
status or grouping selectors. With no selectors it exports every task. In
JSON, omit an optional grouping field to preserve it and use `null` to clear
it; in CSV, a blank cell preserves it and `_clear` explicitly clears it. These
rows are patches, not snapshots, and import validates every row and the final
dependency graph before publishing the batch.

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

## 7. Coordinate handoffs

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

## 8. Verify completion

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
