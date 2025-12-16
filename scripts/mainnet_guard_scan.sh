#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[mainnet-guard-scan] scanning for Arbitrum One RPC usage in scripts"

pattern='https://arb1\.arbitrum\.io/rpc'
guard='PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE'

fail=0

list_files() {
  # Scan only files that would be committed:
  # - tracked files
  # - untracked but not ignored (exclude-standard honors .gitignore)
  git ls-files -z --cached --others --exclude-standard
}

while IFS= read -r -d '' f; do
  case "$f" in
    *.sh|*.bash|*.zsh|*.js|*.ts|*.tsx)
      ;;
    *)
      continue
      ;;
  esac
  if rg -n -S "$pattern" "$f" >/dev/null 2>&1; then
    if ! rg -n -S "$guard" "$f" >/dev/null 2>&1; then
      echo "[mainnet-guard-scan] missing guard in $f (uses Arbitrum One RPC)"
      fail=1
    fi
  fi
done < <(list_files)

if [[ "$fail" -ne 0 ]]; then
  echo "[mainnet-guard-scan] FAILED"
  exit 2
fi

echo "[mainnet-guard-scan] OK"

