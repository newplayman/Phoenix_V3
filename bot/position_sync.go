package bot

import (
	"context"
	"math/big"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/chain/univ3"
	"phoenix-v3/internal/config"
	"phoenix-v3/internal/engine"
	"phoenix-v3/internal/storage"
)

type uniV3PositionSnapshot struct {
	TokenID   *big.Int
	Liquidity *big.Int
	TickLower int64
	TickUpper int64
	Token0    common.Address
	Token1    common.Address
	Fee       uint32
}

func asBigInt(v interface{}) (*big.Int, bool) {
	switch t := v.(type) {
	case *big.Int:
		return new(big.Int).Set(t), true
	case big.Int:
		return new(big.Int).Set(&t), true
	case uint64:
		return new(big.Int).SetUint64(t), true
	case int64:
		return big.NewInt(t), true
	case int32:
		return big.NewInt(int64(t)), true
	case uint32:
		return new(big.Int).SetUint64(uint64(t)), true
	default:
		return nil, false
	}
}

func asInt64(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int32:
		return int64(t), true
	case *big.Int:
		return t.Int64(), true
	case big.Int:
		return t.Int64(), true
	default:
		return 0, false
	}
}

func asUint32(v interface{}) (uint32, bool) {
	switch t := v.(type) {
	case uint32:
		return t, true
	case uint64:
		return uint32(t), true
	case int64:
		if t < 0 {
			return 0, false
		}
		return uint32(t), true
	case *big.Int:
		if t.Sign() < 0 {
			return 0, false
		}
		return uint32(t.Uint64()), true
	case big.Int:
		if t.Sign() < 0 {
			return 0, false
		}
		return uint32(t.Uint64()), true
	default:
		return 0, false
	}
}

func FetchPositionByTokenID(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, pmAddr common.Address, tokenID *big.Int) (int64, int64, *big.Int, bool, error) {
	if ethGw == nil || adapter == nil || tokenID == nil || tokenID.Sign() <= 0 {
		return 0, 0, nil, false, nil
	}
	dataPos, err := adapter.ParsedABI.Pack("positions", tokenID)
	if err != nil {
		return 0, 0, nil, false, err
	}
	resPos, err := ethGw.Call(ctx, pmAddr, dataPos)
	if err != nil {
		return 0, 0, nil, false, err
	}
	upPos, err := adapter.ParsedABI.Unpack("positions", resPos)
	if err != nil || len(upPos) < 8 {
		if err == nil {
			return 0, 0, nil, false, nil
		}
		return 0, 0, nil, false, err
	}
	tL, okTL := asInt64(upPos[5])
	tU, okTU := asInt64(upPos[6])
	liq, okLiq := asBigInt(upPos[7])
	if !okTL || !okTU || !okLiq {
		return 0, 0, nil, false, nil
	}
	return tL, tU, liq, true, nil
}

// listMatchingPositions scans wallet's UniV3 NFT positions and returns those matching (token0,token1,fee).
func listMatchingPositions(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, poolCfg config.PoolConfig, maxScan int) ([]uniV3PositionSnapshot, error) {
	if ethGw == nil || adapter == nil {
		return nil, nil
	}
	if poolCfg.PositionManager == "" {
		return nil, nil
	}
	if maxScan <= 0 {
		maxScan = 64
	}
	pmAddr := common.HexToAddress(poolCfg.PositionManager)
	want0 := common.HexToAddress(poolCfg.Token0)
	want1 := common.HexToAddress(poolCfg.Token1)
	wantFee := uint32(poolCfg.Fee)

	out := make([]uniV3PositionSnapshot, 0, 2)
	for idx := 0; idx < maxScan; idx++ {
		data, err := adapter.ParsedABI.Pack("tokenOfOwnerByIndex", ethGw.WalletAddress(), big.NewInt(int64(idx)))
		if err != nil {
			return out, nil
		}
		res, err := ethGw.Call(ctx, pmAddr, data)
		if err != nil {
			// NonfungiblePositionManager is not enumerable on most chains; treat as "not supported".
			return out, nil
		}
		unpacked, err := adapter.ParsedABI.Unpack("tokenOfOwnerByIndex", res)
		if err != nil || len(unpacked) < 1 {
			return out, nil
		}
		tokenID, ok := asBigInt(unpacked[0])
		if !ok || tokenID == nil || tokenID.Sign() <= 0 {
			continue
		}

		dataPos, err := adapter.ParsedABI.Pack("positions", tokenID)
		if err != nil {
			continue
		}
		resPos, err := ethGw.Call(ctx, pmAddr, dataPos)
		if err != nil {
			continue
		}
		upPos, err := adapter.ParsedABI.Unpack("positions", resPos)
		if err != nil || len(upPos) < 8 {
			continue
		}

		fee, okFee := asUint32(upPos[4])
		tL, okTL := asInt64(upPos[5])
		tU, okTU := asInt64(upPos[6])
		liq, okLiq := asBigInt(upPos[7])
		t0, okT0 := upPos[2].(common.Address)
		t1, okT1 := upPos[3].(common.Address)
		if !okFee || !okTL || !okTU || !okLiq || !okT0 || !okT1 {
			continue
		}
		if fee != wantFee || t0 != want0 || t1 != want1 {
			continue
		}
		out = append(out, uniV3PositionSnapshot{
			TokenID:   tokenID,
			Liquidity: liq,
			TickLower: tL,
			TickUpper: tU,
			Token0:    t0,
			Token1:    t1,
			Fee:       fee,
		})
	}
	return out, nil
}

