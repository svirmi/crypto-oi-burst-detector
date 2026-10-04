Here is a complete, copy-pasteable Markdown technical task written specifically for a **Go (Golang)** development team.

***

```markdown
# Technical Task: Multi-Exchange Symbol Discovery & Overlap Resolver Microservice

**Language / Stack:** Go (Golang 1.22+)  
**Target Exchanges:** Binance Futures (USDⓈ-M), Bybit V5 (Linear), MEXC Futures  
**Component Name:** `symbol-resolver`  
**Purpose:** Dynamically fetch all active USDT perpetual futures symbols across Binance, Bybit, and MEXC, normalize their ticker formats, compute the exact overlapping set (set intersection), and expose the mapped pair data via REST API.

---

## 1. Functional Requirements

1. **Concurrent Exchange Polling:** On startup (and periodically via background ticker), fetch active contract metadata concurrently from Binance, Bybit, and MEXC using `goroutines` and `context.WithTimeout`.
2. **Filtering & Normalization:**
   * Filter only **Active / Trading Perpetual Contracts** margined in **USDT**.
   * Normalize exchange-specific raw tickers into a unified canonical format (e.g., `BTC-USDT`).
3. **Set Intersection Logic:** Compute the set intersection:
   \\[\text{Overlap} = \text{Set}(\text{Binance}) \cap \text{Set}(\text{Bybit}) \cap \text{Set}(\text{MEXC})\\]
4. **Symbol Mapping Store:** Maintain an in-memory thread-safe map (`sync.RWMutex`) that translates the canonical symbol (`BTC-USDT`) back to each exchange’s raw ticker (`BTCUSDT` vs. `BTC_USDT`).
5. **REST API Endpoint:** Expose `/api/v1/symbols/overlapping` returning the current active overlap list, individual exchange symbol counts, and metadata mapping.

---

## 2. API Endpoint Specifications to Consume

### 2.1 Binance Futures (USDⓈ-M)
* **URL:** `GET https://fapi.binance.com/fapi/v1/exchangeInfo`
* **Filter Conditions:**
  * `status == "TRADING"`
  * `quoteAsset == "USDT"`
  * `contractType == "PERPETUAL"`
* **Raw Ticker Field:** `symbol` (e.g., `"BTCUSDT"`)
* **Canonical Mapping:** Strip quote currency or split by base asset (`baseAsset`: `"BTC"`, `quoteAsset`: `"USDT"`) \\(\rightarrow\\) `"BTC-USDT"`.

### 2.2 Bybit V5 (Linear Perpetuals)
* **URL:** `GET https://api.bybit.com/v5/market/instruments-info?category=linear`
* **Filter Conditions:**
  * `status == "Trading"`
  * `quoteCoin == "USDT"`
  * `contractType == "LinearPerpetual"`
* **Raw Ticker Field:** `symbol` (e.g., `"BTCUSDT"`)
* **Canonical Mapping:** `baseCoin` + `"-"` + `quoteCoin` \\(\rightarrow\\) `"BTC-USDT"`.

### 2.3 MEXC Futures
* **URL:** `GET https://contract.mexc.com/api/v1/contract/detail`
* **Filter Conditions:**
  * `state == 0` (Active trading status)
  * `quoteCoin == "USDT"`
* **Raw Ticker Field:** `symbol` (e.g., `"BTC_USDT"`)
* **Canonical Mapping:** Replace `"_"` with `"-"` \\(\rightarrow\\) `"BTC-USDT"`.

---

## 3. Recommended Go Architecture & Package Structure

```text
cmd/
  └── symbol-resolver/
      └── main.go
internal/
  ├── client/
  │   ├── binance.go       # Binance REST client
  │   ├── bybit.go         # Bybit V5 REST client
  │   ├── mexc.go          # MEXC REST client
  │   └── client.go        # ExchangeClient interface
  ├── model/
  │   └── symbol.go        # Canonical and raw symbol struct definitions
  ├── resolver/
  │   └── service.go       # Intersection logic and RWMutex state store
  └── handler/
      └── http.go          # HTTP HTTP/REST Handlers
```

---

## 4. Key Data Structures (Go Code Spec)

### 4.1 Canonical Symbol Models (`internal/model/symbol.go`)

