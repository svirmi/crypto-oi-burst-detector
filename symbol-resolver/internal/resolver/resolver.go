package resolver

import (
	"sort"
	"strings"
	"time"

	"symbol-resolver/internal/model"
)

// Compute takes the active USDT symbol maps from all three exchanges,
// calculates the intersection, and returns a SymbolIntersection snapshot
func Compute(
	binance map[model.CanonicalSymbol]string,
	bybit map[model.CanonicalSymbol]string,
	mexc map[model.CanonicalSymbol]string,
) *model.SymbolIntersection {

	exchangeCounts := map[string]int{
		"binance": len(binance),
		"bybit":   len(bybit),
		"mexc":    len(mexc),
	}

	symbols := make([]model.SymbolMapping, 0)

	for canonical, rawBinance := range binance {
		rawBybit, inBybit := bybit[canonical]
		if !inBybit {
			continue
		}

		rawMexc, inMexc := mexc[canonical]
		if !inMexc {
			continue
		}

		base, quote := splitCanonical(canonical)

		symbols = append(symbols, model.SymbolMapping{
			Canonical:  canonical,
			BaseAsset:  base,
			QuoteAsset: quote,
			RawBinance: rawBinance,
			RawBybit:   rawBybit,
			RawMexc:    rawMexc,
		})
	}
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Canonical < symbols[j].Canonical
	})

	return &model.SymbolIntersection{
		UpdatedAt:      time.Now().UTC(),
		TotalSymbols:   len(symbols),
		ExchangeCounts: exchangeCounts,
		Symbols:        symbols,
	}
}

// splitCanonical splits a canonical symbol (e.g. "BTC-USDT")
// into base ("BTC") and quote ("USDT") components
func splitCanonical(canonical model.CanonicalSymbol) (base, quote string) {
	parts := strings.SplitN(string(canonical), "-", 2)
	if len(parts) != 2 {
		return string(canonical), ""
	}
	return parts[0], parts[1]
}
