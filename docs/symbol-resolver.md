# Technical Specification: Multi-Exchange Symbol Discovery & Overlap Resolver Microservice

**Service Name:** `symbol-resolver`  
**Language / Runtime:** Go 1.26  
**Target Exchanges:** Binance USDⓈ-M Futures, Bybit V5 Linear, MEXC Futures  
**API Version:** v1  
**Document Status:** Implementation-ready specification  
**Owner:** Backend / Trading Infrastructure Team  

---

## 1. Purpose

The `symbol-resolver` microservice dynamically discovers all active USDT-margined perpetual futures symbols from Binance, Bybit, and MEXC, normalizes exchange-specific tickers into a canonical format, computes the exact overlapping symbol set, and exposes that mapping through a low-latency REST API.

The service is intended to be used by trading systems, market makers, execution engines, monitoring tools, and symbol mapping consumers that require a consistent cross-exchange view of actively traded USDT perpetual contracts.

---

## 2. Goals

The service must:

1. Fetch active USDT perpetual futures contract metadata from:
   - Binance Futures USDⓈ-M
   - Bybit V5 Linear
   - MEXC Futures

2. Run exchange fetches concurrently.

3. Filter only active, trading, USDT-margined perpetual contracts.

4. Normalize exchange-specific symbols into a canonical symbol format.

5. Compute the intersection of symbols available on all three exchanges.

6. Maintain an in-memory, thread-safe, immutable snapshot of the current overlap state.

7. Expose a read-only REST endpoint:

   ```text
   GET /api/v1/symbols/overlapping
   ```

8. Refresh automatically on a periodic schedule.

9. Preserve the last known good state if a scheduled refresh fails.

10. Provide health, readiness, metrics, and structured logs suitable for production operation.

---

## 3. Non-Goals

This service does not:

1. Store historical symbol data in a database.

2. Authenticate end-user requests.

3. Provide private exchange account data.

4. Place, modify, or cancel orders.

5. Maintain WebSocket connections.

6. Perform trading logic.

7. Resolve symbols across spot markets.

8. Resolve non-USDT quote assets unless explicitly configured in a future version.

9. Guarantee exchange symbol data is correct if upstream exchange APIs return invalid or inconsistent data.

---

## 4. High-Level Behavior

### 4.1 Startup Behavior

On startup, the service must:

1. Load configuration.

2. Initialize structured logging.

3. Initialize HTTP clients for Binance, Bybit, and MEXC.

4. Attempt an initial full refresh.

5. Retry the initial refresh up to a configured number of attempts using exponential backoff with jitter.

6. Start the HTTP server.

7. Start the background refresh worker.

If the initial refresh fails after all startup attempts, the service must still expose liveness and readiness endpoints, but readiness must report unhealthy until a successful snapshot is available.

The overlapping symbols endpoint must return HTTP `503 Service Unavailable` until the first successful snapshot has been produced.

A strict startup mode may be enabled by configuration. In strict startup mode, if the initial refresh fails after all retries, the process must exit with a non-zero exit code.

---

### 4.2 Background Refresh Behavior

The service must refresh symbol data periodically using a background worker.

Default refresh interval:

```text
1 hour
```

The refresh interval must be configurable.

To avoid thundering herd behavior across multiple deployments, the scheduler must add jitter to the refresh interval.

Example:

```text
next_refresh = refresh_interval + random_jitter
```

The background worker must:

1. Trigger refresh periodically.

2. Prevent concurrent refresh executions.

3. Keep the last successful snapshot if a new refresh fails.

4. Log refresh success and failure with structured fields.

5. Record metrics for refresh attempts, success, failure, duration, and symbol counts.

6. Respect context cancellation and graceful shutdown.

---

### 4.3 Graceful Degradation

If any exchange fails during a scheduled refresh, the service must not replace the current snapshot.

The service must continue serving the previous valid snapshot.

The response payload must indicate whether the current snapshot is stale.

A snapshot is considered stale if:

```text
now - last_successful_refresh_at > max_staleness
```

The max staleness threshold must be configurable.

---

## 5. Functional Requirements

### 5.1 Concurrent Exchange Polling

The service must fetch exchange metadata concurrently.

For each refresh:

1. Create a refresh-level context with timeout.

2. Start one fetch task per exchange.

3. Wait for all fetch tasks to complete.

4. Collect all errors, not only the first error.

5. Do not cancel remaining exchange fetches solely because one exchange failed.

6. If any exchange fails after retries, mark the full refresh as failed.

The implementation should prefer `sync.WaitGroup` and `errors.Join` over fail-fast cancellation for refresh collection.

---

### 5.2 Exchange Filtering Requirements

The service must include only symbols that satisfy all of the following conditions:

1. The contract is active or trading.

2. The contract is a perpetual futures contract.

3. The contract is margined or quoted in USDT.

4. The exchange-specific symbol metadata is complete and valid.

The service must exclude:

1. Delivery futures.

2. Quarterly futures.

3. Options.

4. Spot symbols.

5. USDC-margined contracts.

6. Inverse contracts.

7. Delisted, settled, closed, or inactive contracts.

8. Symbols with missing base asset or quote asset metadata.

9. Symbols with empty raw ticker values.

---

### 5.3 Canonical Symbol Format

