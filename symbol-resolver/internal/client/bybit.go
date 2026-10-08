package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"symbol-resolver/internal/model"
)

const bybitInstrumentsURL = "https://api.bybit.com/v5/market/instruments-info?category=linear&limit=1000"

// bybitInstrument represents a single instrument entry in Bybit V5 response
type bybitInstrument struct {
	Symbol       string `json:"symbol"`
	BaseCoin     string `json:"baseCoin"`
	QuoteCoin    string `json:"quoteCoin"`
	ContractType string `json:"contractType"`
	Status       string `json:"status"`
}

// bybitResult is the inner result object in Bybit V5 response
type bybitResult struct {
	List           []bybitInstrument `json:"list"`
	NextPageCursor string            `json:"nextPageCursor"`
}

// bybitInstrumentsResponse is the top-level Bybit V5 API response
type bybitInstrumentsResponse struct {
	RetCode int         `json:"retCode"`
	RetMsg  string      `json:"retMsg"`
	Result  bybitResult `json:"result"`
}

// BybitClient implements ExchangeClient for Bybit V5 Linear Perpetuals
type BybitClient struct {
	httpClient *http.Client
}

// NewBybitClient constructs a BybitClient with the shared HTTP client
func NewBybitClient(httpClient *http.Client) *BybitClient {
	return &BybitClient{httpClient: httpClient}
}

// Name returns the exchange identifier
func (b *BybitClient) Name() string {
	return "bybit"
}

// FetchActiveUSDTSymbols fetches all active USDT linear perpetual contracts
// from Bybit and returns them as a canonical symbol map.
// Handles pagination via nextPageCursor.
func (b *BybitClient) FetchActiveUSDTSymbols(ctx context.Context) (map[model.CanonicalSymbol]string, error) {
	symbols := make(map[model.CanonicalSymbol]string)
	cursor := ""

	for {
		url := bybitInstrumentsURL
		if cursor != "" {
			url += "&cursor=" + cursor
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("bybit: failed to build request: %w", err)
		}

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("bybit: request failed: %w", err)
		}
		defer resp.Body.Close() // does it smell ?

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("bybit: unexpected status code: %d", resp.StatusCode)
		}

		var instrumentsResp bybitInstrumentsResponse
		if err := json.NewDecoder(resp.Body).Decode(&instrumentsResp); err != nil {
			return nil, fmt.Errorf("bybit: failed to decode response: %w", err)
		}

		if instrumentsResp.RetCode != 0 {
			return nil, fmt.Errorf("bybit: API error: %s (code: %d)", instrumentsResp.RetMsg, instrumentsResp.RetCode)
		}

		for _, s := range instrumentsResp.Result.List {
			if s.Status != "Trading" {
				continue
			}
			quoteValue := strings.TrimSpace(s.QuoteCoin)
			if quoteValue == "" {
				return nil, fmt.Errorf("bybit: missing quote coin for %q", s.Symbol)
			}
			if !strings.EqualFold(quoteValue, "USDT") {
				continue
			}
			if s.ContractType != "LinearPerpetual" {
				continue
			}

			base := strings.ToUpper(strings.TrimSpace(s.BaseCoin))
			quote := strings.ToUpper(quoteValue)
			raw := s.Symbol
			if base == "" || quote != "USDT" || raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(base, "- \t\r\n") {
				return nil, fmt.Errorf("bybit: invalid symbol metadata for %q", s.Symbol)
			}
			canonical := model.CanonicalSymbol(base + "-" + quote)
			if existing, ok := symbols[canonical]; ok {
				return nil, fmt.Errorf("bybit: duplicate canonical symbol %q from %q and %q", canonical, existing, raw)
			}
			symbols[canonical] = raw
		}

		// no more pages
		if instrumentsResp.Result.NextPageCursor == "" {
			break
		}
		cursor = instrumentsResp.Result.NextPageCursor
	}

	return symbols, nil
}
