#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

touch var/contract_v1.jsonl 2>/dev/null || true

tail -n 200 var/contract_v1.jsonl | rg '"schema_version":"v1"' || true

