# Proposal and preview contract

Use this conversational layout; do not write a plan file before approval:

```text
Proposal revision: <revision or helper digest>
Destination: <repository, branch, worktree, resolved store/provider>
Scope / exclusions:
Evidence / assumptions / decisions:
Approach / interfaces and data changes:
Dependencies / sequencing / risks and mitigations:
Acceptance / verification commands and expected outcomes:

1. <title> [priority; estimate; lane/grouping; exact model + effort]
   Implementation: <self-contained work, affected files, constraints>
   Acceptance: <observable finish line>
   Verification: <commands/scenarios and expected results>
   Depends on: <proposal numbers and/or exact existing WTP IDs, or none>
   Assignment reason: <complexity/risk rationale>

Model + effort | Requested % | Rounded quota | Assigned | Actual % | Deviation
...
Amend, refuse/cancel, or approve/task up this revision?
```

The bundled `scripts/proposal.py` uses only Python 3's standard library. It
reads JSON from stdin, validates it, and prints JSON to stdout. It never calls
WTP, opens task storage, saves files, or creates tasks, including in `approve`
mode. If Python is unavailable, perform the same checks manually and disclose
that automated validation was not run; do not install anything just to plan.

Pass the proposal through the runtime's in-memory stdin facility. Do not use
shell interpolation, an unquoted heredoc, `eval`, or a temporary proposal file
before approval. The invocation is:

```sh
python3 -B /installed/skill/path/scripts/proposal.py --decision preview
```

The payload has these required fields (use real researched values, not the
illustrative model labels below):

```json
{
  "context": {
    "root": "/workspace/example",
    "branch": "main",
    "store": "/workspace/example/.wtp"
  },
  "plan": {
    "scope": "Validate settings; no provider replacement.",
    "approach": "Reuse the existing parser; validate before persistence.",
    "assumptions": "Preserve the existing error format.",
    "dependencies": "No new libraries; existing parser is sufficient.",
    "risks": "Malformed input must not alter persisted settings.",
    "acceptance": "Invalid input is rejected without changing settings.",
    "verification": "Run the discovered parser and persistence tests."
  },
  "enabledModels": {"example-model": ["high"]},
  "distribution": [{"model": "example-model", "effort": "high", "percent": 100}],
  "existingTaskIds": [],
  "supportedMetadata": ["lane", "project"],
  "tasks": [{
    "number": 1,
    "title": "Validate settings before persistence",
    "description": "Extend the existing settings parser with validation before the persistence call; preserve the error format and leave prior settings intact on failure.",
    "acceptance": "Invalid settings are rejected and prior settings survive.",
    "verification": "Run the parser tests, including an invalid-input persistence regression.",
    "priority": "high",
    "estimate": "m",
    "lane": "settings",
    "metadata": {"project": "Example"},
    "dependencies": [],
    "model": "example-model",
    "effort": "high",
    "assignmentReason": "Persistence behavior warrants careful validation."
  }]
}
```

`context` is for human review and digest binding; execute WTP from its `root`.
Check whether `root` and `store` are absolute canonical paths for the destination
OS and whether `branch` still matches. Use a descriptive detached/non-Git value
when appropriate. Optional task `metadata` supports `issueId`, `project`,
`milestone`, `version`, `featureId`, `feature`, `gitRepo`, `gitBranch`,
`worktreeName`, and `worktreeDir`. `supportedMetadata` lists only fields
advertised by the installed `wtp help`/`wtp schema`, plus `lane` when supported.
Unsupported supplied metadata stays in the description and is flagged in
preview warnings; never silently drop it. If native grouping fields are
essential to the plan, resolve the capability gap rather than accepting the
fallback. Git/worktree fields default from invocation when omitted; do not
assume setting `gitBranch` changes the branch used to allocate IDs.

Each dependency is either an integer proposal number (forward references are
allowed; creation is topologically sorted) or an exact string in the inspected
`existingTaskIds` snapshot. Numbers are stable identifiers, not predicted WTP
IDs. Supply `distributionDeviation` with a concrete complexity/risk explanation
if actual counts differ from the calculated quotas; per-task
`assignmentReason` remains required. Zero-share pairs cannot receive tasks.

Preview output includes a SHA-256 `digest`, quotas/counts/percentages, warnings,
and command argument arrays with `@N` dependency placeholders. These arrays
are data, not a shell script. They deliberately omit `--agent` (which assigns
tasks) and use `--status todo`. Resolve `@N` only to confirmed created IDs.
WTP has no separate reasoning-effort flag; use `--model "MODEL EFFORT"` and
retain the pair and rationale in the description.

For an explicit decision, pass `--decision amend`, `--decision refuse`, or
`--decision approve --approved-digest DIGEST`. Amend/refuse emit no commands;
approve requires a digest matching the unchanged, validated proposal. Never
choose approval on the user's behalf. This helper remains preview-only even
after approval; the agent performs creation according to `handoff.md`.

An installed WTP version may offer a real dry-run facility in the future:
use it only if its documented contract proves no storage writes. Do not invent
`wtp task create --dry-run`, substitute `task ready`, or execute preview arrays
against the real store for testing.

## Maintainer verification

Run `python3 -B skills/wtp-codex-planning/scripts/test_proposal.py` for invalid
and valid distributions, rounding, metadata, dependencies, revision approval,
amend/refuse, and read-only behavior. `go test ./internal/cli -run WTPPlanning`
also checks this suite and exercises generated commands against an isolated
real CLI store. `go test ./...` includes those checks (explicitly skipped if
Python 3 is absent). Run the installed skill-creator `quick_validate.py` on
this folder when available. Inspect the prompts manually as well: deterministic
checks cannot prove an agent obtained informed approval or researched well.

Forward-check these conversations without spawning workers or writing live
tasks: missing model inventory; 60/30 split corrected to 60/40; a three-task
50/50 split; an amendment after approval; cancellation of an invalid draft;
an unsupported grouping field; and a command failure after one confirmed
creation. Each must retain the approval boundary and report its true state.
