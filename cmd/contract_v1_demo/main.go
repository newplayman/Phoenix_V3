package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"phoenix-v3/internal/control/filecontrol"
	"phoenix-v3/internal/obs/v1jsonl"
	contractv1 "shared/contracts/contract/v1"
)

func main() {
	runID := "demo_run_phoenix"
	if v := strings.TrimSpace(os.Getenv("RUN_ID")); v != "" {
		runID = v
	}
	now := time.Now()

	w := v1jsonl.NewWriter("var/contract_v1.jsonl")
	write := func(typ string, data any) {
		if err := w.WriteEvent(typ, data); err != nil {
			fmt.Fprintln(os.Stderr, "v1jsonl write:", err.Error())
		}
	}

	ctrl, _, err := filecontrol.NewLoader("var/control.json").LoadIfChanged(now)
	if err != nil {
		ctrl = filecontrol.Default()
	}

	intent := contractv1.IntentV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      runID + "_intent_1",
		IntentType:    contractv1.IntentTypeRebalanceV3,
		TsLocalMS:     now.UnixMilli(),
		DryRun:        true,
		TTLms:         60_000,
		Params:        map[string]any{"pool": "v3"},
		Fields:        map[string]any{"demo": true},
		Summary:       "demo intent",
	}
	write("IntentV1", intent)

	level := contractv1.RiskLevelSafe
	reasons := []string{}
	skippedReason := ""

	ctrlDesired := strings.TrimSpace(ctrl.DesiredState)
	ctrlRisk := strings.TrimSpace(ctrl.RiskMode)
	if ctrlRisk != "" && ctrlRisk != "SAFE" {
		skippedReason = "control_risk_mode_override"
		reasons = append(reasons, "control_risk_mode_override", "risk_mode="+ctrlRisk)
		switch ctrlRisk {
		case "DENY":
			level = contractv1.RiskLevelDeny
		case "PAUSE":
			level = contractv1.RiskLevelPause
		case "SAFE_MODE":
			level = contractv1.RiskLevelSafeMode
		case "HALT":
			level = contractv1.RiskLevelHalt
		}
	}
	if ctrlDesired == "PAUSED" {
		level = contractv1.RiskLevelPause
		skippedReason = "control_paused"
		reasons = []string{"control_desired_state", "desired_state=PAUSED"}
	}
	if ctrlDesired == "SAFE_MODE" {
		level = contractv1.RiskLevelSafeMode
		skippedReason = "control_safe_mode"
		reasons = []string{"control_desired_state", "desired_state=SAFE_MODE"}
	}

	risk := contractv1.RiskDecisionV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         runID,
		TsLocalMS:     now.UnixMilli(),
		Level:         level,
		Reasons:       reasons,
		Fields:        map[string]string{"source": "control.json", "reason": ctrl.Reason},
		CooldownMS:    0,
	}
	write("RiskDecisionV1", risk)

	execStatus := contractv1.ExecutionStatusSimulated
	if skippedReason != "" {
		execStatus = contractv1.ExecutionStatusSkipped
	}
	exec := contractv1.ExecutorResultV1{
		SchemaVersion: contractv1.SchemaVersion,
		IntentID:      intent.IntentID,
		TsLocalMS:     now.UnixMilli(),
		Status:        execStatus,
		ErrorKind:     contractv1.ErrorKindNone,
		Receipt: map[string]any{
			"would_broadcast": false,
		},
	}
	if skippedReason != "" {
		exec.Receipt["skipped_reason"] = skippedReason
	}
	write("ExecutorResultV1", exec)

	state := contractv1.StateRunning
	if level == contractv1.RiskLevelPause {
		state = contractv1.StatePaused
	}
	if level == contractv1.RiskLevelSafeMode || level == contractv1.RiskLevelHalt || level == contractv1.RiskLevelDeny {
		state = contractv1.StateSafeMode
	}
	status := contractv1.StatusV1{
		SchemaVersion: contractv1.SchemaVersion,
		RunID:         runID,
		Mode:          contractv1.ModeDryRun,
		State:         state,
		UptimeSec:     1,
		LastIntent:    &contractv1.StatusIntentV1{IntentType: intent.IntentType, IntentID: intent.IntentID, TsLocalMS: intent.TsLocalMS, Summary: intent.Summary, Fields: intent.Fields},
		LastRisk:      &contractv1.StatusRiskV1{Level: risk.Level, TsLocalMS: risk.TsLocalMS, ReasonsCount: len(risk.Reasons)},
		LastExec:      &contractv1.StatusExecV1{Status: exec.Status, TsLocalMS: exec.TsLocalMS, ErrorKind: exec.ErrorKind},
	}
	write("StatusV1", status)
}
