#!/usr/bin/env bash
# Builds throwaway GoReleaser snapshots and tests the direct-download contract
# end-to-end. It never contacts GitHub, changes a real installation, or writes
# into the checkout. Set WTP_QA_CANDIDATE_DIR to validate an already-built,
# exact release asset set; that directory is copied byte-for-byte and is never
# rebuilt. WTP_QA_UPGRADE_BASE_URL remains available for published-release QA.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
fixture_dir="$work_dir/fixture"
initial_dist="$work_dir/initial-dist"
upgrade_dist="$work_dir/upgrade-dist"
downloads="$work_dir/downloads"
home_dir="$work_dir/home"
server_ready="$work_dir/server-url"
server_log="$work_dir/server.log"
server_pid=""
candidate_dir="${WTP_QA_CANDIDATE_DIR:-}"

assets=(
  wtp_darwin_amd64
  wtp_darwin_arm64
  wtp_linux_amd64
  wtp_linux_arm64
  wtp_windows_amd64.exe
  wtp_windows_arm64.exe
)

fail() {
  printf 'release QA failed: %s\n' "$1" >&2
  exit 1
}

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

require() {
  command -v "$1" >/dev/null || fail "missing required command: $1"
}

if [[ -n "$candidate_dir" ]]; then
  candidate_dir="$(cd "$candidate_dir" && pwd)"
fi

sha256_file() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum --algorithm 256 "$1" | awk '{print $1}'
  fi
}

asset_path() {
  local directory="$1"
  local name="$2"
  local found extension="" archive_name="$name"
  found="$(find "$directory" -type f -name "$name" -print)"
  if [[ -z "$found" && "$name" != "checksums.txt" ]]; then
    if [[ "$name" == *.exe ]]; then
      extension=".exe"
      archive_name="${name%.exe}"
    fi
    found="$(find "$directory" -type f -path "*/${archive_name}_*/wtp${extension}" -print)"
  fi
  [[ "$(printf '%s\n' "$found" | sed '/^$/d' | wc -l | tr -d ' ')" == "1" ]] || fail "expected one $name under $directory, found: $found"
  printf '%s\n' "$found"
}

copy_release_assets() {
  local source="$1"
  local destination="$2"
  mkdir -p "$destination"
  for asset in "${assets[@]}" checksums.txt; do
    cp "$(asset_path "$source" "$asset")" "$destination/$asset"
  done
}

build_snapshot() {
  local version="$1"
  local destination="$2"
  local config="$work_dir/goreleaser-$version.yaml"
  cp "$repo_root/.goreleaser.yaml" "$config"
  printf '\ndist: %s\n' "$destination" >> "$config"
  WTP_QA_LATEST_RELEASE_URL="$fixture_url/repos/mattrandles/wtproj/releases/latest" \
  WTP_QA_ALLOW_HTTP=true \
  WTP_QA_SNAPSHOT_VERSION="$version" \
    goreleaser release --snapshot --clean --skip=publish --config "$config"
}

manifest() {
  (
    cd "$1"
    if [[ -d .wtp ]]; then
      find .wtp -type f ! -name 'wtp.lock' -print0 | LC_ALL=C sort -z | xargs -0 sha256sum
    fi
  )
}

file_mode() {
  if stat -c '%a' "$1" >/dev/null 2>&1; then
    stat -c '%a' "$1"
  else
    stat -f '%Lp' "$1"
  fi
}

write_latest_assets_json() {
  local tag="$1"
  local output="$2"
  {
    printf '{"tag_name":"v%s","assets":[' "$tag"
    local index=0
    for asset in "${assets[@]}"; do
      [[ "$index" -eq 0 ]] || printf ','
      printf '{"name":"%s","browser_download_url":"%s/assets/%s"}' "$asset" "$fixture_url" "$asset"
      index=$((index + 1))
    done
    printf ']}\n'
  } > "$output"
}

write_scenario() {
  local latest_body_file="$1"
  local asset_set="$2"
  latest_body_file="${latest_body_file##*/}"
  printf '{"latestBodyFile":"%s","assetSet":%s}\n' "$latest_body_file" "$asset_set" > "$fixture_dir/scenario.json"
}

