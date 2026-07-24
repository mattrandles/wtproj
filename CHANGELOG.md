# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
