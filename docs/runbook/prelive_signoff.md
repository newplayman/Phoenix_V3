# Pre-Live Signoff (Arbitrum Sepolia Only)

This is a **human-run** checklist for the final “ready to test live” verification.

## 1) Build + Guards (must pass)

- [ ] `make ci`
- [ ] Optional wrapper (runs all steps except real broadcast unless explicitly unlocked): `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make prelive-signoff`

## 2) Offline Acceptance (no chain calls)

- [ ] `make rehearsal-offline`

## 3) Testnet Dry-Run Read-Only (no broadcasts)

- [ ] `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make rehearsal-testnet-dryrun`
- [ ] `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> go test ./... -tags=integration -run TestArbitrumSepoliaRPCChainID`

## 4) Broadcast Probe (gas-only, tx to self, **manual**)

Precondition (funds for gas):

- [ ] Print wallet address from local key file (no broadcast): `BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" make wallet-addr`
- [ ] Check native balance: `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> ADDR=0x... make native-balance`
  - Note: Sepolia ETH on L1 must be bridged to **Arbitrum Sepolia** to pay L2 gas.

Sanity (no key; must stay simulated unless explicitly unlocked):

- [ ] `ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe`
  - Expected: prints `effective_dry_run=true`

Actual broadcast (requires explicit unlock + confirmation; prompts for key):

- [ ] `TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe-interactive`
  - Note: this requires an interactive TTY; in non-interactive shells use `SECRETS_FILE=... make broadcast-probe` with explicit unlock instead.
  - Tip: create a local-only key file (no shell history) with `scripts/setup_bot_private_key_file.sh "$HOME/.config/phoenix/bot_private_key.txt"` and use `BOT_PRIVATE_KEY_FILE=...` instead of `BOT_PRIVATE_KEY=...`.
  - Alternative (non-interactive, avoid env key): `BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe`
  - Optional helper (non-interactive, records only if actually sent): `BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe-record`
  - Optional helper (interactive TTY, records only if actually sent): `TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make broadcast-probe-interactive-record`
  - Optional verify receipt (post-broadcast): `TX_HASH=0x... ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make tx-verify`
  - Optional wait until mined (recommended): `TX_HASH=0x... ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make tx-wait`
  - Record:
    - From address:
    - Tx hash:
    - Explorer link:
  - Optional helper (writes record into signoff doc):
    - `PROBE_LINE='status=sent ...' make signoff-record-probe`
  - End-to-end wrapper (requires `SECRETS_FILE` or `BOT_PRIVATE_KEY` and explicit unlock):
    - `SECRETS_FILE="$HOME/.config/phoenix/secrets.sh" TXPROBE_DRY_RUN=false TXPROBE_KILL_SWITCH=false TXPROBE_ALLOW_BROADCAST=true TXPROBE_CONFIRM=I_UNDERSTAND_GAS_COSTS ARBITRUM_SEPOLIA_RPC_URL=<your-rpc> make prelive-signoff`

## 5) Safety Statement (must be true)

- [ ] Default bot config keeps `effective_dry_run=true` unless explicitly unlocked.
- [ ] No secrets committed (private keys, tokens, DB creds).
- [ ] Mainnet (42161) blocked unless `PHOENIX_UNSAFE_ALLOW_ARBITRUM_ONE=1`.

## Broadcast Probe Record

- timestamp_utc: 2025-12-16T16:37:28Z
  - from: 0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217
  - tx_hash: 0x0c5a1d487307e243815ea9a61267e949d349df72235f87688ed5ff6e0aa17849
  - explorer: https://sepolia.arbiscan.io/tx/0x0c5a1d487307e243815ea9a61267e949d349df72235f87688ed5ff6e0aa17849
- timestamp_utc: 2025-12-16T17:47:38Z
  - from: 0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217
  - tx_hash: 0x5a28452cf7b43e9d5c34f11065b026cd7d231445ad14d8f8b261a637bd1c046b
  - explorer: https://sepolia.arbiscan.io/tx/0x5a28452cf7b43e9d5c34f11065b026cd7d231445ad14d8f8b261a637bd1c046b
- timestamp_utc: 2025-12-17T10:32:25Z
  - from: 0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217
  - tx_hash: 0x2762708856548caf010f6e72ff7b3277128dff0887816d488cf128ae9ddcca29
  - explorer: https://sepolia.arbiscan.io/tx/0x2762708856548caf010f6e72ff7b3277128dff0887816d488cf128ae9ddcca29
- timestamp_utc: 2025-12-17T14:03:17Z
  - from: 0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217
  - tx_hash: 0x74ac905524995bd4296b90ba92d27f720bb721566676baf1991345de941e75d1
  - explorer: https://sepolia.arbiscan.io/tx/0x74ac905524995bd4296b90ba92d27f720bb721566676baf1991345de941e75d1
