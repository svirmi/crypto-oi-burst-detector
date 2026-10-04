# Technical Specification: OI Collectors Microservices

**Language / Runtime:** Go (Golang 1.26+)\
**Architecture:** B2 — Three independent collector services + shared internal library\
**Target Exchanges:** Binance Futures (USDⓈ-M), Bybit V5 (Linear), MEXC Futures\
**Primary Responsibility:** Poll Open Interest and Price data per exchange, normalize to USD, and write time-series ticks into Redis for downstream burst detection.

---

## 1. System Overview

Each collector is a standalone Go microservice responsible for a single exchange. All three collectors share common logic via a `shared/` internal package within the `collectors/` directory.

Collectors sit between `symbol-resolver` and the burst detector in the data pipeline:

```
symbol-resolver  →  [collector-binance]  →  Redis
                 →  [collector-bybit]    →  Redis
                 →  [collector-mexc]     →  Redis
                                              ↓
                                       burst-detector
```

---

## 2. Repository Structure

```
collectors/
├── shared/
│   ├── go.mod
│   ├── model/
│   │   └── tick.go            # OITick, shared data structs
│   ├── resolver/
│   │   └── client.go          # HTTP client for symbol-resolver
│   ├── redis/
│   │   └── writer.go          # Redis sorted set writer, TTL logic
│   └── normalize/
│       └── oi.go              # USD normalization formulas per exchange
├── binance/
│   ├── go.mod
│   ├── Dockerfile
│   ├── main.go
│   └── internal/
│       └── collector/
│           └── collector.go   # Binance-specific fetch + poll loop
├── bybit/
│   ├── go.mod
│   ├── Dockerfile
│   ├── main.go
│   └── internal/
│       └── collector/
│           └── collector.go   # Bybit-specific fetch + poll loop
└── mexc/
    ├── go.mod
    ├── Dockerfile
    ├── main.go
    └── internal/
        └── collector/
            └── collector.go   # MEXC-specific fetch + poll loop
```

---

## 3. Symbol List Acquisition

On startup, each collector fetches the overlapping symbol list from `symbol-resolver`:

- **Endpoint:** `GET http://symbol-resolver:8080/api/v1/symbols/overlapping`
- **On failure:** Retry up to 3 times with exponential backoff (1s → 2s → 4s). If all retries fail, exit with fatal log — a collector without a symbol list cannot function.
- **Refresh:** Re-fetch the symbol list every **1 hour** via background ticker to handle new listings and delistings automatically.
- The collector uses only the raw ticker field relevant to its exchange (`binance_symbol`, `bybit_symbol`, or `mexc_symbol`) from the `SymbolMapping` response.

---

## 4. Exchange-Specific Polling Strategy

> ⚠️ **Important:** The polling intervals below are based on observed behavior and third-party research. They **must be confirmed against each exchange's official API documentation** before going to production. Polling too frequently wastes rate limit quota; polling too infrequently loses resolution for burst detection.

### 4.1 Binance Futures (USDⓈ-M)

| Property | Value |
| --- | --- |
| OI Endpoint | `GET https://fapi.binance.com/fapi/v1/openInterest?symbol={symbol}` |
| Price Endpoint | `GET https://fapi.binance.com/fapi/v1/ticker/price` (bulk) |
| OI Update Frequency | ⚠️ \~15–20 seconds (unconfirmed — verify against Binance docs) |
| Poll Interval | **15 seconds** |
| Pattern | Per-symbol OI requests, staggered + one bulk price request |
| IP Weight per OI req | 1 |
| IP Weight per price bulk | 2 |
| IP Weight Quota | 2,400 / minute |

**Polling logic:**\
Binance does not offer a bulk OI endpoint. Each symbol requires an individual request. With \~120 symbols and a 15-second poll cycle:

```
120 symbols / 15s = 8 OI requests/s = 480 weight/min (OI)
+ 1 price bulk req / 15s = 4 weight/min (price)
Total: ~484 weight/min (~20% of 2,400 quota)
```

Requests are staggered evenly across the 15-second window — not fired in a burst.

### 4.2 Bybit V5 (Linear Perpetuals)

| Property | Value |
| --- | --- |
| Endpoint | `GET https://api.bybit.com/v5/market/tickers?category=linear` |
| OI Update Frequency | ⚠️ \~1–3 seconds (unconfirmed — verify against Bybit docs) |
| Poll Interval | **3 seconds** |
| Pattern | Single bulk request returns all symbols |
| OI Fields | `openInterestValue` (double-sided), `singleOpenInterestValue` (one-sided USD) |

Bybit provides a single bulk ticker endpoint that returns OI for all linear symbols in one request. No per-symbol polling required.

### 4.3 MEXC Futures

| Property | Value |
| --- | --- |
| Endpoint | `GET https://contract.mexc.com/api/v1/contract/ticker` |
| OI Update Frequency | ⚠️ \~1–3 seconds (unconfirmed — verify against MEXC docs) |
| Poll Interval | **3 seconds** |
| Pattern | Single bulk request returns all symbols |
| OI Field | `holdVol` (contract sheets — requires normalization to USD) |
| Rate Limit | 10 requests / 2 seconds |

MEXC provides a single bulk ticker endpoint with no symbol parameter required.

---

## 5. OI Normalization to USD

All three exchanges report OI in different units. The `shared/normalize` package converts each to a unified USD value before writing to Redis.

### 5.1 Binance

- **Raw field:** `openInterest` (base asset units, one-sided)
- **Formula:**

