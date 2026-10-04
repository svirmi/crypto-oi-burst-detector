package model

import "time"

// CanonicalSymbol is the unified internal symbol string (e.g., "BTC-USDT")
type CanonicalSymbol string

// SymbolMapping maps the canonical symbol to each exchange's native raw ticker
type SymbolMapping struct {
	Canonical  CanonicalSymbol `json:"canonical_symbol"`
	BaseAsset  string          `json:"base_asset"`
	QuoteAsset string          `json:"quote_asset"`
	RawBinance string          `json:"binance_symbol"`
	RawBybit   string          `json:"bybit_symbol"`
	RawMexc    string          `json:"mexc_symbol"`
}

// SymbolIntersection holds the computed overlap of active USDT perpetual
// contracts across all three exchanges at a given point in time
type SymbolIntersection struct {
	UpdatedAt      time.Time       `json:"updated_at"`
	TotalSymbols   int             `json:"total_symbols"`
	ExchangeCounts map[string]int  `json:"exchange_counts"`
	Symbols        []SymbolMapping `json:"symbols"`
}
