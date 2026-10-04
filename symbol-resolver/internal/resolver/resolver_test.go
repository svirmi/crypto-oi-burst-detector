package resolver

import (
	"testing"

	"symbol-resolver/internal/model"
)

// helper to build a canonical symbol map from a slice of raw symbols
// following the same normalization each exchange client would apply
func makeMap(pairs [][2]string) map[model.CanonicalSymbol]string {
	m := make(map[model.CanonicalSymbol]string)
	for _, p := range pairs {
		m[model.CanonicalSymbol(p[0])] = p[1]
	}
	return m
}

// --- Compute tests ---

func TestCompute_BasicIntersection(t *testing.T) {
	binance := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
		{"ETH-USDT", "ETHUSDT"},
		{"SOL-USDT", "SOLUSDT"},
	})
	bybit := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
		{"ETH-USDT", "ETHUSDT"},
	})
	mexc := makeMap([][2]string{
		{"BTC-USDT", "BTC_USDT"},
		{"ETH-USDT", "ETH_USDT"},
	})

	result := Compute(binance, bybit, mexc)

	if result.TotalSymbols != 2 {
		t.Errorf("expected 2 overlapping symbols, got %d", result.TotalSymbols)
	}
}

func TestCompute_NoIntersection(t *testing.T) {
	binance := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
	})
	bybit := makeMap([][2]string{
		{"ETH-USDT", "ETHUSDT"},
	})
	mexc := makeMap([][2]string{
		{"SOL-USDT", "SOL_USDT"},
	})

	result := Compute(binance, bybit, mexc)

	if result.TotalSymbols != 0 {
		t.Errorf("expected 0 overlapping symbols, got %d", result.TotalSymbols)
	}
	if result.Symbols == nil {
		t.Error("expected empty slice, got nil")
	}
}

func TestCompute_AllExchangesMatch(t *testing.T) {
	binance := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
		{"ETH-USDT", "ETHUSDT"},
		{"SOL-USDT", "SOLUSDT"},
	})
	bybit := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
		{"ETH-USDT", "ETHUSDT"},
		{"SOL-USDT", "SOLUSDT"},
	})
	mexc := makeMap([][2]string{
		{"BTC-USDT", "BTC_USDT"},
		{"ETH-USDT", "ETH_USDT"},
		{"SOL-USDT", "SOL_USDT"},
	})

	result := Compute(binance, bybit, mexc)

	if result.TotalSymbols != 3 {
		t.Errorf("expected 3 overlapping symbols, got %d", result.TotalSymbols)
	}
}

func TestCompute_EmptyExchange(t *testing.T) {
	binance := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
	})
	bybit := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
	})
	mexc := map[model.CanonicalSymbol]string{} // empty

	result := Compute(binance, bybit, mexc)

	if result.TotalSymbols != 0 {
		t.Errorf("expected 0 overlapping symbols, got %d", result.TotalSymbols)
	}
}

func TestCompute_ExchangeCountsAreCorrect(t *testing.T) {
	binance := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
		{"ETH-USDT", "ETHUSDT"},
	})
	bybit := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
		{"ETH-USDT", "ETHUSDT"},
		{"SOL-USDT", "SOLUSDT"},
	})
	mexc := makeMap([][2]string{
		{"BTC-USDT", "BTC_USDT"},
	})

	result := Compute(binance, bybit, mexc)

	if result.ExchangeCounts["binance"] != 2 {
		t.Errorf("expected binance count 2, got %d", result.ExchangeCounts["binance"])
	}
	if result.ExchangeCounts["bybit"] != 3 {
		t.Errorf("expected bybit count 3, got %d", result.ExchangeCounts["bybit"])
	}
	if result.ExchangeCounts["mexc"] != 1 {
		t.Errorf("expected mexc count 1, got %d", result.ExchangeCounts["mexc"])
	}
}

func TestCompute_RawSymbolsArePreserved(t *testing.T) {
	binance := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
	})
	bybit := makeMap([][2]string{
		{"BTC-USDT", "BTCUSDT"},
	})
	mexc := makeMap([][2]string{
		{"BTC-USDT", "BTC_USDT"},
	})

	result := Compute(binance, bybit, mexc)

	if len(result.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(result.Symbols))
	}

	sym := result.Symbols[0]

	if sym.RawBinance != "BTCUSDT" {
		t.Errorf("expected RawBinance BTCUSDT, got %s", sym.RawBinance)
	}
	if sym.RawBybit != "BTCUSDT" {
		t.Errorf("expected RawBybit BTCUSDT, got %s", sym.RawBybit)
	}
	if sym.RawMexc != "BTC_USDT" {
		t.Errorf("expected RawMexc BTC_USDT, got %s", sym.RawMexc)
	}
}

func TestCompute_UpdatedAtIsSet(t *testing.T) {
	result := Compute(
		map[model.CanonicalSymbol]string{},
		map[model.CanonicalSymbol]string{},
		map[model.CanonicalSymbol]string{},
	)

	if result.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set, got zero value")
	}
}

// --- Edge case: high multiplier prefix symbols (DoD requirement) ---

func TestCompute_HighMultiplierPrefixSymbols(t *testing.T) {
	binance := makeMap([][2]string{
		{"1000PEPE-USDT", "1000PEPEUSDT"},
		{"1000000MOG-USDT", "1000000MOGUSDT"},
	})
	bybit := makeMap([][2]string{
		{"1000PEPE-USDT", "1000PEPEUSDT"},
		{"1000000MOG-USDT", "1000000MOGUSDT"},
	})
	mexc := makeMap([][2]string{
		{"1000PEPE-USDT", "1000PEPE_USDT"},
		{"1000000MOG-USDT", "1000000MOG_USDT"},
	})

	result := Compute(binance, bybit, mexc)

	if result.TotalSymbols != 2 {
		t.Errorf("expected 2 overlapping symbols for high multiplier pairs, got %d", result.TotalSymbols)
	}

	found := make(map[model.CanonicalSymbol]bool)
	for _, s := range result.Symbols {
		found[s.Canonical] = true
	}

	if !found["1000PEPE-USDT"] {
		t.Error("expected 1000PEPE-USDT in intersection, not found")
	}
	if !found["1000000MOG-USDT"] {
		t.Error("expected 1000000MOG-USDT in intersection, not found")
	}
}

// --- splitCanonical tests ---

func TestSplitCanonical_Standard(t *testing.T) {
	base, quote := splitCanonical("BTC-USDT")
	if base != "BTC" {
		t.Errorf("expected base BTC, got %s", base)
	}
	if quote != "USDT" {
		t.Errorf("expected quote USDT, got %s", quote)
	}
}

func TestSplitCanonical_HighMultiplierPrefix(t *testing.T) {
	base, quote := splitCanonical("1000PEPE-USDT")
	if base != "1000PEPE" {
		t.Errorf("expected base 1000PEPE, got %s", base)
	}
	if quote != "USDT" {
		t.Errorf("expected quote USDT, got %s", quote)
	}
}

func TestSplitCanonical_MissingDash(t *testing.T) {
	base, quote := splitCanonical("BTCUSDT")
	if base != "BTCUSDT" {
		t.Errorf("expected base BTCUSDT, got %s", base)
	}
	if quote != "" {
		t.Errorf("expected empty quote, got %s", quote)
	}
}
