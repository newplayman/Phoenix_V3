package rebalancer

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/strategy"
)

// RebalanceInput 包含 Rebalancer 计算所需的所有输入
type RebalanceInput struct {
	Intent        strategy.Intent
	WalletBalance map[string]*big.Int // Token Addr -> Amount
	Prices        map[string]float64  // Token Addr -> USD Price
	PoolConfig    PoolConfig
	RiskLimits    RiskLimits
	State         PoolStateSnapshot
}

type PoolConfig struct {
	PoolID         string
	Token0         string
	Token1         string
	Token0Decimals int
	Token1Decimals int
	Fee            int
	MaxCapPct      float64
	StableTokens   []string
}

type RiskLimits struct {
	MinIdleCashPct     float64
	MaxSwapSlippagePct float64
	MaxSwapTradePct    float64
	MaxDailySwapVolPct float64
}

type PoolStateSnapshot struct {
	CurrentTick  int64
	SqrtPriceX96 *big.Int
}

// RebalancePlan 是 Rebalancer 的输出结果
type RebalancePlan struct {
	Swaps       []SwapAction
	FinalLP     LPAction
	UtilizedPct float64
	Reason      string
}

type SwapAction struct {
	FromToken    common.Address
	ToToken      common.Address
	// Path optionally encodes multi-hop swap tokens including FromToken and ToToken.
	// When len(Path) > 2, Router will build exactInput calldata.
	Path         []common.Address
	// Fees contains per-hop Uniswap V3 fee tiers matching Path segments.
	// For single-hop swaps, Fee is used instead.
	Fees         []uint32
	AmountIn     *big.Int
	MinAmountOut *big.Int
	Route        string // e.g., "router_v3"
	SlippagePct  float64
	Fee          uint32 // Uniswap pool fee tier (e.g., 500 for 0.05%)
	FromDecimals int
	ToDecimals   int
	EstimatedUSD float64
}

type LPAction struct {
	PoolID      string
	Amount0     *big.Int
	Amount1     *big.Int
	LowerTick   int64
	UpperTick   int64
	SlippagePct float64
	IsMint      bool
}
