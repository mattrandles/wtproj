#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'Usage: %s --dist DIST --out DIRECTORY\n' "$0"
}

dist=""
out=""
while (($# > 0)); do
  case "$1" in
    --dist) dist="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
[[ -n "$dist" && -n "$out" ]] || { usage >&2; exit 2; }
[[ -d "$dist" ]] || { printf 'candidate collection: missing dist %s\n' "$dist" >&2; exit 1; }
mkdir -p "$out"

assets=(
  wtp_darwin_amd64 wtp_darwin_arm64 wtp_linux_amd64 wtp_linux_arm64
  wtp_windows_amd64.exe wtp_windows_arm64.exe
)

find_asset() {
  local name="$1"
  local direct="$dist/$name"
  if [[ -f "$direct" ]]; then
    printf '%s\n' "$direct"
    return
  fi
  local archive="$name"
  local binary="wtp"
  if [[ "$name" == *.exe ]]; then
    archive="${name%.exe}"
    binary="wtp.exe"
  fi
  mapfile -t matches < <(find "$dist" -type f -path "*/${archive}_*/${binary}" -print)
  [[ "${#matches[@]}" -eq 1 ]] || { printf 'candidate collection: expected one %s, found %s\n' "$name" "${#matches[@]}" >&2; exit 1; }
  printf '%s\n' "${matches[0]}"
}

for asset in "${assets[@]}"; do
  cp "$(find_asset "$asset")" "$out/$asset"
done
cp "$(find_asset checksums.txt)" "$out/checksums.txt"
printf 'candidate collection: six exact assets copied to %s\n' "$out"
