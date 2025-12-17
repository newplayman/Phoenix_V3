package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	t.Fatalf("repo root not found from %s", wd)
	return ""
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestDocsContractOrderbookRawIsDocumented(t *testing.T) {
	root := findRepoRoot(t)
	spec := readFile(t, filepath.Join(root, "docs", "marketdata", "ORDERBOOK_RAW_SPEC.md"))

	for _, n := range []string{
		OrderbookSnapshotType,
		OrderbookDeltaType,
		"seq_gap",
		"best_bid",
		"best_ask",
		"spread",
	} {
		if !strings.Contains(spec, n) {
			t.Fatalf("docs/api/API_AND_EVENT_SPEC.md missing %q (orderbook raw contract)", n)
		}
	}
}
