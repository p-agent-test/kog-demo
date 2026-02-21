package tokenstore

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-memory token store for development.
type MemoryStore struct {
	mu     sync.RWMutex
	tokens map[string]*Token
}

// NewMemoryStore creates a new in-memory token store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tokens: make(map[string]*Token),
	}
}

func (m *MemoryStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[key] = &Token{
		Key:       key,
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (m *MemoryStore) Get(_ context.Context, key string) (*Token, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tok, ok := m.tokens[key]
	if !ok {
		return nil, ErrTokenNotFound
	}
	if tok.IsExpired() {
		return nil, ErrTokenExpired
	}
	return tok, nil
}

func (m *MemoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, key)
	return nil
}

func (m *MemoryStore) Cleanup(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for k, tok := range m.tokens {
		if tok.IsExpired() {
			delete(m.tokens, k)
			count++
		}
	}
	return count, nil
}
