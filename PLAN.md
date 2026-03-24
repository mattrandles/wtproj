Build wtp as a self-contained Go CLI for agent-oriented task workflow management. The first shipped version will fully support a local flat-file backend stored under .wtp/, with a provider abstraction designed so Trello and other PM tools can be added without restructuring the CLI.

The CLI will use subcommands as the primary UX, with compatibility aliases for the flag-style examples in README.me. The local backend will persist a minimal canonical task model with strict dependency validation, deterministic next-task selection, and agent/assignee-aware workflows. The repo will also include a SKILL.md explaining how agents should create plans, record progress, and use wtp consistently.

Product Goals
Give coding agents a simple, scriptable CLI to manage tasks in the current repo/worktree.
Avoid requiring the agent runner itself to become a project-management UI.
Keep the first release immediately usable without any external SaaS dependency.
Make backend integrations additive by defining a stable provider interface and canonical task model.
Make the on-disk format simple enough to inspect, edit manually if needed, and later import/export to remote tools.
Non-Goals for v1
Full Trello integration.
Rich project management features such as labels, due dates, sprints, attachments, or permissions.
Multi-user locking or conflict resolution beyond safe file-writing conventions.
Automatic prioritization based on AI heuristics.
A daemon, background service, or web UI.
Primary UX
Primary command style will be subcommands:


wtp task next --agent Tony
wtp task list --status todo
wtp task get <task-id>
wtp task start <task-id> --agent Tony
wtp task pause <task-id>
wtp task done <task-id>
wtp task comment <task-id> --message "Implemented parser"
wtp task create --title "New Task" --description "..." --depends-on id1,id2
wtp export --out .wtp
Compatibility aliases will be supported for the README-style usage:


wtp --agent Tony --get-next-task
wtp --agent Tony --get-tasks --status todo
wtp --agent Tony --get-task --task-id 123
wtp --agent Tony --set-task-in-progress --task-id 123
wtp --agent Tony --set-task-paused --task-id 123
wtp --agent Tony --set-task-done --task-id 123
wtp --agent Tony --add-comment --task-id 123 --comment "..."
wtp --agent Tony --create-task --title "..." --description "..." --dependencies 123,124
wtp --export-tasks=.wtp
CLI Behavior
If .wtp.json exists in the current directory, load it and select the configured provider.
If .wtp.json does not exist, default to the local flat-file provider rooted at .wtp/.
--agent maps to the canonical assignee field in the local backend.
task next returns the next eligible task for the given agent context.
Eligibility rules:
Status must be paused or todo.
All dependencies must be done.
Order is paused first, then todo.
Within each bucket, sort by creation time ascending.
If an agent is supplied, prefer tasks already assigned to that agent; if none are eligible, fall back to unassigned eligible tasks. This default should be documented explicitly.
Starting a task validates dependencies and moves status to inProgress.
Dependency validation is enforced everywhere relevant:
Cannot start blocked work.
Cannot mark a dependency target as invalid or missing at creation/update time.
Cycles are rejected.
export --out .wtp writes canonical flat-file data from the active provider into the local filesystem format.
Canonical Task Model
Use a minimal, backend-agnostic schema:


{
  "id": "uuid",
  "shortId": "wtp-0012",
  "title": "Implement parser",
  "description": "Add config parsing for provider selection",
  "status": "todo",
  "assignee": "Tony",
  "dependencies": ["uuid-a", "uuid-b"],
  "comments": [
    {
      "id": "uuid",
      "author": "Tony",
      "message": "Started implementation",
      "createdAt": "2026-03-24T13:00:00Z"
    }
  ],
  "createdAt": "2026-03-24T12:00:00Z",
  "updatedAt": "2026-03-24T13:00:00Z",
  "startedAt": null,
  "completedAt": null
}
Canonical statuses
todo
inProgress
paused
done
Flat-File Backend Design
Root directory:


