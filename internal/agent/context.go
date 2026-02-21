package agent

import (
	"sync"
	"time"
)

// ConversationMessage represents a single message in a thread.
type ConversationMessage struct {
	UserID    string
	Text      string
	Timestamp time.Time
	Metadata  map[string]string // e.g. "pr_url" → "...", "task_key" → "..."
}

// ConversationContext tracks messages within a Slack thread.
type ConversationContext struct {
	ThreadID  string
	ChannelID string
	Messages  []ConversationMessage
	CreatedAt time.Time
	LastSeen  time.Time
}

// ContextStore is an in-memory, TTL-based conversation context store.
type ContextStore struct {
	mu       sync.RWMutex
	threads  map[string]*ConversationContext // key: channelID:threadTS
	maxMsgs  int
	ttl      time.Duration
}

// NewContextStore creates a new context store.
func NewContextStore(maxMessages int, ttl time.Duration) *ContextStore {
	cs := &ContextStore{
		threads: make(map[string]*ConversationContext),
		maxMsgs: maxMessages,
		ttl:     ttl,
	}
	return cs
}

func contextKey(channelID, threadTS string) string {
	return channelID + ":" + threadTS
}

// Add records a message in the conversation context for a thread.
func (cs *ContextStore) Add(channelID, threadTS, userID, text string, metadata map[string]string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	key := contextKey(channelID, threadTS)
	ctx, ok := cs.threads[key]
	if !ok {
		ctx = &ConversationContext{
			ThreadID:  threadTS,
			ChannelID: channelID,
			CreatedAt: time.Now(),
		}
		cs.threads[key] = ctx
	}

	ctx.LastSeen = time.Now()
	ctx.Messages = append(ctx.Messages, ConversationMessage{
		UserID:    userID,
		Text:      text,
		Timestamp: time.Now(),
		Metadata:  metadata,
	})

	// Trim to max
	if len(ctx.Messages) > cs.maxMsgs {
		ctx.Messages = ctx.Messages[len(ctx.Messages)-cs.maxMsgs:]
	}
}

// Get returns the conversation context for a thread.
func (cs *ContextStore) Get(channelID, threadTS string) *ConversationContext {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	ctx, ok := cs.threads[contextKey(channelID, threadTS)]
	if !ok {
		return nil
	}
	if time.Since(ctx.LastSeen) > cs.ttl {
		return nil
	}
	return ctx
}

// FindMetadata searches the thread context for a metadata key (most recent first).
func (cs *ContextStore) FindMetadata(channelID, threadTS, key string) string {
	ctx := cs.Get(channelID, threadTS)
	if ctx == nil {
		return ""
	}
	for i := len(ctx.Messages) - 1; i >= 0; i-- {
		if v, ok := ctx.Messages[i].Metadata[key]; ok && v != "" {
			return v
		}
	}
	return ""
}

// Cleanup removes expired entries.
func (cs *ContextStore) Cleanup() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	removed := 0
	for key, ctx := range cs.threads {
		if time.Since(ctx.LastSeen) > cs.ttl {
			delete(cs.threads, key)
			removed++
		}
	}
	return removed
}

// Size returns the number of tracked threads.
func (cs *ContextStore) Size() int {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return len(cs.threads)
}
