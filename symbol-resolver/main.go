package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"symbol-resolver/internal/client"
	"symbol-resolver/internal/model"
	"symbol-resolver/internal/resolver"
	"symbol-resolver/internal/server"
)

const (
	httpAddr        = ":8080"
	refreshInterval = 1 * time.Hour
	retryInterval   = 5 * time.Minute
	fetchTimeout    = 5 * time.Second
)

var retryBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

func main() {
	log.Println("starting symbol-resolver")

	// shared HTTP client with connection pooling
	httpClient := client.NewHTTPClient()

	// exchange clients
	clients := []client.ExchangeClient{
		client.NewBinanceClient(httpClient),
		client.NewBybitClient(httpClient),
		client.NewMexcClient(httpClient),
	}

	// in-memory cache
	cache := server.NewCache()

	// --- startup sync (blocking, must succeed before serving) ---
	log.Println("performing initial symbol sync...")

	intersection, err := fetchWithRetry(clients, retryBackoff)
	if err != nil {
		log.Fatalf("initial sync failed after all retries: %v", err)
	}

	cache.Set(intersection)
	log.Printf("initial sync complete: %d overlapping symbols", intersection.TotalSymbols)

	// --- background refresh goroutine ---
	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("background refresh: starting symbol sync...")

			result, err := fetchOnce(clients)
			if err != nil {
				log.Printf("background refresh failed: %v — retrying in %s", err, retryInterval)

				// retry once after retryInterval, do not wipe cache
				time.Sleep(retryInterval)

				result, err = fetchOnce(clients)
				if err != nil {
					log.Printf("background refresh retry failed: %v — keeping previous cache", err)
					continue
				}
			}

			cache.Set(result)
			log.Printf("background refresh complete: %d overlapping symbols", result.TotalSymbols)
		}
	}()

	// --- HTTP server ---
	srv := &http.Server{
		Addr:         httpAddr,
		Handler:      server.NewServer(cache),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// graceful shutdown listener
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("HTTP server listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// block until shutdown signal received
	<-quit
	log.Println("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}

	log.Println("symbol-resolver stopped")
}

// fetchWithRetry attempts to fetch symbol data from all exchanges,
// retrying with exponential backoff on failure
func fetchWithRetry(clients []client.ExchangeClient, backoff []time.Duration) (*model.SymbolIntersection, error) {
	var lastErr error

	for attempt, wait := range backoff {
		result, err := fetchOnce(clients)
		if err == nil {
			return result, nil
		}

		lastErr = err
		log.Printf("sync attempt %d failed: %v — retrying in %s", attempt+1, err, wait)
		time.Sleep(wait)
	}

	// final attempt after last backoff
	result, err := fetchOnce(clients)
	if err != nil {
		return nil, errors.Join(lastErr, err)
	}

	return result, nil
}

// fetchOnce concurrently fetches symbols from all exchanges and
// computes the intersection. Fails fast if any exchange returns an error.
func fetchOnce(clients []client.ExchangeClient) (*model.SymbolIntersection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	type result struct {
		name    string
		symbols map[model.CanonicalSymbol]string
		err     error
	}

	results := make(chan result, len(clients))

	for _, c := range clients {
		c := c // capture loop variable
		go func() {
			symbols, err := c.FetchActiveUSDTSymbols(ctx)
			results <- result{name: c.Name(), symbols: symbols, err: err}
		}()
	}

	symbolsByExchange := make(map[string]map[model.CanonicalSymbol]string)

	for range clients {
		r := <-results
		if r.err != nil {
			return nil, r.err
		}
		symbolsByExchange[r.name] = r.symbols
		log.Printf("fetched %d symbols from %s", len(r.symbols), r.name)
	}

	return resolver.Compute(
		symbolsByExchange["binance"],
		symbolsByExchange["bybit"],
		symbolsByExchange["mexc"],
	), nil
}
