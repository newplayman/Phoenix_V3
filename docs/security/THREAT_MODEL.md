# Threat Model (Minimum)

## Scope

- Web console is a consumer of backend APIs.
- Backend may read chain state and (optionally) broadcast transactions.
- Testnet only by default; any mainnet-capable behavior must be explicitly unlocked.

## Non-negotiables

- Web must not hold private keys or talk to RPC directly.
- Control endpoints are disabled by default and require auth + audit + explicit confirmation.
- Transaction broadcast is disabled unless all of the following are true:
  - `strategy.dry_run: false`
  - `safety.kill_switch: false`
  - `safety.allow_tx_broadcast: true`
- Any execution-like action (e.g. cleanup/close-position) must be blocked when `effective_dry_run=true` (defense-in-depth).
- Arbitrum One (mainnet, chainId=42161) is blocked by default at runtime and requires explicit override:
  - `PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1`
- CI guard: any repo script that hardcodes Arbitrum One RPC must include the same explicit gate (`scripts/mainnet_guard_scan.sh`).

## Secrets policy

- Never commit private keys, bearer tokens, or DB credentials to git.
- Do not log private keys or raw secrets.
- Prefer env vars and `os.ExpandEnv()` in config templates for non-secret endpoints.

## Test-mode allowances (guarded)

- Offline rehearsals may enable a fake balance reader for preview planning:
  - `PHOENIX_PREVIEW_FAKE_BALANCES=1`
  - Only active when `-offline` and `effective_dry_run=true` (no chain calls, no broadcasting).
