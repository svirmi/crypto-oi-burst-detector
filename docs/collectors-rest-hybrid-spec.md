```markdown
# Technical Specification: OI Collectors

**Component:** `oi-collectors`
**Language:** Go 1.26
**Part of:** `crypto-oi-burst-detector` monorepo
**Exchanges:** Binance Futures (USDⓈ-M), Bybit V5 (Linear), MEXC Futures

---

## 1. Overview

Three independent collector microservices poll Open Interest and Price data
from Binance, Bybit, and MEXC. Each collector writes normalized time-series
ticks into Redis for downstream consumption by the signal engine.

```
symbol-resolver
      |
      v
collector-binance ──┐
collector-bybit   ──┼──> Redis (sorted sets, 300s ring buffer)
collector-mexc    ──┘         |
                              v
                       signal-engine
```

Each collector is an independent Docker container. If one crashes,
the others continue unaffected.

---

## 2. Repository Structure

One Go module. Three independent binaries. Shared internal packages.

```
collectors/
├── go.mod                         # module: oi-collectors
├── go.sum
├── Dockerfile
├── Makefile
│
├── cmd/
│   ├── collector-binance/
│   │   └── main.go
│   ├── collector-bybit/
│   │   └── main.go
│   └── collector-mexc/
│       └── main.go
│
└── internal/
    ├── model/
    │   └── tick.go                # OITick struct
    ├── config/
    │   └── config.go              # env-based config per collector
    ├── resolver/
    │   └── client.go              # fetches symbol list from symbol-resolver
    ├── redis/
    │   └── writer.go              # sorted set writer, ring buffer logic
    ├── normalize/
    │   └── oi.go                  # USD normalization per exchange
    ├── health/
    │   └── health.go              # /livez /readyz /metrics handlers
    └── exchange/
        ├── binance/
        │   ├── ws_price.go        # WebSocket !ticker@arr price listener
        │   └── rest_oi.go         # per-symbol OI REST poller
        ├── bybit/
        │   └── rest.go            # bulk REST ticker
        └── mexc/
            ├── rest.go            # bulk REST ticker
            └── contract.go        # contractSize cache from /contract/detail
```

Build commands:

```bash
go build ./cmd/collector-binance
go build ./cmd/collector-bybit
go build ./cmd/collector-mexc
```

---

## 3. Symbol List

All collectors load their symbol list from `symbol-resolver` on startup.

- **Endpoint:** `GET http://symbol-resolver:8080/api/v1/symbols/overlapping`
- **Expected symbols:** ~448 overlapping USDT perpetual contracts
- **Refresh:** every 1 hour via background ticker
- **On startup failure:** retry indefinitely with exponential backoff
  (1s → 2s → 4s → cap at 30s). Expose `/readyz` as not-ready until loaded.
- **On refresh failure:** keep previous symbol list, log warning,
  increment metric, retry next hour.

Each collector uses only the field relevant to its exchange:
- `collector-binance` → `binance_symbol` (e.g. `"BTCUSDT"`)
- `collector-bybit`   → `bybit_symbol`   (e.g. `"BTCUSDT"`)
- `collector-mexc`    → `mexc_symbol`    (e.g. `"BTC_USDT"`)

---

## 4. Collector: Binance

Binance does not offer a bulk OI endpoint and does not publish OI
via WebSocket. The Binance collector uses a hybrid strategy:

```
Worker 1 — WebSocket price stream (real-time, all symbols, one connection)
Worker 2 — REST OI poller (staggered, 20s cycle, all symbols)

On each OI response:
    OI_USD = openInterest × current_price_from_Worker1
    write tick to Redis
```

### 4.1 WebSocket Price Stream

- **URL:** `wss://fstream.binance.com/ws/!ticker@arr`
- One persistent connection streams price updates for all USDT futures symbols.
- On each message: update in-memory `map[string]float64` (raw symbol → price)
  protected by `sync.RWMutex`.
- **Reconnect:** on any disconnect, reconnect with exponential backoff
  (1s → 2s → 4s → cap at 30s). Never stop attempting reconnect.