The canonical symbol format must be:

```text
BASE-QUOTE
```

Examples:

```text
BTC-USDT
ETH-USDT
1000PEPE-USDT
```

Rules:

1. Base asset and quote asset must be uppercase.

2. Leading and trailing whitespace must be trimmed.

3. Base asset and quote asset must be separated by a single hyphen.

4. Quote asset must be `USDT` for this service version.

5. Base asset must not be empty.

6. Quote asset must not be empty.

7. Base asset and quote asset must not contain whitespace.

8. Base asset and quote asset should be derived from exchange-provided metadata fields whenever available.

9. String suffix stripping, such as removing `USDT` from `BTCUSDT`, must not be used if base and quote metadata fields are available.

10. Canonical symbols must be validated before being accepted into the snapshot.

---

### 5.4 Normalization Rules

The service must normalize exchange symbols as follows.

#### Binance

Binance provides:

```text
symbol
baseAsset
quoteAsset
contractType
status
```

Canonical symbol must be constructed using:

```text
uppercase(baseAsset) + "-" + uppercase(quoteAsset)
```

Example:

```text
baseAsset = BTC
quoteAsset = USDT
canonical = BTC-USDT
```

Binance raw symbol:

```text
BTCUSDT
```

#### Bybit

Bybit provides:

```text
symbol
baseCoin
quoteCoin
contractType
status
```

Canonical symbol must be constructed using:

```text
uppercase(baseCoin) + "-" + uppercase(quoteCoin)
```

Example:

```text
baseCoin = BTC
quoteCoin = USDT
canonical = BTC-USDT
```

Bybit raw symbol:

```text
BTCUSDT
```

#### MEXC

MEXC must be normalized using exchange-provided base and quote fields where available.

Preferred fields:

```text
baseCoin
quoteCoin
```

Canonical symbol must be constructed using:

```text
uppercase(baseCoin) + "-" + uppercase(quoteCoin)
```

Example:

```text
baseCoin = BTC
quoteCoin = USDT
canonical = BTC-USDT
```

MEXC raw symbol:

```text
BTC_USDT
```

If MEXC does not provide a base asset field in a particular response schema version, the fetcher must fail with a clear error unless a configured fallback parsing mode is explicitly enabled.

A fallback parser, if enabled, must parse the raw symbol by splitting on the final underscore only.

Example:

```text
BTC_USDT -> base=BTC, quote=USDT
```

The fallback parser must not replace all underscores blindly.

---

### 5.5 Duplicate Canonical Symbol Handling

If an exchange returns multiple active symbols that normalize to the same canonical symbol, this must be treated as a data integrity issue.

Default behavior:

```text
Fail the refresh for that exchange.
```

The service must log:

1. Exchange name.

2. Canonical symbol.

3. Conflicting raw symbols.

4. Request identifier, if available.

A configuration option may allow downgrade to warning mode:

```text
DUPLICATE_CANONICAL_POLICY=fail | warn
```

Default:

```text
fail
```

In `warn` mode, the service must select one raw symbol deterministically, preferably the lexicographically smallest raw symbol, and increment a metric.

---

### 5.6 Intersection Logic

The service must compute the set intersection:

```text
Overlap = Binance ∩ Bybit ∩ MEXC
```

A canonical symbol is included in the response only if it is present as an active USDT perpetual contract on all three exchanges.

The intersection algorithm must:

1. Build one normalized map per exchange.

2. Validate all canonical symbols.

3. Detect duplicates.

4. Choose the smallest exchange map as the iteration base for efficiency.

5. Check membership in all other exchange maps.

6. Produce a deterministic sorted result.

7. Sort results lexicographically by canonical symbol.

The output must be stable for identical input data.

---

### 5.7 In-Memory State

The service must maintain the current overlap state in memory.

The state must be immutable once published.

The recommended implementation is:

```go
atomic.Pointer[Snapshot]
```

The service should not use a read-write mutex for serving read traffic.

A snapshot must contain:

1. Structured response object.

2. Precomputed JSON payload.

3. ETag or content hash.

4. Timestamp of successful refresh.

5. Exchange counts.

6. Staleness metadata.

The HTTP handler must serve the precomputed JSON payload directly.

---

## 6. External Exchange API Contracts

### 6.1 Binance Futures USDⓈ-M

#### Endpoint

```text
GET https://fapi.binance.com/fapi/v1/exchangeInfo
```

#### Expected Relevant Fields

```text
symbols[].symbol
symbols[].baseAsset
symbols[].quoteAsset
symbols[].contractType
symbols[].status
```

#### Inclusion Filter

A Binance symbol is included if all of the following are true:

```text
status == "TRADING"
quoteAsset == "USDT"
contractType == "PERPETUAL"
symbol != ""
baseAsset != ""
```

#### Raw Symbol

```text
symbol
```

#### Canonical Construction

```text
canonical = uppercase(baseAsset) + "-" + uppercase(quoteAsset)
```

---

### 6.2 Bybit V5 Linear

#### Endpoint

```text
GET https://api.bybit.com/v5/market/instruments-info?category=linear
```

#### Expected Relevant Fields

```text
result.list[].symbol
result.list[].baseCoin
result.list[].quoteCoin
result.list[].contractType
result.list[].status
```

