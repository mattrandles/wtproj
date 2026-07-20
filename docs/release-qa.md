# Direct-download release QA

Run the complete, self-contained release check before publishing:

```sh
./scripts/release_qa.sh
```

It requires Go, Git, curl, `sha256sum` (or `shasum`), PowerShell, and
GoReleaser. Use the GoReleaser version pinned by the release workflow. The
harness creates only a temporary workspace and local loopback HTTP server; it
does not publish, tag, change a user installation, or contact GitHub.

The harness makes two real GoReleaser snapshots: `0.0.0-qa` and `0.0.1`. It
uses their standalone artifacts and `checksums.txt` exactly as release users
do, with a local server that implements the GitHub `releases/latest` response
and asset URLs. It validates all six checksums and executable file formats,
then runs the host Unix executable through project-local, user-local, and a
disposable global-PATH installation. Each scope starts the binary and runs
create, atomic claim, and export. The project-local executable runs the real
`wtp update` command against the local release fixture; the test verifies the
new binary starts and that its project's `.wtp` files are unchanged byte for
byte.

The embedded loopback fixture URL is available only to QA snapshots. Normal
GoReleaser builds retain the canonical HTTPS GitHub endpoint. Even a QA build
can enable HTTP only for `127.0.0.1` or `::1`.

On Unix, the harness cross-compiles all tests for both Windows targets and
runs the PowerShell verifier against both PE artifacts. When run on Windows,
the PowerShell portion also starts the matching Windows executable and runs
the install/create/claim/export workflow. A Windows updater's deferred helper
is covered by its compiled Windows source and unit tests; Unix exercises the
completed in-place replacement end to end.

## Testing a future published release

To make a real published release the upgrade fixture instead of the generated
`0.0.1` snapshot, point the harness at that release's direct-download base
URL and provide its semantic version (without the `v` prefix):

```sh
WTP_QA_UPGRADE_BASE_URL=https://github.com/mattrandles/wtproj/releases/download/v1.2.3 \
WTP_QA_UPGRADE_VERSION=1.2.3 \
./scripts/release_qa.sh
```

The initial binary remains a disposable lower-version snapshot containing the
loopback fixture endpoint. The upgrade bytes, filenames, and checksums are
downloaded from the supplied published release, so the same safe in-place
upgrade validation remains deterministic and publish-independent.

By default the global-PATH scope is a temporary directory. To test a specific
writable global directory, set `WTP_QA_GLOBAL_DIR`; an unwritable requested
directory is reported as skipped rather than escalating privileges.
