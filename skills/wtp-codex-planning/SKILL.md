---
name: wtp-codex-planning
description: Research a workspace and produce a complete implementation plan with an editable WTP task proposal, user-selected model distribution, and explicit approval before task creation. Use when planning work for WTP; do not implement the proposed work or start a dispatcher.
---

# WTP Codex Planning

Turn a request into a decision-complete implementation plan, then offer to
create its WTP tasks in the same conversation. This is an additional skill,
not a replacement for Codex's built-in plan mode. Do not implement the plan,
claim tasks, launch workers, install WTP, or change execution permissions.

## 1. Research without writes

Resolve the intended repository, branch, and worktree. Read applicable
`AGENTS.md`, project documentation, relevant code/tests, and existing changes.
Trace the affected behavior and interfaces; identify constraints, reuse
opportunities, risks, and verification commands. Separate observed facts from
assumptions. Research answers available in the workspace before asking users.

Inspect an installed/native planning skill only if actually available. Do not
invent access to internal instructions or copy unavailable material. Use this
workflow independently when none is installed. Read the repository's
`task-management`, `setup-wtp`, and `codex-wtp-loop` skills when available for
local terminology, storage safety, and eventual routing; reading them does
not authorize their installation, claiming, or dispatch workflows.

Before approval, keep research and proposals in conversation or memory: no
implementation edits, plan files, configuration changes, WTP task mutations,
comments, handoff writes, or setup operations. Avoid tests/builds that write
artifacts during this phase. `wtp version`, `wtp help`, and `wtp schema` can
establish the installed CLI contract without opening the store. Read existing
`.wtp.json` to resolve `wtpDir` from its configuration root, and inspect the
selected task files directly without editing them. Storage-opening commands
such as `wtp task list`, `wtp stats`, `wtp task show`, and `wtp task ready` can
initialize directories, lock files, or migrate legacy UUID filenames; do not
use them as a pre-approval dry run. If a provider has no verified read-only
inspection surface, state the limitation and defer live checks until approval.

Preserve existing task records and retained handoffs. Establish whether tasks
already cover the request; propose reuse via exact existing IDs instead of
duplicates. Never assume a branch name or a predicted WTP ID.

## 2. Clarify decisions, not discoverable facts

Ask only decision-critical questions: scope, behavior, compatibility, risk,
acceptance, or a choice that changes the task breakdown. State reasonable
defaults for low-risk details. Offer concrete tradeoffs and a recommendation
when useful; avoid repeated questions already answered in this conversation.

Discover the current supported user-input tool rather than assuming the old
`ask_user_questions` name. Use `request_user_input` when exposed and allowed
in the active mode (it may be Plan-only); obey its current schema and limits.
Otherwise ask a concise plain-text question in the conversation. Use plain
text for the approval gate if the tool forbids permission questions. An empty
answer is not approval or acceptance of a model default. Continue independent
research, but do not silently resolve a material choice or call unavailable
tools. Do not ask the user to disable plan mode and repeat the planning request.

## 3. Ask for the target model distribution

Before the final WTP proposal, ask explicitly:

> Which enabled models and reasoning efforts should receive these tasks, and
> what percentage should each receive (total 100%)? You can choose 100% on one
> verified model, or give a split. This targets task counts, not tokens, cost,
> or runtime; I will show rounding and explain any risk-based deviations.

Reuse an explicit distribution supplied for this plan without asking again.
Build the enabled model/effort inventory from the destination's available
tool schema or a user-confirmed inventory. Existing WTP labels, examples,
documentation, and the planner's model are not proof of destination access.
If no inventory is available, ask for it; do not guess model IDs. For vague
labels use the available `codex-wtp-loop` resolution rules, preserving explicit
versions and asking about ambiguity. Record exact resolved model IDs and
supported efforts, including an explicit default effort where needed.

Validate that entries are unique enabled model/effort pairs, percentages are
finite non-negative numbers, and the total is exactly 100%. Require at least
one positive share; never assign tasks to a zero-share or unselected pair.
For missing, unsupported, ambiguous, duplicate, or invalid input, explain the
specific problem and ask for a corrected split. Offer 100% on a verified,
user-selected pair as a simple default, but require acceptance; do not silently
normalize a 90% total or substitute a different model.

