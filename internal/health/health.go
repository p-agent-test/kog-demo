// Package health provides liveness and readiness endpoints for the platform agent.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Status represents the health status of a dependency.
type Status string

const (
	StatusOK      Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown    Status = "down"
)

// CheckFunc is a function that checks a dependency's health.
type CheckFunc func(ctx context.Context) Status

// Checker manages health checks for all dependencies.
type Checker struct {
	mu     sync.RWMutex
	checks map[string]CheckFunc
	cache  map[string]Status
	logger zerolog.Logger
}

// NewChecker creates a new health checker.
func NewChecker(logger zerolog.Logger) *Checker {
	return &Checker{
		checks: make(map[string]CheckFunc),
		cache:  make(map[string]Status),
		logger: logger.With().Str("component", "health").Logger(),
	}
}

// Register adds a named health check.
func (c *Checker) Register(name string, fn CheckFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = fn
}

// RunAll executes all health checks concurrently and caches results.
func (c *Checker) RunAll(ctx context.Context) map[string]Status {
	c.mu.RLock()
	checks := make(map[string]CheckFunc, len(c.checks))
	for k, v := range c.checks {
		checks[k] = v
	}
	c.mu.RUnlock()

	results := make(map[string]Status, len(checks))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for name, fn := range checks {
		wg.Add(1)
		go func(n string, f CheckFunc) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			s := f(checkCtx)
			mu.Lock()
			results[n] = s
			mu.Unlock()
		}(name, fn)
	}

	wg.Wait()

	c.mu.Lock()
	c.cache = results
	c.mu.Unlock()

	return results
}

// IsReady returns true if all checks pass.
func (c *Checker) IsReady(ctx context.Context) bool {
	results := c.RunAll(ctx)
	for _, s := range results {
		if s == StatusDown {
			return false
		}
	}
	return true
}

// LivenessHandler returns an HTTP handler for /health (liveness).
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// ReadinessHandler returns an HTTP handler for /ready (readiness).
func (c *Checker) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := c.RunAll(r.Context())

		allOK := true
		for _, s := range results {
			if s == StatusDown {
				allOK = false
				break
			}
		}

		resp := map[string]interface{}{
			"checks": results,
		}

		if allOK {
			resp["status"] = "ready"
			w.WriteHeader(http.StatusOK)
		} else {
			resp["status"] = "not_ready"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		json.NewEncoder(w).Encode(resp)
	}
}
