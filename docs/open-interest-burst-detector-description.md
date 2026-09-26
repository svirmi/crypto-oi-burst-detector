Here is the updated, complete **Project Technical Specification & API Reference Document** in Markdown format, incorporating all full API endpoints, request/response parameters, quota strategies, data normalization formulas, and pattern detection rules. 

You can copy and paste the block below directly into your documentation or repository README.

***

```markdown
# Cross-Exchange Open Interest & Price Dynamic Aggregator
## Technical Overview & Complete API Specification

**Document Version:** 1.1  
**Target Audience:** Software Engineering Team, Quant Developers, System Architects  
**Target Exchanges:** Binance Futures (USDⓈ-M), Bybit V5 (Linear Perpetuals), MEXC Futures  
**Scope:** Real-Time Tracking & Aggregation across 120+ Cryptocurrency Pairs  

---

## 1. Executive Summary & System Goals

The objective of this system is to collect, normalize, and aggregate **Open Interest (OI)** and **Price dynamics** across **Binance Futures**, **Bybit V5**, and **MEXC Futures** for 120+ active derivative symbols in near real-time (1–5s latency).

### Key Architectural Requirements
* **Zero Private Authentication:** All market data must be collected using **Public Endpoints** (no API key lifecycle, signing, or permissions management required).
* **Strict Quota Compliance:** The polling strategy must consume **< 60%** of any exchange's maximum IP rate limits.
* **USD Standardization:** Raw OI metrics (coins, contract sheets, double-sided figures) must be converted into a single unified USD value.
* **Pattern Detection Engine:** Compute the velocity of OI growth vs. Price growth over rolling windows to detect market regimes (Aggressive Longs, Aggressive Shorts, Short Squeezes, Long Liquidations).

---

## 2. Complete API Endpoint Reference

### 2.1 MEXC Futures REST API

MEXC provides bulk market endpoints that allow fetching all contracts in a single HTTP request without authentication.

* **Base URL:** `https://contract.mexc.com`
* **Rate Limit:** 10 requests / 2 seconds

#### Endpoint 1: Bulk Market Ticker (All Symbols) — **[PRIMARY]**
* **Method & Path:** `GET /api/v1/contract/ticker`
* **Authentication:** None (Public)
* **Query Parameters:** None (Omitting `symbol` returns data for ALL active perpetual contracts).
* **Open Interest Field:** `holdVol` (Total holding volume in **contract sheets**).
* **Price Field:** `lastPrice`, `fairPrice`
* **Sample Request:**
  ```http
  GET https://contract.mexc.com/api/v1/contract/ticker
  ```
* **Sample Response Payload:**
  ```json
  {
    "success": true,
    "code": 0,
    "data": [
      {
        "symbol": "BTC_USDT",
        "lastPrice": 6865.5,
        "bid1": 6865.0,
        "ask1": 6866.5,
        "holdVol": 2284742,
        "fairPrice": 6867.4,
        "indexPrice": 6861.6,
        "riseFallRate": -0.0424,
        "volume24": 164586129,
        "timestamp": 1587442022003
      }
    ]
  }
  ```

#### Endpoint 2: Single Symbol Ticker
* **Method & Path:** `GET /api/v1/contract/ticker?symbol={symbol}`
* **Query Parameters:** `symbol` (e.g., `BTC_USDT`)

---

### 2.2 Bybit V5 REST API

Bybit V5 provides comprehensive market data endpoints with native multi-side open interest fields.

* **Base URL:** `https://api.bybit.com`
* **Rate Limit:** ~10–20 requests per second for public market endpoints.

#### Endpoint 1: Bulk Linear Tickers (All Symbols) — **[PRIMARY]**
* **Method & Path:** `GET /v5/market/tickers`
* **Authentication:** None (Public)
* **Query Parameters:**
  * `category` *(required)*: `linear` (USDT / USDC perpetuals) or `inverse`
* **Sample Request:**
  ```http
  GET https://api.bybit.com/v5/market/tickers?category=linear
  ```
