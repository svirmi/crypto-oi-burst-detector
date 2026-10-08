# Technical Specification: Open Interest Collectors Microservices (Pure REST 5s Architecture)

**Language / Runtime:** Go (Golang 1.22+)  
**Architecture:** B2 — Three independent collector microservices + shared internal library  
**Target Exchanges:** Binance Futures (USDⓈ-M), Bybit V5 (Linear), MEXC Futures  
**Transport Strategy:** 100% Pure REST API polling on a strict 5-second interval  
**Primary Responsibility:** Poll Open Interest and Price data per exchange, normalize values to USD, align timestamps to 5-second time buckets, and write time-series ticks into Redis Sorted Sets for downstream signal engine (`signal-engine`) analysis.

---

## 1. System Overview

Each collector operates as a standalone, stateless Go microservice responsible for a single exchange. All collectors share core models, resolver clients, and Redis ring-buffer writers through a `shared/` internal Go package.

Collectors form the middle ingestion layer in the data pipeline:

```text
                        ┌────────────────────────┐
                        │    symbol-resolver     │
                        └───────────┬────────────┘
                                    │ (GET /symbols/overlapping)
            ┌───────────────────────┼───────────────────────┐
            ▼                       ▼                       ▼
┌───────────────────────┐ ┌───────────────────┐ ┌───────────────────────┐
│   collector-binance   │ │  collector-bybit  │ │     collector-mexc    │
│  (REST: 5s Stagger)   │ │  (REST: 5s Bulk)  │ │   (REST: 5s Bulk)     │
└───────────┬───────────┘ └─────────┬─────────┘ └───────────┬───────────┘
            │                       │                       │
            └───────────────────────┼───────────────────────┘
                                    ▼ (Quantized 5s Buckets)
                        ┌────────────────────────┐
                        │ Redis Sorted Sets 5s   │
                        │ (ticks:5s:{symbol}:{e})│
                        └───────────┬────────────┘
                                    ▼
                        ┌────────────────────────┐
                        │     signal-engine      │
                        │ (Calculates Z_oi & T)  │
                        └────────────────────────┘
```

---

## 2. Repository Structure

```text
collectors/
├── shared/
│   ├── go.mod
│   ├── model/
│   │   └── tick.go            # Tick5s struct, Get5sBucketTimestamp() helper
│   ├── resolver/
│   │   └── client.go          # HTTP client for symbol-resolver
│   ├── redis/
│   │   └── writer.go          # Redis ZSET writer, ZREMRANGEBYSCORE 300s ring buffer
│   └── normalize/
│       └── oi.go              # USD normalization formulas per exchange
├── binance/
│   ├── go.mod
│   ├── Dockerfile
│   ├── main.go
│   └── internal/collector/
│       └── collector.go       # Binance 5s staggered REST polling worker
├── bybit/
│   ├── go.mod
│   ├── Dockerfile
│   ├── main.go
│   └── internal/collector/
│       └── collector.go       # Bybit 5s bulk REST polling worker
└── mexc/
    ├── go.mod
    ├── Dockerfile
    ├── main.go
    └── internal/collector/
        └── collector.go       # MEXC 5s bulk REST polling worker
```

---

## 3. Symbol List Acquisition

1. **Startup Synchronization:**
   * On startup, each collector queries `symbol-resolver` at `GET http://symbol-resolver:8080/api/v1/symbols/overlapping`.
   * **Retry Policy:** Up to 3 retries with exponential backoff (1s → 2s → 4s). On persistent failure, exit with fatal log.
2. **Hourly Refresh:**
   * A background `time.Ticker` refreshes the symbol mapping every 1 hour to handle new listings and delistings automatically.
3. **Symbol Mapping:**
   * Each collector filters for its exchange-specific raw ticker string (`binance_symbol`, `bybit_symbol`, `mexc_symbol`) mapped against the unified `canonical_symbol` (e.g., `"BTC-USDT"`).

---

## 4. Exchange-Specific Polling Strategy (Pure REST 5s)

### 4.1 Binance Futures (USDⓈ-M)
* **Endpoints:** 
  * Open Interest: `GET https://fapi.binance.com/fapi/v1/openInterest?symbol={symbol}` (1 weight per symbol)
  * Bulk Price: `GET https://fapi.binance.com/fapi/v1/ticker/price` (2 weight)
