.PHONY: fmt fmt-check vet test secret-scan boundary-scan mainnet-guard-scan repo-hygiene-scan web-ci web-build ci validate-arb-sepolia rehearsal-arb-sepolia rehearsal-offline rehearsal-testnet-dryrun rehearsal-testnet-live-read rehearsal-testnet-mock-lp rehearsal-testnet-real-univ3-dryrun check-contracts broadcast-probe broadcast-probe-interactive broadcast-probe-record broadcast-probe-interactive-record wallet-addr native-balance tx-verify tx-wait signoff-record-probe prelive-signoff pr-split-help

GO ?= go
NPM ?= npm

fmt:
	gofmt -w $$(git ls-files '*.go')

fmt-check:
	./scripts/gofmt_check.sh

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

secret-scan:
	./scripts/secret_scan.sh

boundary-scan:
	./scripts/boundary_scan.sh

mainnet-guard-scan:
	./scripts/mainnet_guard_scan.sh

repo-hygiene-scan:
	./scripts/repo_hygiene_scan.sh

web-ci:
	$(NPM) -C web ci

web-build: web-ci
	$(NPM) -C web run build

ci: fmt-check vet test secret-scan mainnet-guard-scan repo-hygiene-scan boundary-scan web-build

validate-arb-sepolia:
	./scripts/validate_arbitrum_sepolia_template.sh

validate-arb-sepolia-onchain:
	VALIDATE_ONCHAIN_CODE=1 ./scripts/validate_arbitrum_sepolia_template.sh

rehearsal-arb-sepolia:
	./scripts/rehearsal_arbitrum_sepolia.sh

rehearsal-offline:
	./scripts/rehearsal_arbitrum_sepolia_offline.sh

rehearsal-testnet-dryrun:
	./scripts/rehearsal_arbitrum_sepolia_dryrun_testnet.sh

rehearsal-testnet-live-read:
	./scripts/rehearsal_arbitrum_sepolia_live_readonly.sh

rehearsal-testnet-mock-lp:
	./scripts/rehearsal_arbitrum_sepolia_mock_lp_e2e.sh

rehearsal-testnet-real-univ3-dryrun:
	./scripts/rehearsal_arbitrum_sepolia_real_univ3_dryrun.sh

check-contracts:
	./scripts/check_contract_code.sh $(ADDRS)

broadcast-probe:
	./scripts/broadcast_probe_arbitrum_sepolia.sh

broadcast-probe-interactive:
	./scripts/broadcast_probe_arbitrum_sepolia_interactive.sh

broadcast-probe-record:
	./scripts/broadcast_probe_record.sh

broadcast-probe-interactive-record:
	./scripts/broadcast_probe_and_record.sh

wallet-addr:
	$(GO) run ./cmd/walletaddr

native-balance:
	$(GO) run ./cmd/nativebalance -address $(ADDR)

tx-verify:
	./scripts/txverify_arbitrum_sepolia.sh $(TX_HASH)

tx-wait:
	./scripts/wait_tx_mined_arbitrum_sepolia.sh $(TX_HASH)

signoff-record-probe:
	./scripts/record_signoff_broadcast_probe.sh

prelive-signoff:
	./scripts/prelive_signoff.sh

pr-split-help:
	./scripts/pr_split_help.sh
