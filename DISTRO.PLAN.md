# Distribution Prep Plan for `wtp`

## Summary

Prepare `wtp` as a conventional Go CLI release with these initial channels:

1. GitHub repository as source of truth
2. GitHub Releases with versioned binaries for `darwin`, `linux`, and `windows`
3. Homebrew as the first-class package-manager channel

Do not add self-update in the first public release. Add a backlog item for it only after the package/distribution story is stable and there is a clear need for direct binary installs outside Homebrew. For this repo, self-update is a secondary convenience feature, not part of release readiness.

## Release Positioning

`wtp` is currently a repo-local Go CLI with a flat-file default backend and future provider abstraction. The cleanest initial distribution story is:

- Source on GitHub for contributors and `go install` users
- Prebuilt binaries on GitHub Releases for direct download
- Homebrew for the mainstream install path
- Native Windows binaries from GitHub Releases for Windows users until a package-manager channel is worth maintaining

Channels to defer:

- Scoop/Winget: defer even though Windows binaries are supported; add them only when Windows install volume justifies the maintenance
- AUR/Nix/asdf: community-friendly later, but not required for first release
- Docker/OCI: not useful as a primary distribution channel for a repo-local CLI that operates on the host filesystem

## What To Add Before Public Distribution

### Repository hygiene

Add these top-level assets:

- `LICENSE`
  - Use a permissive license unless there is a reason not to. Default: MIT.
- `.gitignore`
  - Ensure `.wtp/`, `.wtp-export/`, build artifacts, and temp binaries are ignored.
- `CHANGELOG.md`
  - Start with `v0.1.0` and keep it manually curated.
- `SECURITY.md`
  - Minimal vulnerability reporting instructions.
- `CONTRIBUTING.md`
  - Local dev, `go test ./...`, `./scripts/check.sh`, release expectations.
- `.github/`
  - Issue templates, PR template, CI workflow, release workflow.

### README upgrades

Expand the existing README so it supports distribution, not just development:

- Add versioned install section:
  - `brew install ...`
  - direct binary download from GitHub Releases
  - `go install` as a secondary path
- Add supported platforms table
- Document Windows install/verification separately from Homebrew
- Add "stability" statement:
  - CLI surface and JSON schema are early/stable-enough for use, or explicitly pre-1.0
- Add upgrade instructions
- Add config-file and storage compatibility notes
- Add clear statement that Trello is not implemented yet

### Versioning and release conventions

Adopt semver from the first public release:

- Start at `v0.1.0`
- Treat these as the initial public compatibility surface:
  - CLI commands and flags
  - `--json` output shape
  - `.wtp.json` schema
  - `.wtp/` on-disk layout
  - status names and `task next` semantics

Embed version metadata into the binary:

- `version`
- `commit`
- `date`

Add a command for this if missing:

- `wtp version`

That becomes important for support, bug reports, and package-manager formulas.

### CI and release automation

Add CI for every PR/push:

- `gofmt -l`
- `go test ./...`
- Unix smoke test via `./scripts/e2e_smoke.sh`
- PowerShell smoke test via `./scripts/e2e_smoke.ps1`

Add release automation for tags:

- Build archives for:
  - `darwin/amd64`
  - `darwin/arm64`
  - `linux/amd64`
  - `linux/arm64`
  - `windows/amd64`
  - `windows/arm64` if the release tooling and test matrix support it cleanly
- Publish checksums
- Create GitHub Release notes from changelog or generated notes
- Optionally sign artifacts if stronger supply-chain posture is desired early

Use a standard Go release toolchain rather than hand-rolled scripts. Default choice:

- `goreleaser`

That will also simplify the Homebrew formula generation path.

### Homebrew distribution

Make Homebrew the first package-manager target.

Recommended setup:

- Create a separate tap repo, for example `matty/homebrew-tap`
- Publish a formula for `wtp`
- Have the release pipeline update the formula automatically from release artifacts/checksums

Install UX should be:

```sh
brew tap matty/tap
brew install wtp
```

If the repo/name allows a cleaner path later, adjust, but use a dedicated tap first rather than waiting for Homebrew core.

### Windows distribution

Support Windows through GitHub Release assets first.

Recommended setup:

- publish `wtp.exe` zip archives in the tagged release
- document a simple PowerShell install path in README
- verify Windows artifacts in CI before each release

Defer Windows-specific package managers until usage justifies the extra maintenance burden.

## What Not To Add Yet

Do not make these blockers for the first release:

- Self-update
- Windows package-manager support beyond direct GitHub Release binaries
- Trello implementation
- Multi-channel installer scripts
- Auto-migration tooling for future schema changes unless the storage format is already changing

## Self-Update Decision

### Recommendation

Add a TODO for self-update, but do not implement it before initial distribution.

### Why

For a Go CLI distributed through Homebrew, self-update adds real complexity and limited value:

- Homebrew already owns upgrades
- self-update can conflict with package-manager ownership of the binary
- it adds network/update logic, release-channel handling, signature/checksum concerns, and platform-specific edge cases
- the current project is early enough that install/release fundamentals matter more

### When it becomes justified

Promote self-update only if one of these becomes true:

- a large share of users install from raw GitHub binaries instead of Homebrew
- fast in-tool upgrade prompts are needed across many repos/users
- multiple official install channels are added that are not package-managed
- enterprise users need a direct upgrade path without relying on external package repos

### TODO shape

Add a backlog item now with this scope:

- evaluate `fynelabs/selfupdate` after release automation exists
- detect package-manager installs and avoid self-mutation in those cases
- define update channels and rollback/checksum behavior before implementation

## Public APIs / Interfaces / Types To Treat Carefully

Before distribution, explicitly declare these as release-sensitive:

- CLI commands in `internal/cli/cli.go`
- config schema in `internal/config/config.go`
- canonical task model and status names in `internal/core`
- README-documented install and usage commands
- exported artifact format from `wtp export`

Additions recommended before release:

- `wtp version`
- explicit docs for storage-compat guarantees:
  - whether `.wtp/` layout may change across `0.x`
- explicit docs for JSON-output stability:
  - whether field names/order are stable enough for scripting

## Test Cases and Scenarios

Add or verify release-focused coverage for:

- `wtp version` prints embedded version metadata
- release build works on all supported target OS/arch pairs
- Homebrew install succeeds from a tagged release
- Windows release zip extracts and runs `wtp.exe`
- installed binary can:
  - create tasks
  - claim next task
  - export tasks
- checksum validation matches published assets
- direct-download tarball extraction produces expected binary name/layout
- upgrade path from one tagged version to the next preserves `.wtp/` behavior
- unsupported/incomplete provider config still fails with clear messaging after packaged install

## Assumptions and Defaults

- Initial audience is developers using local repos on macOS, Linux, and Windows
- First public release is `v0.1.0`
- License default is MIT
- First-class channels are GitHub Releases and Homebrew
- `go install` is supported but not the primary documented install path
- Self-update is backlog only, not in the first release
- Release automation should be built around `goreleaser`
