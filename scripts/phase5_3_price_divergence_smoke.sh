#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts

OUT="artifacts/phase5_3_price_divergence_smoke.txt"
{
  echo "Phase 5.3 Price Divergence Smoke"
  echo "ts_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  go test ./internal/riskcontrol -run TestPhase53PriceDivergenceSmoke -v
} | tee "$OUT"

echo
echo "wrote $OUT"

