#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() { echo "[setup-keyfile] $*" >&2; exit 2; }

target="${1:-${HOME}/.config/phoenix/bot_private_key.txt}"

target_abs=""
if command -v realpath >/dev/null 2>&1; then
  target_abs="$(realpath -m "$target")"
elif command -v readlink >/dev/null 2>&1; then
  # GNU readlink supports -f; if not, fall back below.
  target_abs="$(readlink -f "$target" 2>/dev/null || true)"
fi
if [[ -z "$target_abs" ]]; then
  target_abs="$(cd "$(dirname "$target")" 2>/dev/null && pwd)/$(basename "$target")"
fi

repo_abs="$ROOT_DIR"
case "$target_abs" in
  "$repo_abs"/*) fail "refusing to write inside repo: $target_abs (choose a path under \$HOME/.config/)";;
esac

mkdir -p "$(dirname "$target_abs")"

if [[ -f "$target_abs" ]]; then
  fail "refusing to overwrite existing file: $target_abs (delete it manually if you intend to rotate)"
fi

if [[ ! -t 0 ]]; then
  fail "no TTY available for hidden key prompt; run in an interactive terminal"
fi

umask 077
echo "[setup-keyfile] enter testnet private key hex (hidden input, optional 0x prefix):" >&2
read -r -s key
echo >&2

key="$(echo "$key" | tr -d '[:space:]')"
key="${key#0x}"

if [[ ! "$key" =~ ^[0-9a-fA-F]{64}$ ]]; then
  fail "invalid key format (expected 64 hex chars)"
fi

printf '%s\n' "$key" >"$target_abs"
chmod 600 "$target_abs"

echo "[setup-keyfile] wrote $target_abs (chmod 600)"
