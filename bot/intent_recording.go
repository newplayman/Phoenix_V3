package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"gorm.io/datatypes"

	"phoenix-v3/internal/events"
	"phoenix-v3/internal/storage"
	"phoenix-v3/internal/strategy"
)

func UpsertIntentStatus(store *storage.Store, intent strategy.Intent, status string) {
	if store == nil {
		return
	}
	metaJSON, _ := json.Marshal(intent.Metadata)
	_ = store.UpsertIntentRecord(&storage.IntentRecord{
		IntentID:        intent.ID,
		PoolID:          intent.PoolID,
		ChainID:         intent.ChainID,
		Type:            string(intent.Type),
		Status:          status,
		RiskMode:        intent.RiskMode,
		StrategyVersion: intent.StrategyVersion,
		Metadata:        datatypes.JSON(metaJSON),
	})
}

func RecordStepSent(ctx context.Context, store *storage.Store, stream events.Stream, intentID string, stepIndex int, stepType string, txHash string, details map[string]interface{}) {
	if store != nil {
		b, _ := json.Marshal(details)
		_ = store.CreateIntentStep(&storage.IntentStepRecord{
			IntentID:  intentID,
			StepType:  stepType,
			StepIndex: stepIndex,
			Status:    "sent",
			TxHash:    txHash,
			Details:   datatypes.JSON(b),
		})
	}
	if stream != nil {
		_ = stream.Publish(ctx, events.TopicIntentExec, map[string]interface{}{
			"type":       "step_update",
			"intent_id":  intentID,
			"step_index": stepIndex,
			"step_type":  stepType,
			"status":     "sent",
			"tx_hash":    txHash,
			"details":    details,
		})
	}
}

func RecordStepFinal(ctx context.Context, store *storage.Store, stream events.Stream, intentID string, stepIndex int, stepType string, status string, txHash string, details map[string]interface{}) {
	if store != nil {
		b, _ := json.Marshal(details)
		_ = store.UpdateIntentStep(intentID, stepIndex, status, txHash, datatypes.JSON(b))
	}
	if stream != nil {
		_ = stream.Publish(ctx, events.TopicIntentExec, map[string]interface{}{
			"type":       "step_update",
			"intent_id":  intentID,
			"step_index": stepIndex,
			"step_type":  stepType,
			"status":     status,
			"tx_hash":    txHash,
			"details":    details,
		})
	}
}

func RecordStepSimulated(ctx context.Context, store *storage.Store, stream events.Stream, intentID string, stepIndex int, stepType string, txHash string, details map[string]interface{}) {
	if store != nil {
		b, _ := json.Marshal(details)
		_ = store.CreateIntentStep(&storage.IntentStepRecord{
			IntentID:  intentID,
			StepType:  stepType,
			StepIndex: stepIndex,
			Status:    "simulated",
			TxHash:    txHash,
			Details:   datatypes.JSON(b),
		})
	}
	if stream != nil {
		_ = stream.Publish(ctx, events.TopicIntentExec, map[string]interface{}{
			"type":       "step_update",
			"intent_id":  intentID,
			"step_index": stepIndex,
			"step_type":  stepType,
			"status":     "simulated",
			"tx_hash":    txHash,
			"details":    details,
		})
	}
}

func WeiToEther(gasPrice *big.Int, gasUsed uint64) float64 {
	if gasPrice == nil || gasUsed == 0 {
		return 0
	}
	wei := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasUsed))
	fWei, _ := new(big.Float).SetInt(wei).Float64()
	return fWei / 1e18
}

func SimulatedTxHash(stepIndex int, intentID string, prefix string) string {
	if prefix == "" {
		prefix = "SIMULATED"
	}
	return fmt.Sprintf("0x%s_%d_%s", prefix, stepIndex, intentID)
}
