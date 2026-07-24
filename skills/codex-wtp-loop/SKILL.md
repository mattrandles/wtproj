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
  Default to `git rev-parse --show-toplevel` from the user's requested working
  directory.
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
- Create at most one worker for a claimed task.
- Never leave a claimed task silently stuck in `inProgress`.

## Dispatch loop

### 1. Resolve the saved project

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
description, and model recommendation.

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
  "prompt": "Complete the already-claimed WTP task TASK_ID: TASK_TITLE\n\nTASK_DESCRIPTION\n\nWork directly in PROJECT_ROOT. Start by running `wtp task show TASK_ID --agent AGENT_NAME` and inspect the repository guidance. Do not claim a different task. Implement only this task, run proportionate verification, and add WTP comments for material progress. Mark the task done only after implementation and verification are complete. If blocked, add a clear comment and pause the task. Do not spawn subagents unless the task itself explicitly requires them.",
  "target": {
    "type": "project",
    "projectId": "PROJECT_ID_FROM_PROJECT_DISCOVERY",
    "environment": {
      "type": "local"
    }
  }
}
```

Replace every placeholder. Omit `model` and `thinking` together when the WTP
model is empty; otherwise include both mapped values.

If thread creation fails, add the exact failure as a task comment and pause the
task. Do not proceed as though a worker exists.

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
or user request, continue the same thread with the thread-messaging tool. Do not
pass `model` or `thinking` on follow-ups; omitting them preserves the worker's
settings.

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

The dispatcher must not mark unfinished or unverified work done on the worker's
behalf.

## Pre-launch check

Before each worker launch, confirm:

1. The task was claimed atomically with `wtp task next`.
2. A Codex project thread—not a subagent—is being created.
3. The target is the single saved project whose path equals `project_root`.
4. Full access/no sandbox was confirmed before the claim.
5. A non-empty model recommendation was mapped exactly with reasoning effort.
6. The returned worker thread will be monitored with cursor-aware waits.