#### Inclusion Filter

A Bybit symbol is included if all of the following are true:

```text
status == "Trading"
quoteCoin == "USDT"
contractType == "LinearPerpetual"
symbol != ""
baseCoin != ""
```

#### Raw Symbol

```text
symbol
```

#### Canonical Construction

```text
canonical = uppercase(baseCoin) + "-" + uppercase(quoteCoin)
```

---

### 6.3 MEXC Futures

#### Endpoint

```text
GET https://contract.mexc.com/api/v1/contract/detail
```

#### Expected Relevant Fields

```text
data[].symbol
data[].baseCoin
data[].quoteCoin
data[].state
```

Depending on the exact MEXC schema, contract type may be implicit for futures contract detail. If a contract type field is available, the service must filter to perpetual contracts only.

#### Inclusion Filter

A MEXC symbol is included if all of the following are true:

```text
state == 0
quoteCoin == "USDT"
symbol != ""
baseCoin != ""
```

If a perpetual contract type field is available:

```text
contractType indicates perpetual
```

#### Raw Symbol

```text
symbol
```

#### Canonical Construction

Preferred:

```text
canonical = uppercase(baseCoin) + "-" + uppercase(quoteCoin)
```

Fallback, only if explicitly enabled:

```text
Split raw symbol on the last underscore.
```

Example:

```text
BTC_USDT -> BTC-USDT
```

The fallback parser must not replace all underscores.

---

## 7. HTTP Client Requirements

Each exchange client must use a dedicated, hardened `http.Client`.

The service must not use Go’s default HTTP client.

### 7.1 Timeouts

Default HTTP client timeout:

```text
5 seconds
```

Configurable via:

```text
HTTP_TIMEOUT
```

Refresh-level timeout default:

```text
30 seconds
```

Configurable via:

```text
REFRESH_TIMEOUT
```

The refresh timeout must cover all retries for a single refresh cycle.

---

### 7.2 Transport Settings

The HTTP transport should configure:

```text
Dial timeout
TLS handshake timeout
Response header timeout
Idle connection timeout
Max idle connections
Max idle connections per host
HTTP/2 support
Proxy support from environment
```

Recommended defaults:

```text
Dial timeout:              3s
TLS handshake timeout:     5s
Response header timeout:   5s
Idle connection timeout:   90s
Max idle connections:      100
Max idle conns per host:   10
```

---

### 7.3 Request Headers

Each outgoing request must include:

```text
Accept: application/json
User-Agent: symbol-resolver/<version>
```

The `User-Agent` value must be configurable.

---

### 7.4 Response Body Handling

The service must:

1. Check HTTP status before decoding.

2. Close response bodies.

3. Drain response bodies to allow connection reuse.

4. Limit response body size.

Default maximum response body size:

```text
32 MiB
```

Configurable via:

```text
MAX_RESPONSE_BODY_BYTES
```

---

### 7.5 JSON Decoding

The service must decode JSON using typed response structures.

Unknown fields should be ignored to tolerate additive exchange API changes.

Required fields must be validated.

If required fields are missing, the exchange fetch must fail.

---

## 8. Retry Policy

Each exchange fetch must be retried independently.

### 8.1 Retry Attempts

Default maximum attempts per exchange per refresh:

```text
3
```

Configurable via:

```text
FETCH_MAX_ATTEMPTS
```

---

### 8.2 Backoff

Retry backoff must be exponential with jitter.

Default values:

```text
Initial backoff: 500ms
Backoff multiplier: 2
Max backoff: 5s
Jitter: 20%
```

Configurable via:

```text
BACKOFF_INITIAL
BACKOFF_MAX
BACKOFF_MULTIPLIER
BACKOFF_JITTER_PERCENT
```

---

### 8.3 Retryable Errors

The service must retry on transient errors, including:

1. Network timeout.

2. DNS resolution failure, where retry may succeed.

3. Connection reset.

4. EOF before full response.

5. HTTP `429 Too Many Requests`.

6. HTTP `500 Internal Server Error`.

7. HTTP `502 Bad Gateway`.

8. HTTP `503 Service Unavailable`.

9. HTTP `504 Gateway Timeout`.

For HTTP `429`, the service must honor the `Retry-After` header if present and if the value is within configured bounds.

---

### 8.4 Non-Retryable Errors

The service must not retry on:

1. HTTP `400 Bad Request`.

2. HTTP `401 Unauthorized`.

3. HTTP `403 Forbidden`.

4. HTTP `404 Not Found`.

5. Invalid JSON structure.

6. Missing required fields.

7. Canonicalization failures.

8. Duplicate canonical symbol failures in fail mode.

9. Context cancellation caused by shutdown.

---

## 9. Sanity Checks

Before publishing a new snapshot, the service must perform sanity checks.

### 9.1 Mandatory Checks

A refresh must fail if:

1. Any exchange returns zero active symbols.

2. Any exchange returns fewer symbols than the configured minimum exchange symbol count.

3. The overlapping symbol count is less than the configured minimum overlap count.

4. A canonical symbol is invalid.

5. A duplicate canonical symbol is detected in fail mode.

6. Required exchange metadata fields are missing.

7. Any exchange fetch fails after retries.

---

