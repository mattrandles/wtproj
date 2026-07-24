#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="${1:-commit}"

usage() {
  cat <<'EOF'
Usage: ./scripts/verify.sh [commit|release]

commit   Run the pre-commit gate (the default).
release  Run the commit gate, GoReleaser checks, and release QA.
EOF
}

fail() {
  printf 'verification failed: %s\n' "$1" >&2
  exit 1
}

case "$mode" in
  commit|release) ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    fail "unknown mode '$mode' (expected commit or release)"
    ;;
esac

cd "$repo_root"

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$2 requires '$1'; install it and retry"
}

require_command go "commit verification"
require_command git "commit verification"

unformatted="$(gofmt -l ./cmd ./internal)"
if [[ -n "$unformatted" ]]; then
  printf 'gofmt needed for:\n%s\n' "$unformatted" >&2
  exit 1
fi

go test ./...
go vet ./...
./scripts/e2e_smoke.sh

if [[ "$mode" == "commit" ]]; then
  printf 'commit verification passed\n'
  exit 0
fi

for tool in goreleaser curl pwsh file; do
  require_command "$tool" "release verification"
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  fail "release verification requires 'sha256sum' or 'shasum'; install one and retry"
fi

work_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

config="$work_dir/goreleaser.yaml"
dist="$work_dir/dist"
cp "$repo_root/.goreleaser.yaml" "$config"
printf '\ndist: %s\n' "$dist" >> "$config"

goreleaser check --config "$config"
goreleaser release --snapshot --clean --skip=publish --config "$config"
WTP_RELEASE_DIST="$dist" go test ./internal/releaseasset -run '^TestGoReleaserSnapshotMatchesPlatformContract$' -count=1
WTP_QA_GLOBAL_DIR= ./scripts/release_qa.sh

printf 'release verification passed\n'
