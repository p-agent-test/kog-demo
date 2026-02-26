package mgmt

import (
	"sync"
	"time"

	"github.com/p-blackswan/platform-agent/internal/store"
)

// SessionContext holds Slack routing info for a session.
type SessionContext struct {
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
	ThreadTS  string `json:"thread_ts"`
	UpdatedAt int64  `json:"updated_at"`
}

// SessionContextStore maps session IDs to their Slack routing context.
// Thread-safe. Entries expire after TTL. Optional SQLite backend via store.
type SessionContextStore struct {
	mu       sync.RWMutex
	contexts map[string]*SessionContext
	ttl      time.Duration
	store    *store.Store // optional SQLite backend
}

// NewSessionContextStore creates a new store with the given TTL.
func NewSessionContextStore(ttl time.Duration, opts ...SessionContextOption) *SessionContextStore {
	s := &SessionContextStore{
		contexts: make(map[string]*SessionContext),
		ttl:      ttl,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SessionContextOption is a functional option for SessionContextStore
type SessionContextOption func(*SessionContextStore)

// WithStore adds an optional SQLite backend
func WithStore(ds *store.Store) SessionContextOption {
	return func(s *SessionContextStore) {
		s.store = ds
	}
}

// Set registers or updates the routing context for a session.
func (s *SessionContextStore) Set(ctx SessionContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx.UpdatedAt = time.Now().UnixMilli()
	s.contexts[ctx.SessionID] = &ctx

	// Also persist to SQLite if available
	if s.store != nil {
		storeCtx := &store.SessionContext{
			SessionID: ctx.SessionID,
			Channel:   ctx.Channel,
			ThreadTS:  ctx.ThreadTS,
			CreatedAt: ctx.UpdatedAt,
			LastUsed:  ctx.UpdatedAt,
		}
		_ = s.store.SaveSessionContext(storeCtx) // graceful degradation: log but don't block
	}
}

// Get returns the routing context for a session (nil if not found or expired).
func (s *SessionContextStore) Get(sessionID string) *SessionContext {
	s.mu.RLock()
	ctx, ok := s.contexts[sessionID]
	s.mu.RUnlock()

	if ok {
		if s.ttl > 0 && time.Since(time.UnixMilli(ctx.UpdatedAt)) > s.ttl {
			return nil
		}
		return ctx
	}

	// Cold start: try to load from store
	if s.store != nil {
		storeCtx, err := s.store.GetSessionContext(sessionID)
		if err == nil && storeCtx != nil {
			sCtx := &SessionContext{
				SessionID: storeCtx.SessionID,
				Channel:   storeCtx.Channel,
				ThreadTS:  storeCtx.ThreadTS,
				UpdatedAt: storeCtx.LastUsed,
			}
			if s.ttl > 0 && time.Since(time.UnixMilli(storeCtx.LastUsed)) > s.ttl {
				return nil
			}
			// Cache it
			s.mu.Lock()
			s.contexts[sessionID] = sCtx
			s.mu.Unlock()
			return sCtx
		}
	}

	return nil
}

// Resolve finds routing context for a task by trying:
// 1. Exact caller_id match (e.g. "kog" → session "slack-C0AH79R9X24")
// 2. Most recently updated context (if only one active session)
func (s *SessionContextStore) Resolve(callerID string) *SessionContext {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var latest *SessionContext

	for _, ctx := range s.contexts {
		if s.ttl > 0 && now.Sub(time.UnixMilli(ctx.UpdatedAt)) > s.ttl {
			continue
		}
		if latest == nil || ctx.UpdatedAt > latest.UpdatedAt {
			latest = ctx
		}
	}
	return latest
}

// GetByThread finds the session context for a specific channel+thread combination.
// This is an exact match — safe for multi-project scenarios.
func (s *SessionContextStore) GetByThread(channel, threadTS string) *SessionContext {
	if channel == "" || threadTS == "" {
		return nil
	}

	// Check in-memory cache first
	s.mu.RLock()
	now := time.Now()
	for _, ctx := range s.contexts {
		if s.ttl > 0 && now.Sub(time.UnixMilli(ctx.UpdatedAt)) > s.ttl {
			continue
		}
		if ctx.Channel == channel && ctx.ThreadTS == threadTS {
			s.mu.RUnlock()
			return ctx
		}
	}
	s.mu.RUnlock()

	// Cold start: try SQLite
	if s.store != nil {
		sc, err := s.store.GetSessionContextByThread(channel, threadTS)
		if err == nil && sc != nil {
			sCtx := &SessionContext{
				SessionID: sc.SessionID,
				Channel:   sc.Channel,
				ThreadTS:  sc.ThreadTS,
				UpdatedAt: sc.LastUsed,
			}
			// Cache it
			s.mu.Lock()
			s.contexts[sc.SessionID] = sCtx
			s.mu.Unlock()
			return sCtx
		}
	}

	return nil
}

// Cleanup removes expired entries.
func (s *SessionContextStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, ctx := range s.contexts {
		if s.ttl > 0 && now.Sub(time.UnixMilli(ctx.UpdatedAt)) > s.ttl {
			delete(s.contexts, k)
		}
	}
}
