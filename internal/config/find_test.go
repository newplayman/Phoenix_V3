package config

import "testing"

func TestFindPool(t *testing.T) {
	cfg := &AppConfig{
		Pools: []PoolConfig{
			{ID: "a"},
			{ID: "b"},
		},
	}
	if _, ok := FindPool(cfg, ""); ok {
		t.Fatal("expected not found for empty id")
	}
	if _, ok := FindPool(cfg, "c"); ok {
		t.Fatal("expected not found for missing id")
	}
	if p, ok := FindPool(cfg, "b"); !ok || p.ID != "b" {
		t.Fatalf("expected pool b, got ok=%v id=%q", ok, p.ID)
	}
}
