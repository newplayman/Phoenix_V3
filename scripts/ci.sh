#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[ci] go fmt"
./scripts/gofmt_check.sh

echo "[ci] go vet"
go vet ./...

echo "[ci] go test"
go test ./...

echo "[ci] secret scan"
./scripts/secret_scan.sh

echo "[ci] boundary scan"
./scripts/boundary_scan.sh

echo "[ci] web build"
npm -C web ci
npm -C web run build