```
OI_USD = openInterest × lastPrice
```

### 5.2 Bybit

- **Raw field:** `singleOpenInterestValue` (already in USD, one-sided)
- **Formula:**

```
OI_USD = singleOpenInterestValue  (direct use, no calculation needed)
```

- **Alternative:** `openInterestValue / 2` if `singleOpenInterestValue` is unavailable.

### 5.3 MEXC

- **Raw field:** `holdVol` (contract sheets, one-sided)
- **Formula:**

```
OI_USD = holdVol × contractSize × lastPrice
```

- `contractSize` is fetched once from `GET /api/v1/contract/detail` at startup and cached in memory per symbol.

---

## 6. Redis Data Model

### 6.1 Key Schema

```
oi:{canonical}:{exchange}
```

Examples:

```
oi:BTC-USDT:binance
oi:BTC-USDT:bybit
oi:BTC-USDT:mexc
oi:ETH-USDT:binance
```

### 6.2 Data Structure

**Type:** Redis Sorted Set\
**Score:** Unix timestamp in milliseconds\
**Value:** JSON-encoded `OITick`

```go
// shared/model/tick.go
type OITick struct {
    Canonical    string  `json:"canonical"`      // "BTC-USDT"
    Exchange     string  `json:"exchange"`       // "binance"
    RawSymbol    string  `json:"raw_symbol"`     // "BTCUSDT"
    OIRaw        float64 `json:"oi_raw"`         // raw exchange value
    OIRawUnit    string  `json:"oi_raw_unit"`    // "base_asset" | "contract_sheets" | "usd"
    OIUSD        float64 `json:"oi_usd"`         // normalized USD value
    Price        float64 `json:"price"`          // last price in USD
    TimestampMs  int64   `json:"timestamp_ms"`   // Unix ms
}
```

### 6.3 Ring Buffer (TTL Strategy)

After every write, remove entries older than 300 seconds:

```
ZADD  oi:BTC-USDT:binance  <timestamp_ms>  <json_payload>
ZREMRANGEBYSCORE  oi:BTC-USDT:binance  0  <now_ms - 300000>
```

This maintains exactly 300 seconds of history per symbol per exchange — the ring buffer the burst detector queries.

---

## 7. Shared Package Responsibilities (`collectors/shared/`)

| Package | Responsibility |
| --- | --- |
| `shared/model` | `OITick` struct and any other shared data types |
| `shared/resolver` | HTTP client to fetch `SymbolIntersection` from `symbol-resolver` |
| `shared/redis` | Sorted set writer, `ZADD` + `ZREMRANGEBYSCORE` ring buffer logic |
| `shared/normalize` | USD normalization formulas for Binance, Bybit, MEXC |

The `shared/` package is a Go module imported by each collector via `replace` directive in their `go.mod`.

---

## 8. Service Lifecycle

Each collector follows this lifecycle:

```
1. Connect to Redis — fatal exit if unavailable
2. Fetch symbol list from symbol-resolver — retry 3x with backoff, fatal exit on failure
3. (MEXC only) Fetch contract sizes from /api/v1/contract/detail — cache in memory
4. Start poll loop at exchange-specific interval
5. Start background symbol refresh ticker (1 hour)
6. Listen for SIGINT/SIGTERM — graceful shutdown
```

### Graceful Degradation

- If a single poll cycle fails (exchange returns 5xx or timeout), log the error and continue — do **not** wipe Redis data.
- If 5 consecutive poll cycles fail, log a critical alert and back off to 2× the normal interval before retrying.
- If the symbol-resolver hourly refresh fails, keep the existing symbol list and retry in 5 minutes.

---

## 9. docker-compose Integration

```yaml
services:
  collector-binance:
    build:
      context: ./collectors/binance
      dockerfile: Dockerfile
    environment:
      - SYMBOL_RESOLVER_URL=http://symbol-resolver:8080
      - REDIS_URL=redis:6379
    depends_on:
      - symbol-resolver
      - redis
    restart: unless-stopped

  collector-bybit:
    build:
      context: ./collectors/bybit
      dockerfile: Dockerfile
    environment:
      - SYMBOL_RESOLVER_URL=http://symbol-resolver:8080
      - REDIS_URL=redis:6379
    depends_on:
      - symbol-resolver
      - redis
    restart: unless-stopped

  collector-mexc:
    build:
      context: ./collectors/mexc
      dockerfile: Dockerfile
    environment:
      - SYMBOL_RESOLVER_URL=http://symbol-resolver:8080
      - REDIS_URL=redis:6379
    depends_on:
      - symbol-resolver
      - redis
    restart: unless-stopped

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    restart: unless-stopped
```

---

## 10. Definition of Done (Acceptance Criteria)

- [ ] Polling intervals confirmed against official Binance, Bybit, and MEXC API documentation
- [ ] All three collectors successfully write `OITick` records to Redis within expected poll intervals
- [ ] Redis ring buffer maintains exactly 300 seconds of history per symbol per exchange
- [ ] OI USD normalization values verified against exchange-provided USD figures where available
- [ ] Graceful degradation tested: single exchange outage does not affect other collectors
- [ ] `collector-binance` stagger logic confirmed to stay within 20% of Binance IP weight quota
- [ ] Symbol list refresh tested: new symbol appears in collector within 1 hour of appearing in `symbol-resolver`
- [ ] All three collectors containerized via multi-stage Dockerfile with image size < 25MB
