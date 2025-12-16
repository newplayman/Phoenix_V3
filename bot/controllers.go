package bot

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/chain/univ3"
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/risk"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

type PauseController struct {
	Flags *ControlFlags
}

func (p *PauseController) SetPaused(v bool) {
	if p == nil || p.Flags == nil {
		return
	}
	p.Flags.SetPaused(v)
}

func (p *PauseController) Paused() bool {
	if p == nil || p.Flags == nil {
		return false
	}
	return p.Flags.Paused()
}

type CleanupController struct {
	Ctx      context.Context
	Flags    *ControlFlags
	Gateways map[int64]*gateway.EthGateway
	CfgValue *atomic.Value
	Store    *storage.Store
}

func (c *CleanupController) InProgress() bool {
	if c == nil || c.Flags == nil {
		return false
	}
	return c.Flags.CleanupInProgress()
}

func (c *CleanupController) TriggerCleanup() error {
	if c == nil || c.Flags == nil || c.CfgValue == nil {
		return fmt.Errorf("not configured")
	}
	cfg, _ := c.CfgValue.Load().(*config.AppConfig)
	if config.SafetyFromConfig(cfg).EffectiveDryRun {
		return fmt.Errorf("blocked: effective_dry_run=true")
	}
	if !c.Flags.TryStartCleanup() {
		return fmt.Errorf("cleanup already running")
	}
	go func() {
		defer c.Flags.EndCleanup()
		RunCleanup(c.Ctx, c.Gateways, cfg, c.Store)
	}()
	return nil
}

// RunCleanup closes known UniV3 positions by configured/stored tokenId per pool.
// It is safe to call in dry-run mode; gateways should enforce effective dry-run.
func RunCleanup(ctx context.Context, gateways map[int64]*gateway.EthGateway, cfg *config.AppConfig, store *storage.Store) {
	if config.SafetyFromConfig(cfg).EffectiveDryRun {
		log.Println("[Cleanup] blocked: effective_dry_run=true")
		return
	}
	log.Println(">>> Starting Clean Up Mode: Closing Known Positions <<<")
	if cfg == nil || len(cfg.Pools) == 0 {
		return
	}
	for _, pool := range cfg.Pools {
		if pool.PositionManager == "" {
			continue
		}
		ethGw := gateways[pool.ChainID]
		if ethGw == nil {
			log.Printf("[Cleanup] No gateway for chain %d (pool=%s)", pool.ChainID, pool.ID)
			continue
		}
		tokenID := strings.TrimSpace(pool.PositionTokenID)
		if tokenID == "" && store != nil {
			if tid, err := store.GetPoolPositionTokenID(pool.ID, pool.ChainID); err == nil {
				tokenID = strings.TrimSpace(tid)
			}
		}
		if tokenID == "" {
			log.Printf("[Cleanup] pool %s has no position_token_id; skip", pool.ID)
			continue
		}
		tid, ok := new(big.Int).SetString(tokenID, 10)
		if !ok || tid.Sign() <= 0 {
			log.Printf("[Cleanup] invalid position_token_id for pool %s: %s", pool.ID, tokenID)
			continue
		}
		adapter := univ3.NewAdapter(pool.PositionManager)
		pmAddr := common.HexToAddress(pool.PositionManager)
		log.Printf("[Cleanup] closing pool=%s tokenId=%s", pool.ID, tokenID)
		stepIndex := 0
		if err := ClosePositionTokenID(ctx, ethGw, adapter, pmAddr, tid, nil, nil, "", &stepIndex); err != nil {
			log.Printf("[Cleanup] close failed pool=%s tokenId=%s: %v", pool.ID, tokenID, err)
			continue
		}
		ClearPoolRuntimePosition(pool.ID)
		if store != nil {
			_ = store.ClearPoolPosition(pool.ID, pool.ChainID)
		}
	}
	log.Println(">>> Cleanup Complete <<<")
}

type RebalanceController struct {
	Queue    *strategy.IntentQueue
	CfgValue *atomic.Value
}

func (r *RebalanceController) TriggerRebalance(poolID string) error {
	if r == nil || r.Queue == nil || r.CfgValue == nil {
		return fmt.Errorf("not configured")
	}
	cfg, _ := r.CfgValue.Load().(*config.AppConfig)
	if cfg == nil || len(cfg.Pools) == 0 {
		return fmt.Errorf("no pools configured")
	}
	if poolID == "" {
		poolID = cfg.Pools[0].ID
	}
	poolCfg, ok := config.FindPool(cfg, poolID)
	if !ok {
		return fmt.Errorf("unknown pool %s", poolID)
	}

	runtimeState, hasRuntime := GetPoolRuntimeSnapshot(poolID)
	if !hasRuntime || runtimeState == nil {
		return fmt.Errorf("pool runtime not available for %s", poolID)
	}
	if runtimeState.DexPrice == 0 {
		return fmt.Errorf("dex price not available for %s", poolID)
	}

	spreadPct := 0.05
	lowerPrice := runtimeState.DexPrice * (1 - spreadPct)
	upperPrice := runtimeState.DexPrice * (1 + spreadPct)

	lowerPriceInv := 1.0 / lowerPrice
	upperPriceInv := 1.0 / upperPrice

	lowerTick := engine.PriceToTickWithDecimals(lowerPriceInv, poolCfg.Token0Decimals, poolCfg.Token1Decimals)
	upperTick := engine.PriceToTickWithDecimals(upperPriceInv, poolCfg.Token0Decimals, poolCfg.Token1Decimals)

	spacing := strategy.TickSpacingForFee(poolCfg.Fee)
	if spacing <= 0 {
		spacing = 10
	}
	lowerTick = (lowerTick / spacing) * spacing
	upperTick = (upperTick / spacing) * spacing
	if lowerTick >= upperTick {
		upperTick = lowerTick + spacing
	}

	intent := strategy.Intent{
		ID:                fmt.Sprintf("MANUAL_REBALANCE_%s_%d", poolID, time.Now().Unix()),
		Type:              strategy.IntentRebalance,
		PoolID:            poolID,
		ChainID:           poolCfg.ChainID,
		TargetNotionalPct: config.EffectiveMaxCapPct(poolCfg.MaxCapPct, cfg.Risk.MaxUtilizationPct),
		StrategyVersion:   cfg.StrategyVersion,
		RiskMode:          string(risk.ModeNormal),
		Metadata: map[string]string{
			"token0":          poolCfg.Token0,
			"token1":          poolCfg.Token1,
			"fee":             strconv.Itoa(poolCfg.Fee),
			"lower_tick":      strconv.FormatInt(lowerTick, 10),
			"upper_tick":      strconv.FormatInt(upperTick, 10),
			"token0_decimals": strconv.Itoa(poolCfg.Token0Decimals),
			"token1_decimals": strconv.Itoa(poolCfg.Token1Decimals),
		},
	}
	r.Queue.Enqueue(intent)
	return nil
}