### 9.2 Drop Protection

The service must protect against abrupt upstream data collapse.

Configuration:

```text
MAX_OVERLAP_DROP_PERCENT
```

Default:

```text
40
```

If a previous successful snapshot exists and the new overlap count drops by more than this percentage, the refresh must fail unless override is explicitly configured.

Example:

```text
previous overlap = 200
new overlap = 80
drop = 60%
refresh rejected
```

This prevents transient exchange issues from replacing a good snapshot with a degraded one.

---

### 9.3 Configurable Sanity Enforcement

Configuration:

```text
SANITY_ENFORCE=true | false
```

Default:

```text
true
```

When disabled, sanity violations should log warnings and increment metrics but still publish the new snapshot.

This mode is not recommended for production.

---

## 10. Recommended Architecture

### 10.1 Package Layout

```text
symbol-resolver/
├── cmd/
│   └── symbol-resolver/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── app.go
│   │   └── worker.go
│   ├── config/
│   │   └── config.go
│   ├── exchange/
│   │   ├── exchange.go
│   │   ├── binance.go
│   │   ├── binance_test.go
│   │   ├── bybit.go
│   │   ├── bybit_test.go
│   │   ├── mexc.go
│   │   └── mexc_test.go
│   ├── resolver/
│   │   ├── service.go
│   │   ├── refresh.go
│   │   ├── intersection.go
│   │   ├── snapshot.go
│   │   └── service_test.go
│   ├── symbol/
│   │   ├── canonical.go
│   │   ├── normalize.go
│   │   ├── validate.go
│   │   └── canonical_test.go
│   ├── httpapi/
│   │   ├── server.go
│   │   ├── handlers.go
│   │   ├── middleware.go
│   │   └── handlers_test.go
│   ├── health/
│   │   └── health.go
│   └── metrics/
│       └── metrics.go
├── go.mod
├── go.sum
├── Dockerfile
└── README.md
```

---

### 10.2 Dependency Direction

Dependencies must point inward toward domain logic:

```text
cmd -> app -> resolver -> exchange
                 |
                 +-> symbol
                 |
                 +-> metrics
                 |
                 +-> health

httpapi -> resolver
```

Exchange clients must not depend on HTTP handlers.

HTTP handlers must not directly call exchange clients.

The resolver must not depend on HTTP transport details.

---

## 11. Core Domain Types

### 11.1 Exchange Name

```go
package exchange

type Name string

const (
    Binance Name = "binance"
    Bybit   Name = "bybit"
    Mexc    Name = "mexc"
)
```

---

### 11.2 Instrument

```go
package exchange

type Instrument struct {
    Exchange     Name
    RawSymbol    string
    BaseAsset    string
    QuoteAsset   string
    Status       string
    ContractType string
}
```

---

### 11.3 Canonical Symbol

```go
package symbol

type Canonical string

func (c Canonical) String() string {
    return string(c)
}
```

---

### 11.4 Symbol Mapping

```go
package resolver

import "symbol-resolver/internal/symbol"

type SymbolMapping struct {
    Canonical  symbol.Canonical `json:"canonical_symbol"`
    BaseAsset  string           `json:"base_asset"`
    QuoteAsset string           `json:"quote_asset"`
    RawBinance string           `json:"binance_symbol"`
    RawBybit   string           `json:"bybit_symbol"`
    RawMexc    string           `json:"mexc_symbol"`
}
```

---

### 11.5 Overlap Response

```go
package resolver

import "time"

type OverlapResponse struct {
    SchemaVersion          int             `json:"schema_version"`
    UpdatedAt              time.Time       `json:"updated_at"`
    LastSuccessfulRefresh  time.Time       `json:"last_successful_refresh_at"`
    RefreshStatus          string          `json:"refresh_status"`
    Stale                  bool            `json:"stale"`
    TotalOverlapping       int             `json:"total_overlapping"`
    ExchangeCounts         map[string]int  `json:"exchange_counts"`
    Symbols                []SymbolMapping `json:"symbols"`
}
```

---

### 11.6 Snapshot

```go
package resolver

type Snapshot struct {
    Data *OverlapResponse
    JSON []byte
    ETag string
}
```

---

## 12. Symbol Source Interface

The interface should be defined in the consuming package, `resolver`.

```go
package resolver

import (
    "context"

    "symbol-resolver/internal/exchange"
)

type SymbolSource interface {
    Name() exchange.Name
    FetchActiveUSDTPerpetuals(ctx context.Context) ([]exchange.Instrument, error)
}
```

Exchange clients implement this interface.

The resolver is responsible for:

1. Normalization.

2. Validation.

3. Duplicate detection.

4. Intersection.

5. Snapshot construction.

---

## 13. Refresh Workflow

### 13.1 High-Level Refresh Steps

Each refresh execution must perform the following steps:

1. Create refresh context with timeout.

2. Fetch instruments from all configured exchanges concurrently.

3. Retry transient exchange failures independently.

4. Collect all fetch results and errors.

5. If any exchange failed, return a joined error and do not publish a new snapshot.

6. Normalize instruments into canonical symbols.

7. Validate canonical symbols.

8. Detect duplicate canonical symbols per exchange.