.wtp/
  todo/
  inProgress/
  paused/
  done/
  meta/
File layout
Each task is stored as one JSON file in the directory matching its current status.
File name format: <uuid>.json
meta/index.json stores the short-ID mapping and next display counter.
Optional future metadata files can live under meta/ without changing task files.
Flat-file invariants
The task file’s status must match its containing directory.
Moving between statuses means atomic rewrite into the target directory and removal from the source directory.
shortId is stable after creation.
id is the canonical reference for dependencies and cross-backend portability.
CLI accepts either full UUID or shortId on input; resolution must be deterministic and unique.
Config Design
.wtp.json example for future provider use:


{
  "tool": "trello",
  "apiKeyEnv": "TRELLO_API_KEY",
  "boardId": "board-id",
  "listIds": {
    "todo": "list-a",
    "inProgress": "list-b",
    "paused": "list-c",
    "done": "list-d"
  }
}
v1 config rules
tool is optional; absent means local flat-file provider.
Environment-variable indirection should use field names ending in Env instead of embedding raw secret names ambiguously.
Unknown provider values fail fast with a clear error.
Missing required env vars fail with actionable messages.
For flat-file mode, .wtp.json is not required.
Provider Interface
Define a provider abstraction from the start so flat-file and Trello implementations share one contract.

Core interface surface:

ListTasks(filter)
GetTask(idOrShortId)
CreateTask(input)
UpdateTaskStatus(id, targetStatus, actor)
AddComment(id, actor, message)
GetNextTask(agent)
ExportCanonical(outDir)
Supporting types:

Task
Comment
TaskFilter
CreateTaskInput
ProviderConfig
ProviderFactory
Implementation note:

Keep the canonical model in a provider-independent package.
Put provider-specific mapping logic behind adapters.
Avoid leaking provider-native fields into the CLI contract in v1.
Alias/Compatibility Layer
Support the README examples through a translation layer:

Parse legacy action flags.
Convert them into the equivalent subcommand execution path.
Reject incompatible combinations with a single validation mechanism.
Print help that documents both styles, while marking legacy flag actions as compatibility mode.
Output Contract
Default CLI output should be human-readable and concise. Add --json for machine-readable output from the start.

Human-readable expectations
task next prints a compact task summary.
task list prints one line per task with shortId, status, assignee, title.
task get prints full task detail including dependency and comment sections.
JSON expectations
Emit canonical task objects or arrays of task objects.
Error output remains on stderr; JSON goes to stdout only.
Error Handling
Missing task: exit non-zero with a clear identifier-specific message.
Ambiguous identifier: show matching IDs and require a retry.
Blocked task start: show unresolved dependency IDs/titles.
Cyclic dependencies: reject at create/update time with cycle path details if feasible.
Invalid status transitions: reject with allowed transitions.
Corrupt task file: fail clearly and identify the path.
Repository Structure
Suggested initial layout:


cmd/wtp/
internal/cli/
internal/config/
internal/core/
internal/provider/
internal/provider/flatfile/
internal/provider/trello/
internal/store/
docs/
SKILL.md
README.me
Package responsibilities
cmd/wtp: entrypoint and command wiring.
internal/cli: Cobra or equivalent command definitions, alias parsing, output formatting.
internal/config: .wtp.json discovery, parsing, env resolution.
internal/core: canonical types, validation, status rules, dependency graph checks.
internal/provider: provider interfaces and registration.
internal/provider/flatfile: local backend implementation.
internal/provider/trello: stub or skeletal adapter package for future work.
docs/: CLI examples, config samples, provider extension notes.
SKILL.md: agent workflow guidance.
Implementation Phases
Phase 1: Foundation
Initialize Go module.
Add CLI framework.
Define canonical model, statuses, validation, and output contract.
Implement config discovery and provider selection fallback.
Phase 2: Flat-file provider
Implement task persistence, ID generation, short-ID index, and status-directory layout.
Implement create/get/list/comment/status transition flows.
Implement dependency validation, cycle detection, and next-task selection.
Phase 3: Compatibility and UX
Add legacy flag alias parsing.
Add --json.
Improve help text, examples, and errors.
Add export command for canonical flat-file output.
Phase 4: Docs and agent guidance
Expand README.me into install/use/architecture sections.
Add SKILL.md that tells agents:
how to create tasks from plans,
how to claim work with --agent,
how to pause/resume,
how to add progress comments,
how to break work into dependency-linked tasks.
Phase 5: Future-ready provider scaffolding
Add a non-functional Trello provider skeleton or clearly documented interface placeholder so extension points are concrete without implementing the integration yet.
Public Interfaces and Types
These should be treated as the initial stable surface:

