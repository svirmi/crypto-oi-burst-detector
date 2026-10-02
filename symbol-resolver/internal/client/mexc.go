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
		if c.QuoteCoin != "USDT" {
			continue
		}

		canonical := model.CanonicalSymbol(strings.Replace(c.Symbol, "_", "-", 1))
		symbols[canonical] = c.Symbol
	}

	return symbols, nil
}
