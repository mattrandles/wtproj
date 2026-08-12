# Direct-download release QA

The recommended complete release check is the unified gate. It includes the
commit checks, GoReleaser configuration validation, a non-publishing snapshot,
the release-asset contract test, and this direct-download QA:

```sh
./scripts/verify.sh release
```

```powershell
./scripts/verify.ps1 release
```

To validate the exact six files selected for a local pre-release check, set
`WTP_QA_CANDIDATE_DIR` to the flat candidate directory and
`WTP_QA_CANDIDATE_VERSION` to its embedded version:

```sh
WTP_QA_CANDIDATE_DIR=/absolute/path/candidate \
WTP_QA_CANDIDATE_VERSION=1.2.3 \
WTP_QA_REPORT=/absolute/path/updater.json \
./scripts/release_qa.sh
```

The harness builds only a disposable lower-version fixture for the initial
installation. It copies the supplied candidate assets byte-for-byte for the
target release, verifies their checksums and executable formats, and exercises
the loopback updater matrix against those exact target bytes. It never rebuilds
the candidate directory.

The legacy harness remains available when only the direct-download fixture is
needed:

```sh
./scripts/release_qa.sh
```

The Unix harness requires Go, Git, curl, `sha256sum` (or `shasum`), PowerShell,
and GoReleaser. The unified Unix gate also checks for `file`. The PowerShell
gate requires Go, Git, PowerShell, and GoReleaser; it uses the PowerShell QA
implementation and does not require a Unix shell. Use GoReleaser 2.17.0, the
version pinned by the release workflow.

The harness creates only temporary workspaces and a local loopback HTTP server;
it does not publish, tag, change a user installation, or contact GitHub. The
unified gate also places the GoReleaser snapshot and normalized assets in a
temporary directory, so a failed run cannot leave `dist/` or other generated
files in the checkout.

Commit verification normally takes under a minute with a warm Go cache. Allow
several minutes for the full release gate because it builds and exercises
multiple snapshots.

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

The Unix harness also runs a programmable updater failure matrix against
fresh copies of the initial release asset. The loopback server reads a
temporary `scenario.json` between cases and can return exact API/asset status
and bodies, delays, redirects, malformed or truncated responses, connection
termination, and custom asset sets. Failed cases must leave the installed
executable digest and permissions unchanged, leave the project `.wtp`
manifest byte-for-byte unchanged, leave no `.wtp-update-*` debris, and prove
the old executable still starts. Set `WTP_QA_REPORT` to retain the structured
`wtp-release-qa/v1` report outside the temporary harness root.

The embedded loopback fixture URL is available only to QA snapshots. Normal
GoReleaser builds retain the canonical HTTPS GitHub endpoint. Even a QA build
can enable HTTP only for `127.0.0.1` or `::1`.

On Unix, the harness cross-compiles all tests for both Windows targets and
runs the PowerShell verifier against both PE artifacts. When run on Windows,
the PowerShell portion also starts the matching Windows executable and runs
the install/create/claim/export workflow. A Windows updater's deferred helper
is covered by its compiled Windows source and unit tests; Unix exercises the
completed in-place replacement end to end. The PowerShell verification gate
has the complementary limitation: it executes the Windows workflow only on a
Windows host, while non-Windows PowerShell validates asset checksums and file
formats.

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

Windows deferred replacement-helper execution, rollback, and error-file
behavior require native Windows execution. Unix permission and symlink cases
are recorded separately; cross-compilation validates formats and source
compatibility only and is not native Windows updater evidence.
