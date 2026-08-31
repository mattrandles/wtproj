# Contributing to wtp

Thanks for contributing. Please open an issue before starting substantial work
so the proposed change can be discussed and tracked.

## Development

1. Fork the repository and create a focused branch.
2. Keep changes small, documented, and covered by tests where behavior changes.
3. Run `./scripts/verify.sh commit` before opening a pull request. On Windows,
   run `./scripts/verify.ps1 commit`.
4. Describe the problem, solution, verification, and any follow-up work in the
   pull request.

Before creating a release tag, run the full, non-publishing gate with
`./scripts/verify.sh release` or `./scripts/verify.ps1 release`. See the
[verification section in the README](README.md#verification) for required
tools, runtime expectations, and platform limitations.

## Secret scanning

Never commit credentials or private keys, including test credentials that may
be mistaken for live material. Use clearly non-secret placeholders and resolve
real values from the environment.

CI runs the pinned Gitleaks version and checksum recorded in
`.github/workflows/ci.yml` against both the checked-out tree and all fetched
history. Before opening a pull request, run the same scopes with that version:

```sh
gitleaks dir . --redact=100 --max-archive-depth=3 --max-decode-depth=5
gitleaks git . --log-opts="--all --full-history" --redact=100 \
  --max-archive-depth=3 --max-decode-depth=5
```

Do not add a broad allowlist for a finding. Review it first, then use the
narrowest path, rule, or fingerprint exclusion that documents the false
positive without concealing similar future findings.

## Task backlog policy

This repository dogfoods `wtp`. Its `.wtp/` task records are intentionally
version-controlled so planning and task history travel with the source.
Do not add a broad `.wtp/` ignore rule. The transient
`.wtp/meta/wtp.lock`, `.wtp/meta/batch-update.json`, and
`.wtp/meta/reusable-update.json` files, plus export directories, are ignored
because they are local runtime artifacts. The durable `.wtp/reusable.json`
catalog is intentionally tracked with task files.

Use the CLI for normal task changes rather than editing task JSON directly:

```sh
wtp task list
wtp task comment <task-id> --message "..."
wtp task done <task-id>
```

For a focused multi-task edit, prefer the batch contract over generated
PowerShell scripts or repeated update calls:

```sh
wtp batch export --status todo --out task-edits.json
# Edit only the intended patch fields, then import once.
wtp batch import --in task-edits.json
```

Batch rows require the exported `updatedAt` token, so a concurrent change makes
the complete import fail safely. See `wtp help`, `wtp schema`, and the README
for JSON version 1, CSV `_clear`, selector, response, and recovery-journal
details.

### Reusable advisory definitions

Reusable definitions are store-global advisory instructions, not extra queue
tasks. They have no status, lifecycle, dependency, execution, ordering, or
completion-enforcement behavior, and WTP never infers a group's final task.
Create, list, show, update, and delete them with `wtp reusable`; names are
case-insensitive exact selectors and UUIDs are stable. Update preserves the
UUID and creation time, so renaming a definition updates future live views
without rewriting task files. Delete atomically detaches the definition from
every task in every status and branch scope, including done tasks, while
preserving the order of remaining assignments.

Tasks accept repeatable `--reusable NAME_OR_ID` on create/update/edit. The
provider stores only ordered canonical definition UUIDs. Update replaces the
whole list; one `--reusable=` clears it, while duplicates and mixed empty /
non-empty occurrences are rejected. Detailed task views and `task start` /
`task next` claim results resolve the current definitions in assignment order;
compact list/graph output remains unchanged. Human output has a
`reusableTasks:` section; JSON keeps `reusableTaskIds` and adds the resolved
`reusableTasks` array. Claim-attached handoffs remain additive and separate.

The version-1 `.wtp/reusable.json` catalog is shared by all branches,
worktrees, and projects using the same `wtpDir`. Include it in commits or
backups and use `wtp export` for a consistent canonical snapshot. An absent
catalog is compatible empty storage and reads do not create it. The transient
`.wtp/meta/reusable-update.json` delete journal is recovered under
`.wtp/meta/wtp.lock`: prepared journals roll back, committed journals roll
forward, and a failed recovery retains the journal for diagnosis. Keep a store
backup before any manual recovery, ignore the journal alongside the lock and
`batch-update.json`, and never add an ignore rule for the durable catalog or
task directories. See `wtp schema` for the exact journal and catalog contract.

### Versioned planning workflow

Planning is a flat-file-only, store-global namespace nested at
`.wtp/planning/{toplan,researched,planned,rejected}/`. It is separate from
the executable `todo`, `inProgress`, `paused`, and `done` directories. The
four planning statuses and their only direct transitions are:

```text
toplan     -> researched | rejected
researched -> toplan | planned | rejected
planned    -> researched | rejected
rejected   -> toplan
```

Same-state and unlisted moves fail. `rejected` is the revisable replacement
for deletion; planning has no delete, comment, start, next, ready, done,
graph, or batch command. Planning records always retain the full task payload,
including all metadata, dependencies, comments, and reusable references, and
always have null `startedAt`/`completedAt`. `featureId` is the stable feature
key and `feature` is its independent display name.

There is one UUID/short-ID namespace and one dependency graph across planning
and executable records, including all branch scopes in a shared `wtpDir`.
Validate global identity, missing dependencies, and cycles before publishing
changes. Planning list/show/report/promote are store-wide; normal task
list/show/ready/next/start, stats, batch, and graph intentionally exclude
planning. This separation must remain visible in provider capabilities and
documentation. The only supported backend is the flat-file provider.

The public planning commands are `planning create`, `list`, `show`, `update`,
`set-status`, `report`, and `promote`. Create accepts required `--title`,
optional `--status`, and the complete task-create metadata flags:
`--description`, `--priority`, `--estimate`, `--lane`, `--model`,
`--issue-id`, `--project`, `--milestone`, `--version`, `--feature-id`,
`--feature`, `--git-repo`, `--git-branch`, `--worktree-name`,
`--worktree-dir`, `--depends-on`, `--reusable`, and `--agent`. Update takes
the same mutable metadata flags except `--status`; show and set-status accept
one UUID/full short ID. List/report take `--status` plus any combination of
the six grouping selectors. Promote takes those selectors and optional
`--dry-run`, requiring at least one selector. `--depends-on` and `--reusable`
are the only repeatable flags; update omission preserves, explicit empty
values clear, and a single empty list occurrence clears the complete list.

Grouping selectors are trimmed, case-insensitive exact AND matches. There is
no wildcard, substring, version parsing, or feature key/name fallback. The
report hierarchy is exactly project -> version -> milestone, with total and
four fixed status counts at every level. Promotion selects only planned
records and rejects the entire group unless every reachable planning
dependency—also through executable vertices—is selected and planned. Dry-run
must be read-only and publication must preserve all metadata while changing
only the allowed execution lifecycle fields.

Promotion uses the dedicated version-1 `.wtp/meta/planning-promote.json`
prepared/committed journal and the global lock. Store recovery preflights
pending transactions and always processes `batch-update.json`, then
`reusable-update.json`, then `planning-promote.json`. Prepared promotion rolls
back; committed promotion rolls forward; failed recovery retains the journal.
Canonical export includes flat `planning/<planning-UUID>.json` records,
`handoffs.json`, and `reusable.json`; indexes, locks, journals, and planning
status directories are not exported. A complete example and the exact JSON
contracts are maintained in the README and `wtp schema`; update both when
behavior changes.

### Merge-safe branch-scoped task history

Tasks created on a named Git branch use an opaque scoped ID in the form
`wtp-BBBBBBBB-NNNN`. The eight-character token is derived from the exact branch
name, and each branch scope has its own allocation index under `.wtp/meta/`.
Branch task files and indexes therefore use distinct paths and can be merged
without competing for one shared numeric sequence. Existing `wtp-NNNN` records
remain in the legacy namespace; do not rewrite their IDs.

`wtp task ready` and `wtp task next` select the current branch scope first and
legacy tasks second. They do not automatically select foreign branch-scoped
tasks, even when `wtp task list` displays them. Use `wtp task start <task-id>`
with the exact task ID when intentionally starting foreign or older work.

Branch scopes follow exact branch names rather than branch objects. After a
branch rename, newly created tasks use the new branch's scope, while existing
IDs and files retain their old scope and are not automatically adopted or
migrated. Start an old task explicitly when that is the intended action.

### Retained handoff context

Use retained handoffs for context that should survive a worker boundary. Keep
repository-wide notes global and attach task-specific notes with `--task`:

```sh
wtp handoff write --message "Investigated parser edge cases" --agent Tony
wtp handoff write --task <task-id> --message "Use the existing tokenizer tests"
wtp handoff get --task <task-id> --all
```

Writes append by default; `--replace` replaces only the selected scope.
Handoff reads and claim attachment are non-consuming. `wtp task start` and
`wtp task next` attach retained records for the claimed task, newest first, so
workers should use `wtp handoff purge --task <task-id>` only when that context
is deliberately retired. The default `wtp handoff get` shows the newest
global record; use `--all-scopes --all` to discover every scope. Human output
provides follow-up hints when records are hidden by the default limit or scope.

Purge uses exactly one of `--id`, `--global`, `--task`, or `--all-scopes`, with
at most one cutoff: `--before RFC3339` or `--older-than DURATION`. Cutoffs
remove records strictly older than the computed instant; `--id` cannot be
combined with a cutoff. See `wtp help` and `wtp schema` for the JSON response
shapes and `.wtp/handoffs.json` contract.

The legacy task action flags remain supported. The legacy
`--export-tasks=<directory>` form remains an export alias and includes the
retained `handoffs.json` collection and the version-1 `reusable.json` catalog,
so task automation and portable exports continue to work while both retained
collections are included.

## Code of conduct

Be respectful and constructive. Harassment or discriminatory behavior is not
welcome in project discussions or contributions.
