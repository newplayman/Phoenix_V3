package api

import (
	"log"
	"math/big"
	"os"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/config"
)

// NewDefaultBalanceProvider wires a preview-time balance reader for the API layer.
//
// Priority:
// 1) If a live EthGateway exists for the chain, use it.
// 2) If offline + fake balances enabled (and effective dry-run), return StaticBalanceReader.
// 3) Otherwise, use RPCBalanceReader (read-only, no private key) using BOT_WALLET_ADDRESS + chain RPC.
func NewDefaultBalanceProvider(selectGateway func(int64) gateway.Gateway, cfgProvider func() *config.AppConfig, offline bool) func(int64) BalanceReader {
	var mu sync.Mutex
	readers := map[int64]BalanceReader{}

	return func(chainID int64) BalanceReader {
		if selectGateway != nil {
			if ethGw, ok := selectGateway(chainID).(*gateway.EthGateway); ok && ethGw != nil {
				return ethGw
			}
		}

		mu.Lock()
		if br, ok := readers[chainID]; ok && br != nil {
			mu.Unlock()
			return br
		}
		mu.Unlock()

		walletAddr := common.HexToAddress(strings.TrimSpace(os.Getenv("BOT_WALLET_ADDRESS")))
		if walletAddr == (common.Address{}) {
			return nil
		}

		cfg := (*config.AppConfig)(nil)
		if cfgProvider != nil {
			cfg = cfgProvider()
		}

		// Offline-only escape hatch for acceptance rehearsals: return fixed balances so preview planning works without RPC.
		// Guardrails:
		// - Requires offline=true (no chain calls)
		// - Requires effective_dry_run=true (no broadcasting)
		if strings.TrimSpace(os.Getenv("PHOENIX_PREVIEW_FAKE_BALANCES")) == "1" && offline && config.SafetyFromConfig(cfg).EffectiveDryRun {
			poolCfg := (*config.PoolConfig)(nil)
			if cfg != nil {
				for i := range cfg.Pools {
					if cfg.Pools[i].ChainID == chainID {
						poolCfg = &cfg.Pools[i]
						break
					}
				}
				if poolCfg == nil && len(cfg.Pools) > 0 {
					poolCfg = &cfg.Pools[0]
				}
			}
			if poolCfg != nil {
				pow10 := func(dec int) *big.Int {
					if dec < 0 {
						dec = 0
					}
					return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
				}
				bals := map[common.Address]*big.Int{}
				t0 := common.HexToAddress(poolCfg.Token0)
				t1 := common.HexToAddress(poolCfg.Token1)
				if t0 != (common.Address{}) {
					// default: 1.0 token0
					bals[t0] = new(big.Int).Mul(big.NewInt(1), pow10(poolCfg.Token0Decimals))
				}
				if t1 != (common.Address{}) {
					// default: 1.0 token1
					bals[t1] = new(big.Int).Mul(big.NewInt(1), pow10(poolCfg.Token1Decimals))
				}
				for _, st := range poolCfg.StableTokens {
					a := common.HexToAddress(st)
					if a == (common.Address{}) {
						continue
					}
					dec := 18
					switch {
					case strings.EqualFold(st, poolCfg.Token0):
						dec = poolCfg.Token0Decimals
					case strings.EqualFold(st, poolCfg.Token1):
						dec = poolCfg.Token1Decimals
					}
					// 1000 stable units
					bals[a] = new(big.Int).Mul(big.NewInt(1000), pow10(dec))
				}
				br := gateway.NewStaticBalanceReader(walletAddr, bals)
				mu.Lock()
				readers[chainID] = br
				mu.Unlock()
				return br
			}
		}

		rpcURL := ""
		if cfg != nil {
			for _, ch := range cfg.Chains {
				if ch.ID == chainID {
					rpcURL = ch.RPC
					break
				}
			}
			if rpcURL == "" && len(cfg.Chains) > 0 {
				rpcURL = cfg.Chains[0].RPC
			}
		}
		if strings.TrimSpace(rpcURL) == "" {
			return nil
		}

		ro, err := gateway.NewRPCBalanceReader(rpcURL, walletAddr)
		if err != nil {
			log.Printf("[API] balance reader init failed (chain=%d): %v", chainID, err)
			return nil
		}
		mu.Lock()
		readers[chainID] = ro
		mu.Unlock()
		return ro
	}
}
