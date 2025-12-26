package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	contractv1 "shared/contracts/contract/v1"
)

type dumpLine struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type enums struct {
	IntentType      []contractv1.IntentType      `json:"intent_type"`
	RiskLevel       []contractv1.RiskLevel       `json:"risk_level"`
	ExecutionStatus []contractv1.ExecutionStatus `json:"execution_status"`
	ErrorKind       []contractv1.ErrorKind       `json:"error_kind"`
	Mode            []contractv1.Mode            `json:"mode"`
	State           []contractv1.State           `json:"state"`
	SchemaVersionV1 string                       `json:"schema_version"`
}

func main() {
	var jsonl bool
	flag.BoolVar(&jsonl, "jsonl", false, "emit JSONL only")
	flag.Parse()

	now := time.Now()
	intent := contractv1.IntentV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      "intent_example_1",
		IntentType:    contractv1.IntentTypeRebalanceV3,
		TsLocalMS:     now.UnixMilli(),
		DryRun:        true,
		TTLms:         0,
		Params: map[string]any{
			"pool":              "v3",
			"tick_lower":        int64(200000),
			"tick_upper":        int64(202000),
			"target_lower_tick": int64(200060),
			"target_upper_tick": int64(202060),
			"reason":            "near_edge",
		},
		Fields: map[string]any{
			"summary": "near_edge cur=[200000,202000] tick=201000 new=[200060,202060]",
		},
		Summary: "near_edge cur=[200000,202000] tick=201000 new=[200060,202060]",
	}
	riskDecision := contractv1.RiskDecisionV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         "run_example_1",
		TsLocalMS:     now.UnixMilli(),
		Level:         contractv1.RiskLevelSafe,
		Reasons:       []string{},
		Fields:        map[string]string{},
		CooldownMS:    0,
	}
	execResult := contractv1.ExecutorResultV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      intent.IntentID,
		TsLocalMS:     now.UnixMilli(),
		Status:        contractv1.ExecutionStatusSimulated,
		ErrorKind:     contractv1.ErrorKindNone,
		Receipt: map[string]any{
			"simulated":       true,
			"would_broadcast": false,
			"tx_hash":         "0xSIMULATED_intent_example_1",
		},
	}
	status := contractv1.StatusV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         "run_example_1",
		Mode:          contractv1.ModeDryRun,
		State:         contractv1.StateRunning,
		UptimeSec:     123,
		LastIntent: &contractv1.StatusIntentV1{
			IntentType: intent.IntentType,
			IntentID:   intent.IntentID,
			TsLocalMS:  intent.TsLocalMS,
			Summary:    intent.Summary,
			Fields:     intent.Fields,
		},
		LastRisk: &contractv1.StatusRiskV1{
			Level:        riskDecision.Level,
			TsLocalMS:    riskDecision.TsLocalMS,
			ReasonsCount: len(riskDecision.Reasons),
		},
		LastExec: &contractv1.StatusExecV1{
			Status:    execResult.Status,
			TsLocalMS: execResult.TsLocalMS,
			ErrorKind: execResult.ErrorKind,
		},
	}
	enumList := enums{
		IntentType:      []contractv1.IntentType{contractv1.IntentTypeRebalanceV3, contractv1.IntentTypeCexMake, contractv1.IntentTypeArbitrage, contractv1.IntentTypeHedgePerp},
		RiskLevel:       []contractv1.RiskLevel{contractv1.RiskLevelSafe, contractv1.RiskLevelDeny, contractv1.RiskLevelPause, contractv1.RiskLevelSafeMode, contractv1.RiskLevelHalt},
		ExecutionStatus: []contractv1.ExecutionStatus{contractv1.ExecutionStatusSimulated, contractv1.ExecutionStatusSubmitted, contractv1.ExecutionStatusPartiallyFilled, contractv1.ExecutionStatusFilled, contractv1.ExecutionStatusCanceled, contractv1.ExecutionStatusFailed},
		ErrorKind:       []contractv1.ErrorKind{contractv1.ErrorKindNone, contractv1.ErrorKindTransient, contractv1.ErrorKindAuth, contractv1.ErrorKindRateLimit, contractv1.ErrorKindInsufficientBalance, contractv1.ErrorKindBadParams, contractv1.ErrorKindUnknown},
		Mode:            []contractv1.Mode{contractv1.ModeDryRun, contractv1.ModeShadow, contractv1.ModeLive, contractv1.ModePaper},
		State:           []contractv1.State{contractv1.StateRunning, contractv1.StatePaused, contractv1.StateSafeMode, contractv1.StateError},
		SchemaVersionV1: contractv1.SchemaVersion,
	}

	if err := validate(intent, riskDecision, execResult, status); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if jsonl {
		emitJSONLine(dumpLine{Type: "IntentV1", Data: intent})
		emitJSONLine(dumpLine{Type: "RiskDecisionV1", Data: riskDecision})
		emitJSONLine(dumpLine{Type: "ExecutorResultV1", Data: execResult})
		emitJSONLine(dumpLine{Type: "StatusV1", Data: status})
		emitJSONLine(dumpLine{Type: "Enums", Data: enumList})
		return
	}

	fmt.Println("IntentV1:")
	emitPretty(intent)
	fmt.Println("RiskDecisionV1:")
	emitPretty(riskDecision)
	fmt.Println("ExecutorResultV1:")
	emitPretty(execResult)
	fmt.Println("StatusV1:")
	emitPretty(status)
	fmt.Println("Enums:")
	emitPretty(enumList)
}

func validate(intent contractv1.IntentV1, risk contractv1.RiskDecisionV1, exec contractv1.ExecutorResultV1, status contractv1.StatusV1) error {
	if intent.SchemaVersion != contractv1.SchemaVersion {
		return fmt.Errorf("IntentV1.schema_version must be %q", contractv1.SchemaVersion)
	}
	if risk.SchemaVersion != contractv1.SchemaVersion {
		return fmt.Errorf("RiskDecisionV1.schema_version must be %q", contractv1.SchemaVersion)
	}
	if exec.SchemaVersion != contractv1.SchemaVersion {
		return fmt.Errorf("ExecutorResultV1.schema_version must be %q", contractv1.SchemaVersion)
	}
	if status.SchemaVersion != contractv1.SchemaVersion {
		return fmt.Errorf("StatusV1.schema_version must be %q", contractv1.SchemaVersion)
	}
	return nil
}

func emitPretty(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}

func emitJSONLine(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
