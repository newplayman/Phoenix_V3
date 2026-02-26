package advisory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Generator periodically generates risk advisories and writes them to disk.
// Phase 6+: This is READ-ONLY. It NEVER writes to control.json.
type Generator struct {
	cfg          AdvisoryConfig
	statsPath    string
	advisoryPath string
	stateManager *AdvisoryStateManager
}

func NewGenerator(cfg AdvisoryConfig, statsPath, advisoryPath, stateDir string) *Generator {
	g := &Generator{
		cfg:          cfg,
		statsPath:    statsPath,
		advisoryPath: advisoryPath,
	}
	if stringsTrimSpace(stateDir) != "" {
		g.stateManager = NewAdvisoryStateManager(stateDir)
		_ = g.stateManager.Load()
	}
	return g
}

func (g *Generator) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = g.tick()
		}
	}
}

func (g *Generator) tick() error {
	stats, err := readStatsSnapshot(g.statsPath)
	if err != nil {
		return err
	}
	adv := GenerateAdvisory(g.cfg, stats, time.Now().UTC(), g.stateManager)
	return writeJSONAtomicFile(g.advisoryPath, adv, 0o644)
}

func readStatsSnapshot(path string) (StatsSnapshot, error) {
	var out StatsSnapshot
	b, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(b, &out)
}

func writeJSONAtomicFile(path string, v any, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
