# Versioned planning lifecycle — architecture contract v1

Frozen by `wtp-0d6e4079-0066` and implemented by the dependent planning tasks.
This is the normative version-1 behavior contract for the versioned planning
workflow, after grouping and reusable advisory tasks. The public README,
`wtp help`, `wtp schema`, and contributor guidance summarize this contract;
this document keeps the storage, transaction, and compatibility details in one
place. Architecture v1 is not an added `version` property on records: record
`version` is free-form grouping metadata. Only the promotion journal has a
format version.

## Ownership and capability boundaries

`internal/core/planning.go` owns the fixed status/transition declarations,
`PlanningItem`, `PlanningItemView`, and create/update inputs. Planning records
repeat the complete `Task` payload with `PlanningStatus` instead of `Status`;
they do not embed `Task` or inherit execution validation. Field/tag parity
tests must fail if either metadata surface drifts. Later validation reuses
metadata rules without teaching the execution catalog about planning states.

`internal/provider/planning.go` defines separate optional capabilities:
`PlanningReader`, `PlanningCreator`, `PlanningEditor`, `PlanningPromoter`, and
their composition `PlanningProvider`. Do not extend `provider.Provider` or
advertise methods through nonfunctional concrete-provider stubs. CLI handlers
assert only the capability they require. Non-flat-file support is excluded;
unsupported providers return an explicit capability error, never a fallback.

`internal/planning/report.go` owns report types; future aggregation takes a
`PlanningReader` plus `PlanningFilter`, never the execution stats provider.
`internal/provider/flatfile/planning_contract.go` owns storage names, journal
envelope, and recovery order. Runtime adoption belongs to the dependent tasks.

Explicitly excluded: planning deletion, planning comments, planning batch
editing/import/export, execution-graph expansion, claims/execution of planning
items, non-flat-file support, auto-promotion, and reusable-instruction execution.
There is no planning `edit`, `start`, `next`, `ready`, `done`, `graph`, `comment`,
`delete`, or `batch` command. Reject and later reopen items instead of deleting.
Existing comments are preserved and displayed; no planning operation adds one.

## Fixed lifecycle and timestamps

Status spelling and canonical order are exactly `toplan`, `researched`,
`planned`, `rejected`. Parsing accepts exact case-sensitive values only;
empty, padded, differently cased, unknown, and execution statuses are errors.
Create defaults an omitted/zero status to `toplan` at its boundary and accepts
any of the four explicit statuses. An explicitly blank CLI status is invalid.

| From | Permitted destinations |
| --- | --- |
| toplan | researched, rejected |
| researched | toplan, planned, rejected |
| planned | researched, rejected |
| rejected | toplan |

Same-state and every other direct move are errors, including toplan -> planned
and rejected -> researched/planned. Rejected is revisable, not terminal. Status
changes use `SetPlanningStatus`; metadata update cannot carry status.

Every planning record has `startedAt: null` and `completedAt: null`, even when
planned or rejected. Reject non-nil values on load; do not silently repair them.
`createdAt` and `updatedAt` are required UTC times, with updatedAt >= createdAt.
Creation sets both to one clock value. Actual edits and status changes preserve
createdAt and use max(now.UTC(), previous updatedAt + 1ns). A metadata no-op
preserves updatedAt and exact file bytes. No-op status changes remain errors.
Planning loads do not synthesize execution timestamps or automatic comments.

## Complete payload and input semantics

Stored JSON names, field order, and `omitempty` rules match `core.Task`:
`id`, `shortId`, `title`, `description`, `priority`, `estimate`, `lane`, `model`,
`issueId`, `project`, `milestone`, `version`, `featureId`, `feature`, `gitRepo`,
`gitBranch`, `worktreeName`, `worktreeDir`, `status`, `assignee`, `dependencies`,
`comments`, `createdAt`, `updatedAt`, `startedAt`, `completedAt`,
`reusableTaskIds`. There is no stored lifecycle discriminator or view decoration.
Storage location and the typed loader establish the lifecycle. Canonical writes
use the existing indented JSON plus newline convention. Legacy omitted optional
fields remain valid. Creates emit empty arrays for dependencies/comments; empty
reusable assignments are omitted, matching task storage. No new metadata is lost
on update, status move, or promotion.

