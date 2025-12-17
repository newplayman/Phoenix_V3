package bot

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"phoenix-v3/internal/chain/gateway"
	"phoenix-v3/internal/chain/univ3"
	"phoenix-v3/internal/events"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

// WaitForReceipt polls for a tx receipt until timeout.
func WaitForReceipt(ctx context.Context, ethGw *gateway.EthGateway, hash common.Hash, timeout time.Duration) *types.Receipt {
	if ethGw == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			log.Printf("[ReceiptWait] timeout for %s", hash.Hex())
			return nil
		default:
		}
		rcpt, err := ethGw.TxReceipt(waitCtx, hash)
		if err == nil && rcpt != nil {
			log.Printf("[ReceiptWait] %s mined status=%d gasUsed=%d", hash.Hex(), rcpt.Status, rcpt.GasUsed)
			return rcpt
		}
		time.Sleep(3 * time.Second)
	}
}

// ClosePositionTokenID closes and burns a UniV3 position tokenId.
// Used only by the bot execution layer (never by Web).
func ClosePositionTokenID(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, pmAddr common.Address, tokenID *big.Int, store *storage.Store, stream events.Stream, parentIntentID string, stepIndex *int) error {
	if ethGw == nil || adapter == nil || tokenID == nil || tokenID.Sign() <= 0 {
		return nil
	}
	_, _, liq, ok, posErr := FetchPositionByTokenID(ctx, ethGw, adapter, pmAddr, tokenID)
	if !ok {
		if posErr != nil {
			msg := posErr.Error()
			if strings.Contains(msg, "execution reverted") || strings.Contains(msg, "not found") {
				log.Printf("[Rebalance] position tokenId=%s not found (already closed?): %v", tokenID.String(), posErr)
				return nil
			}
			return fmt.Errorf("failed to fetch position tokenId=%s: %w", tokenID.String(), posErr)
		}
		return fmt.Errorf("failed to fetch position tokenId=%s", tokenID.String())
	}

	// 1) DecreaseLiquidity (if needed)
	if liq != nil && liq.Sign() > 0 {
		intent := strategy.Intent{
			ID:      fmt.Sprintf("WITHDRAW_%s", tokenID.String()),
			Type:    strategy.IntentWithdraw,
			PoolID:  "",
			ChainID: ethGw.ChainID().Int64(),
			Metadata: map[string]string{
				"token_id":  tokenID.String(),
				"liquidity": liq.String(),
				"target":    pmAddr.Hex(),
				"value":     "0",
			},
		}
		data, err := adapter.BuildDecreaseLiquidityData(intent)
		if err != nil {
			return fmt.Errorf("build decreaseLiquidity: %w", err)
		}
		intent.Metadata["calldata"] = hex.EncodeToString(data)
		res, err := ethGw.Send(ctx, intent)
		if err != nil {
			return fmt.Errorf("send decreaseLiquidity: %w", err)
		}
		idx := 0
		if stepIndex != nil {
			idx = *stepIndex
			*stepIndex++
		}
		RecordStepSent(ctx, store, stream, parentIntentID, idx, "withdraw", res.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "liquidity": liq.String(), "mode": "close"})
		rcpt := WaitForReceipt(ctx, ethGw, res.Hash, 2*time.Minute)
		if rcpt == nil || rcpt.Status != 1 {
			RecordStepFinal(ctx, store, stream, parentIntentID, idx, "withdraw", "failed", res.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close"})
			return fmt.Errorf("decreaseLiquidity reverted (hash=%s)", res.Hash.Hex())
		}
		RecordStepFinal(ctx, store, stream, parentIntentID, idx, "withdraw", "mined", res.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close", "gas_used": rcpt.GasUsed})
	}

	// 2) Collect (always attempt)
	collectIntent := strategy.Intent{
		ID:      fmt.Sprintf("COLLECT_%s", tokenID.String()),
		Type:    strategy.IntentCollectFee,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id":  tokenID.String(),
			"recipient": ethGw.Address(),
			"target":    pmAddr.Hex(),
			"value":     "0",
		},
	}
	collectData, err := adapter.BuildCollectData(collectIntent)
	if err != nil {
		return fmt.Errorf("build collect: %w", err)
	}
	collectIntent.Metadata["calldata"] = hex.EncodeToString(collectData)
	res2, err := ethGw.Send(ctx, collectIntent)
	if err != nil {
		return fmt.Errorf("send collect: %w", err)
	}
	idx2 := 0
	if stepIndex != nil {
		idx2 = *stepIndex
		*stepIndex++
	}
	RecordStepSent(ctx, store, stream, parentIntentID, idx2, "collect", res2.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close"})
	rcpt2 := WaitForReceipt(ctx, ethGw, res2.Hash, 2*time.Minute)
	if rcpt2 == nil || rcpt2.Status != 1 {
		RecordStepFinal(ctx, store, stream, parentIntentID, idx2, "collect", "failed", res2.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close"})
		return fmt.Errorf("collect reverted (hash=%s)", res2.Hash.Hex())
	}
	RecordStepFinal(ctx, store, stream, parentIntentID, idx2, "collect", "mined", res2.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close", "gas_used": rcpt2.GasUsed})

	// 3) Burn NFT (safe even if already at 0 liquidity)
	burnIntent := strategy.Intent{
		ID:      fmt.Sprintf("BURN_%s", tokenID.String()),
		Type:    strategy.IntentWithdraw,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id": tokenID.String(),
			"target":   pmAddr.Hex(),
			"value":    "0",
		},
	}
	burnData, err := adapter.BuildBurnNFTData(burnIntent)
	if err != nil {
		return fmt.Errorf("build burn: %w", err)
	}
	burnIntent.Metadata["calldata"] = hex.EncodeToString(burnData)
	res3, err := ethGw.Send(ctx, burnIntent)
	if err != nil {
		return fmt.Errorf("send burn: %w", err)
	}
	idx3 := 0
	if stepIndex != nil {
		idx3 = *stepIndex
		*stepIndex++
	}
	RecordStepSent(ctx, store, stream, parentIntentID, idx3, "burn", res3.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close"})
	rcpt3 := WaitForReceipt(ctx, ethGw, res3.Hash, 2*time.Minute)
	if rcpt3 == nil || rcpt3.Status != 1 {
		RecordStepFinal(ctx, store, stream, parentIntentID, idx3, "burn", "failed", res3.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close"})
		return fmt.Errorf("burn reverted (hash=%s)", res3.Hash.Hex())
	}
	RecordStepFinal(ctx, store, stream, parentIntentID, idx3, "burn", "mined", res3.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "close", "gas_used": rcpt3.GasUsed})
	return nil
}

// DrainPositionTokenID drains most liquidity but keeps a residual percentage (testnet-only swap helper safety).
func DrainPositionTokenID(ctx context.Context, ethGw *gateway.EthGateway, adapter *univ3.Adapter, pmAddr common.Address, tokenID *big.Int, keepPct float64, store *storage.Store, stream events.Stream, parentIntentID string, stepIndex *int) (bool, error) {
	if keepPct <= 0 {
		return false, nil
	}
	if keepPct >= 1 {
		return true, nil
	}

	_, _, liq, ok, posErr := FetchPositionByTokenID(ctx, ethGw, adapter, pmAddr, tokenID)
	if !ok {
		if posErr != nil {
			msg := posErr.Error()
			if strings.Contains(msg, "execution reverted") || strings.Contains(msg, "not found") {
				log.Printf("[Rebalance] position tokenId=%s not found (already closed?): %v", tokenID.String(), posErr)
				return false, nil
			}
			return false, fmt.Errorf("fetch position tokenId=%s: %w", tokenID.String(), posErr)
		}
		return false, fmt.Errorf("fetch position tokenId=%s failed", tokenID.String())
	}
	if liq == nil || liq.Sign() <= 0 {
		return false, nil
	}

	keep := new(big.Int)
	fKeep := new(big.Float).Mul(new(big.Float).SetInt(liq), big.NewFloat(keepPct))
	fKeep.Int(keep)
	if keep.Sign() < 0 {
		keep.SetInt64(0)
	}
	if keep.Cmp(liq) >= 0 {
		keep.Sub(liq, big.NewInt(1))
	}
	if keep.Sign() < 0 {
		keep.SetInt64(0)
	}
	withdraw := new(big.Int).Sub(liq, keep)
	if withdraw.Sign() <= 0 {
		return true, nil
	}

	log.Printf("[Rebalance] draining position tokenId=%s keepPct=%.3f withdrawLiq=%s keepLiq=%s", tokenID.String(), keepPct, withdraw.String(), keep.String())

	intent := strategy.Intent{
		ID:      fmt.Sprintf("DRAIN_%s", tokenID.String()),
		Type:    strategy.IntentWithdraw,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id":  tokenID.String(),
			"liquidity": withdraw.String(),
			"target":    pmAddr.Hex(),
			"value":     "0",
		},
	}
	data, err := adapter.BuildDecreaseLiquidityData(intent)
	if err != nil {
		return false, fmt.Errorf("build decreaseLiquidity: %w", err)
	}
	intent.Metadata["calldata"] = hex.EncodeToString(data)
	res, err := ethGw.Send(ctx, intent)
	if err != nil {
		return false, fmt.Errorf("send decreaseLiquidity: %w", err)
	}
	idx := 0
	if stepIndex != nil {
		idx = *stepIndex
		*stepIndex++
	}
	RecordStepSent(ctx, store, stream, parentIntentID, idx, "withdraw", res.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "liquidity": withdraw.String(), "mode": "drain"})
	rcpt := WaitForReceipt(ctx, ethGw, res.Hash, 2*time.Minute)
	if rcpt == nil || rcpt.Status != 1 {
		RecordStepFinal(ctx, store, stream, parentIntentID, idx, "withdraw", "failed", res.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "drain"})
		return false, fmt.Errorf("decreaseLiquidity reverted (hash=%s)", res.Hash.Hex())
	}
	RecordStepFinal(ctx, store, stream, parentIntentID, idx, "withdraw", "mined", res.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "drain", "gas_used": rcpt.GasUsed})

	collectIntent := strategy.Intent{
		ID:      fmt.Sprintf("COLLECT_DRAIN_%s", tokenID.String()),
		Type:    strategy.IntentCollectFee,
		PoolID:  "",
		ChainID: ethGw.ChainID().Int64(),
		Metadata: map[string]string{
			"token_id":  tokenID.String(),
			"recipient": ethGw.Address(),
			"target":    pmAddr.Hex(),
			"value":     "0",
		},
	}
	collectData, err := adapter.BuildCollectData(collectIntent)
	if err != nil {
		return false, fmt.Errorf("build collect: %w", err)
	}
	collectIntent.Metadata["calldata"] = hex.EncodeToString(collectData)
	res2, err := ethGw.Send(ctx, collectIntent)
	if err != nil {
		return false, fmt.Errorf("send collect: %w", err)
	}
	idx2 := 0
	if stepIndex != nil {
		idx2 = *stepIndex
		*stepIndex++
	}
	RecordStepSent(ctx, store, stream, parentIntentID, idx2, "collect", res2.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "drain"})
	rcpt2 := WaitForReceipt(ctx, ethGw, res2.Hash, 2*time.Minute)
	if rcpt2 == nil || rcpt2.Status != 1 {
		RecordStepFinal(ctx, store, stream, parentIntentID, idx2, "collect", "failed", res2.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "drain"})
		return false, fmt.Errorf("collect reverted (hash=%s)", res2.Hash.Hex())
	}
	RecordStepFinal(ctx, store, stream, parentIntentID, idx2, "collect", "mined", res2.Hash.Hex(), map[string]interface{}{"token_id": tokenID.String(), "mode": "drain", "gas_used": rcpt2.GasUsed})
	return true, nil
}