CLI subcommands under wtp task ...
Compatibility action flags from README.me
.wtp.json config schema with tool and provider-specific fields
Canonical task JSON shape for flat-file storage and --json output
Status names: todo, inProgress, paused, done
Potentially breaking later if changed, so define carefully now:

ID resolution rules for uuid vs shortId
Ordering rules for task next
Dependency enforcement semantics
assignee meaning for --agent
Test Cases and Scenarios
Core validation
Create task with no dependencies.
Create task with valid dependencies.
Reject creation when a dependency ID does not exist.
Reject cyclic dependencies across direct and indirect chains.
Reject invalid status values.
Flat-file storage
Create task writes JSON into .wtp/todo/.
Starting task moves file to .wtp/inProgress/ and updates timestamps.
Pausing task moves file to .wtp/paused/.
Completing task moves file to .wtp/done/.
Corrupt JSON file produces a clear error.
Identifier handling
Resolve by UUID.
Resolve by short ID.
Reject ambiguous or unknown IDs.
Preserve short-ID stability across status changes.
Dependency behavior
Block start when dependencies are not done.
Allow done only when task is already inProgress or paused, depending on final transition rules chosen during implementation.
next excludes blocked tasks.
next returns paused eligible tasks before todo tasks.
Agent behavior
next --agent Tony prefers Tony-assigned paused/todo tasks.
If no Tony-assigned eligible tasks exist, returns an unassigned eligible task.
Starting a task with --agent Tony writes assignee: "Tony".
Compatibility layer
Each legacy flag action maps to the same execution path and result as the equivalent subcommand.
Invalid flag combinations fail consistently.
--json works for both modern and compatibility invocation styles.
Export
Export from flat-file provider to a target directory preserves canonical JSON shape.
Export to an existing directory handles overwrite policy deterministically and documents it.
Cross-platform
Paths work on Linux, macOS, and Windows.
Newline and path separator assumptions do not leak into persisted data.
File operations avoid rename semantics that break on Windows without handling.
Acceptance Criteria
A user in a repo with no .wtp.json can install wtp, run wtp task create, and manage tasks entirely in .wtp/.
An agent can reliably call wtp task next --agent <name> and receive deterministic results.
Dependencies are enforced strongly enough that blocked work cannot be started.
Legacy flag examples from README.me execute successfully or fail with explicit guidance if unsupported.
The repo includes a usable SKILL.md for agent workflows.
The codebase makes adding a real Trello provider a bounded follow-up task instead of a redesign.
Assumptions and Defaults
Language/runtime: Go.
Distribution target: self-contained binary for Linux, macOS, and Windows.
v1 backend scope: fully functional flat-file backend; Trello deferred to later work.
CLI design: subcommands first, compatibility aliases retained.
Task model: minimal canonical schema.
Task IDs: UUID internally, stable human-friendly short IDs for display/input.
--agent maps to persisted assignee.
get-next-task ordering: eligible paused first, then eligible todo, FIFO by creation time.
Dependency policy: validate strictly and prevent blocked starts.
Secrets in config will be referenced by env-var name fields, not stored inline.