For N newly proposed tasks, multiply N by each percentage, take the floors,
then give remaining slots to the largest fractional remainders (ties follow
the user's distribution order). Match tasks to those quotas based on
complexity/risk, not just row order. Document each assignment's rationale.
Depart from the rounded quotas only for documented complexity/risk reasons,
flag the changed counts, and obtain approval of those deviations as part of
the proposal. Distinguish unavoidable integer rounding from discretionary
changes. Do not create artificial tasks to satisfy percentages. Existing
tasks reused as prerequisites are excluded from N and are not reassigned.

## 4. Present a complete plan and editable numbered proposal

Read [references/proposal.md](references/proposal.md) for the template and
validation/preview contract. Include scope and exclusions, evidence with
file references, assumptions and decisions, implementation approach and
interface/data changes, dependencies and sequencing, risks and mitigations,
acceptance criteria, and concrete verification commands/outcomes. Resolve
material choices before calling the plan final. An implementing worker should
not need to rediscover product decisions or read this conversation.

Give each task a stable proposal number and concise title, self-contained
implementation details, acceptance and verification expectations, WTP estimate
(`xs|s|m|l|xl`), priority (`low|medium|high|urgent`), lane/grouping, explicit
dependencies, and exact model/effort plus assignment reason. Split along
testable outcomes; identify work that can run independently and order real
prerequisites. Use proposal numbers for new dependencies and exact opaque IDs
for existing ones. Validate references and reject cycles or self-dependencies.

Display a distribution table with requested percentages, rounded quotas,
assigned counts, actual percentages, and explained deviations. Show the
target repository/worktree/store and any unsupported metadata fallback.
Keep the numbered proposal editable in the conversation. If the user asks
for only a preview/dry run, finish with the proposal and create nothing.

## 5. Amend / refuse / approve

After presenting the complete proposal and distribution, ask directly:

> Would you like to amend these numbered tasks, refuse/cancel them, or
> approve (task up) this proposal in WTP?

- **Amend:** collect the changes, preserve stable numbers where possible,
  revise the plan and tasks, revalidate dependencies/distribution, and present
  a new revision for approval. Approval of an older revision does not apply.
- **Refuse/cancel:** create nothing, mutate nothing, and stop. Retain the
  conversational proposal only if useful; do not delete existing WTP data.
- **Approve/task up:** require explicit approval of the displayed revision,
  including model assignments and deviations. Planning intent, silence,
  a timeout, an ambiguous response, or approval of a different plan is not
  approval to create these tasks. Ask only about the unresolved decision.

Respect approval already given for this exact unchanged proposal; do not ask
again. If only a subset is approved, show and validate that subset's dependency
closure and revised distribution, then obtain approval of that revised subset
before writing. Do not silently include
unapproved prerequisites or reinterpret an invalid subset as full approval.

The helper's digest binds the reviewed proposal to its generated commands;
it is a consistency check, not evidence of user authorization. Never create
or mutate WTP tasks until explicit approval. If the active runtime prohibits
writes even after user approval, retain the approved proposal and exact
handoff, explain the runtime restriction, and stop without bypassing it. Do
not require the user to discard or recreate the plan.

## 6. Create the approved tasks and hand off

Read [references/handoff.md](references/handoff.md) before any WTP write.
Use canonical `wtp --json task create` commands from the intended root in
dependency order. Recheck context, availability, existing prerequisites, and
CLI capabilities; any material change requires an amended proposal. Capture
each returned `shortId` and UUID before proceeding. Never predict IDs or edit
storage directly. Keep tasks `todo` and unassigned unless the proposal
explicitly authorized otherwise. WTP's `model` is recommendation metadata,
not a routing guarantee; the later dispatcher validates it against its host.

Verify created metadata and dependency IDs, report the proposal-number to
WTP-ID map, actual model counts, and ready/blocked relationships. Handle
partial failure by stopping, identifying confirmed creations, uncertain
outcomes, and remaining rows, without blind retries or deletion. End with an
explicit WTP handoff and stop; subsequent implementation or a
`codex-wtp-loop` run needs its own authorization/settings.
