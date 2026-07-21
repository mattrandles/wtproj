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

## Secret scanning

Never commit credentials or private keys, including test credentials that may
be mistaken for live material. Use clearly non-secret placeholders and resolve
real values from the environment.

CI runs the pinned Gitleaks version and checksum recorded in
`.github/workflows/ci.yml` against both the checked-out tree and all fetched
history. Before opening a pull request, run the same scopes with that version:

```sh
gitleaks dir . --redact=100 --max-archive-depth=3 --max-decode-depth=5
gitleaks git . --log-opts="--all --full-history" --redact=100 \
  --max-archive-depth=3 --max-decode-depth=5
```

Do not add a broad allowlist for a finding. Review it first, then use the
narrowest path, rule, or fingerprint exclusion that documents the false
positive without concealing similar future findings.

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
