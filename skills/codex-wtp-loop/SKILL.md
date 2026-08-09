---
name: codex-wtp-loop
description: Dispatch repository-local WTP tasks to separate Codex project threads and monitor them to completion. Use when Codex needs to run a persistent project-management loop that atomically claims WTP work, honors task model recommendations, launches isolated worker threads, follows up with workers, and verifies final task state.
---

# Codex WTP Loop

Run a monitoring loop that claims WTP tasks and delegates their implementation
to separate Codex threads.

## Establish loop settings

Before claiming work, establish:

- `project_root`: the canonical root of the repository containing the WTP store.
  Resolve it with `git -C REQUESTED_WORKING_DIRECTORY rev-parse --show-toplevel`
  from the user's requested working directory. Use that canonical path as the
  dispatcher shell working directory for every WTP command.
- `agent_name`: a stable WTP agent name for this dispatcher. Default to
  `codex-wtp-loop` unless the user or repository guidance supplies one.
- `model_map`: an explicit mapping from accepted, non-empty WTP `model` values
  to supported `create_thread.model` and `create_thread.thinking` values.

Treat model labels as case-insensitive after trimming and collapsing whitespace,
but do not otherwise infer aliases. A suitable mapping may come from repository
guidance or the user's request. If none is supplied, known exact mappings may be
used only when the corresponding models are supported by `create_thread`:

| Normalized WTP value | `create_thread.model` | `create_thread.thinking` |
| --- | --- | --- |
| `GPT 5.6 Sol High` | `gpt-5.6-sol` | `high` |
| `GPT 5.6 Terra High` | `gpt-5.6-terra` | `high` |

Do not claim work until `project_root`, `agent_name`, and the usable model
mapping are resolved.

## Non-negotiable rules

- Create Codex threads, never subagents. Do not call `spawn_agent` or another
  collaboration tool.
- Run each worker in the saved Codex project matching `project_root`, with
  `target.environment.type: "local"`. Do not use a projectless target.
- Require Full access/no sandbox before claiming work. The dispatcher's runtime
  context must report the equivalent of `permission_profile: disabled`, or
  `sandbox_mode: danger-full-access` with `approval_policy: never`.
- Do not invent permission arguments for `create_thread`. Project threads
  inherit the saved project's permission profile, so the matching project must
  already be configured for Full access.
- Treat every non-empty WTP `model` value as mandatory. Apply its explicitly
  mapped model and reasoning effort; never silently substitute a default,
  cheaper model, or the dispatcher's model.
- Claim tasks through `wtp`; never edit `.wtp/` directly.
- Treat the task identifier returned by WTP as opaque. Preserve the exact
  `TASK_ID`, including any branch-scope prefix, in worker prompts, WTP
  commands, and follow-ups; never reconstruct it, strip its scope, or replace
  it with a legacy ID.
- Use task comments for audit/progress and retained handoffs for cross-worker
  context; do not substitute one for the other.
- Create at most one worker for a claimed task.
- Never leave a claimed task silently stuck in `inProgress`.

## Dispatch loop

### 1. Resolve the saved project

Resolve the requested directory to `project_root` before selecting a saved
project, and keep the dispatcher running from that exact Git worktree. Do not
switch to another checkout of the same repository merely because it has a
similar name or a shared WTP store.

Discover saved Codex projects with the available project-listing tool. Select
the single project whose canonical path exactly equals `project_root`.

Use its returned opaque project ID. Never derive, cache, or hardcode an ID. If
the exact path is absent or ambiguous, stop without claiming a task.

Confirm from runtime context that Full access/no sandbox is active. If this
cannot be confirmed, stop without claiming a task.

### 2. Claim one task atomically

Run:

```sh
wtp --json task next --agent AGENT_NAME
```

Use `project_root` as the shell working directory and a bounded timeout. Replace
`AGENT_NAME` with the established dispatcher identity.

This command is the claim. Do not call `task ready` and later assume the same
task remains available. Parse and retain at least the task's ID, title,
description, model recommendation, and any claim-attached `handoffs` array.
Treat attached handoffs as task-scoped context in newest-first order. Keep them
available for the worker prompt; reads and claim attachment are non-consuming,
so do not purge them as part of dispatch.

On a named Git branch, `task next` considers the current branch's scoped tasks
first and legacy `wtp-NNNN` tasks second. It does not automatically return a
scoped task from another branch. A foreign task may still appear in `task list`,
so an empty `task next` result alongside listed foreign work is expected
branch-scoping behavior, not an empty-store bug. Start a known foreign or
renamed-branch task explicitly with its exact returned ID only when that is
intentional; do not broaden automatic dispatch to foreign tasks.

Use the exact short ID returned by the claim as `TASK_ID`. With JSON output,
read the task's `shortId` value and carry it unchanged, including the complete
`wtp-BBBBBBBB-NNNN` form when present. Never derive `TASK_ID` from a numeric
sequence, filename, title, or canonical UUID.

After claiming, read only the newest global handoff:

