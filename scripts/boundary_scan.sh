#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require rg

fail=0

banner() { printf "\n[boundary-scan] %s\n" "$1"; }

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  banner "$label"
  for target in "$@"; do
    if [[ ! -e "$target" ]]; then
      echo "[boundary-scan] OK (skip missing: $target)"
      return 0
    fi
  done
  if rg -n --hidden --glob '!.git' --glob '!web/node_modules/**' --glob '!web/dist/**' --glob '!*package-lock.json' "$pattern" "$@" >/dev/null; then
    echo "[boundary-scan] FAIL: matched pattern: $pattern" >&2
    rg -n --hidden --glob '!.git' --glob '!web/node_modules/**' --glob '!web/dist/**' --glob '!*package-lock.json' "$pattern" "$@" | head -n 50 >&2
    fail=1
  else
    echo "[boundary-scan] OK"
  fi
}

# internal/* must not depend on cmd/* or bot/* (keeps core unaware of entrypoints/orchestration).
check_no_match "internal must not import cmd" '^\\s*"phoenix-v3/cmd/' internal
check_no_match "internal must not import bot" '^\\s*"phoenix-v3/bot"' internal

# bot/* may orchestrate internal, but must not depend on cmd/*.
check_no_match "bot must not import cmd" '^\\s*"phoenix-v3/cmd/' bot

# web/* must not import Go core packages.
check_no_match "web must not reference internal Go packages" 'phoenix-v3/internal/' web

# web/* must not call control-plane endpoints (read-only default).
check_no_match "web must not reference v1 control endpoints" '/api/v1/(operations/|pools/.+/(pause|resume))' web
check_no_match "web must not reference legacy control endpoints" '/api/control/' web

if [[ "$fail" -ne 0 ]]; then
  echo "[boundary-scan] FAILED" >&2
  exit 1
fi

echo "[boundary-scan] PASSED"
