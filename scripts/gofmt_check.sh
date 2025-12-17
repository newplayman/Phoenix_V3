#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require gofmt
require git

base_ref="${GOFMT_BASE_REF:-}"
if [[ -z "$base_ref" && -n "${GITHUB_BASE_REF:-}" ]]; then
  base_ref="origin/${GITHUB_BASE_REF}"
fi
if [[ -z "$base_ref" ]]; then
  if git rev-parse --verify origin/dev >/dev/null 2>&1; then
    base_ref="origin/dev"
  fi
fi

files=()
if [[ -n "$base_ref" ]] && git rev-parse --verify "$base_ref" >/dev/null 2>&1; then
  while IFS= read -r f; do
    files+=("$f")
  done < <(git diff --name-only --diff-filter=ACMRTUXB "$base_ref" -- '*.go')
else
  while IFS= read -r f; do
    files+=("$f")
  done < <(git ls-files '*.go')
fi

if [[ "${#files[@]}" -eq 0 ]]; then
  echo "[gofmt-check] OK"
  exit 0
fi

unformatted="$(gofmt -l "${files[@]}")"
if [[ -n "$unformatted" ]]; then
  echo "[gofmt-check] FAILED: run gofmt on:"
  echo "$unformatted"
  exit 2
fi

echo "[gofmt-check] OK"