* **Sample Response Payload:**
  ```json
  {
    "retCode": 0,
    "retMsg": "OK",
    "result": {
      "category": "linear",
      "list": [
        {
          "symbol": "BTCUSDT",
          "lastPrice": "6865.50",
          "indexPrice": "6861.60",
          "markPrice": "6867.40",
          "openInterest": "2284742.000",
          "openInterestValue": "156847290.50",
          "singleOpenInterest": "1142371.000",
          "singleOpenInterestValue": "78423645.25",
          "turnover24h": "10374579341.00",
          "volume24h": "954830.62"
        }
      ]
    }
  }
  ```

#### Endpoint 2: Historical / Interval Open Interest
* **Method & Path:** `GET /v5/market/open-interest`
* **Authentication:** None (Public)
* **Query Parameters:**
  * `category` *(required)*: `linear`
  * `symbol` *(required)*: e.g., `BTCUSDT`
  * `intervalTime` *(required)*: `5min`, `15min`, `30min`, `1h`, `4h`, `1d`
  * `limit` *(optional)*: 1–200 (Default: 50)
* **Sample Request:**
  ```http
  GET https://api.bybit.com/v5/market/open-interest?category=linear&symbol=BTCUSDT&intervalTime=5min&limit=10
  ```

---

### 2.3 Binance Futures REST API

Binance requires symbol-level queries for current Open Interest, requiring a staggered request queue.

* **Base URL:** `https://fapi.binance.com` (USDⓈ-M Futures)
* **IP Weight Quota:** 2,400 weight per minute per IP.

#### Endpoint 1: Single Symbol Open Interest Snapshot — **[PRIMARY]**
* **Method & Path:** `GET /fapi/v1/openInterest`
* **Authentication:** None (Public)
* **IP Weight:** 1 per request
* **Query Parameters:**
  * `symbol` *(required)*: e.g., `BTCUSDT`
* **Sample Request:**
  ```http
  GET https://fapi.binance.com/fapi/v1/openInterest?symbol=BTCUSDT
  ```
* **Sample Response Payload:**
  ```json
  {
    "symbol": "BTCUSDT",
    "openInterest": "10659.509",
    "time": 1589437530011
  }
  ```

#### Endpoint 2: All Symbol Prices (Bulk Request) — **[PRIMARY]**
* **Method & Path:** `GET /fapi/v1/ticker/price`
* **Authentication:** None (Public)
* **IP Weight:** 2
* **Query Parameters:** None (Returns current price for all symbols)
* **Sample Request:**
  ```http
  GET https://fapi.binance.com/fapi/v1/ticker/price
  ```
* **Sample Response Payload:**
  ```json
  [
    {
      "symbol": "BTCUSDT",
      "price": "6865.50",
      "time": 1589437530011
    }
  ]
  ```

#### Endpoint 3: Historical Open Interest Statistics
* **Method & Path:** `GET /futures/data/openInterestHist`
* **Authentication:** None (Public)
* **IP Weight:** 1
* **Query Parameters:**
  * `symbol` *(required)*: e.g., `BTCUSDT`
  * `period` *(required)*: `5m`, `15m`, `30m`, `1h`, `4h`, `1d`
  * `limit` *(optional)*: 1–500 (Default: 30)
* **Sample Request:**
  ```http
  GET https://fapi.binance.com/futures/data/openInterestHist?symbol=BTCUSDT&period=5m&limit=10
  ```

---

## 3. Quota & Rate Limit Execution Strategy (120 Symbols)

To avoid IP bans while maintaining 1-second to 5-second polling resolution across 120+ symbols:

```
[System Scheduler - 1s Interval Loop]
  ├──> Bybit Worker   ──> GET /v5/market/tickers?category=linear (1 req/s | 100% symbols)
  ├──> MEXC Worker    ──> GET /api/v1/contract/ticker            (1 req/s | 100% symbols)
  └──> Binance Queue  ──> Batch of 24 Symbols / sec              (24 reqs/s | 5s rotation)
                            └──> Plus 1 req/s for GET /fapi/v1/ticker/price
```

### Rate Limit Budget Table

