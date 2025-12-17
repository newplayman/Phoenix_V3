#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v python3 >/dev/null 2>&1; then
  echo "[py-check] python3 not found"
  exit 2
fi

files="$(rg --files -g'*.py' scripts | tr '\n' ' ')"
if [[ -z "${files// }" ]]; then
  echo "[py-check] no python files"
  exit 0
fi

python3 -m py_compile $files
echo "[py-check] OK"
