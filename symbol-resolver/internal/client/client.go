package client

import (
	"context"
	"net"
	"net/http"
	"time"

	"symbol-resolver/internal/model"
)

// ExchangeClient is implemented by every exchange-specific client
type ExchangeClient interface {
	Name() string
	FetchActiveUSDTSymbols(ctx context.Context) (map[model.CanonicalSymbol]string, error)
}

// NewHTTPClient returns a shared HTTP client with connection pooling and
// timeouts configured for exchange REST API calls
func NewHTTPClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     60 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}
}
