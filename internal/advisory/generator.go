package advisory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"phoenix-v3/internal/riskcontrol"
)

// Generator periodically generates risk advisories and writes them to disk.
// Phase 6.0: This is READ-ONLY. It NEVER writes to control.json.
type Generator struct {
	cfg        AdvisoryConfig
	stats      *riskcontrol.RiskStats
	outputPath string
	auditPath  string
}

// NewGenerator creates a new advisory generator.
func NewGenerator(cfg AdvisoryConfig, stats *riskcontrol.RiskStats, outputPath, auditPath string) *Generator {
	return &Generator{
		cfg:        cfg,
		stats:      stats,
		outputPath: outputPath,
		auditPath:  auditPath,
	}
}

// Run starts the advisory generation loop. Blocks until context is cancelled.
func (g *Generator) Run(ctx context.Context) error {
	if !g.cfg.Enabled {
		return nil
	}

	// Generate initial advisory immediately
	if err := g.generateAndWrite(); err != nil {
		fmt.Fprintf(os.Stderr, "[advisory] initial generation failed: %v\n", err)
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := g.generateAndWrite(); err != nil {
				fmt.Fprintf(os.Stderr, "[advisory] generation failed: %v\n", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *Generator) generateAndWrite() error {
	now := time.Now()

	// Get current stats snapshot
	snapshot := g.stats.Snapshot(now)

	// Convert to advisory StatsSnapshot format
	advStats := StatsSnapshot{
		TotalEvaluations:         snapshot.TotalEvaluations,
		VerdictCounts:            snapshot.VerdictCounts,
		RejectCountsByRuleID:     snapshot.RejectCountsByRuleID,
		SkipReasons:              snapshot.SkipReasons,
		CooldownRejectCountByKey: snapshot.CooldownRejectCountByKey,
		TimeMismatchSkipCount:    snapshot.TimeMismatchSkipCount,
		TimeMismatchSkipRate:     snapshot.TimeMismatchSkipRate,
	}
	advStats.PriceDivergence.MaxDeviationBps = snapshot.PriceDivergence.MaxDeviationBps
	advStats.PriceDivergence.P95DeviationBps = snapshot.PriceDivergence.P95DeviationBps

	// Generate advisory
	advisory := GenerateAdvisory(g.cfg, advStats, now)

	// Write to output file (atomic)
	if err := g.writeAdvisory(advisory); err != nil {
		return fmt.Errorf("write advisory: %w", err)
	}

	// Append to audit trail
	if err := g.appendAudit(advisory); err != nil {
		return fmt.Errorf("append audit: %w", err)
	}

	return nil
}

// writeAdvisory writes the advisory to disk atomically (tmp + rename).
// Phase 6.0: This ONLY writes to var/risk_advisory.json, NEVER control.json.
func (g *Generator) writeAdvisory(adv RiskAdvisory) error {
	if g.outputPath == "" {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(g.outputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(adv, "", "  ")
	if err != nil {
		return err
	}

	// Write to temp file
	tmp := g.outputPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}

	// Atomic rename
	if err := os.Rename(tmp, g.outputPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return nil
}

// appendAudit appends a lightweight audit entry to the audit trail.
func (g *Generator) appendAudit(adv RiskAdvisory) error {
	if g.auditPath == "" {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(g.auditPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Create audit entry (lightweight)
	entry := map[string]interface{}{
		"ts_ms":               adv.TsMS,
		"suggested_risk_mode": adv.SuggestedRiskMode,
		"confidence":          adv.Confidence,
		"reasons":             adv.Reasons,
		"evidence_summary": map[string]interface{}{
			"total_evaluations":       adv.Evidence.TotalEvaluations,
			"reject_rate":             adv.Evidence.RejectRate,
			"time_mismatch_skip_rate": adv.Evidence.TimeMismatchSkipRate,
			"max_deviation_bps":       adv.Evidence.MaxDeviationBps,
		},
	}

	// Marshal to single-line JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	// Append to file
	f, err := os.OpenFile(g.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}

// GetLatestAdvisory reads the latest advisory from disk.
func GetLatestAdvisory(path string) (*RiskAdvisory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var adv RiskAdvisory
	if err := json.Unmarshal(data, &adv); err != nil {
		return nil, err
	}

	return &adv, nil
}