Reuse task UUID/short-ID, title, priority, estimate, origin path, grouping,
comment, dependency, reusable-ID, and UTC validation rules. Trim inputs as task
create/update does, preserving internal Unicode and multiline text. Required
title cannot be blank. Present grouping values cannot be blank on create.
CLI create discovers omitted Git/worktree fields at the existing invocation
boundary; an explicit empty override suppresses discovery. Providers never
discover origin and the origin branch does not control allocation scope.

Update exposes every mutable task field except status. Omitted values preserve;
explicit empty optional values clear; blank title is invalid. Identity, comments,
and timestamps are not user-editable. Require at least one supplied patch field
at the CLI; a supplied unchanged value succeeds as a no-op. Dependencies use
`OptionalStrings`, intentionally replacing the legacy task-update comma-string
input; supplied lists replace, empty clears. Resolve UUID/full short-ID
selectors globally, deduplicate and sort canonical UUIDs with the existing
dependency normalization. Self references, missing references and cycles fail.

Reusable names/UUIDs resolve against one current store-global catalog under
the lock. Preserve caller order, reject duplicate resolved definitions, and
store canonical UUIDs only. Omission preserves; a supplied list replaces;
empty clears. Every planning view resolves live definitions in that same
snapshot; missing/malformed catalogs or missing referenced definitions fail.
An absent catalog is valid only when there are no assignments. Views never
contain readiness or handoffs. Human show includes all populated metadata,
dependencies, stored comments, and ordered reusable definitions.

Reusable deletion must atomically detach references from BOTH lifecycles,
preserving remaining order, with no comments or lifecycle changes. Extend the
existing reusable-update snapshot validation/recovery to planning paths; do not
invent a second deletion flow or leave dangling planning IDs. Its existing
`detachedTaskCount` counts all detached records across both lifecycles. This is
referential-integrity maintenance, not a planning deletion/comment operation.

## Flat-file layout, identity, and validation snapshot

```text
<wtpDir>/                         # normally .wtp, shared across branches
  <execution-status>/<shortId>.json
  planning/
    toplan/<shortId>.json
    researched/<shortId>.json
    planned/<shortId>.json
    rejected/<shortId>.json
  reusable.json
  handoffs.json
  meta/
    index.json
    index-<branchToken>.json
    wtp.lock
    batch-update.json             # transient
    reusable-update.json          # transient
    planning-promote.json         # transient
```

Initialize all four planning leaf directories without changing existing task
files. The `planning` root is a namespace, not an execution status directory.
Reject a configured execution status named `planning` at flat-file open with a
clear collision error; do not migrate or delete its files. Configured execution
statuses named `toplan`, `researched`, `planned`, or `rejected` remain independent
at the store root, with their configured execution semantics. Direct JSON task
files in the planning root and unknown planning status directories containing
records are errors, not execution tasks or silently ignored planning records.

Canonical live names are short IDs; accept legacy UUID filenames only when
the basename matches that record. Require directory/status agreement. Validate
every record and the complete snapshot before repairs or cleanup. Same UUID
with inconsistent short IDs, shared short IDs, identical duplicates, and tied
updatedAt conflicting copies are errors. Within planning only, a newer copy
can be recognized as status-move residue iff identities and all other fields
match, timestamps are nil, updatedAt increases, and the old -> new transition
is in the fixed table. Keep the newer copy; stale-file cleanup follows the
existing best-effort convention after validation. Other malformed planning
records fail without execution-style timestamp/comment repair. Cross-lifecycle
duplicates require a valid promotion journal; never guess a winner by age.

There is one UUID namespace for executable/planning records, not one per
lifecycle, directory, project, or version. Use `core.NewID` with a union collision
check. Reuse existing `index.json` / `index-<branchToken>.json`, branch-token
collision guards, short-ID parsing, and `nextAvailableShortID` allocation rules
under the global lock. Union identity validation runs before either creator
publishes an allocation; a stale index skips IDs used by either lifecycle.
Never create a planning index or renumber IDs. Preserve existing allocation
failure policy (validation publishes nothing; certain write failures may leave
monotonic gaps). Promotion allocates no UUID or short ID and writes no index.

