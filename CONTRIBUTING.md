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
`.wtp/meta/wtp.lock` and `.wtp/meta/batch-update.json` files, plus export
directories, are ignored because they are local runtime artifacts.

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
retained `handoffs.json` collection, so task automation and portable exports
continue to work while handoffs are added.

## Code of conduct

Be respectful and constructive. Harassment or discriminatory behavior is not
welcome in project discussions or contributions.
