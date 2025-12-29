#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p artifacts

OUT="artifacts/phase5_6_divergence_samples_smoke.txt"
{
  echo "Phase 5.6 Divergence Reject Samples Smoke"
  echo "ts_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  go test ./internal/riskcontrol -run TestPhase56DivergenceSamplesSmoke -v
  echo
  echo "=== samples.json head ==="
  head -n 60 artifacts/phase5_6_divergence_reject_samples.json || true
  echo
  echo "=== samples.txt head ==="
  head -n 60 artifacts/phase5_6_divergence_reject_samples.txt || true
} | tee "$OUT"

echo
echo "wrote $OUT"

