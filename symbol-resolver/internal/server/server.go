package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Server holds the HTTP mux and the cache it serves from
type Server struct {
	mux   *http.ServeMux
	cache *Cache
}

// NewServer constructs a Server with all routes registered
func NewServer(cache *Cache) *Server {
	s := &Server{
		mux:   http.NewServeMux(),
		cache: cache,
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler, allowing Server to be passed
// directly to http.ListenAndServe
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// routes registers all HTTP endpoints
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/symbols/overlapping", s.handleOverlapping())
	s.mux.HandleFunc("GET /health", s.handleHealth())
}

// handleOverlapping returns the current SymbolIntersection from cache
func (s *Server) handleOverlapping() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cache.IsReady() {
			writeError(w, "service is initializing", http.StatusServiceUnavailable)
			return
		}

		intersection := s.cache.Get()

		writeJSON(w, intersection, http.StatusOK)
	}
}

// handleHealth returns the service health status and last cache update time
func (s *Server) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cache.IsReady() {
			writeJSON(w, map[string]string{
				"status":      "initializing",
				"last_update": "",
			}, http.StatusServiceUnavailable)
			return
		}

		intersection := s.cache.Get()

		writeJSON(w, map[string]string{
			"status":      "ok",
			"last_update": intersection.UpdatedAt.Format(time.RFC3339),
		}, http.StatusOK)
	}
}

// writeJSON encodes v as JSON and writes it to w with the given status code
func writeJSON(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("error encoding JSON response: %v", err)
	}
}

// writeError writes a JSON error response with the given message and status code
func writeError(w http.ResponseWriter, message string, status int) {
	writeJSON(w, map[string]string{
		"error": message,
	}, status)
}
