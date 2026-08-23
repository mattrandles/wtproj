---
name: codex-wtp-loop
description: Dispatch repository-local WTP tasks through user-selected Codex project threads or model-selected subagents and monitor them safely. Use when Codex needs a persistent WTP project-management loop that atomically claims work, resolves exact or vague model recommendations, preserves handoffs, stops for human decisions, explicitly reclaims paused tasks, and verifies completion without runaway dispatch.
---

# Codex WTP Loop

Run a sequential monitoring loop that claims one WTP task, delegates it to one
primary worker, and verifies its state before considering another claim.

## Establish run settings

Before claiming work, establish:

- `project_root`: resolve the repository root with
  `git -C REQUESTED_WORKING_DIRECTORY rev-parse --show-toplevel`. Use that
  canonical path as the dispatcher working directory for every WTP command.
- `agent_name`: use the user- or repository-supplied WTP agent name, or default
  to `codex-wtp-loop`.
- `worker_backend`: either `thread` or `subagent`.
- `default_model` and `default_effort`: an explicit supported combination for
  tasks whose recommendation omits either part.
- `intervention_policy`: either stop immediately for a human or allow one
  read-only reviewer escalation before stopping. When escalation is enabled,
  also establish an explicit `reviewer_model` and `reviewer_effort`.
- `model_inventory`: the models and model/effort combinations advertised by
  the selected backend's currently available tool.

Always ask the user once per loop run to choose the backend, default model and
effort, and intervention policy. If escalation is selected, also ask for the
reviewer model and effort. Use the user-input tool when available. Do not infer
these choices from an earlier run, repository guidance, task labels, or the
dispatcher's model. Do not claim work until every setting is resolved.

Run `wtp --json stats model` from `project_root` before the first claim. Use its
distinct non-empty labels to identify unresolved or ambiguous recommendations
while asking for run settings; this inspection is not a claim.

For `thread`, discover saved Codex projects and select the single project whose
canonical path exactly equals `project_root`. Use its opaque project ID and the
explicit local environment. If the path is absent or ambiguous, stop without
claiming. Selecting project threads for this run is the user's authorization to
create those worker threads.

For `subagent`, keep work in the dispatcher's exact `project_root`; subagents
share its filesystem. Do not discover or create a saved project merely to run
a subagent.

Keep every worker's sandbox and approval posture aligned with the dispatcher's
current posture. Subagents inherit the current runtime; project threads inherit
the selected saved project's settings. Before claiming, verify that the chosen
backend preserves an equivalent posture. If it cannot be verified or preserved,
stop without claiming. Never request, weaken, or invent permission settings for
a worker launch.

## Resolve model recommendations

Treat WTP `model` as a mandatory routing recommendation after normalization.
Build candidates only from `model_inventory`; documentation or the examples
below never prove that a model is available on the active or destination host.

Normalize case, outer whitespace, repeated whitespace, hyphens, underscores,
and parentheses for comparison. Recognize an attached `gpt` version such as
`gpt5.6` as equivalent to `gpt-5.6`. Parse a trailing recognized effort only
when the complete value is not already an advertised model ID.

Resolve from most specific to least specific:

1. Match an exact advertised model ID plus an optional explicit effort.
2. Match an explicit GPT version and a meaningful family/name segment plus an
   optional effort.
3. Match a meaningful family/name segment plus an optional effort, such as
   `sol`, `terra high`, or `luna-max`.

For a family-only value, consider advertised GPT models containing that
standalone normalized family/name segment. Choose the greatest numeric GPT
version. For candidates at the same version, prefer the stable family alias
over dated snapshots. If equally current candidates remain, ask the user.
Thus `sol` selects the latest advertised GPT model with a `sol` name segment.

Preserve an explicit version. Never upgrade a label such as `gpt5.6-sol` to a
later major or minor version. Use `default_effort` when the recommendation
omits effort. Use the explicit effort when present. Ask the user if the result
has no candidate, remains ambiguous, or does not support the resolved effort;
never substitute another model or effort.

Require a meaningful family/name segment. Values such as `gpt`, `latest`, or
`high` alone are insufficient and require user resolution.

The following mappings are illustrative, not an exhaustive whitelist:

| Normalized WTP example | `create_thread.model` / `spawn_agent.model` | `create_thread.thinking` / `spawn_agent.reasoning_effort` |
| --- | --- | --- |
| `GPT 5.6 Sol High` | `gpt-5.6-sol` | `high` |
| `GPT 5.6 Terra High` | `gpt-5.6-terra` | `high` |
| `GPT 5.6 Luna High` | `gpt-5.6-luna` | `high` |
| `GPT 5.6 Luna Max` | `gpt-5.6-luna` | `max` |

