# Pre-release test plan

This document defines the repeatable qualification gate for a `wtp` release.
It complements [release-qa.md](release-qa.md), which is specifically about
release assets, direct downloads, installation scopes, and self-update.

## Current baseline

As of 2026-08-09, the repository has a strong automated baseline:

- 239 top-level tests in 23 test files and 83.9% statement coverage from
  `go test ./... -coverprofile=...`.
- `./scripts/verify.sh commit` and `./scripts/verify.ps1 commit` run formatting,
  unit/integration tests, `go vet`, and a compiled-binary smoke workflow.
- CLI integration tests spawn the real test executable in temporary
  repositories and cover external storage, linked worktrees, branch scopes,
  legacy stores, configuration changes, handoffs, and export behavior.
- The smoke scripts create disposable Git repositories and exercise create,
  dependencies, claim, comment, pause, resume, completion, export, and retained
  handoffs on Unix and Windows.
- `./scripts/verify.sh release` creates non-publishing GoReleaser snapshots,
  validates all six platform assets and checksums, installs the host binary in
  three disposable scopes, runs a workflow, and performs an in-place update
  through a local loopback release fixture.
- CI runs the commit gate on Linux and Windows and performs a pinned secret
  scan. A release tag runs the Linux release gate before publishing.

This is better than a typical unit-test-only project, but it is not yet the
complete gate described below. In particular, the compiled release candidate
is not subjected to a broad black-box repo/worktree matrix, real OS-process
contention, or black-box corruption and no-data-loss scenarios. The release
workflow also rebuilds for publication after validating a separate snapshot,
and it does not execute the release asset on macOS or execute the Windows
release asset in the tag-validation job. Results are console text rather than
a retained, machine-readable evidence bundle.

The 2026-08-09 audit ran the gates rather than only reading them. Commit
verification passed. Release verification failed in `scripts/release_qa.sh`
because its fresh-repository assertion still required the legacy
`.wtp/meta/index.json`; current named-branch behavior correctly created
`.wtp/meta/index-0d6e4079.json` for `main`. Task `wtp-0d6e4079-0006` is the
prerequisite repair and includes a regression test requirement.

## Release decision

A release is eligible only when every required gate passes on the release
commit. A skipped required scenario is a failure. An explicitly unsupported
platform scenario may be recorded as `not_applicable`, but the reason must be
present in the report.

| Gate | Purpose | Required result |
| --- | --- | --- |
| A. Commit | Fast correctness and static checks | Linux and Windows pass |
| B. Workflow | Real user workflows in disposable repos | Every scenario passes |
| C. Contention and recovery | Multi-process safety and no lost writes | Every invariant passes repeatedly |
| D. Candidate artifacts | Test the bytes intended for release | All checksums, installs, executions, and update cases pass |
| E. Evaluation | Review evidence, omissions, and flakiness | Agent issues an evidence-backed `GO` verdict |

The final verdict is one of `GO`, `NO_GO`, or `INCONCLUSIVE`. Only `GO` permits
publication. `INCONCLUSIVE` is used for missing evidence, an unexpected skip,
or an infrastructure failure; it is never treated as a pass.

## Isolation and repeatability contract

Every workflow runner must:

1. Build or receive one candidate binary and use that exact path throughout a
   run. Do not fall back to a `wtp` already on `PATH`.
2. Create a unique workspace below the operating system temporary directory.
   Set `HOME`, `USERPROFILE`, and relevant Git configuration to disposable
   locations. Do not read or mutate the user's real `.wtp`, Git config, or
   installation.
3. Record the source checkout status and a content manifest before and after
   the run. The checkout must be unchanged except for an explicitly selected
   report directory outside the checkout.
4. Use semantic JSON assertions for JSON output. Do not use substring matching
   when a field or collection can be decoded and compared.
5. Put a deadline on every child process and scenario. On failure, terminate
   descendants and retain stdout, stderr, exit code, scenario inputs, storage
   files, and a redacted environment allowlist.
6. Avoid the public network in hermetic mode. Update tests use the loopback
   fixture. A separately named published-release mode may use GitHub and must
   record every requested URL and asset digest.
7. Accept a fixed seed and repeat count. The same seed must produce the same
   scenario order and expected state. Temporary paths, UUIDs, and timestamps
   are normalized before comparing reports.