// StartPositionSync periodically updates bot pool runtime positions from on-chain UniV3 positions.
// It also attempts to adopt an existing matching position if config/store lacks tokenId (best-effort).
func StartPositionSync(ctx context.Context, cfgValue *atomic.Value, store *storage.Store, gwSelector func(int64) gateway.Gateway) {
	syncOnce := func() {
		cfg, _ := cfgValue.Load().(*config.AppConfig)
		if cfg == nil {
			return
		}
		for _, pool := range cfg.Pools {
			func(pool config.PoolConfig) {
				callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()

				gw := gwSelector(pool.ChainID)
				ethGw, ok := gw.(*gateway.EthGateway)
				if !ok || ethGw == nil || pool.PositionManager == "" {
					return
				}

				adapter := univ3.NewAdapter(pool.PositionManager)

				tokenID := ""
				if store != nil {
					if tid, err := store.GetPoolPositionTokenID(pool.ID, pool.ChainID); err == nil {
						tokenID = strings.TrimSpace(tid)
					}
				}
				if tokenID == "" {
					tokenID = strings.TrimSpace(pool.PositionTokenID)
				}
				if tokenID == "" {
					pos, err := listMatchingPositions(callCtx, ethGw, adapter, pool, 64)
					if err != nil || len(pos) == 0 {
						return
					}

					var best *uniV3PositionSnapshot
					for i := range pos {
						if pos[i].TokenID == nil {
							continue
						}
						if best == nil {
							best = &pos[i]
							continue
						}
						liqA := pos[i].Liquidity
						liqB := best.Liquidity
						if liqA != nil && liqB != nil {
							if liqA.Cmp(liqB) > 0 {
								best = &pos[i]
								continue
							}
							if liqA.Cmp(liqB) == 0 && pos[i].TokenID.Cmp(best.TokenID) > 0 {
								best = &pos[i]
								continue
							}
						} else if liqA != nil && (liqB == nil || liqB.Sign() <= 0) {
							best = &pos[i]
							continue
						}
					}
					if best == nil || best.TokenID == nil {
						return
					}
					tokenID = best.TokenID.String()
					if store != nil {
						_ = store.UpsertPoolPosition(pool.ID, pool.ChainID, tokenID)
					}
				}

				pmAddr := common.HexToAddress(pool.PositionManager)
				tid, ok := new(big.Int).SetString(tokenID, 10)
				if !ok || tid.Sign() <= 0 {
					return
				}

				tL, tU, liq, ok, _ := FetchPositionByTokenID(callCtx, ethGw, adapter, pmAddr, tid)
				if !ok {
					return
				}
				if liq == nil || liq.Sign() <= 0 {
					SetPoolRuntimePosition(pool.ID, tokenID, engine.CurrentPosition{})
					return
				}
				liqF, _ := new(big.Float).SetInt(liq).Float64()
				SetPoolRuntimePosition(pool.ID, tokenID, engine.CurrentPosition{LowerTick: tL, UpperTick: tU, Liquidity: liqF})
			}(pool)
		}
	}

	syncOnce()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			syncOnce()
		}
	}
}
