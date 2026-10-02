package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"symbol-resolver/internal/model"
)

const binanceExchangeInfoURL = "https://fapi.binance.com/fapi/v1/exchangeInfo"

// binanceSymbol represents a single symbol entry in Binance exchangeInfo response
type binanceSymbol struct {
	Symbol       string `json:"symbol"`
	BaseAsset    string `json:"baseAsset"`
	QuoteAsset   string `json:"quoteAsset"`
	ContractType string `json:"contractType"`
	Status       string `json:"status"`
}

// binanceExchangeInfoResponse is the top-level Binance API response
type binanceExchangeInfoResponse struct {
	Symbols []binanceSymbol `json:"symbols"`
}

// BinanceClient implements ExchangeClient for Binance USDM Futures
type BinanceClient struct {
	httpClient *http.Client
}

// NewBinanceClient constructs a BinanceClient with the shared HTTP client
func NewBinanceClient(httpClient *http.Client) *BinanceClient {
	return &BinanceClient{httpClient: httpClient}
}

// Name returns the exchange identifier
func (b *BinanceClient) Name() string {
	return "binance"
}

// FetchActiveUSDTSymbols fetches all active USDT perpetual contracts from
// Binance and returns them as a canonical symbol map
func (b *BinanceClient) FetchActiveUSDTSymbols(ctx context.Context) (map[model.CanonicalSymbol]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binanceExchangeInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("binance: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance: unexpected status code: %d", resp.StatusCode)
	}

	var exchangeInfo binanceExchangeInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&exchangeInfo); err != nil {
		return nil, fmt.Errorf("binance: failed to decode response: %w", err)
	}

	symbols := make(map[model.CanonicalSymbol]string)

	for _, s := range exchangeInfo.Symbols {
		if s.Status != "TRADING" {
			continue
		}
		if s.QuoteAsset != "USDT" {
			continue
		}
		if s.ContractType != "PERPETUAL" {
			continue
		}

		canonical := model.CanonicalSymbol(s.BaseAsset + "-" + s.QuoteAsset)
		symbols[canonical] = s.Symbol
	}

	return symbols, nil
}