* **Polling Interval:** **5 seconds**
* **Execution Pattern:** Staggered per-symbol worker pool. For 150 overlapping symbols across a 5-second window, issue 1 request every **~33ms** ($5000\text{ms} / 150$), combined with 1 bulk price fetch per cycle.
* **Rate Limit Budget:**  
  $$\text{OI Weight}: 150 \text{ symbols} / 5\text{s} = 30 \text{ req/s} = 1,800 \text{ weight/min}$$  
  $$\text{Price Weight}: 12 \text{ bulk req/min} \times 2 = 24 \text{ weight/min}$$  
  $$\text{Total}: 1,824 \text{ weight/min} \quad (\approx 76\% \text{ of Binance's } 2,400 \text{ weight/min quota})$$

### 4.2 Bybit V5 (Linear Perpetuals)
* **Endpoint:** `GET https://api.bybit.com/v5/market/tickers?category=linear`
* **Polling Interval:** **5 seconds**
* **Execution Pattern:** Single bulk GET request returning Open Interest (`singleOpenInterestValue`) and `lastPrice` for all linear symbols in a single payload.
* **Rate Limit Budget:** 12 requests/minute ($< 10\%$ of Bybit's 120+ req/min rate limit).

### 4.3 MEXC Futures
* **Endpoint:** `GET https://contract.mexc.com/api/v1/contract/ticker`
* **Polling Interval:** **5 seconds**
* **Execution Pattern:** Single bulk GET request returning `holdVol` and `lastPrice` for all active contracts.
* **Metadata Cache:** `contractSize` is fetched once on startup via `GET /api/v1/contract/detail` and cached in memory per symbol.
* **Rate Limit Budget:** 12 requests/minute (negligible impact against MEXC's 300 req/min quota).

---

## 5. OI Normalization & Time Quantization

### 5.1 Time Quantization (5s Raster)
All incoming timestamps are bucketed to a strict 5-second raster to guarantee clean cross-exchange alignment in Redis:

$$\text{BucketTimestamp} = \left\lfloor \frac{\text{UnixTimestamp}_{\text{sec}}}{5} \right\rfloor \times 5$$

### 5.2 Open Interest USD Normalization
* **Binance:** $\text{OI}_{\text{USD}} = \text{openInterest} \times \text{lastPrice}$
* **Bybit:** $\text{OI}_{\text{USD}} = \text{singleOpenInterestValue}$ (or $\text{openInterestValue} / 2$)
* **MEXC:** $\text{OI}_{\text{USD}} = \text{holdVol} \times \text{contractSize} \times \text{lastPrice}$

---

## 6. Redis Data Model & Ring Buffer

### 6.1 Key Schema
Keys incorporate the explicit 5-second sampling resolution:
`ticks:5s:{canonical_symbol}:{exchange}`

*Examples:*
* `ticks:5s:BTC-USDT:binance`
* `ticks:5s:BTC-USDT:bybit`
* `ticks:5s:BTC-USDT:mexc`

### 6.2 Data Structure
* **Type:** Redis Sorted Set (ZSET)
* **Score:** `BucketTimestamp` (Unix timestamp in seconds, aligned to 5s)
* **Member:** Compact JSON-encoded `Tick5s` struct

```go
// shared/model/tick.go
type Tick5s struct {
    Timestamp       int64   `json:"t"`      // Unix timestamp (bucketized to 5s)
    Symbol          string  `json:"s"`      // Canonical symbol ("BTC-USDT")
    Exchange        string  `json:"e"`      // "binance", "bybit", "mexc"
    Price           float64 `json:"p"`      // Last price in USD
    OpenInterestRaw float64 `json:"oi_raw"` // Raw exchange OI
    OpenInterestUSD float64 `json:"oi_usd"` // Normalized USD value
    Volume          float64 `json:"v"`      // Volume (24h or 5s delta)
}
```

### 6.3 Ring Buffer Maintenance (300-Second Rolling Window)
Every write operation executes a pipeline containing an insertion and an atomic cleanup of ticks older than 300 seconds (5 minutes = 60 ticks per symbol):

```text
ZADD ticks:5s:BTC-USDT:binance <BucketTimestamp> <json_payload>
ZREMRANGEBYSCORE ticks:5s:BTC-USDT:binance -inf (<BucketTimestamp> - 300)
EXPIRE ticks:5s:BTC-USDT:binance 600
```

---

## 7. Shared Package Implementation (`collectors/shared/`)

```go
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"collectors/shared/model"
)

type RingBufferWriter struct {
	rdb *redis.Client
}

func NewRingBufferWriter(rdb *redis.Client) *RingBufferWriter {
	return &RingBufferWriter{rdb: rdb}
}

// Get5sBucketTimestamp aligns a given time to the nearest 5-second boundary
func Get5sBucketTimestamp(t time.Time) int64 {
	return (t.Unix() / 5) * 5
}

// WriteTick writes a 5s tick and cleans entries older than 300 seconds
func (w *RingBufferWriter) WriteTick(ctx context.Context, tick model.Tick5s) error {
	key := fmt.Sprintf("ticks:5s:%s:%s", tick.Symbol, tick.Exchange)

	payload, err := json.Marshal(tick)
	if err != nil {
		return fmt.Errorf("marshal tick error: %w", err)
	}

	pipe := w.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(tick.Timestamp),
		Member: payload,
	})

	// Remove entries older than 300 seconds (5 minutes)
	minScore := "-inf"
	maxScore := fmt.Sprintf("(%d", tick.Timestamp-300)
	pipe.ZRemRangeBYSCORE(ctx, key, minScore, maxScore)
	pipe.Expire(ctx, key, 10*time.Minute)

	_, err = pipe.Exec(ctx)
	return err
}
```

---

## 8. Service Lifecycle & Resilience Rules

1. **Stateless Retries:** If an HTTP poll request fails due to temporary network error or exchange 5xx response, log the warning, skip writing the single tick, and retry on the next 5s ticker cycle.
2. **Symbol Safety Valve:**
   * If overlapping symbols $\le 150$: Maintain standard 5-second polling interval (1,800 weight/min for Binance).
   * If overlapping symbols expand to $151 - 180$: Automatically scale polling interval to 6 seconds to keep Binance weight $< 2,100 / \text{min}$.
   * If overlapping symbols $> 180$: Activate volume-tiered polling (top 100 volume pairs at 5s, remaining at 10s).
3. **Graceful Shutdown:** Intercept `SIGINT`/`SIGTERM` to allow in-flight HTTP requests to finish before container exit.

---

## 9. Definition of Done (Acceptance Criteria)

* [ ] All 3 collectors (`collector-binance`, `collector-bybit`, `collector-mexc`) poll exclusively via Pure REST at 5-second intervals.
* [ ] All written tick timestamps are properly quantized to 5-second boundaries (`t % 5 == 0`).
* [ ] Redis Sorted Sets use key format `ticks:5s:{canonical}:{exchange}` and maintain exactly 60 entries (300 seconds).
* [ ] Binance stagger pool confirmed to stay under 1,824 IP weight/minute for 150 symbols.
* [ ] Containerized via multi-stage Dockerfiles with final image size $< 25\text{MB}$.
