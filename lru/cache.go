// Package lru implements a generic, thread-safe LRU cache.
//
// Time complexity: O(1) for Get, Put, Delete, Len.
// Space complexity: O(n) where n is capacity.
//
// Implementation uses a hash map for O(1) key lookup combined with
// a doubly linked list for O(1) eviction ordering.
package lru

import "sync"

// node is a doubly linked list node holding a key-value pair.
type node[K comparable, V any] struct {
	key  K
	val  V
	prev *node[K, V]
	next *node[K, V]
}

// Cache is a generic, thread-safe LRU cache.
// K must be comparable (map key constraint), V can be any type.
type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	items    map[K]*node[K, V]
	head     *node[K, V] // most recently used (sentinel)
	tail     *node[K, V] // least recently used (sentinel)
}

// New creates an LRU cache with the given capacity.
// Panics if capacity < 1.
func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity < 1 {
		panic("lru: capacity must be >= 1")
	}

	head := &node[K, V]{}
	tail := &node[K, V]{}
	head.next = tail
	tail.prev = head

	return &Cache[K, V]{
		capacity: capacity,
		items:    make(map[K]*node[K, V], capacity),
		head:     head,
		tail:     tail,
	}
}

// Get retrieves a value by key. Returns the value and true if found,
// or the zero value and false if not found. O(1).
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}

	c.moveToFront(n)
	return n.val, true
}

// Put inserts or updates a key-value pair. If the cache is at capacity,
// the least recently used entry is evicted. O(1).
// Returns the evicted key and true if an eviction occurred.
func (c *Cache[K, V]) Put(key K, val V) (K, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing
	if n, ok := c.items[key]; ok {
		n.val = val
		c.moveToFront(n)
		var zero K
		return zero, false
	}

	// Evict if at capacity
	var evictedKey K
	evicted := false
	if len(c.items) >= c.capacity {
		victim := c.tail.prev
		c.remove(victim)
		delete(c.items, victim.key)
		evictedKey = victim.key
		evicted = true
	}

	// Insert new
	n := &node[K, V]{key: key, val: val}
	c.items[key] = n
	c.pushFront(n)

	return evictedKey, evicted
}

// Delete removes a key from the cache. Returns true if the key existed. O(1).
func (c *Cache[K, V]) Delete(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, ok := c.items[key]
	if !ok {
		return false
	}

	c.remove(n)
	delete(c.items, key)
	return true
}

// Len returns the current number of entries in the cache. O(1).
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Peek retrieves a value without updating access order. O(1).
func (c *Cache[K, V]) Peek(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	return n.val, true
}

// Keys returns all keys in order from most to least recently used. O(n).
func (c *Cache[K, V]) Keys() []K {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := make([]K, 0, len(c.items))
	for cur := c.head.next; cur != c.tail; cur = cur.next {
		keys = append(keys, cur.key)
	}
	return keys
}

// Clear removes all entries from the cache. O(n).
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.head.next = c.tail
	c.tail.prev = c.head
	c.items = make(map[K]*node[K, V], c.capacity)
}

// --- internal linked list operations (caller must hold lock) ---

// remove detaches a node from the list.
func (c *Cache[K, V]) remove(n *node[K, V]) {
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
	c.remove(n)
	c.pushFront(n)
}
