package main

import (
	"testing"
)

func TestNormalizeCSV(t *testing.T) {
	out := normalizeCSV(" ticker, pool_state ,ticker ,, ")
	if len(out) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out))
	}
	if out[0] != "pool_state" || out[1] != "ticker" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestBuildStreams(t *testing.T) {
	out := buildStreams("phoenix", []string{"ticker", "pool_state"}, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0] != "phoenix:pool_state" || out[1] != "phoenix:ticker" {
		t.Fatalf("unexpected streams: %#v", out)
	}
}

func TestTopicFromStream(t *testing.T) {
	if got := topicFromStream("phoenix", "phoenix:ticker"); got != "ticker" {
		t.Fatalf("expected ticker, got %q", got)
	}
	if got := topicFromStream("phoenix", "other:ticker"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

