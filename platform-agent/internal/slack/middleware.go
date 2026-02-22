package slack

import (
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Middleware provides rate limiting and auth checks.
type Middleware struct {
	logger      zerolog.Logger
	rateLimiter *RateLimiter
}

// NewMiddleware creates a new middleware instance.
func NewMiddleware(logger zerolog.Logger, maxRequests int, window time.Duration) *Middleware {
	return &Middleware{
		logger:      logger.With().Str("component", "slack.middleware").Logger(),
		rateLimiter: NewRateLimiter(maxRequests, window),
	}
}

// CheckRateLimit returns true if the user is within rate limits.
func (m *Middleware) CheckRateLimit(userID string) bool {
	allowed := m.rateLimiter.Allow(userID)
	if !allowed {
		m.logger.Warn().Str("user_id", userID).Msg("rate limited")
	}
	return allowed
}

// RateLimiter implements a simple sliding window rate limiter per user.
type RateLimiter struct {
	mu          sync.Mutex
	maxRequests int
	window      time.Duration
	requests    map[string][]time.Time
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		maxRequests: maxRequests,
		window:      window,
		requests:    make(map[string][]time.Time),
	}
}

// Allow checks if a request from the given key is allowed.
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Filter expired entries
	times := r.requests[key]
	valid := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= r.maxRequests {
		r.requests[key] = valid
		return false
	}

	r.requests[key] = append(valid, now)
	return true
}
