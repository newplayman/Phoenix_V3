#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[secret-scan] scanning for likely secrets"

fail=0

list_files() {
  # Scan only files that would be committed:
  # - tracked files
  # - untracked but not ignored (exclude-standard honors .gitignore)
  git ls-files -z --cached --others --exclude-standard
}

scan() {
  local name="$1"
  local pattern="$2"
  if list_files | xargs -0 rg -n -S "$pattern" >/dev/null 2>&1; then
    echo "[secret-scan] match: $name"
    list_files | xargs -0 rg -n -S "$pattern" || true
    fail=1
  fi
}

# Key material / mnemonics (focus on assignments to avoid tx-hash false positives).
scan "private key assignment" '(?i)\b(BOT_PRIVATE_KEY|PRIVATE_KEY|WALLET_PRIVATE_KEY)\s*=\s*"?0x[0-9a-fA-F]{64}"?'
scan "mnemonic assignment" '(?i)\b(MNEMONIC|SEED_PHRASE)\s*=\s*".{16,}"'

# Cloud/API tokens in-line.
scan "infura project id in URL" 'infura\.io/v3/[0-9a-fA-F]{32}'
scan "supabase anon/service key assignment" '(?i)\b(supabase_anon_key|supabase_service_role_key)\b\s*[:=]\s*["'\'']?[A-Za-z0-9._-]{20,}["'\'']?'

if [[ "$fail" -ne 0 ]]; then
  echo "[secret-scan] FAILED"
  exit 2
fi

echo "[secret-scan] OK"
