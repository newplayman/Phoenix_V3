package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const CurrentSchemaVersion = "2024-07-01"

type AppConfig struct {
	SchemaVersion   string           `yaml:"schema_version"`
	StrategyVersion string           `yaml:"strategy_version"`
	Events          EventConfig      `yaml:"events"`
	Chains          []ChainConfig    `yaml:"chains"`
	Pools           []PoolConfig     `yaml:"pools"`
	Strategy        StrategyConfig   `yaml:"strategy"`
	Risk            RiskConfig       `yaml:"risk"`
	Gateway         GatewayConfig    `yaml:"gateway"`
	PoolGuard       PoolGuardConfig  `yaml:"poolguard"`
	Monitoring      MonitoringConfig `yaml:"monitoring"`
	Wallet          WalletConfig     `yaml:"wallet"`
}

type EventConfig struct {
	Driver          string `yaml:"driver"`       // memory | redis
	RedisURL        string `yaml:"redis_url"`    // redis://user:pass@host:6379/0
	RedisPrefix     string `yaml:"redis_prefix"` // default: phoenix
	RedisGroup      string `yaml:"redis_group"`  // default: phoenix-consumer
	FilePath        string `yaml:"file_path"`   // for driver=file
	ReplayRetention string `yaml:"replay_retention"`
	AcksRequired    bool   `yaml:"acks_required"`
}

type ChainConfig struct {
	ID            int64  `yaml:"id"`
	Name          string `yaml:"name"`
	RPC           string `yaml:"rpc"`
	QuoterAddress string `yaml:"quoter_address"` // Optional Uniswap V3 Quoter address for slippage control
	SwapHelperAddress string `yaml:"swap_helper_address"` // Optional SwapHelper for direct pool.swap on testnets
	PrivKey       string `yaml:"-"`              // Loaded from env
}

type PoolConfig struct {
	ID              string   `yaml:"id"`
	ChainID         int64    `yaml:"chain_id"`
	Token0          string   `yaml:"token0"`
	Token1          string   `yaml:"token1"`
	// CEXPriceToken is the token address whose "stable per token" price comes from the CEX feed.
	// This avoids relying on Uniswap token ordering (token0/token1 is address-sorted).
	// If empty, defaults to token1 for backward compatibility.
	CEXPriceToken   string   `yaml:"cex_price_token"`
	Fee             int      `yaml:"fee"`
	Address         string   `yaml:"address"`
	MaxInvestment   string   `yaml:"max_investment"`
	MaxCapPct       float64  `yaml:"max_cap_pct"`
	MinWidthPct     float64  `yaml:"min_width_pct"`
	MaxWidthPct     float64  `yaml:"max_width_pct"`
	MaxDailyRebalances int   `yaml:"max_daily_rebalances"`
	PositionManager string   `yaml:"position_manager"`
	// PositionTokenID optionally pins the UniV3 NFT position tokenId for this pool.
	// Uniswap V3 NonfungiblePositionManager does NOT implement ERC721Enumerable,
	// so the bot cannot reliably discover tokenIds via tokenOfOwnerByIndex.
	// If empty, the bot may still learn it from its own mint receipts and persist it.
	PositionTokenID string `yaml:"position_token_id"`
	Amount0         string   `yaml:"amount0"`
	Amount1         string   `yaml:"amount1"`
	Token0Decimals  int      `yaml:"token0_decimals"`
	Token1Decimals  int      `yaml:"token1_decimals"`
	StableTokens    []string `yaml:"stable_tokens"`
}

type StrategyConfig struct {
	Name   string                 `yaml:"name"`
	Params map[string]interface{} `yaml:"params"`
	DryRun bool                   `yaml:"dry_run"`
	Profiles map[string]StrategyProfile `yaml:"profiles"`
	Range  StrategyRangeConfig    `yaml:"range"`
	Rebalance StrategyRebalanceConfig `yaml:"rebalance"`
}