9. Build per-exchange maps:

   ```go
   map[exchange.Name]map[symbol.Canonical]exchange.Instrument
   ```

10. Compute intersection.

11. Build sorted symbol mappings.

12. Build exchange counts.

13. Run sanity checks.

14. Marshal JSON payload.

15. Compute ETag.

16. Atomically store the new snapshot.

17. Log success.

18. Record metrics.

---

### 13.2 Refresh Error Behavior

If refresh fails:

1. Do not replace existing snapshot.

2. Log all errors.

3. Increment failure metrics.

4. Continue serving previous snapshot if one exists.

5. Mark readiness unhealthy if no snapshot exists or current snapshot exceeds max staleness.

---

### 13.3 Single-Flight Protection

The service must ensure only one refresh can run at a time.

If a scheduled refresh starts while another refresh is already running, the new trigger must be skipped.

A metric or log field should record skipped refreshes.

---

## 14. Intersection Algorithm Requirements

The intersection algorithm must be deterministic and efficient.

Recommended behavior:

1. Build maps for Binance, Bybit, and MEXC.

2. Select the smallest map as the base set.

3. For each canonical symbol in the base set:

   - Check presence in all other exchange maps.
   - If present in all, construct a `SymbolMapping`.

4. Sort the final slice by canonical symbol.

Sorting must use modern Go standard library helpers, for example:

```go
slices.SortFunc(mappings, func(a, b SymbolMapping) int {
    return cmp.Compare(a.Canonical, b.Canonical)
})
```

The service must not rely on Go map iteration order.

---

## 15. REST API Specification

### 15.1 Endpoint

```text
GET /api/v1/symbols/overlapping
```

---

### 15.2 Response Headers

Successful response:

```text
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
ETag: "<computed-etag>"
Cache-Control: public, max-age=30
```

Optional support:

```text
If-None-Match
```

If the provided `If-None-Match` header matches the current ETag, the service may return:

```text
304 Not Modified
```

---

### 15.3 Success Response Body

```json
{
  "schema_version": 1,
  "updated_at": "2026-09-26T03:30:00Z",
  "last_successful_refresh_at": "2026-09-26T03:30:00Z",
  "refresh_status": "ok",
  "stale": false,
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

### 15.4 Empty Overlap Response

If no overlapping symbols exist, the response must use an empty array, not `null`.

```json
{
  "schema_version": 1,
  "updated_at": "2026-09-26T03:30:00Z",
  "last_successful_refresh_at": "2026-09-26T03:30:00Z",
  "refresh_status": "ok",
  "stale": false,
  "total_overlapping": 0,
  "exchange_counts": {
    "binance": 285,
    "bybit": 310,
    "mexc": 420
  },
  "symbols": []
}
```

---

### 15.5 Not Ready Response

If no successful snapshot exists:

```text
HTTP/1.1 503 Service Unavailable
Content-Type: application/json; charset=utf-8
```

Body:

```json
{
  "error": {
    "code": "service_unavailable",
    "message": "symbol overlap snapshot is not available yet"
  }
}
```

---

### 15.6 Method Not Allowed

For non-GET requests:

```text
HTTP/1.1 405 Method Not Allowed
Allow: GET
```

---

### 15.7 Performance Requirement

The overlapping endpoint must usually respond in:

```text
< 5 ms
```

This should be achieved by:

1. Serving an immutable in-memory snapshot.

2. Using precomputed JSON.

3. Avoiding locks on the read path.

4. Avoiding per-request marshaling.

5. Avoiding heavy middleware on the hot path.

---

## 16. Health Endpoints

### 16.1 Liveness

```text
GET /livez
```

Returns:

```text
200 OK
```

if the process is alive and not deadlocked.

Response:

```json
{
  "status": "ok"
}
```

---

### 16.2 Readiness

```text
GET /readyz
```

Returns:

```text
200 OK
```

if all of the following are true:

1. A snapshot exists.

2. The snapshot age is less than or equal to configured max staleness.

3. The service is not shutting down.

Otherwise returns:

```text
503 Service Unavailable
```

Response:

```json
{
  "status": "ready",
  "last_successful_refresh_at": "2026-09-26T03:30:00Z",
  "stale": false
}
```

or:

```json
{
  "status": "not_ready",
  "reason": "stale_snapshot",
  "last_successful_refresh_at": "2026-09-26T01:30:00Z",
  "stale": true
}
```

---

### 16.3 Metrics

```text
GET /metrics
```

The service should expose Prometheus-compatible metrics.

If Prometheus is not used, the endpoint may expose internal metrics in JSON format.

---

## 17. Observability

### 17.1 Structured Logging

The service must use Go’s structured logging package:

```go
log/slog
```

Logs must include fields such as:

```text
exchange
attempt
duration_ms
symbol_count
overlap_count
error
refresh_id
```

Example success log:

```text
level=INFO msg="refresh completed" exchange_counts="map[binance:285 bybit:310 mexc:420]" overlap_count=142 duration_ms=1830
```

Example failure log:

```text
level=ERROR msg="refresh failed" exchange="mexc" attempt=3 error="context deadline exceeded"
```

---

### 17.2 Metrics

Recommended metrics:

```text
symbol_resolver_refresh_total{exchange, result}
symbol_resolver_refresh_duration_seconds{exchange}
symbol_resolver_refresh_skipped_total
symbol_resolver_symbols_total{exchange}
symbol_resolver_overlapping_symbols
symbol_resolver_last_successful_refresh_timestamp_seconds
symbol_resolver_snapshot_stale
symbol_resolver_http_request_duration_seconds{path, method, status}
symbol_resolver_sanity_check_failures_total{reason}
symbol_resolver_duplicate_canonical_total{exchange}
```

---

### 17.3 Request Logging

Request logging for the hot overlapping endpoint should be sampled or disabled by default to preserve latency.

Health endpoint logging should be low verbosity.

---

## 18. Configuration

Configuration should be loaded from environment variables.

### 18.1 Core Configuration

| Variable | Default | Description |
|---|---:|---|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` or `text` |
| `STRICT_STARTUP` | `false` | Exit if initial refresh fails after retries |
| `USER_AGENT` | `symbol-resolver/1.0` | Outbound HTTP User-Agent |