- **Heartbeat:** respond to Binance ping frames within 10 minutes
  (Go `nhooyr.io/websocket` or `gorilla/websocket` handles this automatically).
- **Collector is not ready** if WebSocket has never connected successfully.

### 4.2 REST OI Poller

- **Endpoint:** `GET https://fapi.binance.com/fapi/v1/openInterest?symbol={symbol}`
- **IP Weight:** 1 per request
- **Cycle interval:** 20 seconds for all 448 symbols

Rate limit budget at 20s cycle:

```
448 symbols / 20s = 22.4 req/s
22.4 × 60 = 1344 weight/min
1344 / 2400 = 56% of quota ✅
```

⚠️ Confirm 2400 weight/min quota against current Binance API documentation
before going to production.

Staggering: requests are spread evenly across the 20s window using a
token bucket rate limiter (`golang.org/x/time/rate`):

```go
// 22 requests/sec with burst of 5
limiter := rate.NewLimiter(rate.Limit(22), 5)

for _, symbol := range symbols {
    if err := limiter.Wait(ctx); err != nil {
        return
    }
    go fetchOI(ctx, symbol)
}
```

Do not batch or fire all 448 requests simultaneously.

**On each OI response:**

```go
price := priceMap.Get(symbol)  // from WebSocket Worker 1
if price <= 0 {
    // price not yet available, skip this symbol this cycle
    continue
}
oiUSD := openInterest * price
writeTick(canonical, oiUSD, openInterest, price)
```

**On request failure:** skip symbol for this cycle, log warning,
increment `collector_poll_errors_total`. Do not crash.

**On HTTP 429:** honor `Retry-After` header if present,
increment `collector_rate_limit_hits_total`, pause the limiter.

**Timeout:** 2s per OI request.

### 4.3 OI Normalization

```
Raw field:  openInterest (base asset units, one-sided)
Formula:    OI_USD = openInterest × lastPrice
```

Validation before writing:
- `openInterest > 0`
- `price > 0`
- `OI_USD` is finite and non-negative

---

## 5. Collector: Bybit

Single bulk REST endpoint. All symbols in one request.

- **Endpoint:** `GET https://api.bybit.com/v5/market/tickers?category=linear`
- **Poll interval:** 5 seconds
- **Timeout:** 3s

⚠️ Confirm OI field update frequency against Bybit V5 API documentation.
Currently assumed ~1–3 seconds server-side refresh.

**Filter:** only process symbols present in the resolver mapping.
Ignore other linear symbols the endpoint returns.

**Validation per symbol:**
- `lastPrice > 0`
- `singleOpenInterestValue` is present, finite, non-negative

**On request failure:** skip cycle, log warning, increment metric.
Do not crash. Retry next cycle.

### 5.1 OI Normalization

```
Raw field:  singleOpenInterestValue (USD, one-sided — direct use)
Fallback:   openInterestValue / 2 if singleOpenInterestValue unavailable
Formula:    OI_USD = singleOpenInterestValue
```

---

## 6. Collector: MEXC

Single bulk REST endpoint. All symbols in one request.

- **Endpoint:** `GET https://contract.mexc.com/api/v1/contract/ticker`
- **Poll interval:** 5 seconds
- **Timeout:** 3s
- **Rate limit:** 10 requests per 2 seconds — 5s interval uses 1 req/5s = safe

⚠️ Confirm OI field update frequency against MEXC API documentation.
Currently assumed ~1–3 seconds server-side refresh.

**Filter:** only process symbols present in the resolver mapping.

### 6.1 Contract Size Cache

Required for OI normalization. MEXC reports OI in contract sheets, not USD.

- **Source:** `GET https://contract.mexc.com/api/v1/contract/detail`
- **Fetch on:** startup and every 1 hour, and whenever symbol list refreshes
- **Cache:** in-memory `map[string]float64` (raw symbol → contractSize)
- **On fetch failure:** keep previous cache, log warning, increment metric
- **Skip symbol** if `contractSize` is missing or zero

**Validation per symbol:**
- `contractSize > 0`
- `holdVol >= 0`
- `lastPrice > 0`

