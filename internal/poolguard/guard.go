package poolguard

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/config"
)

type PoolRiskLevel string

const (
	RiskSafe    PoolRiskLevel = "safe"
	RiskWarning PoolRiskLevel = "warning"
	RiskDanger  PoolRiskLevel = "danger"
)

type PoolCheckResult struct {
	PoolID      string        `json:"pool_id"`
	Risk        PoolRiskLevel `json:"risk"`
	Reason      string        `json:"reason"`
	Score       float64       `json:"score,omitempty"`
	LastChecked time.Time     `json:"last_checked"`
}

type Guard struct {
	allowlistTokens map[common.Address]bool
	blacklistTokens map[common.Address]bool
	providers       []Provider
	chainCallers    map[int64]ChainCaller
	cacheTTL        time.Duration
	cacheMu         sync.Mutex
	cache           map[common.Address]*PoolCheckResult
	lastResultsMu   sync.RWMutex
	lastResults     map[string]*PoolCheckResult
}

func NewGuard() *Guard {
	return NewGuardWithConfig(config.PoolGuardConfig{})
}

func NewGuardWithConfig(cfg config.PoolGuardConfig) *Guard {
	ttl, err := time.ParseDuration(cfg.CacheTTL)
	if err != nil || ttl <= 0 {
		ttl = 6 * time.Hour
	}
	allow := make(map[common.Address]bool)
	black := make(map[common.Address]bool)
	for _, a := range cfg.AllowlistTokens {
		allow[common.HexToAddress(a)] = true
	}
	for _, a := range cfg.BlacklistTokens {
		black[common.HexToAddress(a)] = true
	}
	providers := []Provider{}
	if cfg.EnableRemote {
		if cfg.GoPlusURL != "" {
			providers = append(providers, NewGoPlusProvider(cfg.GoPlusURL, cfg.GoPlusKeyEnv))
		}
		if cfg.HoneypotURL != "" {
			providers = append(providers, NewHoneypotProvider(cfg.HoneypotURL, cfg.HoneypotKeyEnv))
		}
	}
	return &Guard{
		allowlistTokens: allow,
		blacklistTokens: black,
		providers:       providers,
		chainCallers:    map[int64]ChainCaller{},
		cacheTTL:        ttl,
		cache:           map[common.Address]*PoolCheckResult{},
		lastResults:     map[string]*PoolCheckResult{},
	}
}

// SetChainCaller injects a chain caller for on-chain token checks for a specific chain.
func (g *Guard) SetChainCaller(chainID int64, caller ChainCaller) {
	if chainID == 0 || caller == nil {
		return
	}
	if g.chainCallers == nil {
		g.chainCallers = map[int64]ChainCaller{}
	}
	g.chainCallers[chainID] = caller
}

// AddBlacklistToken manually adds a token to blacklist
func (g *Guard) AddBlacklistToken(addr string) {
	if g.blacklistTokens == nil {
		g.blacklistTokens = make(map[common.Address]bool)
	}
	g.blacklistTokens[common.HexToAddress(addr)] = true
}

// CheckPool performs basic safety checks on a pool/token
func (g *Guard) CheckPool(ctx context.Context, poolID string, chainID int64, token0, token1 string) *PoolCheckResult {
	// 1. Check Local Allow/Blacklist
	t0 := common.HexToAddress(token0)
	t1 := common.HexToAddress(token1)
	if g.blacklistTokens[t0] || g.blacklistTokens[t1] {
		return g.record(poolID, RiskDanger, "Token found in blacklist")
	}
	if g.allowlistTokens[t0] && g.allowlistTokens[t1] {
		return g.record(poolID, RiskSafe, "Tokens in allowlist")
	}

	// 2. Check for suspicious names (simple heuristic for demo)
	// In reality, we would call an external API like GoPlus / Honeypot.is
	if strings.Contains(strings.ToLower(poolID), "scam") {
		return g.record(poolID, RiskDanger, "Suspicious pool name")
	}

	// 3. On-chain totalSupply check (best-effort).
	worstRisk := RiskSafe
	worstReason := "Passed basic checks"
	for _, token := range []common.Address{t0, t1} {
		onChainRisk, onChainReason := g.checkTotalSupply(ctx, chainID, token)
		if onChainRisk == RiskDanger || (onChainRisk == RiskWarning && worstRisk == RiskSafe) {
			worstRisk = onChainRisk
			worstReason = onChainReason
		}
	}

	// 4. Optional remote providers (worst risk wins)
	for _, token := range []common.Address{t0, t1} {
		res := g.checkTokenWithProviders(ctx, chainID, token)
		if res == nil {
			continue
		}
		if res.Risk == RiskDanger || (res.Risk == RiskWarning && worstRisk == RiskSafe) {
			worstRisk = res.Risk
			worstReason = res.Reason
		}
	}

	return g.record(poolID, worstRisk, worstReason)
}

func (g *Guard) checkTotalSupply(ctx context.Context, chainID int64, token common.Address) (PoolRiskLevel, string) {
	if chainID == 0 || g.chainCallers == nil {
		return RiskSafe, "on-chain check skipped"
	}
	caller := g.chainCallers[chainID]
	if caller == nil {
		return RiskSafe, "on-chain check skipped"
	}
	data, err := packTotalSupply()
	if err != nil {
		return RiskWarning, "totalSupply pack failed"
	}
	res, err := caller.Call(ctx, token, data)
	if err != nil {
		return RiskWarning, "totalSupply call failed"
	}
	supply, err := unpackTotalSupply(res)
	if err != nil {
		return RiskWarning, "totalSupply decode failed"
	}
	if supply == nil || supply.Sign() <= 0 {
		return RiskDanger, "token totalSupply is zero"
	}
	return RiskSafe, "totalSupply ok"
}

func (g *Guard) checkTokenWithProviders(ctx context.Context, chainID int64, token common.Address) *PoolCheckResult {
	if len(g.providers) == 0 {
		return nil
	}
	// Cache lookup
	g.cacheMu.Lock()
	if cached, ok := g.cache[token]; ok && time.Since(cached.LastChecked) < g.cacheTTL {
		g.cacheMu.Unlock()
		return cached
	}
	g.cacheMu.Unlock()

	worst := &PoolCheckResult{PoolID: token.Hex(), Risk: RiskSafe, Reason: "remote ok", LastChecked: time.Now()}
	for _, p := range g.providers {
		pr, err := p.CheckToken(ctx, chainID, token)
		if err != nil {
			log.Printf("[PoolGuard] provider %s error: %v", p.Name(), err)
			continue
		}
		if pr.Risk == RiskDanger || (pr.Risk == RiskWarning && worst.Risk == RiskSafe) {
			worst = pr
		}
	}
	g.cacheMu.Lock()
	g.cache[token] = worst
	g.cacheMu.Unlock()
	return worst
}

func (g *Guard) record(poolID string, risk PoolRiskLevel, reason string) *PoolCheckResult {
	res := &PoolCheckResult{PoolID: poolID, Risk: risk, Reason: reason, LastChecked: time.Now()}
	g.lastResultsMu.Lock()
	g.lastResults[poolID] = res
	g.lastResultsMu.Unlock()
	return res
}

// Snapshot returns last known results per pool.
func (g *Guard) Snapshot() map[string]PoolCheckResult {
	g.lastResultsMu.RLock()
	defer g.lastResultsMu.RUnlock()
	out := make(map[string]PoolCheckResult, len(g.lastResults))
	for k, v := range g.lastResults {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}