---

### 18.2 Refresh Configuration

| Variable | Default | Description |
|---|---:|---|
| `REFRESH_INTERVAL` | `1h` | Background refresh interval |
| `REFRESH_JITTER` | `5m` | Max random jitter added to interval |
| `REFRESH_TIMEOUT` | `30s` | Timeout for entire refresh operation |
| `HTTP_TIMEOUT` | `5s` | Timeout for one HTTP attempt |
| `FETCH_MAX_ATTEMPTS` | `3` | Max attempts per exchange per refresh |
| `BACKOFF_INITIAL` | `500ms` | Initial retry backoff |
| `BACKOFF_MAX` | `5s` | Max retry backoff |
| `BACKOFF_MULTIPLIER` | `2` | Exponential multiplier |
| `BACKOFF_JITTER_PERCENT` | `20` | Jitter percentage |

---

### 18.3 Sanity Configuration

| Variable | Default | Description |
|---|---:|---|
| `MIN_EXCHANGE_SYMBOLS` | `50` | Minimum symbols per exchange |
| `MIN_OVERLAP_SYMBOLS` | `100` | Minimum overlapping symbols |
| `MAX_OVERLAP_DROP_PERCENT` | `40` | Max allowed overlap drop |
| `MAX_STALENESS` | `2h` | Max snapshot age before not ready |
| `SANITY_ENFORCE` | `true` | Reject snapshot on sanity violation |
| `DUPLICATE_CANONICAL_POLICY` | `fail` | `fail` or `warn` |

---

### 18.4 Exchange URL Configuration

| Variable | Default | Description |
|---|---:|---|
| `BINANCE_BASE_URL` | `https://fapi.binance.com` | Binance Futures base URL |
| `BYBIT_BASE_URL` | `https://api.bybit.com` | Bybit V5 base URL |
| `MEXC_BASE_URL` | `https://contract.mexc.com` | MEXC Futures base URL |

---

### 18.5 HTTP Client Configuration

| Variable | Default | Description |
|---|---:|---|
| `MAX_IDLE_CONNS` | `100` | Transport max idle connections |
| `MAX_IDLE_CONNS_PER_HOST` | `10` | Transport max idle connections per host |
| `MAX_RESPONSE_BODY_BYTES` | `33554432` | 32 MiB response body limit |
| `TLS_HANDSHAKE_TIMEOUT` | `5s` | TLS handshake timeout |
| `DIAL_TIMEOUT` | `3s` | TCP dial timeout |

---

## 19. Graceful Shutdown

The service must handle:

```text
SIGINT
SIGTERM
```

On shutdown signal:

1. Stop accepting new HTTP connections.

2. Notify background worker to stop.

3. Wait for in-flight refresh to finish, bounded by shutdown timeout.

4. Shut down HTTP server gracefully.

5. Exit with code `0` if shutdown completed cleanly.

Default shutdown timeout:

```text
10s
```

Configurable via:

```text
SHUTDOWN_TIMEOUT
```

---

## 20. Security Requirements

### 20.1 Transport Security

All outbound exchange requests must use HTTPS.

The service must validate TLS certificates using the system certificate pool.

Custom or insecure TLS settings must not be enabled by default.

---

### 20.2 Request Safety

The service must:

1. Reject non-GET requests on the overlapping endpoint.

2. Not accept request bodies.

3. Not expose internal stack traces in API responses.

4. Limit response body sizes from upstream exchanges.

5. Avoid logging full upstream payloads by default.

6. Avoid logging sensitive headers.

---

### 20.3 CORS

CORS is not required by default.

If required, allowed origins must be explicit and configurable.

Default:

```text
No CORS headers
```

---

## 21. Error Handling Requirements

### 21.1 Error Wrapping

All errors must be wrapped with context using `%w`.

Example:

```go
return fmt.Errorf("fetch binance exchange info: %w", err)
```

---

### 21.2 Error Classification

The service should classify errors as:

1. Transient.

2. Permanent.

3. Validation.

4. Configuration.

5. Shutdown.

Transient errors may be retried.

Permanent errors must not be retried.

Validation errors should include exchange, raw symbol, canonical symbol, and reason.

---

### 21.3 Error Response Format

All API errors should use:

```json
{
  "error": {
    "code": "service_unavailable",
    "message": "symbol overlap snapshot is not available yet"
  }
}
```