record_updater_result() {
  local name="$1"
  local status="$2"
  local details="$3"
  if [[ "$updater_report_first" == 1 ]]; then
    updater_report_first=0
  else
    printf ',\n' >> "$updater_report"
  fi
  printf '    {"name":"%s","status":"%s","details":"%s"}' "$name" "$status" "$details" >> "$updater_report"
}

exercise_update_case() {
  local name="$1"
  local expected_success="$2"
  local symlink_launch="$3"
  local readonly_target="${4:-0}"
  local case_root="$work_dir/updater-$name"
  local bin_dir="$case_root/bin"
  local project="$case_root/project"
  mkdir -p "$bin_dir" "$project"
  git -C "$project" init -q
  install -m 751 "$downloads/$host_asset" "$bin_dir/wtp-real"
  if [[ "$symlink_launch" == 1 ]]; then
    ln -s wtp-real "$bin_dir/wtp"
  else
    cp "$bin_dir/wtp-real" "$bin_dir/wtp"
    chmod 751 "$bin_dir/wtp"
  fi
  (
    cd "$project"
    HOME="$home_dir" USERPROFILE="$home_dir" PATH="$bin_dir:$PATH" "$bin_dir/wtp" task create --title "updater preservation $name" >/dev/null
  )
  if [[ "$readonly_target" == 1 ]]; then
    chmod 555 "$bin_dir"
  fi
  local before_binary before_mode before_manifest after_binary after_mode after_manifest
  before_binary="$(sha256_file "$bin_dir/wtp-real")"
  before_mode="$(file_mode "$bin_dir/wtp-real")"
  before_manifest="$case_root/before.manifest"
  after_manifest="$case_root/after.manifest"
  manifest "$project" > "$before_manifest"
  local output code
  set +e
  output="$(cd "$project" && HOME="$home_dir" USERPROFILE="$home_dir" PATH="$bin_dir:$PATH" timeout 4s "$bin_dir/wtp" update 2>&1)"
  code=$?
  set -e
  if [[ "$readonly_target" == 1 ]]; then
    chmod 755 "$bin_dir"
  fi
  after_binary="$(sha256_file "$bin_dir/wtp-real")"
  after_mode="$(file_mode "$bin_dir/wtp-real")"
  manifest "$project" > "$after_manifest"
  if [[ "$expected_success" == 1 ]]; then
    [[ "$code" -eq 0 ]] || fail "$name update failed ($code): $output"
  else
    [[ "$code" -ne 0 ]] || fail "$name unexpectedly succeeded: $output"
  fi
  [[ "$before_binary" == "$after_binary" ]] || fail "$name changed installed executable digest"
  [[ "$before_mode" == "$after_mode" ]] || fail "$name changed installed executable mode"
  cmp --silent "$before_manifest" "$after_manifest" || fail "$name changed project .wtp manifest"
  if find "$bin_dir" -maxdepth 1 -name '.wtp-update-*' -print -quit | grep -q .; then
    fail "$name left update staging debris"
  fi
  HOME="$home_dir" USERPROFILE="$home_dir" PATH="$bin_dir:$PATH" "$bin_dir/wtp-real" version >/dev/null || fail "$name old executable no longer starts"
  record_updater_result "$name" "passed" "exit=$code;binary=$before_binary;mode=$before_mode;manifest=unchanged"
}

run_wtp() {
  local bin_dir="$1"
  shift
  PATH="$bin_dir:$PATH" HOME="$home_dir" wtp "$@"
}