Build one locked validation snapshot partitioned into execution/planning slices,
plus global identity/dependency lookup and one reusable catalog snapshot.
Dependencies may point in either direction. Validate missing references and
cycles across BOTH lifecycles on load, create, update, and final batch graphs.
Do not feed planning states into an execution catalog by coercing them to todo
or adding fake custom statuses. Only an executable dependency in `done`
resolves readiness; all planning states block until promoted and then done.

Execution queries remain partitioned: task list/show/ready/next/start, stats
overview/focused/series, batch export/import, graph, and task-scoped handoffs
return or mutate executable records only, even for matching IDs or status
names. Exact planning IDs are errors in operational mutations/selection.
Graph nodes and edges have executable endpoints only; do not expand planning
blockers. Existing dependency metadata/counts may include a planning reference,
but stats never count planning records and reverse-dependency counts do not
include incoming planning records. Readiness may report a planning blocker as
`<shortId> (<title>; planning <status>)` inside the existing blocked-reason text.
Global integrity validation can fail on planning corruption without exposing it
as an operational query result. Branch claim eligibility/preferences remain
unchanged. Planning list/show/promotion operate store-wide across branch scopes.

## Planning CLI and query contracts

Root `--json` selects JSON; no new legacy global action flags. Every command
rejects unknown flags, extra positionals, and duplicate singleton flags. Flags
may appear in any order after the required identifier positionals. Grouping
flags are `--issue-id`, `--project`, `--milestone`, `--version`, `--feature-id`,
`--feature`. Supplied selectors are non-empty after trimming. `--agent` is the
existing assignee spelling, available only on create/update (not a claim).

| Command | Arguments and output |
| --- | --- |
| `wtp planning create` | Required `--title`; optional `--status` and all task-create metadata flags; returns PlanningItemView |
| `wtp planning list` | Optional single `--status` plus six grouping selectors; returns non-nil array of PlanningItemView |
| `wtp planning show ID` | One UUID/full short ID, no agent/filter flags; returns PlanningItemView |
| `wtp planning update ID` | All mutable task metadata flags except `--status`; returns PlanningItemView |
| `wtp planning set-status ID STATUS` | Exactly two positionals, no agent mutation; returns PlanningItemView |
| `wtp planning report` | Optional single `--status` plus six grouping selectors; returns planning.Report |
| `wtp planning promote` | At least one grouping selector, optional `--dry-run`; returns PlanningPromotionResult |

Create/update metadata flags: `--title`, `--description`, `--priority`,
`--estimate`, `--lane`, `--model`, six grouping flags, `--git-repo`, `--git-branch`,
`--worktree-name`, `--worktree-dir`, `--agent`, `--depends-on`, and `--reusable`.
Priority/estimate accept existing task values. `--depends-on` and `--reusable`
are repeatable exceptions to duplicate-flag rejection. Dependencies accept
comma-separated IDs per occurrence (preserving existing CLI compatibility);
reusable names are not comma-split. One empty occurrence clears on update and
cannot be mixed with non-empty occurrences; empty occurrences on create fail.
Blank optional metadata clears on update, including grouping and origin fields.
An explicit `--status=` never means default. No `--assignee` alias is added.

List/report share `PlanningFilter`: optional status AND the shared six-field
`GroupingFilter`. Match trimmed selectors with case-insensitive exact AND
semantics; omitted fields are unrestricted, unset metadata cannot match a
non-empty selector. `featureId` is a stable key, `feature` a display name;
neither substitutes for the other. No wildcard, substring, semantic-version
parsing, or hierarchy-derived selection. Keep stored casing unchanged.
Use the existing shared matcher through an explicit metadata projection or a
shared field accessor; do not copy a subtly different planning matcher.

List and promotion order is createdAt ascending, then shortId lexical. Show
resolves only the planning partition, using exact UUID/full short ID, with
deterministic not-found/ambiguity errors. Human create/update/status results
include identifier/title/status; list stays compact. JSON emits only the typed
result on stdout; failures never emit partial result JSON. Planning report
has no chart flag, execution stats aliases, agent filter, or handoff loading.

## Report shape

Root and every project/version/milestone node contain `totalItems` and
`statusCounts`, an array of `{ "value": STATUS, "count": N }` in fixed status
order including zeros. Root has `projects`; project nodes have `value` and
`versions`; version nodes have `value` and `milestones`; milestone nodes have
`value` and no child field. No flattened alternatives or generic recursive
children property. Filter before aggregation; each item contributes once per
level. Emit observed hierarchy values only, with non-nil child arrays. An empty
report has totalItems 0, four zero buckets, and `projects: []`.

