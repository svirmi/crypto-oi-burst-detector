package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"symbol-resolver/internal/model"
)

const mexcContractDetailURL = "https://contract.mexc.com/api/v1/contract/detail"

// mexcContract represents a single contract entry in MEXC Futures response
type mexcContract struct {
	Symbol    string `json:"symbol"`
	BaseCoin  string `json:"baseCoin"`
	QuoteCoin string `json:"quoteCoin"`
	State     int    `json:"state"`
}

// mexcContractDetailResponse is the top-level MEXC API response
type mexcContractDetailResponse struct {
	Success bool           `json:"success"`
	Code    int            `json:"code"`
	Data    []mexcContract `json:"data"`
}

// MexcClient implements ExchangeClient for MEXC Futures
type MexcClient struct {
	httpClient *http.Client
}

// NewMexcClient constructs a MexcClient with the shared HTTP client
func NewMexcClient(httpClient *http.Client) *MexcClient {
	return &MexcClient{httpClient: httpClient}
}

// Name returns the exchange identifier
func (m *MexcClient) Name() string {
	return "mexc"
}

// FetchActiveUSDTSymbols fetches all active USDT perpetual contracts from
// MEXC and returns them as a canonical symbol map.
// MEXC symbols use underscore notation (e.g. "BTC_USDT") which is normalized
// to canonical dash notation (e.g. "BTC-USDT")
func (m *MexcClient) FetchActiveUSDTSymbols(ctx context.Context) (map[model.CanonicalSymbol]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mexcContractDetailURL, nil)
	if err != nil {
		return nil, fmt.Errorf("mexc: failed to build request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mexc: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mexc: unexpected status code: %d", resp.StatusCode)
	}

	var contractResp mexcContractDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&contractResp); err != nil {
		return nil, fmt.Errorf("mexc: failed to decode response: %w", err)
	}

	if !contractResp.Success {
		return nil, fmt.Errorf("mexc: API error: code %d", contractResp.Code)
	}

	symbols := make(map[model.CanonicalSymbol]string)

	for _, c := range contractResp.Data {
		if c.State != 0 {
			continue
		}
		quoteValue := strings.TrimSpace(c.QuoteCoin)
		if quoteValue == "" {
			return nil, fmt.Errorf("mexc: missing quote coin for %q", c.Symbol)
		}
		quote := strings.ToUpper(quoteValue)
		if quote != "USDT" {
			continue
		}

		base := strings.ToUpper(strings.TrimSpace(c.BaseCoin))
		raw := c.Symbol
		if base == "" || raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(base, "- \t\r\n") {
			return nil, fmt.Errorf("mexc: invalid symbol metadata for %q", c.Symbol)
		}
		canonical := model.CanonicalSymbol(base + "-" + quote)
		if existing, ok := symbols[canonical]; ok {
			return nil, fmt.Errorf("mexc: duplicate canonical symbol %q from %q and %q", canonical, existing, raw)
		}
		symbols[canonical] = raw
	}

	return symbols, nil
}
