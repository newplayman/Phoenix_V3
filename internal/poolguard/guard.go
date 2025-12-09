package poolguard

import (
	"context"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type PoolRiskLevel string

const (
	RiskSafe    PoolRiskLevel = "safe"
	RiskWarning PoolRiskLevel = "warning"
	RiskDanger  PoolRiskLevel = "danger"
)

type PoolCheckResult struct {
	PoolID      string
	Risk        PoolRiskLevel
	Reason      string
	LastChecked time.Time
}

type Guard struct {
	BlacklistTokens map[common.Address]bool
}

func NewGuard() *Guard {
	return &Guard{
		BlacklistTokens: make(map[common.Address]bool),
	}
}

// AddBlacklistToken manually adds a token to blacklist
func (g *Guard) AddBlacklistToken(addr string) {
	g.BlacklistTokens[common.HexToAddress(addr)] = true
}

// CheckPool performs basic safety checks on a pool/token
func (g *Guard) CheckPool(ctx context.Context, poolID string, token0, token1 string) *PoolCheckResult {
	// 1. Check Local Blacklist
	t0 := common.HexToAddress(token0)
	t1 := common.HexToAddress(token1)

	if g.BlacklistTokens[t0] || g.BlacklistTokens[t1] {
		return &PoolCheckResult{
			PoolID:      poolID,
			Risk:        RiskDanger,
			Reason:      "Token found in blacklist",
			LastChecked: time.Now(),
		}
	}

	// 2. Check for suspicious names (simple heuristic for demo)
	// In reality, we would call an external API like GoPlus / Honeypot.is
	if strings.Contains(strings.ToLower(poolID), "scam") {
		return &PoolCheckResult{
			PoolID:      poolID,
			Risk:        RiskDanger,
			Reason:      "Suspicious pool name",
			LastChecked: time.Now(),
		}
	}

	// 3. Simple Honeypot Check (Placeholder)
	// We assume standard ERC20 behavior.
	// Real implementation needs to simulate buy/sell tx to check for tax/revert.

	return &PoolCheckResult{
		PoolID:      poolID,
		Risk:        RiskSafe,
		Reason:      "Passed basic checks",
		LastChecked: time.Now(),
	}
}