### 6.2 OI Normalization

```
Raw field:  holdVol (contract sheets, one-sided)
Formula:    OI_USD = holdVol × contractSize × lastPrice
```

---

## 7. Redis Data Model

### 7.1 Key Schema

```
oi:{canonical_symbol}:{exchange}
```

Examples:
```
oi:BTC-USDT:binance
oi:BTC-USDT:bybit
oi:BTC-USDT:mexc
oi:ETH-USDT:binance
```

### 7.2 Data Type

Redis Sorted Set.
- **Score:** Unix timestamp in milliseconds (used for range queries)
- **Member:** JSON-encoded `OITick`

### 7.3 OITick Struct

```go
// internal/model/tick.go
package model

type OITick struct {
    Canonical   string  `json:"canonical"`   // "BTC-USDT"
    Exchange    string  `json:"exchange"`    // "binance"
    RawSymbol   string  `json:"raw_symbol"`  // "BTCUSDT"
    OIRaw       float64 `json:"oi_raw"`      // raw value from exchange
    OIRawUnit   string  `json:"oi_raw_unit"` // "base_asset" | "contract_sheets" | "usd"
    OIUSD       float64 `json:"oi_usd"`      // normalized USD value
    Price       float64 `json:"price"`       // last price USD
    TimestampMs int64   `json:"ts_ms"`       // Unix milliseconds
}
```

### 7.4 Write Behavior (Ring Buffer)

Timestamp is quantized to 5-second buckets:

```go
bucket := (time.Now().UnixMilli() / 5000) * 5000
```

For each tick, execute as a Redis pipeline:

```
1. ZREMRANGEBYSCORE key bucket bucket     ← remove existing tick for same bucket
2. ZADD key bucket <json_payload>         ← write new tick
3. ZREMRANGEBYSCORE key -inf (now-300000) ← remove ticks older than 300s
4. EXPIRE key 600                         ← safety TTL (10 min)
```

Last-write-wins per bucket. One tick per 5-second bucket per symbol per exchange.

### 7.5 Retention

300 seconds of ticks per key. Bybit and MEXC will have up to 60 ticks.
Binance will have up to 15 ticks (20s interval). Signal engine must
tolerate sparse ticks — never assume exactly 60 entries exist.

### 7.6 Redis Failure Behavior

- If Redis write fails: log warning, increment metric, continue polling.
- Do not crash. Do not buffer failed ticks in memory.
- Losing one tick is acceptable. Blocking the poll loop is not.
- **Timeout:** 2s per Redis pipeline operation.

---

## 8. Timestamp Policy

Use collector-side response completion time for all exchanges.
Do not rely on exchange-provided timestamps (inconsistent across exchanges).

```go
ts := time.Now().UnixMilli()
bucket := (ts / 5000) * 5000
```

Reject and skip ticks where:
- `bucket < now - 300_000` (older than retention window)
- `bucket > now + 5_000` (suspiciously far in the future)

---

## 9. Health and Observability

Each collector exposes an HTTP server (default `:8080`) with three endpoints.

### 9.1 `GET /livez`

Returns `200 OK` if the process is alive.

```json
{"status": "ok"}
```

### 9.2 `GET /readyz`

Returns `200 OK` when ready, `503` when not ready.

Ready conditions:
- Symbol list loaded from `symbol-resolver`
- At least one successful poll completed
- At least one successful Redis write completed
- (Binance only) WebSocket price stream connected at least once

```json
{
  "status": "ready",
  "exchange": "binance",
  "symbols_loaded": 448,
  "last_poll_age_seconds": 4,
  "last_redis_write_age_seconds": 4,
  "websocket_connected": true
}
```

Not-ready example:

```json
{
  "status": "not_ready",
  "reason": "symbol_list_not_loaded"
}
```

### 9.3 `GET /metrics`

Prometheus-format metrics. Key metrics per collector:

