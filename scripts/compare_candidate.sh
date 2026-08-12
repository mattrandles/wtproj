#!/usr/bin/env bash
set -euo pipefail

candidate_dir="${1:-}"
version="${2:-}"
[[ -n "$candidate_dir" && -n "$version" ]] || { printf 'Usage: %s CANDIDATE_DIRECTORY VERSION\n' "$0" >&2; exit 2; }
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
build_date="$(jq -r '.buildDate // empty' "$candidate_dir/candidate-manifest.json")"
[[ -n "$build_date" ]] || { printf 'candidate comparison: manifest lacks buildDate\n' >&2; exit 1; }

cp "$repo_root/.goreleaser.yaml" "$work_dir/goreleaser.yaml"
printf '\ndist: %s\n' "$work_dir/dist" >> "$work_dir/goreleaser.yaml"
WTP_QA_SNAPSHOT_VERSION="$version" WTP_QA_SNAPSHOT_BUILD_DATE="$build_date" goreleaser release --snapshot --clean --skip=publish --config "$work_dir/goreleaser.yaml"
"$repo_root/scripts/collect_candidate.sh" --dist "$work_dir/dist" --out "$work_dir/candidate"
for asset in wtp_darwin_amd64 wtp_darwin_arm64 wtp_linux_amd64 wtp_linux_arm64 wtp_windows_amd64.exe wtp_windows_arm64.exe checksums.txt; do
  cmp --silent "$candidate_dir/$asset" "$work_dir/candidate/$asset" || {
    printf 'candidate reproducibility mismatch: %s\n' "$asset" >&2
    exit 1
  }
done
printf 'candidate reproducibility comparison passed: %s (%s)\n' "$version" "$build_date"
