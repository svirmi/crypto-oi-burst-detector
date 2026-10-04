Here is the complete, production-grade **Technical Specification** for the **`symbol-resolver`** microservice, formatted in Markdown for direct use by your Go development team.

curl [http://localhost:8080/health](http://localhost:8080/health)
curl [http://localhost:8080/api/v1/symbols/overlapping](http://localhost:8080/api/v1/symbols/overlapping)

***

# Technical Specification: `symbol-resolver` Microservice

**Language / Runtime:** Go (Golang 1.22+)  
**Architecture:** Standalone Single Microservice  
**Target Exchanges:** Binance Futures (USDⓈ-M), Bybit V5 (Linear), MEXC Futures  
**Primary Responsibility:** Symbol discovery, ticker format normalization, set intersection calculation, and symbol mapping registry for downstream exchange collectors.

---

## 1. System Overview & Objectives

The `symbol-resolver` microservice acts as the single source of truth for symbol metadata across the cross-exchange Open Interest aggregation platform. 

It dynamically discovers all active USDT perpetual contracts across Binance, Bybit, and MEXC, normalizes exchange-specific naming conventions into a canonical format (`BASE-QUOTE`), computes the exact overlapping pair intersection set, and serves this mapping in-memory to downstream collector services.

---

## 2. Upstream Exchange Specifications

The microservice queries public REST API endpoints from each exchange concurrently using Go `goroutines` and `context.WithTimeout`.

### 2.1 Binance Futures (USDⓈ-M)
* **Endpoint:** `GET https://fapi.binance.com/fapi/v1/exchangeInfo`
* **Filter Criteria:**
  * `status == "TRADING"`
  * `quoteAsset == "USDT"`
  * `contractType == "PERPETUAL"`
* **Raw Ticker Field:** `symbol` (e.g., `"BTCUSDT"`)
* **Canonical Mapping:** `baseAsset` + `"-"` + `quoteAsset` \\(\rightarrow\\) `"BTC-USDT"`

### 2.2 Bybit V5 (Linear Perpetuals)
* **Endpoint:** `GET https://api.bybit.com/v5/market/instruments-info?category=linear`
* **Filter Criteria:**
  * `status == "Trading"`
  * `quoteCoin == "USDT"`
  * `contractType == "LinearPerpetual"`
* **Raw Ticker Field:** `symbol` (e.g., `"BTCUSDT"`)
* **Canonical Mapping:** `baseCoin` + `"-"` + `quoteCoin` \\(\rightarrow\\) `"BTC-USDT"`

### 2.3 MEXC Futures
* **Endpoint:** `GET https://contract.mexc.com/api/v1/contract/detail`
* **Filter Criteria:**
  * `state == 0` (Active trading state)
  * `quoteCoin == "USDT"`
* **Raw Ticker Field:** `symbol` (e.g., `"BTC_USDT"`)
* **Canonical Mapping:** Replace `"_"` with `"-"` \\(\rightarrow\\) `"BTC-USDT"`

---

## 3. Core Logic & Normalization Flow

1. **Concurrent Ingestion:** On trigger (startup or hourly cron), dispatch 3 concurrent HTTP client calls bounded by a 5-second `context.WithTimeout`.
2. **Canonical Transformation:** Normalize each exchange's active pairs into a `map[CanonicalSymbol]string` where key is `"BTC-USDT"` and value is raw exchange symbol (e.g., `"BTC_USDT"`).
3. **Intersection Engine:**
   \\[\text{Overlap Set} = \text{Set}(\text{Binance}) \cap \text{Set}(\text{Bybit}) \cap \text{Set}(\text{MEXC})\\]
4. **Thread-Safe State Update:** Atomically swap the memory cache protected by a `sync.RWMutex`.

---

## 4. Go Data Models & Interfaces

### 4.1 Struct Definitions (`internal/model/symbol.go`)

```go
package model

import "time"

// CanonicalSymbol is the unified internal symbol string (e.g., "BTC-USDT")
type CanonicalSymbol string

// SymbolMapping maps the canonical symbol to each exchange's native raw ticker
type SymbolMapping struct {
	Canonical  CanonicalSymbol `json:"canonical_symbol"` // "BTC-USDT"
	BaseAsset  string          `json:"base_asset"`       // "BTC"
	QuoteAsset string          `json:"quote_asset"`      // "USDT"
	RawBinance string          `json:"binance_symbol"`   // "BTCUSDT"
	RawBybit   string          `json:"bybit_symbol"`     // "BTCUSDT"
	RawMexc    string          `json:"mexc_symbol"`      // "BTC_USDT"
}

// OverlapResponse is the payload returned by the REST API
type OverlapResponse struct {
	UpdatedAt        time.Time       `json:"updated_at"`
	TotalOverlapping int             `json:"total_overlapping"`
	ExchangeCounts   map[string]int  `json:"exchange_counts"`
	Symbols          []SymbolMapping `json:"symbols"`
}
```

### 4.2 Exchange Client Interface (`internal/client/client.go`)

```go
package client

import (
	"context"
	"symbol-resolver/internal/model"
)

type ExchangeClient interface {
	Name() string
	FetchActiveUSDTSymbols(ctx context.Context) (map[model.CanonicalSymbol]string, error)
}
```

---

## 5. API Endpoint Specifications

### 5.1 `GET /api/v1/symbols/overlapping`
Returns the cached list of overlapping perpetual pairs across all three exchanges.

* **Response Status:** `200 OK`
* **Response Header:** `Content-Type: application/json`
* **Sample Payload:**
```json
{
  "updated_at": "2026-09-26T05:00:00Z",
  "total_overlapping": 142,
  "exchange_counts": {
    "binance": 285,
    "bybit": 310,
    "mexc": 420
  },
  "symbols": [
    {
      "canonical_symbol": "BTC-USDT",
      "base_asset": "BTC",
      "quote_asset": "USDT",
      "binance_symbol": "BTCUSDT",
      "bybit_symbol": "BTCUSDT",
      "mexc_symbol": "BTC_USDT"
    },
    {
      "canonical_symbol": "ETH-USDT",
      "base_asset": "ETH",
      "quote_asset": "USDT",
      "binance_symbol": "ETHUSDT",
      "bybit_symbol": "ETHUSDT",
      "mexc_symbol": "ETH_USDT"
    }
  ]
}
```

### 5.2 `GET /health`
Returns health check and system status.
* **Response Status:** `200 OK`
* **Payload:** `{"status": "ok", "last_update": "2026-09-26T05:00:00Z"}`

---

## 6. Service Lifecycle & Resilience Rules

1. **Startup Initialization:**
   * The service must attempt an initial sync synchronously before opening the HTTP server port.
   * If any exchange endpoint fails on startup, perform up to **3 retries with exponential backoff (1s, 2s, 4s)**.
2. **Background Refresh Routine:**
   * Run a background worker using `time.Ticker` set to **1 hour**.
3. **Graceful Degradation:**
   * If a background refresh fails (e.g., MEXC API returns 5xx), log an error metric, **keep serving the previous valid overlapping set from memory**, and retry in 5 minutes. Do not wipe or corrupt the current state cache.
4. **HTTP Transport Tuning:**
   * Standard `http.Client` timeout: `5 * time.Second`.
   * Enable HTTP Keep-Alive and Connection Pooling (`MaxIdleConnsPerHost: 10`).

---

## 7. Definition of Done (Acceptance Criteria)

* [ ] Unit tests covering symbol normalization logic (handling special prefixes like `1000PEPE` or `1000000MOG`).
* [ ] Successful startup resolving \\(\ge 100\\) overlapping USDT perpetual contracts across Binance, Bybit, and MEXC.
* [ ] In-memory response latency for `/api/v1/symbols/overlapping` under **5ms**.
* [ ] Containerized via Dockerfile with image size \\(< 25\text{MB}\\) (using multi-stage build with `scratch` or `alpine`).
