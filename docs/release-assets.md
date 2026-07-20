# GitHub Release asset contract

GitHub Releases publish `wtp` as standalone, uncompressed executables. Asset
names do not contain the release version, so clients can resolve a platform to
the same name across all releases. They are the sole supported distribution
channel; direct installation examples and install-scope permissions are in the
[README](../README.md#install).

| GOOS | GOARCH | Asset |
| --- | --- | --- |
| `darwin` | `amd64` | `wtp_darwin_amd64` |
| `darwin` | `arm64` | `wtp_darwin_arm64` |
| `linux` | `amd64` | `wtp_linux_amd64` |
| `linux` | `arm64` | `wtp_linux_arm64` |
| `windows` | `amd64` | `wtp_windows_amd64.exe` |
| `windows` | `arm64` | `wtp_windows_arm64.exe` |

The canonical machine-readable mapping is exposed by the
`internal/releaseasset` package. Both release configuration and update code
must remain covered by its exact-matrix tests when this list changes.

## Checksums

Every release also contains `checksums.txt`. It uses SHA-256 and the standard
`sha256sum` text format: one lowercase 64-character hexadecimal digest, two
spaces, the exact asset filename, and a newline. It contains one entry for each
standalone executable above. Clients must select the entry by the complete
asset filename; they must not rely on line order or accept a digest for a
different filename.

## Latest-release lookup

Clients discover updates with an unauthenticated `GET` to:

```text
https://api.github.com/repos/mattrandles/wtproj/releases/latest
```

Requests use `Accept: application/vnd.github+json` and
`X-GitHub-Api-Version: 2022-11-28`. GitHub defines this endpoint as the latest
published release that is neither a draft nor a prerelease. Clients read
`tag_name`, then locate both their exact platform asset and `checksums.txt` in
`assets` by `name`, downloading each from `browser_download_url`.

Release tags use `v` followed by the semantic version, while the version
embedded in the binary omits that prefix. A missing or duplicate required
asset, an unsupported platform, an invalid tag, or a missing/mismatched
checksum is a hard update error; clients must leave the installed executable
unchanged.

## Self-update safety

`wtp update` accepts only strict Semantic Versioning for both the embedded
version and the latest release tag. Build metadata does not affect precedence,
and a latest version equal to or older than the running version is a no-op.
Development builds report that they cannot self-update rather than contacting
GitHub.

The updater streams the selected executable into a temporary file in the same
directory as the installed executable, limits response sizes, hashes the bytes
as they arrive, and compares the result with the checksum selected by the exact
asset filename. It preserves the installed permission bits and does not touch
the target before all validation succeeds. Symlinked launch paths resolve to
their real target.

Unix uses an atomic same-filesystem rename followed by a parent-directory sync.
Windows stages the verified executable and starts a detached PowerShell helper,
because a process cannot overwrite its own running `.exe`. The helper waits for
the updater process to exit, moves the old executable to a backup, installs the
new one, and restores the backup if installation fails. Deferred failures are
written to `<executable>.wtp-update-error.txt`. All install scopes use this same
path-based behavior; replacement errors identify the target and explain that
the executable and its directory must be writable.