```go
package model

import "time"

// CanonicalSymbol represents the unified internal symbol format
type CanonicalSymbol string // e.g. "BTC-USDT"

// SymbolMapping holds the raw ticker representations for each exchange
type SymbolMapping struct {
	Canonical CanonicalSymbol `json:"canonical_symbol"` // "BTC-USDT"
	BaseAsset string          `json:"base_asset"`       // "BTC"
	QuoteAsset string         `json:"quote_asset"`      // "USDT"
	RawBinance string         `json:"binance_symbol"`   // "BTCUSDT"
	RawBybit   string         `json:"bybit_symbol"`     // "BTCUSDT"
	RawMexc    string         `json:"mexc_symbol"`      // "BTC_USDT"
}

// OverlapResponse represents the API response payload
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

## 5. Concurrent Fetch & Intersection Logic

The core service must fetch all 3 exchanges concurrently using `golang.org/x/sync/errgroup` with a 5-second timeout budget:

```go
package resolver

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"symbol-resolver/internal/client"
	"symbol-resolver/internal/model"

	"golang.org/x/sync/errgroup"
)

type SymbolResolverService struct {
	clients []client.ExchangeClient
	mu      sync.RWMutex
	current *model.OverlapResponse
}

func NewSymbolResolverService(clients []client.ExchangeClient) *SymbolResolverService {
	return &SymbolResolverService{
		clients: clients,
	}
}

func (s *SymbolResolverService) Refresh(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	
	results := make([]map[model.CanonicalSymbol]string, len(s.clients))

	for i, cl := range s.clients {
		i, cl := i, cl
		g.Go(func() error {
			symbols, err := cl.FetchActiveUSDTSymbols(ctx)
			if err != nil {
				return fmt.Errorf("failed fetching from %s: %w", cl.Name(), err)
			}
			results[i] = symbols
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	binanceMap := results
	bybitMap := results
	mexcMap := results

	var overlapping []model.SymbolMapping
	counts := make(map[string]int)
	counts["binance"] = len(binanceMap)
	counts["bybit"] = len(bybitMap)
	counts["mexc"] = len(mexcMap)

	// Compute Intersection
	for canonical, binanceRaw := range binanceMap {
		bybitRaw, inBybit := bybitMap[canonical]
		mexcRaw, inMexc := mexcMap[canonical]

		if inBybit && inMexc {
			overlapping = append(overlapping, model.SymbolMapping{
				Canonical:  canonical,
				BaseAsset:  string(canonical[:len(canonical)-5]), // e.g. "BTC" from "BTC-USDT"
				QuoteAsset: "USDT",
				RawBinance: binanceRaw,
				RawBybit:   bybitRaw,
				RawMexc:    mexcRaw,
			})
		}
	}

	sort.Slice(overlapping, func(i, j int) bool {
		return overlapping[i].Canonical < overlapping[j].Canonical
	})

	resp := &model.OverlapResponse{
		UpdatedAt:        time.Now().UTC(),
		TotalOverlapping: len(overlapping),
		ExchangeCounts:   counts,
		Symbols:          overlapping,
	}

	s.mu.Lock()
	s.current = resp
	s.mu.Unlock()

	return nil
}

func (s *SymbolResolverService) GetOverlap() *model.OverlapResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}
```

---

## 6. HTTP API Endpoint Specification

### `GET /api/v1/symbols/overlapping`

* **Response Code:** `200 OK`
* **Content-Type:** `application/json`
* **Sample Output:**

```json
{
  "updated_at": "2026-09-26T03:30:00Z",
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

---

## 7. Operational Details & Background Refresh Worker

1. **Startup Check:** On service initialization, execute `Refresh(ctx)` synchronously once before starting the HTTP listener. If any exchange fails to respond, log a warning and retry up to 3 times with exponential backoff.
2. **Periodic Cron Worker:** Run a `time.Ticker` every **1 hour** to refresh symbol mappings automatically (handles new listings and delistings in real time).
3. **HTTP Timeouts:** Configure HTTP client transport with:
   * `Timeout: 5 * time.Second`
   * `MaxIdleConnsPerHost: 10`

---

## 8. Definition of Done (Acceptance Criteria)

* [ ] Unit tests for symbol normalization functions covering edge cases (e.g., 1000PEPE vs PEPE, coin formats).
* [ ] Successful startup returning \\(\ge 100\\) overlapping USDT perpetual pairs across Binance, Bybit, and MEXC.
* [ ] HTTP server exposing `/api/v1/symbols/overlapping` in \\(< 5\text{ms}\\) response time from memory.
* [ ] Graceful degradation: if one exchange endpoint returns a 5xx error during scheduled background refresh, keep serving the existing valid state from `sync.RWMutex`.
```