# wtp Agent Workflow

Use `wtp` as the repo-local source of truth for task state.

## Core Rules

1. Break plans into concrete tasks before starting implementation.
2. Prefer short IDs like `wtp-0005` in human discussion and CLI usage.
3. Use dependencies for ordering, not for loose notes.
4. Add progress comments when the task state materially changes.
5. Pause blocked work instead of leaving it in progress.
6. Mark a task done only after implementation and verification are both complete.

## Claiming Work

Preferred pattern:

```sh
wtp task next --agent Tony
```

That command claims work atomically by moving the returned task to `inProgress`. It is the safest way for multiple agents on the same filesystem to pull work without colliding.

Explicit claim is still available when needed:

```sh
wtp task start wtp-0005 --agent Tony
```

Use explicit start when you already know which task should be claimed.

## Breaking Down Plans

When starting from a plan document:

1. Identify the smallest useful implementation slices.
2. Create one task per slice.
3. Add dependency links where ordering matters.
4. Put finish-line details in `description`.

Example:

```sh
wtp task create \
  --title "Add config validation" \
  --description "Validate provider-specific env vars and fail with actionable messages"

wtp task create \
  --title "Add provider tests" \
  --description "Cover unsupported tool and missing env-var cases" \
  --depends-on wtp-0007
```

## Day-to-Day Loop

Claim or start work:

```sh
wtp task next --agent Tony
wtp task start wtp-0008 --agent Tony
```

Record progress:

```sh
wtp task comment wtp-0008 --agent Tony --message "Added provider fixture coverage"
```

Pause when blocked:

```sh
wtp task pause wtp-0008
wtp task comment wtp-0008 --agent Tony --message "Paused on missing provider config contract"
```

Finish after verification:

```sh
wtp task done wtp-0008
```

## Writing Good Tasks

- Keep tasks small enough to finish in one focused pass.
- Use clear, imperative titles.
- Keep descriptions concrete and testable.
- Avoid combining unrelated changes into one task.
- Create follow-up tasks instead of stretching a task beyond its original scope.

## Manual Inspection

The flat-file backend is intentionally inspectable:

- `.wtp/todo/`
- `.wtp/inProgress/`
- `.wtp/paused/`
- `.wtp/done/`

Task filenames use short IDs such as `wtp-0005.json`, so agents can inspect status directories directly even if they are not using the CLI at that moment.

## Notes on Safety

- `wtp` uses a repo-local lock file for mutating operations.
- The lock only helps if agents go through `wtp`.
- Manual edits to `.wtp/` should be rare and deliberate.
- If manual repair is needed, keep `status` in the JSON aligned with the containing directory.