Unset hierarchy values are `""` in JSON and `(unset)` in text. Preserve stored
casing and keep differently cased buckets distinct even though selection is
case-insensitive. Sort each level empty first, then Go string lexical order
(no locale folding or semantic-version sorting). Human output is a readable
project/version/milestone tree with aligned counts in the same four-state order.

## Dependency-closed promotion and preview

`PreviewPlanningPromotion(GroupingFilter)` returns
`PlanningPromotionResult[PlanningItemView]` with dryRun true;
`PromotePlanningItems(GroupingFilter)` returns
`PlanningPromotionResult[TaskView]` with dryRun false. Both JSON forms have
exactly `dryRun`, `count`, `items`; count equals the non-empty items length.
Preview items retain planned status and no readiness. Publish items are todo
execution views, decorated after the complete group is published, without
claim-attached handoffs. Human output says would-promote/promoted count and
lists the exact ordered items. No automatic claim occurs.

Require at least one of the six non-empty selectors. No status flag, explicit
task IDs, agent, positional selector, or implicit all-items promotion. Select
only planned records matching every filter; non-planned matches are ignored.
Zero selection is an error, not successful count zero.

Walk the transitive dependency graph from every selected item, including
through executable vertices: every encountered planning dependency must itself
be in the selected set and planned. Executable dependencies need not match the
filters or be done and remain unchanged. Do not auto-add missing planning
dependencies or promote them silently. Reject the whole group and show a
missing chain of short IDs with planning status. Choose errors deterministically:
selected roots in selection order, outgoing dependencies by canonical UUID,
first missing planning dependency in depth-first traversal. Cycles/missing
global references are already store-integrity errors. Example: selected A ->
executable B -> planning C still requires C in the selected set.

Preview acquires the ordinary store lock for a consistent read snapshot, but
never initializes directories/indexes, repairs files, updates timestamps, or
creates/consumes journals. An absent store yields no-match without creation.
If pending recovery or residue/metadata repair prevents a clean snapshot,
return an actionable recovery-required error without writes. The CLI/provider
opening path must preserve this property, not invoke mutating initialization
before preview. Lock bookkeeping is the only transient coordination exception;
all durable files and journals must remain byte-identical. Preview is not a
reservation: publish repeats selection and validation inside its global lock.

Publish preserves UUID, shortId, createdAt, all metadata, assignee, dependency
UUIDs, comments, reusable IDs and their order. Only status -> todo, updatedAt,
and nil execution lifecycle normalization may differ. Capture one UTC clock
value and choose a common timestamp max(now, every selected updatedAt + 1ns),
so all resulting updatedAt values are equal and strictly advance. No allocation,
origin rediscovery, auto-assignment, handoff creation, or instruction execution.

## Dedicated transaction and recovery

The strict version-1 journal is `.wtp/meta/planning-promote.json`. Envelope:
`{ "version": 1, "state": "prepared|committed", "selectedIds": [UUID, ...],
"entries": [{ "before": { "path": STRING, "data": BASE64 },
"after": { "path": STRING, "data": BASE64 } }, ...] }`.
All properties are required; arrays are non-null/non-empty and have matching
length/order. UUIDs are unique and entries match selectedIds one-to-one.
Entries use selection order. Bytes are exact encoded record files including
formatting/newline, not embedded parsed objects. There is no `exists` field:
both snapshots represent existing endpoints; the opposite endpoint is removed.

Paths are canonical slash-separated store-relative paths. Before must be
`planning/planned/<shortId-or-UUID>.json`; after must be
`todo/<shortId>.json`. Reject absolute paths, backslashes, Windows drives/UNC,
dot segments, traversal, noncanonical spelling, symlinks, wrong filenames,
duplicate/overlapping targets, and escapes on every platform. Snapshot bytes
must decode to the same UUID/shortId and planned/todo respectively, preserve
every field except the allowed promotion changes, have nil lifecycle times,
and strictly increasing updatedAt with one common after timestamp. Reject
unsupported versions/states, unknown/duplicate properties, invalid UTF-8,
malformed/base64-empty snapshots, nulls, stale/unrelated live bytes, and trailing
JSON before any replacement. Live endpoints may be absent or exact before/after
bytes as appropriate for interrupted publication/recovery; unrelated bytes are
an error and must never be overwritten. Both endpoints absent can be restored
from the validated journal. Duplicate IDs across lifecycles are tolerated only
as journal-identified endpoints during recovery, not by the normal loader.

