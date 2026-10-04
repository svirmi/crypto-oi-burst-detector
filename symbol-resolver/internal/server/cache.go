package server

import (
	"sync"

	"symbol-resolver/internal/model"
)

// Cache holds the latest computed SymbolIntersection in memory,
// protected by a RWMutex for concurrent read access
type Cache struct {
	mu   sync.RWMutex
	data *model.SymbolIntersection
}

// NewCache returns an empty Cache instance
func NewCache() *Cache {
	return &Cache{}
}

// Set atomically replaces the cached SymbolIntersection with a new snapshot
func (c *Cache) Set(intersection *model.SymbolIntersection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = intersection
}

// Get returns the current cached SymbolIntersection.
// Returns nil if the cache has not been populated yet.
func (c *Cache) Get() *model.SymbolIntersection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}

// IsReady reports whether the cache has been populated with at least
// one successful intersection computation
func (c *Cache) IsReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data != nil
}