8. Clean successful workspaces by default and support a documented
   `--keep-workdir`/equivalent option for investigation. Never recursively
   delete an unresolved or non-temporary path.
9. Write a versioned JSON report plus a concise human summary. The report must
   include commit, dirty state, OS/architecture, Go and Git versions, candidate
   SHA-256, seed, start/end time, scenario status, duration, assertions,
   retained artifact paths, and final verdict.

## Scenario matrix

### B1. Repository and task lifecycle

Run with paths containing spaces and non-ASCII characters. Assert both human
and JSON behavior where they are public contracts.

- Empty named-branch repository: help/schema/version have no storage side
  effect; create tasks with every scheduling and worktree field; show, list,
  edit/update, comment, graph, ready, start, pause, next, done, and export.
- Statistics and configurable statuses: configure waiting, blocked, and failed
  project statuses; exercise their timestamp, transition, filtering, and
  scheduling semantics through the candidate binary. Verify overview and
  focused `stats` output in JSON and human formats, including deterministic
  zero-count status buckets, comments, dependencies, and global/task-scoped
  handoff metrics. Removing an in-use status must fail without changing task
  storage.
- Reusable advisory tasks: create ordered reusable definitions, assign them to
  a task, verify live resolution in task views and claims, rename a definition,
  delete it with task detachment, and validate the exported `reusable.json`
  catalog.
- Versioned planning: create and revise grouped planning records with
  dependencies, verify list/show/report output and planning isolation from
  ordinary task commands, validate the canonical planning export, then run a
  dependency-closed dry-run and promotion and verify the resulting executable
  tasks.
- Dependency graph: create a fan-out/fan-in graph, prove blocked tasks cannot
  start, complete prerequisites, and verify deterministic ready ordering.
- Agent ownership: matching, unassigned, and foreign-assigned tasks; prove a
  foreign task is not automatically claimed and explicit operations retain
  documented behavior.
- Retained handoffs: global and task-scoped append, replace, pagination, claim
  attachment, cutoff purge, ID purge, and export. Reads must not consume data.
- Export: empty and populated stores, stale managed output cleanup, idempotent
  repeated export, spaces in the output path, and rejection of unmanaged or
  overlapping destinations without partial modification.
- Legacy compatibility: canonical subcommands and retained legacy flags
  produce equivalent persisted state and compatible JSON.

### B2. Git and storage topology

- Main worktree plus at least two linked worktrees on distinct, case-sensitive
  branch names. Verify scoped IDs/indexes, current-scope automatic selection,
  explicit foreign-scope access, and merge-safe independent task creation.
- Branch rename: existing tasks keep their IDs and are not automatically
  adopted; new tasks use the new branch scope.
- Detached `HEAD` and non-Git directory: legacy allocation and selection only.
- Default repository-local store, valid relative `.wtp.json` store, absolute
  external store, and two repositories sharing one external store.
- Invocation from a nested directory, a symlinked path where supported, and a
  repository/worktree path containing spaces and Unicode.
- Invalid config, missing environment substitution, inaccessible storage, and
  config changes after data exists. Failures must be actionable and must not
  move, initialize, or partially mutate unintended storage.

### C1. Multi-process contention

Use separate OS processes, not goroutines calling provider methods directly.
Synchronize their start and enforce deadlines.

- At least 32 concurrent creates in one legacy scope and one named-branch
  scope. All commands succeed, IDs and UUIDs are unique, sequences contain no
  unexplained gaps, indexes have the expected next value, and every task
  decodes and validates.
- At least 16 concurrent `task next` calls against fewer eligible tasks. Each
  eligible task is claimed at most once, excess claimers return the documented
  no-work result, and assignee safety is preserved.
- At least 32 concurrent handoff appends. No record is lost or duplicated and
  ordering remains deterministic under the documented rule.
- Concurrent reads, status transitions, and exports while writers are active.
  Readers may observe an old or new atomic state, never corrupt JSON, duplicate
  logical tasks, or a mixed partial snapshot.
- A layered acyclic graph with converging dependency paths must expand every
  logical task once, use explicit references for repeated paths, and produce a
  number of graph records proportional to tasks plus direct dependency edges.
