#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

unformatted="$(gofmt -l ./cmd ./internal)"
if [ -n "$unformatted" ]; then
  printf 'gofmt needed for:\n%s\n' "$unformatted" >&2
  exit 1
fi

go test ./...
./scripts/e2e_smoke.sh
