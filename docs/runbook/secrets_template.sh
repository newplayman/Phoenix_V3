#!/usr/bin/env bash
set -euo pipefail

# Secrets template (DO NOT COMMIT REAL VALUES).
# Recommended location: "$HOME/.config/phoenix/secrets.sh" with permissions 0600.

export ADMIN_TOKEN="replace-me"

# Optional: used for on-chain tx broadcast (testnet only). Never print or commit.
export BOT_PRIVATE_KEY="0xreplace-me"

# Alternative: store the key in a local file and point to it (recommended for non-interactive shells).
# export BOT_PRIVATE_KEY_FILE="$HOME/.config/phoenix/bot_private_key.txt"

# Optional: read-only balance reads for preview without a private key.
export BOT_WALLET_ADDRESS="0xreplace-me"

# Testnet RPC endpoints should be provided via env (not committed in configs).
export ARBITRUM_SEPOLIA_RPC_URL="https://arbitrum-sepolia.infura.io/v3/<project-id>"