exercise_workflow() {
  local scope="$1"
  local bin_dir="$2"
  local project="$work_dir/$scope-project"
  local task_json="$project/task.json"
  local task_out="$project/task.out"
  mkdir -p "$project"
  git -C "$project" init -q

  (
    cd "$project"
    local version_output
    version_output="$(run_wtp "$bin_dir" version)"
    grep -Fq "wtp $initial_version" <<<"$version_output" || fail "$scope install did not start the initial release: $version_output"
    run_wtp "$bin_dir" --json task create --title "$scope direct-download task" --description "created by release QA" > "$task_json"
    local short_id
    short_id="$(sed -n 's/.*"shortId": "\([^"]*\)".*/\1/p' "$task_json" | head -n 1)"
    [[ -n "$short_id" ]] || fail "$scope create did not return shortId"
    run_wtp "$bin_dir" --json task next --agent "release-qa" > "$task_out"
    grep -Fq '"status": "inProgress"' "$task_out" || fail "$scope claim did not start the task"
    run_wtp "$bin_dir" export --out exported >/dev/null
    (
      cd "$repo_root"
      go run "$repo_root/scripts/assert_allocation_index.go" \
        --project-dir "$project" \
        --store-dir .wtp \
        --task-id "$short_id" > /dev/null
    )
    [[ -f "exported/$(sed -n 's/.*"id": "\([^"]*\)".*/\1/p' "$task_json" | head -n 1).json" ]] || fail "$scope workflow did not export task"
  )
}

require goreleaser
require curl
require go
require git
require pwsh
if ! command -v sha256sum >/dev/null && ! command -v shasum >/dev/null; then
  fail "need sha256sum or shasum"
fi

host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
case "$host_os/$host_arch" in
  darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) host_asset="wtp_${host_os}_${host_arch}" ;;
  *) fail "Unix execution requires a supported host target, got $host_os/$host_arch" ;;
esac

mkdir -p "$fixture_dir"
go run "$repo_root/scripts/release_fixture_server.go" --root "$fixture_dir" --ready-file "$server_ready" >"$server_log" 2>&1 &
server_pid=$!
for _ in {1..100}; do
  [[ -s "$server_ready" ]] && break
  sleep 0.05
done
[[ -s "$server_ready" ]] || { cat "$server_log" >&2; fail "fixture server did not start"; }
fixture_url="$(<"$server_ready")"

initial_version="${WTP_QA_INITIAL_VERSION:-0.0.0-qa}"
if [[ -n "$candidate_dir" ]]; then
  [[ -f "$candidate_dir/checksums.txt" ]] || fail "candidate directory is missing checksums.txt"
  [[ -n "${WTP_QA_CANDIDATE_VERSION:-}" ]] || fail "WTP_QA_CANDIDATE_VERSION is required with WTP_QA_CANDIDATE_DIR"
  # The initial binary is only a disposable lower-version fixture. The target
  # release bytes below are caller-supplied and are not rebuilt.
  build_snapshot "$initial_version" "$initial_dist"
  copy_release_assets "$initial_dist" "$fixture_dir/initial"
  copy_release_assets "$candidate_dir" "$fixture_dir"
  upgrade_version="$WTP_QA_CANDIDATE_VERSION"
else
  build_snapshot "$initial_version" "$initial_dist"
  copy_release_assets "$initial_dist" "$fixture_dir/initial"
fi

if [[ -n "${WTP_QA_UPGRADE_BASE_URL:-}" ]]; then
  [[ -n "${WTP_QA_UPGRADE_VERSION:-}" ]] || fail "WTP_QA_UPGRADE_VERSION is required with WTP_QA_UPGRADE_BASE_URL"
  for asset in "${assets[@]}" checksums.txt; do
    curl --fail --location --silent --show-error "$WTP_QA_UPGRADE_BASE_URL/$asset" --output "$fixture_dir/$asset"
  done
  upgrade_version="$WTP_QA_UPGRADE_VERSION"
else
  if [[ -z "$candidate_dir" ]]; then
    upgrade_version="${WTP_QA_UPGRADE_VERSION:-0.0.1}"
    build_snapshot "$upgrade_version" "$upgrade_dist"
    copy_release_assets "$upgrade_dist" "$fixture_dir"
  fi
fi
printf '{"tag_name":"v%s"}\n' "$upgrade_version" > "$fixture_dir/latest.json"

# Download every fixture asset by its stable release name, then validate its
# exact checksums entry before any installation is attempted.
mkdir -p "$downloads"
for asset in "${assets[@]}" checksums.txt; do
  curl --fail --location --silent --show-error "$fixture_url/initial-assets/$asset" --output "$downloads/$asset"
done
for asset in "${assets[@]}"; do
  actual="$(sha256_file "$downloads/$asset")"
  grep -Fxq "$actual  $asset" "$downloads/checksums.txt" || fail "checksum mismatch or malformed entry for $asset"
  file "$downloads/$asset" | grep -Eq 'Mach-O|ELF|PE32' || fail "$asset is not a platform executable"
done

if [[ -n "$candidate_dir" ]]; then
  candidate_downloads="$work_dir/candidate-downloads"
  mkdir -p "$candidate_downloads"
  for asset in "${assets[@]}" checksums.txt; do
    curl --fail --location --silent --show-error "$fixture_url/assets/$asset" --output "$candidate_downloads/$asset"
  done
  for asset in "${assets[@]}"; do
    actual="$(sha256_file "$candidate_downloads/$asset")"
    grep -Fxq "$actual  $asset" "$candidate_downloads/checksums.txt" || fail "candidate checksum mismatch for $asset"
    file "$candidate_downloads/$asset" | grep -Eq 'Mach-O|ELF|PE32' || fail "candidate $asset is not a platform executable"
  done
fi

# Project-local, user-local, and a disposable global-PATH directory all use
# the same direct-download installation mechanics. Override WTP_QA_GLOBAL_DIR
# to exercise a real writable global directory; it is skipped when unavailable.
project_bin="$work_dir/project-install/.tools/bin"
user_bin="$home_dir/.local/bin"
global_bin="${WTP_QA_GLOBAL_DIR:-$work_dir/global/bin}"
for target in "$project_bin" "$user_bin"; do
  mkdir -p "$target"
  install -m 755 "$downloads/$host_asset" "$target/wtp"
done
exercise_workflow project "$project_bin"
exercise_workflow user "$user_bin"
if mkdir -p "$global_bin" 2>/dev/null && install -m 755 "$downloads/$host_asset" "$global_bin/wtp" 2>/dev/null; then
  exercise_workflow global "$global_bin"
else
  printf 'release QA: skipped global-path installation at %s (not writable)\n' "$global_bin"
fi

# Run the real self-update command from disposable installations and prove
# every failed updater case preserves the installed executable and project
# storage byte-for-byte. The fixture scenario is changed only through files
# below its temporary root; no public network is contacted.
updater_report="$work_dir/updater-failure-matrix.json"
updater_report_first=1
printf '{"schemaVersion":"wtp-release-qa/v1","status":"passed","scenarios":[\n' > "$updater_report"
write_latest_assets_json "$upgrade_version" "$fixture_dir/latest-valid.json"
cp "$fixture_dir/latest-valid.json" "$fixture_dir/latest-unsafe-url.json"
sed -i "s#${fixture_url}/assets/${host_asset}#http://example.invalid/wtp#" "$fixture_dir/latest-unsafe-url.json"
printf '{"tag_name":"v%s"}\n' "$initial_version" > "$fixture_dir/latest-equal.json"
printf '{"tag_name":"v0.0.0-alpha"}\n' > "$fixture_dir/latest-older.json"
printf '{"tag_name":"not-a-version"}\n' > "$fixture_dir/latest-invalid-tag.json"
printf '{"tag_name":"v%s","assets":[]}\n' "$upgrade_version" > "$fixture_dir/latest-missing-assets.json"
printf '{"tag_name":"v%s","assets":[{"name":"checksums.txt","browser_download_url":"%s/assets/checksums.txt"},{"name":"checksums.txt","browser_download_url":"%s/assets/checksums.txt"}]}\n' "$upgrade_version" "$fixture_url" "$fixture_url" > "$fixture_dir/latest-duplicate-assets.json"
printf '{"latestRedirect":"http://example.invalid/releases/latest"}\n' > "$fixture_dir/redirect-scenario.json"
printf 'not a checksum file\n' > "$fixture_dir/malformed-checksums.txt"
printf '%064d  %s\n' 0 "$host_asset" > "$fixture_dir/mismatch-checksums.txt"
sha256_file "$fixture_dir/$host_asset" | awk -v asset="$host_asset" '{print $1 "  " asset}' > "$fixture_dir/valid-checksums.txt"

write_scenario "$fixture_dir/latest-equal.json" false
exercise_update_case equal-version-noop 1 0
write_scenario "$fixture_dir/latest-older.json" false
exercise_update_case older-version-noop 1 0
write_scenario "$fixture_dir/latest-invalid-tag.json" false
exercise_update_case invalid-tag 0 0
write_scenario "$fixture_dir/latest-missing-assets.json" true
exercise_update_case missing-assets 0 0
write_scenario "$fixture_dir/latest-duplicate-assets.json" true
exercise_update_case duplicate-assets 0 0
printf '{"latestBodyFile":"latest-valid.json","assets":[{"name":"checksums.txt","bodyFile":"malformed-checksums.txt"}]}\n' > "$fixture_dir/scenario.json"
exercise_update_case malformed-checksums 0 0
printf '{"latestBodyFile":"latest-valid.json","assets":[{"name":"checksums.txt","bodyFile":"mismatch-checksums.txt"}]}\n' > "$fixture_dir/scenario.json"
exercise_update_case checksum-mismatch 0 0
printf '{"latestBodyFile":"latest-valid.json","assets":[{"name":"checksums.txt","status":503,"body":"checksum unavailable"}]}\n' > "$fixture_dir/scenario.json"
exercise_update_case failed-checksum-download 0 0
printf '{"latestBodyFile":"latest-valid.json","assets":[{"name":"checksums.txt","close":true}]}\n' > "$fixture_dir/scenario.json"
exercise_update_case connection-termination 0 0
printf '{"latestBodyFile":"latest-valid.json","assets":[{"name":"checksums.txt","bodyFile":"valid-checksums.txt"},{"name":"%s","bodyFile":"%s","truncateAt":32}]}\n' "$host_asset" "$host_asset" > "$fixture_dir/scenario.json"
exercise_update_case truncated-download 0 0
printf '{"latestBodyFile":"latest-valid.json","latestDelayMs":5000}\n' > "$fixture_dir/scenario.json"
exercise_update_case timeout-download 0 0
printf '{"latestBodyFile":"latest-unsafe-url.json"}\n' > "$fixture_dir/scenario.json"
exercise_update_case unsafe-url 0 0
cp "$fixture_dir/redirect-scenario.json" "$fixture_dir/scenario.json"
exercise_update_case unsafe-redirect 0 0
exercise_update_case symlink-launch 0 1
if [[ "$(id -u)" -eq 0 ]]; then
  record_updater_result unwritable-target not_applicable "root can bypass Unix permission fixture"
  record_updater_result replacement-rollback not_applicable "native replacement fault injection is covered by focused seam tests; permission fixture requires non-root execution"
else
  rm -f "$fixture_dir/scenario.json"
  exercise_update_case unwritable-target 0 0 1
  exercise_update_case replacement-rollback 0 0 1
fi

printf '\n  ],"platform":"%s/%s","candidateSha256":"%s"}\n' "$host_os" "$host_arch" "$(sha256_file "$downloads/$host_asset")" >> "$updater_report"
if [[ -n "${WTP_QA_REPORT:-}" ]]; then
  cp "$updater_report" "$WTP_QA_REPORT"
fi
printf 'release QA updater report: %s\n' "$updater_report"

# Successful in-place replacement uses the normal latest fixture and proves
# the repository storage remains untouched by replacing the running binary.
rm -f "$fixture_dir/scenario.json"
update_project="$work_dir/project-project"
before_manifest="$work_dir/before.wtp"
after_manifest="$work_dir/after.wtp"
manifest "$update_project" > "$before_manifest"
(
  cd "$update_project"
  run_wtp "$project_bin" update | grep -Fq "updated wtp from $initial_version to $upgrade_version" || fail "wtp update did not report an in-place upgrade"
  run_wtp "$project_bin" version | grep -Fq "wtp $upgrade_version" || fail "updated executable did not start the upgrade release"
  run_wtp "$project_bin" task list >/dev/null
)
manifest "$update_project" > "$after_manifest"
cmp --silent "$before_manifest" "$after_manifest" || fail "wtp update changed .wtp storage"

# Verify the Windows updater and CLI test sources compile for both shipped
# Windows targets. PowerShell also validates every Windows release asset; on a
# Windows host it runs the corresponding executable workflow.
GOOS=windows GOARCH=amd64 go test -exec /bin/true ./... -run '^$'
GOOS=windows GOARCH=arm64 go test -exec /bin/true ./... -run '^$'
pwsh -NoProfile -File "$repo_root/scripts/release_qa.ps1" -AssetDirectory "$downloads" -ExpectedVersion "$initial_version"

printf 'release QA passed: six direct-download assets, three install scopes, workflow, and in-place update\n'
