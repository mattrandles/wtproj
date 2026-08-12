#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="${1:-commit}"

usage() {
  cat <<'EOF'
Usage: ./scripts/verify.sh [commit|release|prerelease] [options]

commit   Run the pre-commit gate (the default).
release  Run the commit gate, GoReleaser checks, and release QA.
prerelease Run the hermetic black-box workflow matrix. Options are passed to
          the Go runner; --candidate is optional and defaults to a fresh build.
EOF
}

fail() {
  printf 'verification failed: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$2 requires '$1'; install it and retry"
}

case "$mode" in
  prerelease)
    shift
    require_command go "prerelease verification"
    require_command git "prerelease verification"
    prerelease_work_dir="$(mktemp -d)"
    preserve_prerelease_work_dir=0
    prerelease_cleanup() {
      if [[ "$preserve_prerelease_work_dir" != 1 ]]; then
        rm -rf "$prerelease_work_dir"
      else
        printf 'prerelease artifacts retained at: %s\n' "$prerelease_work_dir" >&2
      fi
    }
    trap prerelease_cleanup EXIT
    cd "$repo_root"
    unformatted="$(gofmt -l ./cmd ./internal)"
    if [[ -n "$unformatted" ]]; then
      printf 'gofmt needed for:\n%s\n' "$unformatted" >&2
      preserve_prerelease_work_dir=1
      exit 1
    fi
    go test ./...
    go vet ./...
    candidate=""
    report=""
    args=(--source-root "$repo_root")
    while (($# > 0)); do
      case "$1" in
        --candidate) candidate="$2"; args+=(--candidate "$2"); shift 2 ;;
        --report) report="$2"; args+=(--report "$2"); shift 2 ;;
        --candidate=*) candidate="${1#*=}"; args+=(--candidate "$candidate"); shift ;;
        --report=*) report="${1#*=}"; args+=(--report "$report"); shift ;;
        *) args+=("$1"); shift ;;
      esac
    done
    if [[ -z "$candidate" ]]; then
      candidate="$prerelease_work_dir/wtp"
      go build -tags wtp_fault_injection -o "$candidate" "$repo_root/cmd/wtp"
      args+=(--candidate "$candidate")
    fi
    if [[ -z "$report" ]]; then
      report="${TMPDIR:-/tmp}/wtp-prerelease-$$.json"
      args+=(--report "$report")
    fi
    if ! go run ./cmd/wtp-prerelease-qa "${args[@]}"; then
      preserve_prerelease_work_dir=1
      exit 1
    fi
    printf 'prerelease verification passed; report: %s\n' "$report"
    exit 0
    ;;
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