For example, normalize all of these to `GPT 5.6 Luna High` before lookup:

- `gpt-5.6-luna (high)`
- `gpt-5.6-luna-high`
- `gpt5.6-luna-high`
- `luna-high`, when the latest matching Luna model is GPT 5.6

Apply `default_model` and `default_effort` when the WTP model value is empty.
Do not omit model settings and fall back silently to a backend default.

If a recommendation cannot be resolved after a task was claimed, comment with
the exact label and resolution failure, write the required handoff, pause that
exact task, and stop the loop. Ask the user to resolve it. After the response,
use the explicit reclaim procedure below; do not continue to another claim.

## Non-negotiable rules

- Claim and update tasks through `wtp`; never edit `.wtp/` directly.
- Preserve the exact returned `TASK_ID`, including a branch-scope prefix, in
  every prompt, WTP command, comment, handoff, and follow-up. Never reconstruct
  it from a sequence, filename, title, UUID, or sanitized worker name.
- Use comments for audit/progress and retained handoffs for cross-worker
  context; do not substitute one for the other.
- Keep at most one claimed task and one primary worker active. Do not
  pre-dispatch a batch.
- Create at most one read-only reviewer for the current blocker, and only when
  the user selected reviewer escalation for this run.
- Let only a verified `done` task advance the automatic loop. Stop on every
  other terminal, paused, waiting, blocked, failed, or unexplained state.
- Never answer approvals or make product, credential, destructive-action,
  external-write, scope-expanding, or other user-owned decisions.
- Never leave a claimed task silently stuck in `inProgress`.

## Dispatch one task

### 1. Claim atomically

Run from `project_root` with a bounded timeout:

```sh
wtp --json task next --agent AGENT_NAME
```

This command is the initial automatic claim. Do not use `task ready` and later
assume its result remains available. Parse and retain the exact JSON `shortId`,
title, description, model recommendation, and claim-attached `handoffs` array.
Treat attached handoffs as task-scoped context in newest-first order. Reads and
claim attachment are non-consuming; do not purge them during dispatch.

On a named Git branch, automatic selection considers current-branch scoped
tasks before legacy tasks and does not select a foreign scoped task. Do not
broaden automatic selection because foreign work appears in a listing.

After the claim, read only the newest global handoff:

```sh
wtp --json handoff get --limit 1
```

Do not read global handoffs before claiming or request older/all global
records. Pass an empty global-context value when none exists. If no task is
claimable, stop or wait; never create an empty worker.

### 2. Launch the selected primary worker

Build one common worker message containing the exact task ID, title,
description, task handoffs, newest global handoff, `project_root`, and
`agent_name`. Require the worker to:

- inspect the exact task and repository guidance before editing;
- implement only that task and run proportionate verification;
- add concise comments for material progress;
- write one concise global handoff before `task done` or `task pause`;
- keep supplied task handoffs intact;
- avoid spawning or creating other workers because the dispatcher owns
  orchestration;
- mark `done` only after implementation and verification;
- use the blocker protocol below whenever direction is required.

For a project thread, call the thread-creation tool with the resolved model and
effort and this target:

```jsonc
{
  "model": "RESOLVED_MODEL",
  "thinking": "RESOLVED_EFFORT",
  "prompt": "COMMON_WORKER_MESSAGE",
  "target": {
    "type": "project",
    "projectId": "DISCOVERED_PROJECT_ID",
    "environment": {"type": "local"}
  }
}
```

For a subagent, call `spawn_agent` with the resolved model and effort:

```jsonc
{
  "task_name": "SAFE_UNIQUE_WORKER_NAME",
  "fork_turns": "none",
  "model": "RESOLVED_MODEL",
  "reasoning_effort": "RESOLVED_EFFORT",
  "message": "COMMON_WORKER_MESSAGE"
}
```

Use `fork_turns: "none"` so the custom subagent model is applied and the worker
receives only the explicit task context. Sanitize only `task_name`; keep the
opaque WTP ID unchanged inside the message and all task operations.

If worker creation fails, write a concise global handoff with the exact task,
failure, and state; add the failure as a task comment; pause the exact task;
verify it is paused; and stop the loop. Do not act as though a worker exists or
claim another task.

