# PR Split Guide (Pathspec Recipes)

This repo is intended to land changes as **small, green PRs**. Use pathspec-based staging to keep PR scope tight.

## Phase 0 (CI + Guards + Runbooks)

Stage:

- `git add Makefile .github/ scripts/ docs/runbook/ docs/security/ docs/architecture/`

Verify:

- `make ci`

## Phase 1 (Boundary Refactors)

Stage:

- `git add bot/ cmd/bot/ internal/config/ internal/api/ internal/monitor/ internal/events/ internal/storage/ internal/poolguard/ internal/risk/ internal/strategy/ internal/rebalancer/ internal/engine/ internal/dexstate/ internal/chain/gateway/dryrun_gateway.go internal/chain/gateway/eth_gateway.go internal/chain/gateway/rpc_balance_reader.go internal/chain/gateway/static_balance_reader.go docs/api/`

Verify:

- `make ci`
- `make rehearsal-offline`

## Phase 2 (API Contract Alignment)

Stage:

- `git add scripts/smoke_api_v1_readonly.sh scripts/accept_control_plane_v1.sh`

Verify:

- `go test ./internal/api`
- `make ci`

## Phase 3 (Dry-run + Testnet)

Stage:

- `git add internal/config/ internal/chain/ cmd/txprobe/ scripts/ docs/runbook/ docs/security/`

Verify:

- `make rehearsal-offline`
- `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make rehearsal-testnet-dryrun`

## Phase 4 (Web Minimal)

Stage:

- `git add web/ docs/web/`

Verify:

- `npm -C web ci`
- `npm -C web run build`

## Hygiene-only PR (Recommended)

If the repo historically tracked large artifacts, land a dedicated PR that only removes them:

- `git add .gitignore scripts/repo_hygiene_scan.sh Makefile`
- `git rm -r --cached web/node_modules`
- `git rm --cached phoenix.db`
- `git rm --cached bot`

Verify:

- `make ci`

## Patch Export (No-Commit, Optional)

If you want “small PRs” without manually staging hunks, generate phase patches from the current working tree:

- `scripts/export_pr_patches.sh /tmp/phoenix_pr_patches`

This writes:
- `/tmp/phoenix_pr_patches/hygiene.patch` (recommended first)
- `/tmp/phoenix_pr_patches/phase0.patch`
- `/tmp/phoenix_pr_patches/phase1.patch`
- `/tmp/phoenix_pr_patches/phase2.patch`
- `/tmp/phoenix_pr_patches/phase3.patch`
- `/tmp/phoenix_pr_patches/phase4.patch`

Apply per PR (example for phase0):

- `git checkout -b pr-phase0 origin/dev`
- `git apply --3way /tmp/phoenix_pr_patches/phase0.patch`
- `git add -A`
- `make ci`
