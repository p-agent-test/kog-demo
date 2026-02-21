package tokenstore

import (
	"context"
	"errors"
	"time"
)

var (
	ErrTokenNotFound = errors.New("token not found")
	ErrTokenExpired  = errors.New("token expired")
)

// Token represents a stored token with metadata.
type Token struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// IsExpired checks if the token has expired.
func (t *Token) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// Store defines the token storage interface.
type Store interface {
	// Set stores a token with the given key and TTL.
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// Get retrieves a token by key. Returns ErrTokenNotFound or ErrTokenExpired.
	Get(ctx context.Context, key string) (*Token, error)
	// Delete removes a token by key.
	Delete(ctx context.Context, key string) error
	// Cleanup removes all expired tokens.
	Cleanup(ctx context.Context) (int, error)
}