Error codes should be stable strings, not arbitrary prose.

Recommended codes:

```text
service_unavailable
method_not_allowed
not_found
internal
```

---

## 22. Testing Requirements

### 22.1 Unit Tests

The following must be unit tested:

1. Canonical symbol construction.

2. Canonical symbol validation.

3. Binance filtering.

4. Bybit filtering.

5. MEXC filtering.

6. Duplicate canonical detection.

7. Intersection logic.

8. Sanity checks.

9. Staleness calculation.

10. Retry classification.

11. Backoff calculation.

12. JSON response construction.

---

### 22.2 Table-Driven Normalization Cases

Required cases include:

```text
BTCUSDT -> BTC-USDT
ETHUSDT -> ETH-USDT
1000PEPEUSDT -> 1000PEPE-USDT
BTC_USDT -> BTC-USDT
lowercase input
whitespace input
missing base asset
missing quote asset
empty raw symbol
non-USDT quote
duplicate canonical symbols
```

---

### 22.3 HTTP Tests

Use `httptest` to test:

1. Successful overlap response.

2. Empty overlap response.

3. Service unavailable before first snapshot.

4. Method not allowed.

5. ETag and conditional request behavior.

6. Readiness behavior when snapshot is stale.

7. Liveness behavior.

---

### 22.4 Exchange Client Tests

Exchange client tests must use fixture payloads served by `httptest.Server`.

Each exchange client must test:

1. Successful parsing.

2. Filtering of inactive symbols.

3. Filtering of non-USDT symbols.

4. Filtering of non-perpetual contracts.

5. HTTP 500 retry behavior.

6. HTTP 429 retry behavior.

7. HTTP 400 non-retry behavior.

8. Malformed JSON.

9. Missing required fields.

10. Empty response.

---

### 22.5 Concurrency Tests

The service must pass:

```bash
go test ./... -race
```

Concurrency tests should cover:

1. Concurrent reads while refresh is running.

2. Multiple refresh triggers.

3. Snapshot replacement during active reads.

4. Graceful shutdown during refresh.

---

### 22.6 Fuzz Testing

Fuzz testing should be added for:

1. Canonical parsing.

2. MEXC fallback parsing, if enabled.

3. Symbol validation.

Fuzz targets must not panic on arbitrary input.

---

## 23. Performance Requirements

### 23.1 Read Latency

The `/api/v1/symbols/overlapping` endpoint should respond in:

```text
< 5 ms
```

under normal load when serving from memory.

This should be achieved by storing precomputed JSON in the snapshot.

---

### 23.2 Refresh Duration

A full refresh should usually complete within:

```text
< 10 seconds
```

under healthy upstream conditions.

The refresh timeout default is higher to allow retries.

---

### 23.3 Memory Usage

The service is expected to use modest memory because the active symbol set is small.

The implementation should avoid:

1. Retaining full upstream payloads after parsing.

2. Rebuilding response JSON per HTTP request.

3. Excessive allocations in the intersection loop.

4. Holding large decoded structures longer than necessary.

---

## 24. Deployment Requirements

### 24.1 Container

The service should be buildable as a minimal container image.

Recommended build:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" ./cmd/symbol-resolver

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /app/symbol-resolver /symbol-resolver
USER nonroot:nonroot
ENTRYPOINT ["/symbol-resolver"]
```

---

### 24.2 Kubernetes Probes

Recommended probes:

```yaml
livenessProbe:
  httpGet:
    path: /livez
    port: http
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 5
  periodSeconds: 10
```

---

### 24.3 Environment

The service should be runnable with:

```bash
symbol-resolver
```

or:

```bash
go run ./cmd/symbol-resolver
```

No database or external cache is required.

---

## 25. Implementation Notes for Go 1.26

The implementation should use modern Go standard library features and idioms.

Recommended:

1. `context.Context` as first argument for all operations that block or perform I/O.

2. `log/slog` for structured logging.

3. `errors.Join` for combining multiple exchange errors.

4. `atomic.Pointer[T]` for immutable snapshot state.

5. `slices.SortFunc` for deterministic sorting.

6. `cmp.Compare` for comparison functions.

7. `strings.Cut` for safe separator parsing.

8. `net/http` ServeMux method routing:

   ```go
   mux.HandleFunc("GET /api/v1/symbols/overlapping", handler.OverlappingSymbols)
   ```

9. `math/rand/v2` for jitter if random numbers are needed.

10. Avoid `init()` functions for service wiring.

11. Avoid global mutable state.

12. Avoid third-party web frameworks.

13. Prefer standard library dependencies where possible.

---

## 26. Recommended Go Type Summary

```go
package exchange

type Name string

const (
    Binance Name = "binance"
    Bybit   Name = "bybit"
    Mexc    Name = "mexc"
)

type Instrument struct {
    Exchange     Name
    RawSymbol    string
    BaseAsset    string
    QuoteAsset   string
    Status       string
    ContractType string
}
```

```go
package symbol

type Canonical string
```

```go
package resolver

import (
    "context"
    "time"

    "symbol-resolver/internal/exchange"
    "symbol-resolver/internal/symbol"
)

