#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts

OUT="artifacts/phase5_2_risk_cooldown_smoke.txt"
{
  echo "Phase 5.2 Risk Cooldown Smoke"
  echo "ts_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  go test ./internal/riskcontrol -run TestPhase52CooldownSmoke -v
} | tee "$OUT"

echo
echo "wrote $OUT"