```
# polling
collector_poll_total{exchange, result}          ← result: success|error
collector_poll_duration_seconds{exchange}
collector_poll_errors_total{exchange}

# ticks
collector_ticks_written_total{exchange}
collector_tick_write_errors_total{exchange}

# redis
collector_redis_errors_total

# symbols
collector_symbols_active{exchange}

# timestamps
collector_last_successful_poll_timestamp_seconds{exchange}
collector_last_successful_redis_write_timestamp_seconds

# binance-specific
collector_binance_weight_per_minute_estimate
collector_binance_rate_limit_hits_total
collector_binance_websocket_reconnects_total
collector_binance_websocket_connected   ← gauge: 0|1
```

---

## 10. Configuration (Environment Variables)

All configuration is environment-driven. No config files.

### Shared (all collectors)

```env
LISTEN_ADDR=:8080
LOG_LEVEL=info                        # debug | info | warn | error
LOG_FORMAT=json                       # json | text

SYMBOL_RESOLVER_URL=http://symbol-resolver:8080
SYMBOL_RESOLVER_TIMEOUT=5s
SYMBOL_RESOLVER_REFRESH_INTERVAL=1h

REDIS_ADDR=redis:6379
REDIS_DB=0
REDIS_TIMEOUT=2s

RETENTION_SECONDS=300
BUCKET_SIZE_SECONDS=5
```

### Bybit / MEXC

```env
POLL_INTERVAL=5s
HTTP_TIMEOUT=3s
```

### Binance

```env
OI_POLL_INTERVAL=20s
OI_HTTP_TIMEOUT=2s
OI_RATE_LIMIT_RPS=22
OI_RATE_LIMIT_BURST=5

BINANCE_WS_URL=wss://fstream.binance.com/ws/!ticker@arr
BINANCE_WS_RECONNECT_BACKOFF_MAX=30s
```

---

## 11. Service Lifecycle

```
1. Parse and validate config — fatal exit if required env vars missing
2. Connect to Redis — retry indefinitely with backoff, report not-ready
3. Fetch symbol list from symbol-resolver — retry indefinitely, report not-ready
4. (MEXC only) Fetch contract sizes — retry indefinitely, report not-ready
5. (Binance only) Connect WebSocket price stream — retry indefinitely, report not-ready
6. Start poll loop
7. Start hourly symbol refresh ticker
8. Start health HTTP server
9. Report ready
10. On SIGINT/SIGTERM → graceful shutdown (10s timeout)
```

### Graceful Shutdown

1. Receive `SIGINT` or `SIGTERM`
2. Cancel root context
3. Stop poll ticker — no new poll cycles start
4. Wait for in-flight poll requests to finish (timeout: 5s)
5. Close WebSocket connection (Binance only)
6. Close Redis connection
7. Shut down health HTTP server
8. Exit 0

---

## 12. Dockerfile

One parameterized Dockerfile builds all three collectors.

```dockerfile
FROM golang:1.26-alpine AS builder

ARG SERVICE_NAME

WORKDIR /app

COPY go.mod go.su[m] ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /collector \
    ./cmd/${SERVICE_NAME}

FROM gcr.io/distroless/static-debian11:nonroot

COPY --from=builder /collector /collector

EXPOSE 8080

CMD ["/collector"]
```

Build example:

```bash
docker build --build-arg SERVICE_NAME=collector-binance -t collector-binance .
docker build --build-arg SERVICE_NAME=collector-bybit   -t collector-bybit .
docker build --build-arg SERVICE_NAME=collector-mexc    -t collector-mexc .
```

---

## 13. docker-compose Integration

