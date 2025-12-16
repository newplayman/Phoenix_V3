package bot

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/dexstate"
	"phoenix-v3/internal/events"
)

// PoolWatchers manages DEX pool-state polling goroutines.
// This lives in bot/ (orchestration layer) so cmd/ stays thin and testable.
type PoolWatchers struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewPoolWatchers() *PoolWatchers {
	return &PoolWatchers{cancels: map[string]context.CancelFunc{}}
}

func (w *PoolWatchers) StopAll() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, cancel := range w.cancels {
		log.Printf("[DEX] stopping watcher for pool %s", id)
		cancel()
	}
	w.cancels = map[string]context.CancelFunc{}
}

// Restart stops all existing watchers and starts a new set based on cfg.Pools.
func (w *PoolWatchers) Restart(ctx context.Context, stateMap map[int64]*dexstate.UniV3State, cfg *config.AppConfig, stream events.Stream) {
	if w == nil {
		return
	}
	w.StopAll()

	if cfg == nil || stream == nil {
		return
	}

	for _, pool := range cfg.Pools {
		client := stateMap[pool.ChainID]
		if client == nil || pool.Address == "" {
			log.Printf("[DEX] missing rpc or address for pool %s", pool.ID)
			continue
		}
		addr := common.HexToAddress(pool.Address)
		watchCtx, cancel := context.WithCancel(ctx)
		poolID := pool.ID
		chainID := pool.ChainID

		go func(c *dexstate.UniV3State, watchAddr common.Address, pid string, localCtx context.Context) {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-localCtx.Done():
					return
				case <-ticker.C:
					state, err := c.GetPoolState(chainID, watchAddr)
					if err != nil {
						log.Printf("[DEX] fetch pool state failed (%s): %v", pid, err)
						continue
					}
					payload := map[string]interface{}{
						"pool_id":        pid,
						"chain_id":       state.ChainID,
						"pool_address":   state.PoolAddress.Hex(),
						"current_tick":   state.CurrentTick,
						"liquidity":      state.Liquidity.String(),
						"sqrt_price_x96": state.SqrtPriceX96.String(),
					}
					b, _ := json.Marshal(payload)
					_ = stream.Publish(localCtx, events.TopicPoolState, json.RawMessage(b))
				}
			}
		}(client, addr, poolID, watchCtx)

		w.mu.Lock()
		if w.cancels == nil {
			w.cancels = map[string]context.CancelFunc{}
		}
		w.cancels[poolID] = cancel
		w.mu.Unlock()
	}
}