type SymbolSource interface {
    Name() exchange.Name
    FetchActiveUSDTPerpetuals(ctx context.Context) ([]exchange.Instrument, error)
}

type SymbolMapping struct {
    Canonical  symbol.Canonical `json:"canonical_symbol"`
    BaseAsset  string           `json:"base_asset"`
    QuoteAsset string           `json:"quote_asset"`
    RawBinance string           `json:"binance_symbol"`
    RawBybit   string           `json:"bybit_symbol"`
    RawMexc    string           `json:"mexc_symbol"`
}

type OverlapResponse struct {
    SchemaVersion         int             `json:"schema_version"`
    UpdatedAt             time.Time       `json:"updated_at"`
    LastSuccessfulRefresh time.Time       `json:"last_successful_refresh_at"`
    RefreshStatus         string          `json:"refresh_status"`
    Stale                 bool            `json:"stale"`
    TotalOverlapping      int             `json:"total_overlapping"`
    ExchangeCounts        map[string]int  `json:"exchange_counts"`
    Symbols               []SymbolMapping `json:"symbols"`
}

type Snapshot struct {
    Data *OverlapResponse
    JSON []byte
    ETag string
}
```

---

## 27. Acceptance Criteria / Definition of Done

The implementation is complete when all of the following are true:

### 27.1 Functional

- [ ] Service fetches Binance, Bybit, and MEXC concurrently.

- [ ] Service filters only active USDT perpetual contracts.

- [ ] Service normalizes symbols into `BASE-QUOTE` canonical format.

- [ ] Service computes the intersection across all three exchanges.

- [ ] Service returns raw exchange tickers for each overlapping canonical symbol.

- [ ] Response symbols are sorted lexicographically by canonical symbol.

- [ ] Empty overlap returns `"symbols": []`, not `null`.

- [ ] Service returns HTTP `503` before first successful snapshot.

- [ ] Service returns precomputed JSON from memory after successful refresh.

- [ ] Service supports ETag-based conditional responses.

---

### 27.2 Reliability

- [ ] Initial refresh retries with exponential backoff and jitter.

- [ ] Background refresh runs hourly with jitter.

- [ ] Only one refresh can run at a time.

- [ ] If a scheduled refresh fails, the previous snapshot remains available.

- [ ] Snapshot staleness is exposed in API and readiness checks.

- [ ] Transient HTTP errors are retried.

- [ ] Permanent HTTP errors are not retried.

- [ ] HTTP `429` honors `Retry-After` where present.

- [ ] Sanity checks prevent obviously broken upstream data from replacing a good snapshot.

- [ ] Duplicate canonical symbols are detected and handled according to configuration.

---

### 27.3 Operability

- [ ] `/livez` endpoint returns liveness status.

- [ ] `/readyz` endpoint returns readiness status.

- [ ] `/metrics` endpoint exposes operational metrics.

- [ ] Structured logs include exchange, duration, attempt, counts, and errors.

- [ ] Graceful shutdown works for SIGINT and SIGTERM.

- [ ] Shutdown does not hang indefinitely.

- [ ] Configuration is loaded from environment variables.

- [ ] Service can run in containerized environments.

---

### 27.4 Quality

- [ ] Unit tests cover normalization and intersection logic.

- [ ] Table-driven tests cover symbol edge cases.

- [ ] Exchange client tests use `httptest` fixtures.

- [ ] HTTP handler tests cover success, empty, unavailable, and method-not-allowed cases.

- [ ] Concurrency tests pass with `-race`.

- [ ] No data race exists between refresh and read handlers.

- [ ] Code is formatted with `gofmt`.

- [ ] `go vet ./...` passes.

- [ ] `staticcheck ./...` or equivalent linter passes.

- [ ] `go build ./...` succeeds with Go 1.26.

---

### 27.5 Performance

- [ ] `/api/v1/symbols/overlapping` responds in under 5 ms from memory under normal load.

- [ ] Read path does not marshal JSON on every request.

- [ ] Read path does not hold a mutex for response generation.

- [ ] Refresh does not allocate unnecessary large copies of upstream payloads.

---

## 28. Future Extensions

The following are intentionally out of scope for v1 but may be considered later:

1. Additional exchanges.

2. Additional quote assets, such as USDC or USD.

3. Spot symbol resolution.

4. Historical symbol snapshot storage.

5. WebSocket-based symbol change detection.

6. Admin endpoint to force manual refresh.

7. Symbol metadata enrichment, such as tick size, lot size, and price precision.

8. Exchange-specific trading rule metadata.

9. Shared snapshot storage across multiple instances.

10. gRPC API.

11. GraphQL API.

12. Symbol mapping diff endpoint.

13. Alerts when overlap count changes materially.

14. Exchange API schema drift detection.

---

## 29. Open Questions for Production Tuning

These values should be tuned per environment:

1. Exact minimum expected symbol count per exchange.

2. Exact minimum expected overlap count.

3. Maximum acceptable staleness.

4. Whether strict startup should be enabled.

5. Whether MEXC fallback parsing is required.

6. Whether duplicate canonical symbols should fail or warn.

7. Desired metrics backend.

8. Desired log retention and sampling policy.

9. Whether multiple replicas require shared state.

10. Whether rate-limit protection or API keys are required for exchange endpoints.