# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.3] - 2026-08-23

### Fixed

- Bounded `wtp graph` memory and output growth for converging dependency paths
  by expanding each task once and emitting explicit references for repeats.
- Added unit and native prerelease regression coverage for layered shared
  dependency graphs that previously caused exponential allocation.

## [0.3.2] - 2026-08-21

### Added

- Added configurable project task statuses with catalog-aware storage,
  scheduling, CLI filtering, statistics, documentation, and regression tests.

## [0.3.1] - 2026-08-21

### Added

- Added the `wtp stats` command with deterministic overview and focused
  reports, status filtering, JSON output, and retained-handoff metrics.

## [0.3.0] - 2026-08-09

### Added

- Documented the retained handoff workflow, including global and task scopes,
  non-consuming claims, JSON storage and responses, purge cutoffs, and export
  compatibility.

### Changed

- Made task-history allocation merge-safe across named Git branches with
  branch-scoped IDs and per-branch indexes. Automatic selection still checks
  the current branch and then legacy tasks; foreign tasks require explicit
  start, and branch renames do not migrate existing task IDs or files.

## [0.2.0] - 2026-07-23

### Added

- Git and worktree context metadata with configurable centralized task storage.
- Unified Unix and PowerShell commit and release verification gates.
- Expanded behavioral coverage for CLI, storage, updater, installation, and
  release-contract workflows.

## [0.1.0] - 2026-07-21

### Added

- Initial public project policy and contribution documentation.
- Stable standalone GitHub Release assets, SHA-256 metadata, and a reusable
  platform lookup contract for future self-updates.
- A checksum-verified `wtp update` command with strict semantic-version
  comparison, exact platform selection, permission-preserving replacement,
  Unix atomic updates, and rollback-aware deferred Windows replacement.
- First public `wtp` release with standalone binaries for macOS, Linux, and
  Windows on AMD64 and ARM64, plus SHA-256 checksums and direct-download
  installation guidance.
