#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts

OUT="artifacts/phase5_5_price_normalization_smoke.txt"
{
  echo "Phase 5.5 Price Normalization Smoke"
  echo "ts_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  go test ./internal/riskcontrol -run TestPhase55PriceNormalizationSmoke -v
} | tee "$OUT"

echo
echo "wrote $OUT"

