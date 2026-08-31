# Approved WTP creation and recovery

Approval covers the displayed task proposal, not installation, configuration,
implementation, dispatch, rewriting unrelated tasks, or destructive recovery.
The runtime must also permit writes. If either condition is missing, keep the
proposal in conversation and stop without mutating WTP.

1. Confirm the approved revision/digest, task subset, model distribution and
   documented deviations. Recheck root, branch, worktree, resolved store and
   installed CLI support. Do not auto-install/update WTP. A missing CLI or
   required unsupported capability needs a separate setup decision.
2. Before opening an existing legacy store, follow available `setup-wtp`
   backup guidance within existing authorization. If a required backup or
   migration needs additional permission, report that concrete prerequisite
   and preserve the approved proposal. Do not mutate config or copy stores
   opportunistically. Inspect existing dependencies using canonical commands
   only once storage opening is safe; verify that each exists in this store
   and is still the intended prerequisite. A failed prerequisite does not
   satisfy a dependency: only `done` does. Retain intentional unfinished
   dependencies, reporting which tasks will be blocked.
3. Prepare canonical argument arrays, one creation per task, in topological
   order. Use supported native flags for metadata; preserve unsupported
   grouping fields in descriptions exactly as previewed. Include scope,
   implementation detail, acceptance, verification and assignment rationale
   in each description so workers need no access to the planning conversation.
   For example, with real approved values and properly quoted arguments:

   ```sh
   wtp --json task create --title "Validate settings" \
     --description "Self-contained approved implementation and verification" \
     --status todo --priority high --estimate m --lane settings \
     --model "RESOLVED_MODEL RESOLVED_EFFORT"
   ```

   Use structured argument arrays with a process runner and `shell=False`
   when available. Never `eval` generated text or interpolate user content
   into shell code. For a shell-only runtime, quote for that exact shell and
   preserve literal backticks, dollar signs, newlines and quotes. Do not assume
   JSON escaping is shell escaping.
4. After every successful `wtp --json task create`, parse and retain its
   `shortId` and canonical UUID `id` in a conversational/in-memory ledger:
   proposal number, exact ID, model/effort, dependencies, verification result.
   Use returned IDs for subsequent `--depends-on ID1,ID2` values. Dependencies
   are stored as canonical UUIDs even when submitted as short IDs. Never
   predict sequence numbers, reconstruct branch prefixes, or edit `.wtp/`.
   Do not create all tasks first and patch dependencies later: a failure could
   otherwise leave dependent tasks incorrectly claimable.
5. Read each created task back with `wtp --json task show EXACT_ID`. Verify its
   title, full description, `todo` status, unassigned owner, estimate, priority,
   lane/grouping, model and dependency UUIDs against the approved row. Also
   check inferred Git/worktree metadata. Stop on a mismatch or concurrent
   claim; do not overwrite another worker's changes.

## Partial failure

Task creation is not a multi-task transaction. Stop at the first failed
creation or verification; do not continue independent rows, automatically
retry, roll back by deleting tasks, or mark anything done. Preserve already
created tasks and retained handoffs.

Report three disjoint sets:

- **Confirmed created:** proposal numbers, exact short IDs/UUIDs, model pairs,
  dependencies, and whether metadata was verified.
- **Uncertain outcome:** the failed row and command/error when a timeout,
  truncated JSON, missing ID, or lost response could hide a successful write.
  Read the store safely to reconcile by full metadata and creation context;
  title alone is not a unique match. Do not recreate while uncertain.
- **Remaining/not attempted:** all other proposal numbers, including those
  blocked on the failed row. Distinguish a row confirmed not created after an
  error from rows never attempted, and give the precise resumption step.

Distinguish requested totals from the distribution of confirmed creations;
never report the entire proposal as created. WTP may consume an allocation
sequence before publication fails, so gaps are normal and must not be reused.
After reconciliation, ask to resume only the confirmed-missing rows if that
retry is not already explicitly authorized. Preserve the ledger and original
dependency mappings; re-present any material change for approval. Never repeat
the whole batch or use `batch import` as an undocumented create transaction.

## Final handoff

Return a compact handoff in the conversation:

```text
WTP handoff: <complete | partial | approved but runtime-blocked | preview only>
Destination/revision: <root, branch, store, approved revision>
Created: <proposal number -> exact short ID; model + effort>
Dependencies: <resolved IDs; ready tasks and blocked prerequisites>
Distribution: <requested percentages, confirmed counts, deviations>
Verification: <metadata readback result or exact failure>
Remaining/uncertain: <numbers and next safe action, or none>
Next: <tasks available for authorized execution; no dispatcher started>
```

This handoff does not require a global WTP handoff write. Write a retained
handoff only when explicitly authorized or required by repository guidance
within the approved scope; append, never replace or purge supplied context.
Do not start `codex-wtp-loop`, select its backend, or launch workers here.
