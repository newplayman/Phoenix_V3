#!/usr/bin/env bash
set -euo pipefail

fail() { echo "[repo-hygiene] $*" >&2; exit 2; }

require_git() { command -v git >/dev/null 2>&1 || fail "missing git"; }
require_git

tracked_any() {
  local path="$1"
  git ls-files --error-unmatch "$path" >/dev/null 2>&1
}

tracked_prefix_any() {
  local prefix="$1"
  [[ -n "$(git ls-files "$prefix" 2>/dev/null | head -n 1)" ]]
}

# Never commit node_modules or build output.
if tracked_prefix_any "web/node_modules"; then
  fail "tracked web/node_modules detected; run: git rm -r --cached web/node_modules"
fi
if tracked_prefix_any "web/dist"; then
  fail "tracked web/dist detected; run: git rm -r --cached web/dist"
fi

# Never commit local DB/state.
if tracked_any "phoenix.db"; then
  fail "tracked phoenix.db detected; run: git rm --cached phoenix.db"
fi
if tracked_prefix_any "logs/"; then
  fail "tracked logs/ detected; add logs/ to .gitignore and untrack it"
fi

# Root-level 'bot' must be a directory (not a compiled binary artifact).
if git ls-files --stage -- bot 2>/dev/null | awk '{print $4}' | grep -Fxq "bot"; then
  fail "tracked root path 'bot' detected; expected bot/ package dir only (no root binary)"
fi

echo "[repo-hygiene] OK"
