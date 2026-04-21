#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

install_dir="${WTP_INSTALL_DIR:-$HOME/.local/bin}"
binary_name="${WTP_INSTALL_NAME:-wtp}"
target_path="$install_dir/$binary_name"

mkdir -p "$install_dir"
tmp_path="$(mktemp "$install_dir/.${binary_name}.tmp.XXXXXX")"
trap 'rm -f "$tmp_path"' EXIT

go build -trimpath -o "$tmp_path" ./cmd/wtp
chmod 755 "$tmp_path"
mv "$tmp_path" "$target_path"

printf 'installed %s\n' "$target_path"
