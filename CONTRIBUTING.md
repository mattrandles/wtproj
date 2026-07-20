# Contributing to wtp

Thanks for contributing. Please open an issue before starting substantial work
so the proposed change can be discussed and tracked.

## Development

1. Fork the repository and create a focused branch.
2. Keep changes small, documented, and covered by tests where behavior changes.
3. Run `./scripts/check.sh` before opening a pull request. On Windows, run
   `./scripts/check.ps1`.
4. Describe the problem, solution, verification, and any follow-up work in the
   pull request.

## Task backlog policy

This repository dogfoods `wtp`. Its `.wtp/` task records are intentionally
version-controlled so planning and task history travel with the source.
Do not add a broad `.wtp/` ignore rule. The transient `.wtp/meta/wtp.lock` and
export directories are ignored because they are local runtime artifacts.

Use the CLI for normal task changes rather than editing task JSON directly:

```sh
wtp task list
wtp task comment <task-id> --message "..."
wtp task done <task-id>
```

## Code of conduct

Be respectful and constructive. Harassment or discriminatory behavior is not
welcome in project discussions or contributions.