```yaml
services:
  symbol-resolver:
    build:
      context: ./symbol-resolver
    ports:
      - "8080:8080"
    restart: unless-stopped

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    restart: unless-stopped

  collector-binance:
    build:
      context: ./collectors
      args:
        SERVICE_NAME: collector-binance
    environment:
      SYMBOL_RESOLVER_URL: http://symbol-resolver:8080
      REDIS_ADDR: redis:6379
      OI_POLL_INTERVAL: 20s
      OI_RATE_LIMIT_RPS: "22"
    ports:
      - "8081:8080"
    depends_on:
      - symbol-resolver
      - redis
    restart: unless-stopped

  collector-bybit:
    build:
      context: ./collectors
      args:
        SERVICE_NAME: collector-bybit
    environment:
      SYMBOL_RESOLVER_URL: http://symbol-resolver:8080
      REDIS_ADDR: redis:6379
      POLL_INTERVAL: 5s
    ports:
      - "8082:8080"
    depends_on:
      - symbol-resolver
      - redis
    restart: unless-stopped

  collector-mexc:
    build:
      context: ./collectors
      args:
        SERVICE_NAME: collector-mexc
    environment:
      SYMBOL_RESOLVER_URL: http://symbol-resolver:8080
      REDIS_ADDR: redis:6379
      POLL_INTERVAL: 5s
    ports:
      - "8083:8080"
    depends_on:
      - symbol-resolver
      - redis
    restart: unless-stopped
```

---

## 14. Rate Limit Summary

⚠️ All intervals and quota figures below must be confirmed against
official exchange API documentation before production deployment.

| Exchange | Symbols | Transport | Interval | Est. Requests | Est. Quota Usage |
|---|---|---|---|---|---|
| Binance OI | 448 | REST (per-symbol) | 20s | ~22 req/s | ~56% of 2400/min |
| Binance Price | 448 | WebSocket | real-time | 1 connection | negligible |
| Bybit | 448 | REST (bulk) | 5s | 1 req/5s | <5% |
| MEXC | 448 | REST (bulk) | 5s | 1 req/5s | <7% of 300/2s |

---

## 15. Definition of Done

### Functional
- [ ] All collectors load symbols from `symbol-resolver` on startup
- [ ] All collectors refresh symbol list hourly
- [ ] All collectors keep previous symbol list if refresh fails
- [ ] `collector-bybit` and `collector-mexc` poll bulk REST every 5s
- [ ] `collector-binance` maintains live WebSocket price stream for all symbols
- [ ] `collector-binance` polls OI via staggered REST every 20s for all symbols
- [ ] All collectors write `OITick` records to Redis sorted sets
- [ ] Ticks are quantized to 5-second buckets
- [ ] Duplicate ticks in the same bucket are overwritten (last-write-wins)
- [ ] Redis retains maximum 300 seconds of ticks per key

### Reliability
- [ ] No collector crashes on single poll failure
- [ ] No collector crashes on single symbol validation failure
- [ ] No collector crashes on temporary Redis unavailability
- [ ] No collector crashes on symbol-resolver temporary unavailability
- [ ] `collector-binance` WebSocket reconnects automatically on disconnect
- [ ] Binance REST poller stays within configured quota target
- [ ] Graceful shutdown completes within 10 seconds on SIGINT/SIGTERM

### Operability
- [ ] `/livez` returns 200 when process is alive
- [ ] `/readyz` returns 503 until fully initialized
- [ ] `/metrics` exposes Prometheus metrics
- [ ] All logs are structured JSON with exchange, symbol count, error context
- [ ] All configuration is environment-driven
- [ ] All three collectors build from one Dockerfile with `SERVICE_NAME` arg

### Data Quality
- [ ] `OI_USD` values are finite and non-negative before Redis write
- [ ] `price` values are positive before Redis write
- [ ] MEXC `contractSize` validated as `> 0` before normalization
- [ ] Bybit `singleOpenInterestValue` validated as finite and non-negative
- [ ] Binance `OI_USD = openInterest × price` with both values validated
- [ ] Rate limit quota confirmed against official exchange documentation

---

## 16. Open Questions (Must Resolve Before Implementation)

1. **Binance OI refresh rate** — does `/fapi/v1/openInterest` update more
   frequently than 15–20 seconds? If yes, reduce poll interval accordingly.
2. **Bybit OI refresh rate** — confirm actual server-side update cadence
   for `singleOpenInterestValue` in `/v5/market/tickers`.
3. **MEXC OI refresh rate** — confirm actual server-side update cadence
   for `holdVol` in `/api/v1/contract/ticker`.
4. **Binance weight quota** — confirm 2400 weight/min is current limit.
   Adjust `OI_RATE_LIMIT_RPS` and `OI_POLL_INTERVAL` accordingly.
```