Under one global lock: recover/preflight existing transactions; select/validate;
prepare all snapshots; atomically publish and sync prepared journal; publish and
sync every todo snapshot; publish and sync committed journal; remove planning
sources and sync source directories; remove journal and sync meta directory.
Commit-marker publication is the durability boundary. Never delete a source
before commit. Return success only after convergence; an error after the commit
marker can mean committed data with cleanup pending, so report that clearly.
Do not blindly retry a failed promotion: recovery then planning/task show
determines the outcome (a completed promotion may make a retry no-match).

Recovery order is fixed: batch-update -> reusable-update -> planning-promote.
Use the single `recoverPendingJournals` entry point on store open and relevant
load paths under the global lock. Preflight all pending journals before any
writes; overlapping identities/targets (including status-move alternatives)
are conflicts and all diagnostic journals remain. Disjoint transactions recover
in that order. Reject new live transactions while unresolved journals remain.

Prepared promotion recovery restores every exact planning before snapshot and
removes every todo after copy. Committed recovery publishes every exact todo
after snapshot and removes every planning source. Validate the complete journal
and live endpoint set first; recovery is idempotent. Replacement/removal/sync
failures retain the journal for retry and diagnosis. Remove it only after all
endpoint directories confirm convergence. If final meta sync fails after
unlink, retain/re-publish the journal where possible and report failure; never
claim guaranteed durable cleanup on a failed sync. Do not fold promotion into
batch-update or reusable-update transaction formats.

## Canonical export

Both `wtp export` and legacy `--export-tasks` snapshot executable tasks,
planning items, handoffs, and reusable definitions under one global lock:

```text
<exportDir>/
  <execution-UUID>.json
  handoffs.json
  reusable.json
  planning/
    <planning-UUID>.json
```

Planning export is flat under the managed `planning` directory, NOT subdivided
by status and NOT at the export root. Emit raw records, not decorated views;
all statuses are distinguished by record status. Always create the empty
managed planning directory. No index, locks, or journals are exported.
Keep existing execution bytes/layout compatible. Sort records by UUID and use
existing deterministic JSON formatting. Preflight the complete destination
before publishing: only expected managed regular UUID JSON files, the two
catalog files, and the real managed planning directory are accepted. Reject
symlinks, unmanaged entries/subdirectories, and any overlap with active storage.
Remove only stale managed records, never recursively erase a destination.
Publish snapshots before stale cleanup; preserve idempotence and existing
per-file atomic export guarantees (no new whole-directory transaction promise).
Normal batch export remains planning-blind and has no planning export mode.

## Implementation allocation and verification

The remaining tasks implement this contract without changing policy:

- 0067–0068: parser/validation/serialization on these fixed declarations.
- 0069–0070: layout, partitioned loaders, union identity/dependency snapshot,
  allocator reuse, and execution readiness with planning blockers.
- 0071–0074: provider CRUD/status/views and reusable referential integrity,
  including deletion detach journal support for planning paths.
- 0075: operational isolation regressions, including final batch graph validation.
- 0076–0078: create/list/show/update/set-status CLI and precise flag semantics.
- 0079–0080: report aggregation and CLI using the declared report hierarchy.
- 0081–0085: closure selection/preview, strict codec, recovery, publish, CLI;
  preview opening must not perform implicit repairs or initialization.
- 0086: canonical exports with one managed flat planning directory.
- 0087–0089: public documentation/skills and end-to-end verification.

Contract tests freeze status/transition data, full metadata/tag parity, input
exclusions, provider signatures and capability separation, report and
promotion JSON shapes, journal envelope/order, and architectural boundaries.
Behavioral tests cover mixed lifecycles/scopes, all transitions, nil
timestamps, filters, live assignments, closure through execution vertices,
no-write preview, every journal fault boundary, path safety, and canonical
export compatibility. Documentation contract tests keep this document and the
public README/help/schema/CONTRIBUTING surfaces synchronized with the final
behavior.
