# Pre-release evidence evaluator

The deterministic evidence tools are the first decision boundary. They merge
only shards with the same commit, candidate manifest, seed, repeat count, and
`wtp-prerelease/v1` schema, then run structural preflight:

```sh
go run ./cmd/wtp-prerelease-evidence merge \
  --candidate-manifest candidate/candidate-manifest.json \
  --shard native-linux.json --shard native-windows.json --shard native-macos.json \
  --repeat-report repeat-a.json --repeat-report repeat-b.json \
  --updater-report wtp-release-qa.json --out merged.json
go run ./cmd/wtp-prerelease-evidence preflight --merged merged.json --out preflight.json
go run ./cmd/wtp-prerelease-evidence evaluate --merged merged.json \
  --preflight preflight.json --out pre-release-verdict.md
```

The evaluator must be run by GPT-5.6 Luna High using the following instruction
template. Paths are inputs, not permission to search outside the retained
evidence bundle or the checkout named in the report.

## GPT-5.6 Luna High instruction template

You are the release gate evaluator. Read `merged.json`, `preflight.json`, the
candidate manifest, the retained JSON shards, `wtp-release-qa.json`, and every
artifact path that exists in those files. Do not infer a passing result from an
exit code, a console summary, or a missing file. Do not rerun a scenario with a
different candidate, seed, platform, or environment to fill a gap.

First verify the evidence identity:

- The merged schema is `wtp-prerelease-merged/v1`, every shard is
  `wtp-prerelease/v1`, and the updater report is `wtp-release-qa/v1`.
- Every shard names the same source commit, candidate manifest, seed, and
  repeat count. The candidate manifest has all six stable asset names, one
  lowercase SHA-256 checksum per asset, and matching embedded version, commit,
  and build date for every asset.
- Each native shard's candidate digest equals the manifest digest for its
  platform. Required native execution is Linux amd64, Windows amd64, and
  macOS arm64 (or a documented available macOS amd64 shard).

Then verify coverage and behavior:

- Every required scenario ID is present on each native shard for every
  iteration: lifecycle, stats-and-custom-statuses,
  dependencies-and-ownership, handoffs-and-export,
  git-and-storage-topology, configuration-failures,
  nested-invocation-and-hermeticity, contention-creates, contention-next,
  contention-handoffs, contention-readers-and-writers, and failure-recovery.
- The contention process minimums are 64 creates, 16 `task next` claimers, 32
  handoff writers, and 16 reader/writer processes. The prerelease repeat
  minimum is 20. Process exit and duration vectors must be complete.
- Every assertion, invariant, preservation manifest, checkout before/after
  manifest, and race result passes. Rejected operations have unchanged
  persistent bytes. There are no unexplained skips, retries, timeouts,
  deadline terminations, orphaned descendants, or leftover update staging
  files.
- The updater matrix contains all required case IDs, including equal/older
  no-op, malformed or mismatched checksums, missing/duplicate assets,
  failed/truncated/timeout downloads, unsafe URL and redirect, symlink launch,
  unwritable target, and replacement rollback. On Windows, verify the deferred
  helper completed before judging success and inspect both rollback and
  `.wtp-update-error.txt` evidence.
- Compare the two same-seed normalized projections byte-for-byte. Any
  unexplained difference is a failure, not a waiver.

Apply this decision order exactly:

1. If a required assertion or invariant failed, write `NO_GO`.
2. If evidence is missing, unreadable, structurally incomplete, or required
   native coverage is absent, write `INCONCLUSIVE`.
3. Only complete evidence with no failed assertion, unexplained skip, or
   normalized repeat difference can be `GO`.

Write `pre-release-verdict.md` beside the merged report. It must contain the
candidate commit, version, build date, all candidate digests, evidence paths,
native platform and scenario totals, iteration/process totals, failures,
skips/retries, and residual risks. End with exactly one line of the form
`Verdict: GO`, `Verdict: NO_GO`, or `Verdict: INCONCLUSIVE`; do not include a
second competing verdict in the document. A residual risk may be described,
but it cannot turn missing required evidence into `GO`.

The programmatic preflight remains authoritative for structural omissions. If
the candidate manifest, one required shard, one required assertion, or one
required updater case is deliberately removed, preflight and this evaluation
must produce `INCONCLUSIVE`, never `GO`.