type StrategyRangeConfig struct {
	// Volatility-driven active LP width, in percent terms (0.02 == ±2%).
	MinWidthPct float64 `yaml:"min_width_pct"`
	MaxWidthPct float64 `yaml:"max_width_pct"`
	VolK        float64 `yaml:"vol_k"`       // width ~= vol_k * sigma_daily
	VolWindow   string  `yaml:"vol_window"`  // e.g. "6h"
}

type StrategyRebalanceConfig struct {
	// MinInterval is a duration string like "30s" to enforce a cooldown after a successful rebalance.
	MinInterval string `yaml:"min_interval"`
	// KeepLiquidityPctForSwaps keeps a small residual liquidity in the existing position while executing swaps.
	// This is mainly for testnets where the bot might be the only liquidity provider (otherwise swaps would revert).
	KeepLiquidityPctForSwaps float64 `yaml:"keep_liquidity_pct_for_swaps"`
}

// StrategyProfile defines per-risk-mode strategy tuning knobs.
// Values are treated as multipliers on per-pool base parameters.
type StrategyProfile struct {
	TargetNotionalPctMultiplier float64 `yaml:"target_notional_pct_multiplier"`
	MinSpreadTicksMultiplier    float64 `yaml:"min_spread_ticks_multiplier"`
	EngineRiskFactor            float64 `yaml:"engine_risk_factor"`
	RangeWidthMultiplier        float64 `yaml:"range_width_multiplier"`
}

type RiskConfig struct {
	MaxDailyGas       float64 `yaml:"max_daily_gas"`
	MaxDrawdown       float64 `yaml:"max_drawdown"`
	ConsecutiveFails  int     `yaml:"consecutive_fails"`
	MaxUtilizationPct float64 `yaml:"max_utilization_pct"`
	MaxSwapSlippagePct float64 `yaml:"max_swap_slippage_pct"`
}

// GatewayConfig controls nonce/gas retry behavior.
type GatewayConfig struct {
	GasMultiplier   float64 `yaml:"gas_multiplier"`     // e.g. 1.1 to pay 10% premium
	MaxRetries      int     `yaml:"max_retries"`        // total send retries
	RetryBackoffMs  int     `yaml:"retry_backoff_ms"`   // base backoff in ms
	GasBumpPct      float64 `yaml:"gas_bump_pct"`       // bump gas by pct on underpriced/retry
	ApprovalMultiplier float64 `yaml:"approval_multiplier"` // approve amount * multiplier (>=1.0)
	Preflight       *bool   `yaml:"preflight"`          // estimateGas/callStatic before sending (default: true)
}

type MonitoringConfig struct {
	Port int `yaml:"port"`
}

type WalletConfig struct {
	MinIdlePct float64 `yaml:"min_idle_pct"`
}

type PoolGuardConfig struct {
	EnableRemote    bool     `yaml:"enable_remote"`
	CacheTTL        string   `yaml:"cache_ttl"` // e.g. "6h"
	GoPlusURL       string   `yaml:"goplus_url"`
	GoPlusKeyEnv    string   `yaml:"goplus_key_env"`
	HoneypotURL     string   `yaml:"honeypot_url"`
	HoneypotKeyEnv  string   `yaml:"honeypot_key_env"`
	AllowlistTokens []string `yaml:"allowlist_tokens"`
	BlacklistTokens []string `yaml:"blacklist_tokens"`
}

func LoadConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func ValidateConfig(cfg *AppConfig) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	if cfg.SchemaVersion != "" && cfg.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("schema version mismatch: expect %s got %s", CurrentSchemaVersion, cfg.SchemaVersion)
	}

	if len(cfg.Chains) == 0 {
		return errors.New("at least one chain must be configured")
	}
	for i, chain := range cfg.Chains {
		if chain.RPC == "" {
			return fmt.Errorf("chains[%d] missing rpc url", i)
		}
		if chain.ID == 0 {
			return fmt.Errorf("chains[%d] missing id", i)
		}
		if strings.TrimSpace(chain.QuoterAddress) != "" {
			if _, ok := parseAddr20(chain.QuoterAddress); !ok {
				return fmt.Errorf("chains[%d] quoter_address invalid: %s", i, chain.QuoterAddress)
			}
		}
		if strings.TrimSpace(chain.SwapHelperAddress) != "" {
			if _, ok := parseAddr20(chain.SwapHelperAddress); !ok {
				return fmt.Errorf("chains[%d] swap_helper_address invalid: %s", i, chain.SwapHelperAddress)
			}
		}
	}

		if len(cfg.Pools) == 0 {
			return errors.New("at least one pool must be configured")
		}
			for i, pool := range cfg.Pools {
				t0, ok := parseAddr20(pool.Token0)
				if !ok {
					return fmt.Errorf("pools[%d] token0 invalid address: %s", i, pool.Token0)
				}
				t1, ok := parseAddr20(pool.Token1)
				if !ok {
					return fmt.Errorf("pools[%d] token1 invalid address: %s", i, pool.Token1)
				}
				if !addrLess(t0, t1) {
					return fmt.Errorf("pools[%d] token0 must be < token1 (uniswap ordering), got token0=%s token1=%s", i, pool.Token0, pool.Token1)
				}
				if pool.Address == "" {
					return fmt.Errorf("pools[%d] address required", i)
				}
				if _, ok := parseAddr20(pool.Address); !ok {
					return fmt.Errorf("pools[%d] address invalid: %s", i, pool.Address)
				}
				if pool.PositionManager == "" {
					return fmt.Errorf("pools[%d] position_manager required", i)
				}
				if _, ok := parseAddr20(pool.PositionManager); !ok {
					return fmt.Errorf("pools[%d] position_manager invalid: %s", i, pool.PositionManager)
				}
				if strings.TrimSpace(pool.PositionTokenID) != "" {
					if _, err := strconv.ParseUint(strings.TrimSpace(pool.PositionTokenID), 10, 64); err != nil {
						return fmt.Errorf("pools[%d] position_token_id must be a base-10 integer string", i)
					}
				}
				if pool.Token0Decimals <= 0 || pool.Token1Decimals <= 0 {
					return fmt.Errorf("pools[%d] token decimals must be > 0", i)
				}
			// Phase-1 safety rails:
			// Phoenix derives "CEX price" for exactly one token (e.g. WETH) and assigns 1.0 to stable_tokens.
			// Because Uniswap token0/token1 ordering is address-sorted, we must not assume "token1 is always WETH".
			// The config MUST declare which token is CEX-priced, and which token is stable (via stable_tokens),
			// otherwise price math will be poisoned and can lead to waste pools / revert spam / gas burn.
			token0 := strings.ToLower(strings.TrimSpace(pool.Token0))
			token1 := strings.ToLower(strings.TrimSpace(pool.Token1))

			cexToken := strings.ToLower(strings.TrimSpace(pool.CEXPriceToken))
			if cexToken == "" {
				cexToken = token1
				cfg.Pools[i].CEXPriceToken = pool.Token1
			}
			if cexToken != token0 && cexToken != token1 {
				return fmt.Errorf("pools[%d] cex_price_token must be token0 or token1", i)
			}
			expectedStable := token0
			if cexToken == token0 {
				expectedStable = token1
			}

			if len(pool.StableTokens) == 0 {
				return fmt.Errorf("pools[%d] stable_tokens required (must include the stable side of the pool)", i)
			}
			seenStables := map[string]struct{}{}
			hasExpectedStable := false
			for _, stable := range pool.StableTokens {
				s := strings.ToLower(strings.TrimSpace(stable))
				if s == "" {
					return fmt.Errorf("pools[%d] stable_tokens contains empty entry", i)
				}
				if _, ok := parseAddr20(s); !ok {
					return fmt.Errorf("pools[%d] stable_tokens contains invalid address %s", i, stable)
				}
				if s == cexToken {
					return fmt.Errorf("pools[%d] stable_tokens must not include cex_price_token (%s)", i, pool.CEXPriceToken)
				}
				if _, dup := seenStables[s]; dup {
					return fmt.Errorf("pools[%d] stable_tokens contains duplicate token %s", i, stable)
				}
				seenStables[s] = struct{}{}
				if s == expectedStable {
					hasExpectedStable = true
				}
			}
			if !hasExpectedStable {
				return fmt.Errorf("pools[%d] stable_tokens must include the stable side of the pool (%s)", i, expectedStable)
			}
			if pool.MaxCapPct <= 0 || pool.MaxCapPct > 1 {
				if cfg.Risk.MaxUtilizationPct > 0 {
					cfg.Pools[i].MaxCapPct = cfg.Risk.MaxUtilizationPct
				} else {
				return fmt.Errorf("pools[%d] max_cap_pct invalid", i)
			}
		}
		// Default per-pool rebalance cap (user-level safety rail).
		if pool.MaxDailyRebalances <= 0 {
			cfg.Pools[i].MaxDailyRebalances = 20
		}
	}

	if cfg.Risk.MaxDailyGas <= 0 {
		return errors.New("risk.max_daily_gas must be > 0")
	}

	if cfg.Risk.MaxDrawdown <= 0 || cfg.Risk.MaxDrawdown >= 1 {
		return errors.New("risk.max_drawdown must be between (0,1)")
	}

	if cfg.Risk.ConsecutiveFails <= 0 {
		return errors.New("risk.consecutive_fails must be > 0")
	}
	if cfg.Risk.MaxUtilizationPct <= 0 || cfg.Risk.MaxUtilizationPct > 1 {
		cfg.Risk.MaxUtilizationPct = 0.5
	}
	if cfg.Risk.MaxSwapSlippagePct <= 0 || cfg.Risk.MaxSwapSlippagePct >= 0.5 {
		// Default tighter than 1% given no MEV protection; tune per-chain/pool as needed.
		cfg.Risk.MaxSwapSlippagePct = 0.005
	}

	if cfg.Wallet.MinIdlePct <= 0 || cfg.Wallet.MinIdlePct >= 1 {
		if cfg.Wallet.MinIdlePct == 0 {
			cfg.Wallet.MinIdlePct = 0.1
		} else {
			return errors.New("wallet.min_idle_pct must be between (0,1)")
		}
	}

	if cfg.Events.Driver == "" {
		cfg.Events.Driver = "memory"
	}
	switch cfg.Events.Driver {
	case "memory":
	case "file":
		if cfg.Events.FilePath == "" {
			cfg.Events.FilePath = "logs/events.jsonl"
		}
	case "redis":
		if cfg.Events.RedisURL == "" {
			return errors.New("events.redis_url required for redis driver")
		}
		if cfg.Events.RedisPrefix == "" {
			cfg.Events.RedisPrefix = "phoenix"
		}
		if cfg.Events.RedisGroup == "" {
			cfg.Events.RedisGroup = "phoenix-consumer"
		}
	default:
		return fmt.Errorf("unsupported events driver: %s", cfg.Events.Driver)
	}
	if cfg.Events.ReplayRetention == "" {
		cfg.Events.ReplayRetention = "24h"
	}

	// Default strategy profiles.
	if cfg.Strategy.Profiles == nil {
		cfg.Strategy.Profiles = map[string]StrategyProfile{}
	}
	if _, ok := cfg.Strategy.Profiles["normal"]; !ok {
		cfg.Strategy.Profiles["normal"] = StrategyProfile{TargetNotionalPctMultiplier: 1.0, MinSpreadTicksMultiplier: 1.0, EngineRiskFactor: 1.0, RangeWidthMultiplier: 1.0}
	}
	if _, ok := cfg.Strategy.Profiles["caution"]; !ok {
		cfg.Strategy.Profiles["caution"] = StrategyProfile{TargetNotionalPctMultiplier: 0.5, MinSpreadTicksMultiplier: 1.5, EngineRiskFactor: 0.7, RangeWidthMultiplier: 1.5}
	}
	if _, ok := cfg.Strategy.Profiles["frozen"]; !ok {
		cfg.Strategy.Profiles["frozen"] = StrategyProfile{TargetNotionalPctMultiplier: 0.0, MinSpreadTicksMultiplier: 10.0, EngineRiskFactor: 0.0, RangeWidthMultiplier: 0.0}
	}

	// Default range config for volatility-targeted active LP.
	if cfg.Strategy.Range.MinWidthPct <= 0 || cfg.Strategy.Range.MinWidthPct >= 1 {
		cfg.Strategy.Range.MinWidthPct = 0.02
	}
	if cfg.Strategy.Range.MaxWidthPct <= 0 || cfg.Strategy.Range.MaxWidthPct >= 1 {
		cfg.Strategy.Range.MaxWidthPct = 0.20
	}
	if cfg.Strategy.Range.MaxWidthPct < cfg.Strategy.Range.MinWidthPct {
		cfg.Strategy.Range.MaxWidthPct = cfg.Strategy.Range.MinWidthPct
	}
	if cfg.Strategy.Range.VolK <= 0 {
		cfg.Strategy.Range.VolK = 2.0
	}
	if cfg.Strategy.Range.VolWindow == "" {
		cfg.Strategy.Range.VolWindow = "6h"
	}

	if strings.TrimSpace(cfg.Strategy.Rebalance.MinInterval) != "" {
		if _, err := time.ParseDuration(strings.TrimSpace(cfg.Strategy.Rebalance.MinInterval)); err != nil {
			return fmt.Errorf("strategy.rebalance.min_interval invalid duration: %w", err)
		}
	}
	if cfg.Strategy.Rebalance.KeepLiquidityPctForSwaps < 0 || cfg.Strategy.Rebalance.KeepLiquidityPctForSwaps >= 1 {
		return fmt.Errorf("strategy.rebalance.keep_liquidity_pct_for_swaps must be in [0,1)")
	}

	if cfg.Gateway.GasMultiplier <= 0 {
		cfg.Gateway.GasMultiplier = 1.0
	}
	if cfg.Gateway.MaxRetries <= 0 {
		cfg.Gateway.MaxRetries = 3
	}
	if cfg.Gateway.RetryBackoffMs <= 0 {
		cfg.Gateway.RetryBackoffMs = 1500
	}
	if cfg.Gateway.GasBumpPct <= 0 {
		cfg.Gateway.GasBumpPct = 0.15
	}
	if cfg.Gateway.ApprovalMultiplier < 1.0 {
		cfg.Gateway.ApprovalMultiplier = 1.05
	}
	// Preflight on by default: blocks obvious reverts and prevents "blind gas burn".
	if cfg.Gateway.Preflight == nil {
		v := true
		cfg.Gateway.Preflight = &v
	}

	if cfg.PoolGuard.CacheTTL == "" {
		cfg.PoolGuard.CacheTTL = "6h"
	}
	if cfg.PoolGuard.GoPlusKeyEnv == "" {
		cfg.PoolGuard.GoPlusKeyEnv = "GOPLUS_API_KEY"
	}
	if cfg.PoolGuard.HoneypotKeyEnv == "" {
		cfg.PoolGuard.HoneypotKeyEnv = "HONEYPOT_API_KEY"
	}

	return nil
}

func parseAddr20(s string) ([20]byte, bool) {
	var out [20]byte
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 40 {
		return out, false
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 20 {
		return out, false
	}
	copy(out[:], b)
	return out, true
}

func addrLess(a, b [20]byte) bool {
	for i := 0; i < 20; i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return false
}