- Repeat the contention matrix at least 20 times in the pre-release gate and
  at least 100 times in a scheduled soak job. Also run `go test -race ./...`.

The hermetic runner launches every contention operation as a separate
candidate OS process. Each batch has a common start barrier, each process has
the `--timeout` deadline, and the batch has `--suite-timeout`. A failed
assertion stops the next iteration, terminates descendant process groups or
trees, and keeps the complete iteration root (store files, command output,
and report inputs). The scenario report records its iteration and schedule
seed, process count, exit-code vector, duration vector, and invariant failures.

### C2. Failure and recovery

For every rejected operation, hash the relevant files before and after and
assert the documented preservation boundary.

- Corrupt task JSON, canonical invariant violations, missing dependencies,
  dependency cycles, corrupt handoffs, corrupt/missing/stale indexes, UUID-name
  legacy files, and duplicate/residue files from interrupted status moves.
- Fresh lock, malformed lock, and stale lock behavior. A fresh lock must time
  out without mutation; a stale lock must be recovered safely. Tests may use a
  runner-only clock seam, but must not weaken production stale-lock rules.
- Read-only directories/files, unwritable install directories, failed atomic
  publication, and destination collisions.
- Send termination to a runner-controlled writer at defined fault points. If a
  production hook is necessary, it must be compile-time or dependency-injected
  test code and impossible to enable in release binaries.
- Reopen the store after every fault, run list/show/graph/export, and validate
  that it is either the complete old state or complete new state.

### D1. Release candidate and updater

- Produce all six standalone assets once. Validate exact filenames, executable
  formats, embedded version/commit/build date, executable permissions where
  applicable, and exactly one checksum entry per asset.
- Execute the native candidate on the controlled Linux, Windows, and macOS
  development environments for the repository/task lifecycle subset.
  Cross-compilation alone is not execution. These environments may use the
  private access configuration required by the release team and are not
  GitHub-hosted runners.
- Exercise project-local, user-local, disposable global-PATH, paths with
  spaces, and a symlink launch path where supported.
- Through a programmable loopback server, test successful upgrade, equal/older
  no-op, invalid tag, missing/duplicate assets, malformed checksums, checksum
  mismatch, truncated/failed download, timeout, unsafe redirect/URL, permission
  failure, and replacement failure. The installed executable and `.wtp` data
  must remain byte-for-byte unchanged for every failed update.
- On Windows, wait for the deferred replacement helper, start the replacement,
  verify version and workflow data, and exercise helper rollback/error-file
  behavior. Source inspection or cross-compilation is not a substitute.
- Prefer promoting the exact validated candidate artifacts. If publication
  must rebuild them, compare reproducible fields/digests or explicitly record
  why the published bytes cannot be claimed as the validated bytes.

## Execution and evidence review

The implemented top-level commands are:

```sh
./scripts/verify.sh prerelease --seed 1 --repeat 1 --timeout 30s --suite-timeout 3m \
  --report /tmp/wtp-qa/quick.json
```

with an equivalent PowerShell command:

```powershell
./scripts/verify.ps1 prerelease --seed 1 --repeat 1 --timeout 30s --suite-timeout 3m `
  --report "$env:TEMP/wtp-qa/quick.json"
```

The workflow entry points build a disposable candidate when `--candidate` is
omitted. For release qualification, pass the exact artifact under review:

```sh
./scripts/verify.sh prerelease \
  --candidate /absolute/path/to/wtp \
  --seed 1 --repeat 20 --timeout 30s --suite-timeout 3m \
  --report /tmp/wtp-qa/prerelease.json
```

```powershell
./scripts/verify.ps1 prerelease \
  --candidate C:\qa\wtp.exe \
  --seed 1 --repeat 20 --timeout 30s --suite-timeout 3m \
  --report "$env:TEMP\wtp-qa\prerelease.json"
```

The Go runner defaults to `--repeat 20`. A scheduled/agent soak uses the
same candidate and seed with at least 100 iterations:

```sh
./scripts/verify.sh prerelease \
  --candidate /absolute/path/to/wtp \
  --seed 20260810 --repeat 100 --timeout 30s --suite-timeout 3m \
  --report /tmp/wtp-qa/soak.json --keep-workdir
