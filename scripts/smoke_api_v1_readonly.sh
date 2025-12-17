#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 2; }; }
require curl
require jq

API_BASE="${API_BASE:-http://127.0.0.1:8081}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"

if [[ -z "$ADMIN_TOKEN" ]]; then
  echo "missing required env: ADMIN_TOKEN" >&2
  exit 2
fi

auth_hdr=(-H "Authorization: Bearer ${ADMIN_TOKEN}")

echo "[smoke-readonly] GET /api/v1/health"
curl -sfS "${auth_hdr[@]}" "${API_BASE}/api/v1/health" | jq -e '.bot and .risk and .safety' >/dev/null

echo "[smoke-readonly] GET /api/v1/pools"
pools_json="$(curl -sfS "${auth_hdr[@]}" "${API_BASE}/api/v1/pools")"
pool_id="$(echo "$pools_json" | jq -r '.pools[0].pool_id // empty')"
if [[ -z "$pool_id" ]]; then
  echo "no pools returned by /api/v1/pools" >&2
  exit 2
fi

echo "[smoke-readonly] GET /api/v1/pools/${pool_id}/state (wait until ready)"
deadline=$((SECONDS + 60))
while true; do
  if curl -sfS "${auth_hdr[@]}" "${API_BASE}/api/v1/pools/${pool_id}/state" | jq -e '.pool_id and .dex and .cex and .position and .strategy' >/dev/null; then
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "timeout waiting for pool state for ${pool_id}" >&2
    exit 2
  fi
  sleep 2
done

echo "[smoke-readonly] GET /api/v1/intents"
curl -sfS "${auth_hdr[@]}" "${API_BASE}/api/v1/intents?limit=5" | jq -e '.intents' >/dev/null

echo "[smoke-readonly] GET /api/v1/tx"
curl -sfS "${auth_hdr[@]}" "${API_BASE}/api/v1/tx?limit=5" | jq -e '.tx' >/dev/null

echo "[smoke-readonly] GET /api/v1/audit"
curl -sfS "${auth_hdr[@]}" "${API_BASE}/api/v1/audit?limit=5" | jq -e '.actions' >/dev/null

echo "[smoke-readonly] SSE /api/v1/stream (wait for ping)"
set +o pipefail
timeout 20s curl -sfS -N "${auth_hdr[@]}" "${API_BASE}/api/v1/stream" 2>/dev/null \
  | grep -m 1 -E '^: ping ' >/dev/null
set -o pipefail

echo "[smoke-readonly] OK"
