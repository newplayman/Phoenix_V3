package main

import (
	"encoding/json"
	"fmt"
	"time"

	"phoenix-v3/internal/contracts"
	"phoenix-v3/internal/strategy"
	contractv1 "shared/contracts/contract/v1"
)

func main() {
	runID := "sample_run_phoenix"
	now := time.Now()

	legacy := contracts.Intent{
		ID:      "legacy_intent_1",
		Type:    contracts.IntentRebalanceV3,
		PoolID:  "v3",
		Urgency: 3,
		Metadata: map[string]string{
			"reason":            "near_edge",
			"observed_at":       now.UTC().Format(time.RFC3339Nano),
			"current_tick":      "201000",
			"current_lower":     "200000",
			"current_upper":     "202000",
			"new_lower":         "200060",
			"new_upper":         "202060",
			"new_center_tick":   "201060",
			"width_ticks":       "600",
			"edge_buffer_ticks": "60",
			"tick_spacing":      "60",
		},
	}

	// Chain A: gate blocked -> SKIPPED + non-empty risk reasons.
	intentA := strategy.ToIntentV1FromRebalance(legacy, runID, now, true, 60_000)
	riskA := contractv1.RiskDecisionV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         runID,
		TsLocalMS:     now.UnixMilli(),
		Level:         contractv1.RiskLevelPause,
		Reasons:       []string{"manual_pause"},
		Fields:        map[string]string{"block_reason": "manual_pause"},
		CooldownMS:    0,
	}
	execA := contractv1.ExecutorResultV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      intentA.IntentID,
		TsLocalMS:     now.UnixMilli(),
		Status:        contractv1.ExecutionStatusSkipped,
		ErrorKind:     contractv1.ErrorKindNone,
		Receipt: map[string]any{
			"skipped_reason":  "risk_denied",
			"would_broadcast": false,
		},
	}
	statusA := contractv1.StatusV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         runID,
		Mode:          contractv1.ModeDryRun,
		State:         contractv1.StatePaused,
		UptimeSec:     1,
		LastIntent: &contractv1.StatusIntentV1{
			IntentType: intentA.IntentType,
			IntentID:   intentA.IntentID,
			TsLocalMS:  intentA.TsLocalMS,
			Summary:    intentA.Summary,
			Fields:     intentA.Fields,
		},
		LastRisk: &contractv1.StatusRiskV1{
			Level:        riskA.Level,
			TsLocalMS:    riskA.TsLocalMS,
			ReasonsCount: len(riskA.Reasons),
		},
		LastExec: &contractv1.StatusExecV1{
			Status:    execA.Status,
			TsLocalMS: execA.TsLocalMS,
			ErrorKind: execA.ErrorKind,
		},
	}
	emit(intentA)
	emit(riskA)
	emit(execA)
	emit(statusA)

	// Chain B: ttl expired -> SKIPPED(intent_expired).
	intentB := intentA
	intentB.TsLocalMS = now.Add(-5 * time.Second).UnixMilli()
	intentB.TTLms = 1000
	riskB := contractv1.RiskDecisionV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         runID,
		TsLocalMS:     now.UnixMilli(),
		Level:         contractv1.RiskLevelSafe,
		Reasons:       []string{},
		Fields:        map[string]string{},
		CooldownMS:    0,
	}
	execB := contractv1.ExecutorResultV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      intentB.IntentID,
		TsLocalMS:     now.UnixMilli(),
		Status:        contractv1.ExecutionStatusSkipped,
		ErrorKind:     contractv1.ErrorKindNone,
		Receipt: map[string]any{
			"skipped_reason": "intent_expired",
			"ttl_ms":         intentB.TTLms,
		},
	}
	statusB := statusA
	statusB.State = contractv1.StateRunning
	statusB.LastIntent = &contractv1.StatusIntentV1{
		IntentType: intentB.IntentType,
		IntentID:   intentB.IntentID,
		TsLocalMS:  intentB.TsLocalMS,
		Summary:    intentB.Summary,
		Fields:     intentB.Fields,
	}
	statusB.LastRisk = &contractv1.StatusRiskV1{Level: riskB.Level, TsLocalMS: riskB.TsLocalMS, ReasonsCount: 0}
	statusB.LastExec = &contractv1.StatusExecV1{Status: execB.Status, TsLocalMS: execB.TsLocalMS, ErrorKind: execB.ErrorKind}
	emit(intentB)
	emit(riskB)
	emit(execB)
	emit(statusB)
}

func emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