```

Compare two same-seed runs using only their normalized projections:

```sh
jq -S '.normalized' /tmp/wtp-qa/run-a.json > /tmp/wtp-qa/run-a.normalized.json
jq -S '.normalized' /tmp/wtp-qa/run-b.json > /tmp/wtp-qa/run-b.normalized.json
cmp /tmp/wtp-qa/run-a.normalized.json /tmp/wtp-qa/run-b.normalized.json
```

The Go runner can also be invoked directly:

```sh
go run ./cmd/wtp-prerelease-qa --candidate /absolute/path/to/wtp \
  --source-root "$PWD" --seed 1 --repeat 1 --timeout 30s \
  --report /tmp/wtp-qa/report.json --keep-workdir
```

`--candidate` is always an explicit path; the runner never resolves `wtp`
from `PATH`. Each command receives a disposable `HOME`/`USERPROFILE`, XDG
configuration directory, global Git config path, and `GIT_CONFIG_NOSYSTEM=1`.
The candidate is run only in disposable repositories and no update/network
scenario is enabled in this hermetic mode. A successful run removes its
fixture root. Failures and `--keep-workdir` retain it and include the path in
the report. Every child command has the requested timeout; timed-out Unix
process groups and Windows process trees are terminated.

The `failure-recovery` scenario is the black-box Gate C2 matrix. It seeds
real stores, corrupts or interrupts only disposable copies, invokes public
`list`, `show`, `graph`, `handoff`, and `export` commands, and records a
`preservation` entry for every rejected case. Each entry contains sorted
before/after SHA-256 inventories and must be unchanged for rejected input;
missing indexes, legacy UUID filenames, stale indexes, and valid status-move
residue are explicitly documented recovery cases and must end as one valid
store. Lock files are excluded from persistent manifests because they are
transient process machinery. Fresh and malformed locks are terminated at a
350ms runner deadline, while stale locks are recovered normally.

The provider's publication seams are dependency-injected and default to the
production filesystem in release builds. Focused tests exercise deterministic
replace failures; the black-box runner exercises the resulting public
commands and real-process reopen/invariant checks. No fault-injection switch
is present in release binaries.

The versioned report schema is `wtp-prerelease/v1`:

```json
{
  "schemaVersion": "wtp-prerelease/v1",
  "status": "passed|failed",
  "verdict": "GO|NO_GO|INCONCLUSIVE",
  "seed": 1,
  "repeat": 1,
  "startedAt": "RFC3339 timestamp",
  "endedAt": "RFC3339 timestamp",
  "durationMs": 1234,
  "platform": {"os":"linux", "arch":"amd64", "goVersion":"...", "gitVersion":"..."},
  "candidate": {"path":"...", "sha256":"64 hex characters", "version":{}},
  "source": {
    "root":"...", "commit":"...", "dirty":false,
    "statusBefore":"...", "statusAfter":"...",
    "manifestBefore":[{"path":"...", "sha256":"...", "size":123}],
    "manifestAfter":[{"path":"...", "sha256":"...", "size":123}],
    "manifestUnchanged":true
  },
  "scenarios": [{
    "name":"contention-creates", "status":"passed", "iteration":1,
    "scheduleSeed":123, "processCount":64,
    "processExitCodes":[0], "processDurationsMs":[12],
    "invariantFailures":[], "durationMs":123,
    "assertions":["typed task fields"], "artifacts":[],
    "commands":[{
      "argv":["$CANDIDATE", "--json", "task", "list"],
      "environment":{"HOME":"$WORKDIR/home", "GIT_CONFIG_NOSYSTEM":"1"},
      "stdout":"...", "stderr":"", "exitCode":0,
      "durationMs":12, "assertions":["valid JSON"]
    }]
  }],
  "artifacts":["..."],
  "platformSkips":[],
  "race":{"status":"passed|failed|not_applicable|not_run", "exitCode":0,
    "durationMs":123, "reason":""},
  "reproduction":"go run ./cmd/wtp-prerelease-qa ... --keep-workdir",
  "normalized": {
    "schemaVersion":"wtp-prerelease/v1", "seed":1, "repeat":1,
    "candidateSha256":"...", "status":"passed",
    "sourceManifestSha256":"...", "raceStatus":"passed",
    "scenarios":[{"name":"lifecycle", "status":"passed", "assertions":["..."]}]
  }
}
```

Contention scenario entries always record the complete process vectors; the
example shortens those arrays only for readability. A passed report must
contain every required B and C1 scenario, complete process vectors, and an
explicit race result. A race run is `not_applicable` only when the
platform/toolchain explicitly reports an unsupported race architecture or
missing C compiler; timeouts and ordinary test failures remain failures.
Failed reports print the one-line `reproduction` command and retain the
artifact root.

Command stdout/stderr and paths in evidence replace disposable roots, UUIDs,
and timestamps with stable tokens. The `normalized` projection excludes
run-specific timings and paths and is the field to compare across repeated
runs with the same seed.

For the remaining contention, fault-recovery, and release-candidate gates, also run:

```sh
./scripts/verify.sh commit
go test -race ./...
./scripts/verify.sh release
```

The Unix release-QA fixture writes a second report for the updater matrix. Set
`WTP_QA_REPORT=/absolute/path/updater.json` to retain it. Its schema is
`wtp-release-qa/v1` and includes equal/older no-ops, invalid tags, missing and
duplicate assets, malformed and mismatched checksums, failed/truncated/time-
out downloads, unsafe URLs, symlink launch, unwritable targets, and
replacement-failure coverage. Every failed case records the candidate digest,
mode, and byte-for-byte `.wtp` manifest preservation result. Windows native
execution and deferred-helper rollback remain required final-gate evidence;
Unix and cross-compilation do not claim that coverage.

An evaluation agent must then inspect the JSON report and retained failure
artifacts, not merely the final exit code. It must confirm:

- the candidate digest matches the artifact being proposed for release;
- all required scenarios ran on the required native platforms;
- every assertion and preservation check passed;
- there are no unexplained skips, timeouts, retries, or leftover processes;
- repeated runs did not change normalized outcomes or reveal flakiness;
- the source checkout and real user environment were unchanged; and
- known limitations are called out in the verdict rather than silently waived.

The agent writes `pre-release-verdict.md` beside the JSON report with the
candidate identity, evidence paths, failures/skips, residual risks, and one of
the three allowed verdicts. Any failed assertion produces `NO_GO`; missing or
unreadable evidence produces `INCONCLUSIVE`. The complete model-ready
instruction is maintained in [pre-release-evaluator.md](pre-release-evaluator.md).

The deterministic candidate manifest, shard merge, and preflight commands are
separate from model judgment. They record all six exact standalone bytes,
embedded metadata, checksums, native platform identity, and updater evidence;
they refuse to merge a shard with a different commit, seed, schema, or digest.
Release operators run the native qualification locally in the controlled
development environments, retain the candidate, shard, updater, merged,
preflight, and verdict evidence with their release records, and inspect it
before tagging. The tag release workflow does not run or consume this native
qualification, and publication rebuilds the release assets in GitHub Actions.

Use Go 1.22 or the version in `go.mod`, Git, pinned GoReleaser 2.17.0,
PowerShell on Windows, and the platform's native executable checks. Native
checks use loopback-only updater fixtures; only the separately documented
published-release mode may contact GitHub. Reports and failure artifacts stay
in the operator-controlled evidence location and contain no secrets.
Successful workdirs are disposable; `--keep-workdir` retains a runner-owned
temporary root for investigation. Release operators should inspect the
retained verdict and residual risks before tagging. The publish job has the
sole `contents: write` permission.

## Implementation backlog

The repository backlog contains five Luna High tasks that implement this plan:

- `wtp-0d6e4079-0006`: repair the currently failing release QA index assertion.
- `wtp-0d6e4079-0002`: cross-platform hermetic workflow matrix and report schema.
- `wtp-0d6e4079-0003`: real multi-process contention and repeat/soak coverage.
- `wtp-0d6e4079-0004`: black-box fault injection, recovery, and preservation checks.
- `wtp-0d6e4079-0005`: pre-release orchestration, native platform CI, artifact
  promotion/identity, and agent evaluation instructions.

Start with `wtp-0d6e4079-0006`, then `wtp-0d6e4079-0002`. The contention and
fault tasks can proceed in parallel after the workflow harness, and the final
gate depends on both. Each task contains
file-level guidance, required scenarios, commands, and acceptance criteria so
an agent can execute it without relying on unstated context.
