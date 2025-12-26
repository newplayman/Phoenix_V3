package feed

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type PriceAggregatorConfig struct {
	Symbol        string
	StaleWindow   time.Duration
	FreezeWindow  time.Duration
	DivergencePct float64
	LogInterval   time.Duration
	PriceMode     string // ws_only | ws_with_rest_fallback
}

func DefaultPriceAggregatorConfig() PriceAggregatorConfig {
	return PriceAggregatorConfig{
		Symbol:        "ETH/USDT",
		StaleWindow:   5 * time.Second,
		FreezeWindow:  20 * time.Second,
		DivergencePct: 0.003,
		LogInterval:   7 * time.Second,
		PriceMode:     "ws_only",
	}
}

func LoadPriceAggregatorConfigFromEnv() PriceAggregatorConfig {
	cfg := DefaultPriceAggregatorConfig()

	if v := strings.TrimSpace(os.Getenv("PRICE_SYMBOL")); v != "" {
		cfg.Symbol = v
	}
	if sec := parseEnvInt("PRICE_STALE_SEC", int(cfg.StaleWindow/time.Second)); sec > 0 {
		cfg.StaleWindow = time.Duration(sec) * time.Second
	}
	if sec := parseEnvInt("PRICE_FREEZE_SEC", int(cfg.FreezeWindow/time.Second)); sec > 0 {
		cfg.FreezeWindow = time.Duration(sec) * time.Second
	}
	if v := parseEnvFloat("DIVERGENCE_PCT", cfg.DivergencePct); v > 0 {
		cfg.DivergencePct = v
	}
	if v := parseEnvInt("PRICE_LOG_SEC", int(cfg.LogInterval/time.Second)); v > 0 {
		cfg.LogInterval = time.Duration(v) * time.Second
	}

	// Hard switch: WS-only by default. REST polling must be explicitly enabled.
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("PRICE_MODE")))
	switch mode {
	case "", "ws_only":
		cfg.PriceMode = "ws_only"
	case "ws_with_rest_fallback":
		cfg.PriceMode = "ws_with_rest_fallback"
	default:
		cfg.PriceMode = "ws_only"
	}

	// Backward-compat: accept the old env as an alias for ws_with_rest_fallback.
	if strings.TrimSpace(os.Getenv("PRICE_ENABLE_REST_FALLBACK")) == "1" {
		cfg.PriceMode = "ws_with_rest_fallback"
	}
	return cfg
}

func (c PriceAggregatorConfig) WSOnly() bool {
	return strings.ToLower(strings.TrimSpace(c.PriceMode)) != "ws_with_rest_fallback"
}

func parseEnvInt(key string, def int) int {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func parseEnvFloat(key string, def float64) float64 {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

type SymbolSpec struct {
	Display string
	Binance string
	OKX     string
}

func NormalizeSymbol(input string) SymbolSpec {
	raw := strings.ToUpper(strings.TrimSpace(input))
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		raw = "ETH/USDT"
	}

	var base, quote string
	if strings.Contains(raw, "/") {
		parts := strings.SplitN(raw, "/", 2)
		base, quote = parts[0], parts[1]
	} else if strings.Contains(raw, "-") {
		parts := strings.SplitN(raw, "-", 2)
		base, quote = parts[0], parts[1]
	} else if len(raw) >= 6 {
		// Best-effort: assume quote is USDT.
		if strings.HasSuffix(raw, "USDT") {
			base, quote = strings.TrimSuffix(raw, "USDT"), "USDT"
		} else {
			base, quote = raw[:3], raw[3:]
		}
	}
	if base == "" || quote == "" {
		base, quote = "ETH", "USDT"
	}

	display := base + "/" + quote
	return SymbolSpec{
		Display: display,
		Binance: strings.ToLower(base + quote),
		OKX:     base + "-" + quote,
	}
}
