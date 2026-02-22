package mgmt

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// RateLimitConfig holds rate limiter configuration.
type RateLimitConfig struct {
	RPS   int // requests per second
	Burst int // burst size
}

type rateLimiter struct {
	mu      sync.Mutex
	clients map[string]*tokenBucket
	rps     int
	burst   int
}

type tokenBucket struct {
	tokens    float64
	maxTokens float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

func newTokenBucket(rps, burst int) *tokenBucket {
	return &tokenBucket{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: float64(rps),
		lastRefill: time.Now(),
	}
}

func (b *tokenBucket) allow() bool {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.maxTokens {
		b.tokens = b.maxTokens
	}
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// NewRateLimitMiddleware returns a per-client token-bucket rate limiter.
func NewRateLimitMiddleware(cfg RateLimitConfig) fiber.Handler {
	rl := &rateLimiter{
		clients: make(map[string]*tokenBucket),
		rps:     cfg.RPS,
		burst:   cfg.Burst,
	}

	// Cleanup stale entries periodically
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for k, v := range rl.clients {
				if now.Sub(v.lastRefill) > 10*time.Minute {
					delete(rl.clients, k)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return func(c *fiber.Ctx) error {
		// Skip rate limiting for probe endpoints
		path := c.Path()
		if path == "/healthz" || path == "/readyz" || path == "/metrics" {
			return c.Next()
		}

		clientIP := c.IP()

		rl.mu.Lock()
		bucket, ok := rl.clients[clientIP]
		if !ok {
			bucket = newTokenBucket(rl.rps, rl.burst)
			rl.clients[clientIP] = bucket
		}
		allowed := bucket.allow()
		rl.mu.Unlock()

		if !allowed {
			return problemResponse(c, fiber.StatusTooManyRequests,
				"rate_limit_exceeded", "Too Many Requests",
				"Rate limit exceeded. Please try again later.")
		}

		return c.Next()
	}
}