| Exchange | Target Symbols | Endpoint Pattern | Polling Freq | IP Weight / Sec | IP Weight / Min | Allowed Quota / Min | Usage Ratio |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Bybit** | 120+ | Bulk Ticker (`/v5/market/tickers`) | 1 sec | 1 req/s | 60 reqs/min | 600–1200 reqs/min | **~5%** |
| **MEXC** | 120+ | Bulk Ticker (`/api/v1/contract/ticker`) | 1 sec | 1 req/s | 60 reqs/min | 300 reqs/min | **20%** |
| **Binance** | 120 | Staggered 24 symbols/s + Price Bulk | 1 sec (Batch) | 25 weight/s | 1,500 weight/min | 2,400 weight/min | **62.5%** |

---

## 4. Data Normalization & Accounting Nuances

Each exchange reports Open Interest using different conventions. All raw metrics **must** be converted to **USD Value** before aggregation.

### 4.1 Exchange Normalization Formulas

1. **Binance Futures:**
   * *Raw Field:* `openInterest` (Base Asset / Coin Units)
   * *Accounting:* One-Sided (Longs = Shorts)
   * *Formula:*
     \\[\text{OI}_{\text{Binance, USD}} = \text{openInterest} \times \text{lastPrice}\\]

2. **Bybit V5:**
   * *Raw Fields:* `openInterest` (Double-sided), `singleOpenInterestValue` (USD One-Sided)
   * *Accounting:* Provides both double-sided and single-sided figures.
   * *Formula (Direct Field Use):*
     \\[\text{OI}_{\text{Bybit, USD}} = \text{singleOpenInterestValue}\\]
   * *(Alternative if using double-sided volume:)*
     \\[\text{OI}_{\text{Bybit, USD}} = \frac{\text{openInterestValue}}{2}\\]

3. **MEXC Futures:**
   * *Raw Field:* `holdVol` (Contract Sheets/Pieces)
   * *Accounting:* One-Sided
   * *Formula:*
     \\[\text{OI}_{\text{MEXC, USD}} = \text{holdVol} \times \text{contractSize} \times \text{lastPrice}\\]

### 4.2 Global Aggregated Open Interest
\\[\text{Global Open Interest (USD)} = \text{OI}_{\text{Binance, USD}} + \text{OI}_{\text{Bybit, USD}} + \text{OI}_{\text{MEXC, USD}}\\]

---

## 5. Pattern & Market Regime Detection Engine

The system computes rolling velocity for Price (\\(\Delta P\\)) and Open Interest (\\(\Delta OI\\)) over a **5-second to 1-minute window**:

\\[\Delta P = \frac{P_t - P_{t-k}}{P_{t-k}} \qquad \text{and} \qquad \Delta OI = \frac{OI_t - OI_{t-k}}{OI_{t-k}}\\]

### Market Regime Classification Matrix

| Price Delta (\\(\Delta P\\)) | OI Delta (\\(\Delta OI\\)) | Dominant Market Behavior | Market Regime & Signal |
| :---: | :---: | :--- | :--- |
| **Rising (↑)** | **Rising (↑)** | Aggressive Long Expansion | 🟢 **Bullish Trend Expansion** |
| **Falling (↓)** | **Rising (↑)** | Aggressive Short Expansion | 🔴 **Bearish Trend Expansion** |
| **Rising (↑)** | **Falling (↓)** | Short Covering / Squeeze | 🟡 **Short Squeeze / Exhaustion Rally** |
| **Falling (↓)** | **Falling (↓)** | Long Liquidation / Unwinding | 🟡 **Long Unwinding / Flushout** |

### Velocity Anomaly Trigger (Spike Detection)
An alert is dispatched when the 1-minute velocity of Open Interest exceeds **3 standard deviations** (\\(\sigma\\)) from the 1-hour rolling mean:

\\[\text{Anomaly Alert} \iff \left| \frac{\Delta OI}{\Delta t} - \mu_{\text{OI, 1h}} \right| > 3 \times \sigma_{\text{OI, 1h}}\\]

---

## 6. Technical Stack & Implementation Architecture

* **Language/Runtime:** Python 3.12 (`asyncio`)
* **HTTP Client:** `aiohttp` with `ClientSession` TCP Connection Pooling & Keep-Alive Enabled
* **In-Memory Buffer / Storage:** Redis Ring Buffer (Stores last 300 seconds of time-series ticks per symbol)
* **Synchronization Smoothing:** 5-second Exponential Moving Average (EMA) applied to Binance metrics to align with Bybit/MEXC 1-second refresh rates.
```
