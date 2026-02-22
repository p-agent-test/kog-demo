package mgmt

import (
	"sync"
	"time"
)

// SessionContext holds Slack routing info for a session.
type SessionContext struct {
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
	ThreadTS  string `json:"thread_ts"`
	UpdatedAt int64  `json:"updated_at"`
}

// SessionContextStore maps session IDs to their Slack routing context.
// Thread-safe. Entries expire after TTL.
type SessionContextStore struct {
	mu       sync.RWMutex
	contexts map[string]*SessionContext
	ttl      time.Duration
}

// NewSessionContextStore creates a new store with the given TTL.
func NewSessionContextStore(ttl time.Duration) *SessionContextStore {
	return &SessionContextStore{
		contexts: make(map[string]*SessionContext),
		ttl:      ttl,
	}
}

// Set registers or updates the routing context for a session.
func (s *SessionContextStore) Set(ctx SessionContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx.UpdatedAt = time.Now().UnixMilli()
	s.contexts[ctx.SessionID] = &ctx
}

// Get returns the routing context for a session (nil if not found or expired).
func (s *SessionContextStore) Get(sessionID string) *SessionContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx, ok := s.contexts[sessionID]
	if !ok {
		return nil
	}
	if s.ttl > 0 && time.Since(time.UnixMilli(ctx.UpdatedAt)) > s.ttl {
		return nil
	}
	return ctx
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
