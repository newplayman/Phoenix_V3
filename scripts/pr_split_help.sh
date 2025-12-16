#!/usr/bin/env bash
set -euo pipefail

cat <<'EOF'
Phoenix_V3 PR Split Helper (no-op)

Goal: land changes as small PRs while keeping `make ci` green.

Recommended sequence:

1) Hygiene-only PR (remove tracked artifacts)
   git restore --staged .
   git add .gitignore Makefile scripts/repo_hygiene_scan.sh
   git rm -r --cached web/node_modules
   git rm --cached phoenix.db
   git rm --cached bot
   make ci

2) Phase 0 PR (CI + guards + runbooks)
   git restore --staged .
   git add .github/ Makefile scripts/ docs/runbook/ docs/security/ docs/architecture/
   make ci

3) Phase 1 PR (boundary refactors)
   git restore --staged .
   git add bot/ cmd/bot/ docs/architecture/
   make ci
   make rehearsal-offline

4) Phase 2 PR (API contract alignment)
   git restore --staged .
   git add docs/api/ internal/api/
   make ci

5) Phase 3 PR (dry-run + testnet)
   git restore --staged .
   git add internal/config/ internal/chain/ cmd/txprobe/ scripts/ docs/runbook/ docs/security/ configs/config_arbitrum_sepolia.template.yaml
   make ci
   ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make rehearsal-testnet-dryrun

6) Phase 4 PR (web minimal)
   git restore --staged .
   git add web/ docs/web/
   make ci

Final operator signoff (manual):
   ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make prelive-signoff

EOF
