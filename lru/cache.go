// Package lru implements a generic, thread-safe LRU cache with TTL support.
//
// Time complexity: O(1) for Get, Put, Delete, Len.
// Space complexity: O(n) where n is capacity.
//
// Implementation uses a hash map for O(1) key lookup combined with
// a doubly linked list for O(1) eviction ordering.
package lru

import (
	"sync"
	"sync/atomic"
	"time"
)

// node is a doubly linked list node holding a key-value pair with expiration.
type node[K comparable, V any] struct {
	key       K
	val       V
	prev      *node[K, V]
	next      *node[K, V]
	expiresAt time.Time // zero means no expiration
}

// isExpired checks if the node has expired.
func (n *node[K, V]) isExpired(now time.Time) bool {
	return !n.expiresAt.IsZero() && now.After(n.expiresAt)
}

// OnEvictFunc is called when an entry is evicted from the cache.
// Receives the evicted key and value. Called with the lock released.
type OnEvictFunc[K comparable, V any] func(key K, val V)

// Metrics holds cache performance counters (atomic, lock-free reads).
type Metrics struct {
	Hits       atomic.Int64
	Misses     atomic.Int64
	Evictions  atomic.Int64
	Expirations atomic.Int64
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Hits:        m.Hits.Load(),
		Misses:      m.Misses.Load(),
		Evictions:   m.Evictions.Load(),
		Expirations: m.Expirations.Load(),
	}
}

// MetricsSnapshot is an immutable copy of cache metrics.
type MetricsSnapshot struct {
	Hits        int64
	Misses      int64
	Evictions   int64
	Expirations int64
}

// HitRate returns the cache hit ratio (0.0 to 1.0). Returns 0 if no lookups.
func (s MetricsSnapshot) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// Option configures the cache.
type Option[K comparable, V any] func(*Cache[K, V])

// WithTTL sets a default TTL for all entries.
func WithTTL[K comparable, V any](ttl time.Duration) Option[K, V] {
	return func(c *Cache[K, V]) {
		c.defaultTTL = ttl
	}
}

// WithOnEvict sets a callback invoked when entries are evicted.
func WithOnEvict[K comparable, V any](fn OnEvictFunc[K, V]) Option[K, V] {
	return func(c *Cache[K, V]) {
		c.onEvict = fn
	}
}

// Cache is a generic, thread-safe LRU cache with optional TTL and metrics.
// K must be comparable (map key constraint), V can be any type.
type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	capacity   int
	defaultTTL time.Duration
	items      map[K]*node[K, V]
	head       *node[K, V] // most recently used (sentinel)
	tail       *node[K, V] // least recently used (sentinel)
	onEvict    OnEvictFunc[K, V]
	metrics    Metrics
	now        func() time.Time // injectable for testing
}

// New creates an LRU cache with the given capacity and options.
// Panics if capacity < 1.
func New[K comparable, V any](capacity int, opts ...Option[K, V]) *Cache[K, V] {
	if capacity < 1 {
		panic("lru: capacity must be >= 1")
	}

	head := &node[K, V]{}
	tail := &node[K, V]{}
	head.next = tail
	tail.prev = head

	c := &Cache[K, V]{
		capacity: capacity,
		items:    make(map[K]*node[K, V], capacity),
		head:     head,
		tail:     tail,
		now:      time.Now,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Get retrieves a value by key. Returns the value and true if found and not expired,
// or the zero value and false otherwise. O(1).
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		c.metrics.Misses.Add(1)
		var zero V
		return zero, false
	}

	// Check TTL expiration
	if n.isExpired(c.now()) {
		c.removeLocked(n)
		delete(c.items, key)
		c.mu.Unlock()
		c.metrics.Misses.Add(1)
		c.metrics.Expirations.Add(1)
		if c.onEvict != nil {
			c.onEvict(n.key, n.val)
		}
		var zero V
		return zero, false
	}

	c.moveToFront(n)
	val := n.val
	c.mu.Unlock()
	c.metrics.Hits.Add(1)
	return val, true
}

// Put inserts or updates a key-value pair using the default TTL.
// If the cache is at capacity, the least recently used entry is evicted. O(1).
// Returns the evicted key, value, and true if an eviction occurred.
func (c *Cache[K, V]) Put(key K, val V) (K, V, bool) {
	return c.PutWithTTL(key, val, c.defaultTTL)
}

// PutWithTTL inserts or updates a key-value pair with a specific TTL.
// A zero TTL means no expiration. O(1).
func (c *Cache[K, V]) PutWithTTL(key K, val V, ttl time.Duration) (K, V, bool) {
	now := c.now()
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
	}

	c.mu.Lock()

	// Update existing
	if n, ok := c.items[key]; ok {
		n.val = val
		n.expiresAt = expiresAt
		c.moveToFront(n)
		c.mu.Unlock()
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}

	// Evict if at capacity
	var evictedKey K
	var evictedVal V
	evicted := false
	if len(c.items) >= c.capacity {
		victim := c.tail.prev
		evictedKey = victim.key
		evictedVal = victim.val
		c.removeLocked(victim)
		delete(c.items, victim.key)
		evicted = true
	}

	// Insert new
	n := &node[K, V]{key: key, val: val, expiresAt: expiresAt}
	c.items[key] = n
	c.pushFront(n)
	c.mu.Unlock()

	if evicted {
		c.metrics.Evictions.Add(1)
		if c.onEvict != nil {
			c.onEvict(evictedKey, evictedVal)
		}
	}

	return evictedKey, evictedVal, evicted
}

// Delete removes a key from the cache. Returns true if the key existed. O(1).
func (c *Cache[K, V]) Delete(key K) bool {
	c.mu.Lock()

	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	c.removeLocked(n)
	delete(c.items, key)
	c.mu.Unlock()
	return true
}

// Len returns the current number of entries in the cache (including expired). O(1).
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Peek retrieves a value without updating access order. Returns false for expired entries. O(1).
func (c *Cache[K, V]) Peek(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, ok := c.items[key]
	if !ok || n.isExpired(c.now()) {
		var zero V
		return zero, false
	}
	return n.val, true
}

// Keys returns all non-expired keys in order from most to least recently used. O(n).
func (c *Cache[K, V]) Keys() []K {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	keys := make([]K, 0, len(c.items))
	for cur := c.head.next; cur != c.tail; cur = cur.next {
		if !cur.isExpired(now) {
			keys = append(keys, cur.key)
		}
	}
	return keys
}

// Clear removes all entries from the cache. O(1).
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	c.head.next = c.tail
	c.tail.prev = c.head
	c.items = make(map[K]*node[K, V], c.capacity)
	c.mu.Unlock()
}

// Metrics returns the cache metrics (lock-free).
func (c *Cache[K, V]) Metrics() MetricsSnapshot {
	return c.metrics.Snapshot()
}

// --- internal linked list operations (caller must hold lock) ---

// removeLocked detaches a node from the list.
func (c *Cache[K, V]) removeLocked(n *node[K, V]) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

// pushFront inserts a node right after head sentinel.
func (c *Cache[K, V]) pushFront(n *node[K, V]) {
	n.next = c.head.next
	n.prev = c.head
	c.head.next.prev = n
	c.head.next = n
}

// moveToFront detaches and reinserts a node at front.
func (c *Cache[K, V]) moveToFront(n *node[K, V]) {
	c.removeLocked(n)
	c.pushFront(n)
}