### 3. Monitor and direct the worker

For a thread, retain its thread and host IDs. Use cursor-aware bounded
`wait_threads` calls, and use `send_message_to_thread` for follow-ups without
model or thinking overrides.

For a subagent, retain its agent ID/canonical task name. Use bounded
`wait_agent` calls. Use `send_message` while it is running and `followup_task`
to resume it when idle, without changing its model selection.

Continue monitoring while the worker makes progress. Answer only questions
whose answers are already explicit in the task, repository, or current user
request. Apply the blocker protocol to every question requiring judgment not
already supplied.

## Handle blockers and human intervention

Require a blocked worker to comment on the task and return this concise
structure:

```text
BLOCKER_KIND: technical_verification | user_decision | approval | credentials | external_action | scope_change | environment_failure
QUESTION: exact decision or missing input
OPTIONS: concrete choices, when applicable
RECOMMENDATION: worker's recommendation, or none
EVIDENCE: concise relevant facts and verification
```

Before stopping, require the worker to write its global handoff and run:

```sh
wtp task pause TASK_ID --agent AGENT_NAME
```

### Optional reviewer escalation

Use one reviewer only when the run policy allows it and the blocker is a
repository-grounded `technical_verification` question. Launch the reviewer with
the selected backend, `reviewer_model`, and `reviewer_effort`. Give it the task,
question, evidence, and repository context, but instruct it to remain read-only,
avoid WTP state changes, and return a decision with evidence.

Verify that the retained exact task is `paused` before launching the reviewer.

Do not escalate product intent, approvals, credentials, external actions,
destructive actions, or scope changes. Route them directly to the user. If the
reviewer cannot resolve the technical question, route it to the user. Never
create a second reviewer for the same blocker.

When the reviewer resolves the blocker, explicitly reclaim the task before
passing its answer to the same primary worker. Do not let reviewer completion
advance the main loop.

### Stop for the user

After the worker pauses, run `wtp --json task show TASK_ID --agent AGENT_NAME`
and verify that the retained exact task is `paused`. Save the worker identity,
resolved model settings, blocker structure, and exact task ID. Stop the entire
loop and report all of those details plus the required user decision. Do not
call `task next`, create another primary worker, or continue through the queue.

Built-in WTP `paused` tasks are claimable and are prioritized before `todo`.
Therefore never rely on a later `task next` to return the same task: it is not
an explicit or race-safe continuation mechanism.

### Reclaim after manual continuation

After the user answers, inspect the retained exact task again. Require it still
to be `paused`, then run:

```sh
wtp --json task start TASK_ID --agent AGENT_NAME
```

Never use `wtp task next` for this recovery. Verify that `shortId` exactly
equals the retained `TASK_ID` and the returned state is `inProgress`. If a
primary worker already exists, only then send the user's or reviewer's answer
to that same thread or subagent and resume monitoring. If model resolution or
worker creation failed before any primary worker existed, launch the first
primary worker only after this explicit reclaim and with the resolved run
settings.

If inspection or explicit start reports another state, owner, task ID, or
claim conflict, stop and report the conflict. Do not launch a replacement
worker, claim another task, or guess that the task is safe to continue.

## Verify state before continuing

After any worker completion, run:

```sh
wtp task show TASK_ID --agent AGENT_NAME
```

- If the exact task is `done`, require its global handoff, then return to the
  initial automatic claim step.
- If it is `paused`, verify the blocker and stop for explicit resolution and
  reclaim.
- If it is waiting, blocked, failed, or another configured non-done status,
  report the recorded reason and stop.
- If it remains `inProgress` while the worker is recoverable, follow up with
  that same worker. Otherwise comment with the concrete failure, write the
  fallback handoff, pause the task, verify the pause, and stop.

Never mark unfinished or unverified work done on the worker's behalf.

## Pre-launch check

Before every primary-worker launch, confirm:

1. The user selected the backend, default model/effort, and intervention policy
   for this run.
2. The initial task was claimed atomically, or a retained paused task was
   explicitly reclaimed by its exact ID.
3. The resolved model and effort are supported by the selected backend.
4. Thread mode targets the exact saved project locally; subagent mode uses
   `fork_turns: "none"`.
5. The worker will preserve the dispatcher's sandbox and approval posture.
6. Task and global handoffs were retained according to their distinct scopes.
7. The primary worker's identity and backend-specific monitor state will be
   retained for follow-up and manual continuation.
8. No other claimed task, primary worker, or reviewer is active.
