package bot

import (
	"math"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/engine"
)

// PoolRuntime is the bot-maintained in-memory snapshot for a pool.
// It is used for read APIs and for coordinating safe execution behavior.
type PoolRuntime struct {
	Cfg             config.PoolConfig
	Position        engine.CurrentPosition
	PositionTokenID string

	DexPrice      float64
	CurrentTick   int64
	SqrtPriceX96  *big.Int
	PoolLiquidity *big.Int

	LastSigmaDaily float64
	LastWidthPct   float64
	LastVolWindow  string
	LastProfile    string
	LastCexPrice   float64
}

var (
	poolStateMu    sync.RWMutex
	poolStates     = map[string]*PoolRuntime{}
	mintGuardMu    sync.RWMutex
	poolMintGuards = map[string]*atomic.Bool{}
)

func SyncPoolStatesFromConfig(cfg *config.AppConfig) {
	if cfg == nil {
		return
	}
	poolStateMu.Lock()
	defer poolStateMu.Unlock()
	newStates := make(map[string]*PoolRuntime, len(cfg.Pools))
	for _, pool := range cfg.Pools {
		if runtime, ok := poolStates[pool.ID]; ok && runtime != nil {
			runtime.Cfg = pool
			newStates[pool.ID] = runtime
		} else {
			newStates[pool.ID] = &PoolRuntime{Cfg: pool, Position: engine.CurrentPosition{}}
		}
	}
	poolStates = newStates
}

func SyncMintGuardsFromConfig(cfg *config.AppConfig) {
	if cfg == nil {
		return
	}
	mintGuardMu.Lock()
	defer mintGuardMu.Unlock()
	if poolMintGuards == nil {
		poolMintGuards = map[string]*atomic.Bool{}
	}
	for _, pool := range cfg.Pools {
		if _, ok := poolMintGuards[pool.ID]; !ok {
			poolMintGuards[pool.ID] = &atomic.Bool{}
		}
	}
	for id := range poolMintGuards {
		exists := false
		for _, pool := range cfg.Pools {
			if pool.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			delete(poolMintGuards, id)
		}
	}
}

func GetMintGuard(poolID string) *atomic.Bool {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return &atomic.Bool{}
	}
	mintGuardMu.RLock()
	if guard, ok := poolMintGuards[poolID]; ok && guard != nil {
		mintGuardMu.RUnlock()
		return guard
	}
	mintGuardMu.RUnlock()

	mintGuardMu.Lock()
	defer mintGuardMu.Unlock()
	if guard, ok := poolMintGuards[poolID]; ok && guard != nil {
		return guard
	}
	guard := &atomic.Bool{}
	poolMintGuards[poolID] = guard
	return guard
}

func SnapshotPoolRuntimes() []*PoolRuntime {
	poolStateMu.RLock()
	defer poolStateMu.RUnlock()
	result := make([]*PoolRuntime, 0, len(poolStates))
	for _, rt := range poolStates {
		if clone := clonePoolRuntime(rt); clone != nil {
			result = append(result, clone)
		}
	}
	return result
}

func GetPoolRuntimeSnapshot(poolID string) (*PoolRuntime, bool) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return nil, false
	}
	poolStateMu.RLock()
	defer poolStateMu.RUnlock()
	rt, ok := poolStates[poolID]
	if !ok {
		return nil, false
	}
	return clonePoolRuntime(rt), true
}

func SetPoolRuntimePosition(poolID string, tokenID string, pos engine.CurrentPosition) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return
	}
	poolStateMu.Lock()
	defer poolStateMu.Unlock()
	if rt, ok := poolStates[poolID]; ok && rt != nil {
		rt.Position = pos
		rt.PositionTokenID = strings.TrimSpace(tokenID)
	}
}

func ClearPoolRuntimePosition(poolID string) {
	SetPoolRuntimePosition(poolID, "", engine.CurrentPosition{})
}

func SetPoolStrategySnapshot(poolID string, sigmaDaily float64, widthPct float64, volWindow string, profile string, cexPrice float64) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return
	}
	poolStateMu.Lock()
	defer poolStateMu.Unlock()
	if rt, ok := poolStates[poolID]; ok && rt != nil {
		rt.LastSigmaDaily = sigmaDaily
		rt.LastWidthPct = widthPct
		rt.LastVolWindow = strings.TrimSpace(volWindow)
		rt.LastProfile = strings.TrimSpace(profile)
		rt.LastCexPrice = cexPrice
	}
}

// UpdatePoolStateFromEvent updates the pool runtime using a (tick,liquidity,sqrtPrice) snapshot.
// It returns (dexPrice, ok) where ok indicates the pool is known.
func UpdatePoolStateFromEvent(poolID string, currentTick int64, liquidityStr string, sqrtPriceX96 string) (float64, bool) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return 0, false
	}

	poolStateMu.Lock()
	defer poolStateMu.Unlock()
	rt, ok := poolStates[poolID]
	if !ok || rt == nil {
		return 0, false
	}

	liqStr := strings.TrimSpace(liquidityStr)
	if liqStr != "" {
		if liqBI, ok := new(big.Int).SetString(liqStr, 10); ok {
			rt.PoolLiquidity = liqBI
		}
	}

	priceToken := rt.Cfg.CEXPriceToken
	if priceToken == "" {
		priceToken = rt.Cfg.Token1
	}
	// If the CEX-priced token is token1, then the stable side is token0; otherwise stable side is token1.
	stableIsToken0 := strings.EqualFold(priceToken, rt.Cfg.Token1)
	rt.DexPrice = tickToDexPrice(currentTick, rt.Cfg.Token0Decimals, rt.Cfg.Token1Decimals, stableIsToken0)
	rt.CurrentTick = currentTick

	if strings.TrimSpace(sqrtPriceX96) != "" {
		if sqrt, ok := new(big.Int).SetString(strings.TrimSpace(sqrtPriceX96), 10); ok {
			rt.SqrtPriceX96 = sqrt
		}
	}
	return rt.DexPrice, true
}

func clonePoolRuntime(rt *PoolRuntime) *PoolRuntime {
	if rt == nil {
		return nil
	}
	clone := *rt
	if rt.SqrtPriceX96 != nil {
		clone.SqrtPriceX96 = new(big.Int).Set(rt.SqrtPriceX96)
	}
	if rt.PoolLiquidity != nil {
		clone.PoolLiquidity = new(big.Int).Set(rt.PoolLiquidity)
	}
	return &clone
}

func tickToDexPrice(tick int64, token0Decimals, token1Decimals int, stableIsToken0 bool) float64 {
	rawPrice := math.Pow(1.0001, float64(tick))
	if rawPrice <= 0 {
		return 0
	}
	if stableIsToken0 {
		// stable(token0) per priced(token1)
		return (1.0 / rawPrice) * math.Pow10(token1Decimals-token0Decimals)
	}
	// stable(token1) per priced(token0)
	return rawPrice * math.Pow10(token0Decimals-token1Decimals)
}
