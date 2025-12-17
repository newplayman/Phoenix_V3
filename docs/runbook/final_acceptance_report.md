# Final Acceptance Report (Arbitrum Sepolia, Read-Only by Default)

This report captures a **reproducible pre-live verification** snapshot for `dev`.

## Scope / Safety

- Target network: **Arbitrum Sepolia** (`chainId=421614`)
- Defaults remain safe:
  - `DRY_RUN=true`
  - `KILL_SWITCH=true`
  - `allow_tx_broadcast=false`
- Any real broadcast requires explicit unlock + confirmation (see `docs/runbook/prelive_signoff.md:1`).

## Source Revision

- `origin/dev` merged: `b674543` (Merge pull request #14: `pr-20251217-stack-phase4`)

## Build + Unit Tests

Command:

```bash
make ci
```

Expected:
- gofmt/vet/test pass
- secret scan / mainnet guard / boundary scan pass
- web build passes (`npm -C web ci && npm -C web run build`)

## Market Data Replay (Step 2-2)

Command:

```bash
make rehearsal-orderbook-120s
```

Observed (example run):
- runner: `snapshots=2 deltas=1188 resyncs=1 stale_deltas=2`
- replay types: `types=map[ORDERBOOK_DELTA:1188 ORDERBOOK_SNAPSHOT:2] gaps=1`
- final top-of-book printed as `best_bid/best_ask/spread`

Contract:
- `docs/marketdata/ORDERBOOK_RAW_SPEC.md:1`

## Testnet Read-Only Rehearsal

Command:

```bash
ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/<your-project>' make prelive-signoff
```

Notes:
- This wrapper runs `make ci`, offline rehearsal, testnet dry-run rehearsal, chainId integration test.
- Broadcast probe remains **simulated** unless explicitly unlocked.

## Live Pool State Integration (Optional)

Command:

```bash
ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/<your-project>' \
ARBITRUM_SEPOLIA_POOL_ADDRESS='0x53448a5c2c61da7A797F25cEd6d11BE838E674fb' \
go test ./internal/dexstate -tags=integration -run TestArbitrumSepoliaPoolState_Slot0AndLiquidity
```

## Real Broadcast Probe (Optional, Gas-Only)

Evidence is recorded in:
- `docs/runbook/prelive_signoff.md:1`

Example recorded tx (gas-only self-transfer on Arbitrum Sepolia):
- `0x74ac905524995bd4296b90ba92d27f720bb721566676baf1991345de941e75d1`

## Real UniV3 Dry-Run (Optional)

Command (example pool TRL/USDT):

```bash
ARBITRUM_SEPOLIA_RPC_URL='https://arbitrum-sepolia.infura.io/v3/<your-project>' \
POOL_ID='arbsepolia-real-univ3-trl-usdt' \
POOL_ADDRESS='0x53448a5c2c61da7A797f25cEd6D11BE838E674Fb' \
POSITION_MANAGER_ADDRESS='0x6b2937Bde17889EDCf8fbD8dE31C3C2a70Bc4d65' \
TOKEN0_ADDRESS='0x1b46aA4C362788E3b2557CE465487d9E41742Fd9' \
TOKEN1_ADDRESS='0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E' \
TOKEN0_DECIMALS=9 TOKEN1_DECIMALS=6 POOL_FEE=3000 \
STABLE_TOKEN_ADDRESS='0xF8BFc8301BfcC32862BdaC962a8C34c7ED13E51E' \
CEX_PRICE_TOKEN_ADDRESS='0x1b46aA4C362788E3b2557CE465487d9E41742Fd9' \
BOT_WALLET_ADDRESS='0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217' \
bash scripts/rehearsal_arbitrum_sepolia_real_univ3_dryrun.sh
```

Notes:
- Dry-run only; does not broadcast swaps/mints unless separately unlocked.
- See `docs/runbook/real_univ3_e2e_testnet.md:1` for discovery + mint tooling.