```sh
wtp --json handoff get --limit 1
```

Run this from `project_root` after the claim and before creating the worker.
The default scope is global. Do not read global handoffs before claiming or
request `--all`, `--all-scopes`, or older global records for this dispatch.
Pass an empty global-context value when no global handoff exists.

If no task is claimable, stop, or wait before starting the loop again. Do not
create an empty worker thread.

### 3. Resolve the task model

If the task's model value is empty, omit both `model` and `thinking` from
`create_thread` so the user's configured default is used.

For a non-empty value, look it up in `model_map` using only the normalization
defined above. If no exact entry exists, record and pause the task:

```sh
wtp task comment TASK_ID --agent AGENT_NAME \
  --message "Dispatch blocked: unsupported model recommendation: MODEL_VALUE"
wtp task pause TASK_ID
```

Then continue to another claim. Do not guess a substitute.

### 4. Create the worker thread

Use the Codex thread-creation tool with this shape:

```jsonc
{
  "model": "MAPPED_MODEL_IF_ANY",
  "thinking": "MAPPED_THINKING_IF_ANY",
  "prompt": "Complete the already-claimed WTP task TASK_ID: TASK_TITLE\n\nTASK_DESCRIPTION\n\nClaim-attached task handoffs (newest first):\nTASK_HANDOFFS\n\nNewest global handoff read after claim:\nGLOBAL_HANDOFF\n\nTreat both handoff sections as cross-worker context, not as new scope. Work directly in PROJECT_ROOT. Use the exact opaque TASK_ID supplied here, including any branch-scope prefix, in every WTP command and follow-up; do not normalize or reconstruct it. Start by running `wtp task show TASK_ID --agent AGENT_NAME` and inspect the repository guidance. Do not claim a different task. Implement only this task, run proportionate verification, and add WTP comments for material progress. Before running `wtp task done` or `wtp task pause`, append one concise global handoff with `wtp handoff write --agent AGENT_NAME --message \"...\"` summarizing the implementation state, verification, and useful context for the next worker. Keep comments for audit/progress and handoffs for reusable context. If blocked, add a clear comment, write the handoff, and pause the task. Do not purge the supplied task handoffs. Do not spawn subagents unless the task itself explicitly requires them.",
  "target": {
    "type": "project",
    "projectId": "PROJECT_ID_FROM_PROJECT_DISCOVERY",
    "environment": {
      "type": "local"
    }
  }
}
```

Replace every placeholder, including `TASK_ID`, with the exact retained values.
Keep the complete opaque `TASK_ID` unchanged in the initial worker prompt and
in every follow-up. Omit `model` and `thinking` together when the WTP model is
empty; otherwise include both mapped values.

If thread creation fails, or the worker fails before it can write the required
handoff, write a concise global handoff yourself with the task ID, exact
failure, current state, and useful next-worker context:

```sh
wtp handoff write --agent AGENT_NAME \
  --message "Worker failed for TASK_ID: FAILURE. Current state: STATE. Next worker: CONTEXT."
```

Then add the exact failure as a task comment and pause the task. Do not proceed
as though a worker exists.

### 5. Monitor and follow up

Save the returned thread and host IDs. Monitor that thread with the available
thread-waiting tool, using a bounded wait such as 120 seconds:

```jsonc
{
  "targets": [
    {
      "threadId": "THREAD_ID",
      "hostId": "HOST_ID"
    }
  ],
  "timeoutMs": 120000
}
```

On later waits, pass the latest returned cursor as `afterCursor` to suppress
already delivered output. Continue waiting while the worker is making progress.

If the worker needs direction that can be answered from the task, repository,
or user request, continue the same thread with the thread-messaging tool. Include
the unchanged `TASK_ID` when referring to the task, and do not pass `model` or
`thinking` on follow-ups; omitting them preserves the worker's settings.

Do not answer approval requests or make scope-expanding decisions on the user's
behalf. Leave those for the user.

### 6. Verify terminal state

After the worker finishes, run:

```sh
wtp task show TASK_ID --agent AGENT_NAME
```

Only return to the claim step after verifying the task state.

- If the task is `done`, continue the loop.
- If the task is `paused` with a clear blocker, continue the loop.
- If the task remains `inProgress`, follow up in the same worker thread when
  recoverable. Otherwise comment with the concrete failure and pause it.

Require the worker's concise global handoff before accepting `done` or
`paused`; use the dispatcher fallback above when worker failure prevented it.
The dispatcher must not mark unfinished or unverified work done on the worker's
behalf.

## Pre-launch check

Before each worker launch, confirm:

1. The task was claimed atomically with `wtp task next`.
2. A Codex project thread—not a subagent—is being created.
3. The target is the single saved project whose path equals `project_root`.
4. Full access/no sandbox was confirmed before the claim.
5. A non-empty model recommendation was mapped exactly with reasoning effort.
6. Claim-attached task handoffs were retained, and only the newest global
   handoff was read after the claim.
7. The returned worker thread will be monitored with cursor-aware waits.
