package lru

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- Functional Tests ---

func TestBasicGetPut(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("expected a=1, got %v %v", v, ok)
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("expected b=2, got %v %v", v, ok)
	}
}

func TestEviction(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	// Access "a" to make it MRU — "b" becomes LRU
	c.Get("a")

	// Insert "c" — should evict "b" (LRU)
	evKey, evVal, evicted := c.Put("c", 3)
	if !evicted || evKey != "b" || evVal != 2 {
		t.Fatalf("expected eviction of b=2, got key=%v val=%v evicted=%v", evKey, evVal, evicted)
	}

	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("expected a=1 after eviction, got %v %v", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != 3 {
		t.Fatalf("expected c=3, got %v %v", v, ok)
	}
}

func TestEvictionReturnsValue(t *testing.T) {
	c := New[string, string](1)
	c.Put("a", "hello")

	evKey, evVal, evicted := c.Put("b", "world")
	if !evicted || evKey != "a" || evVal != "hello" {
		t.Fatalf("expected eviction of a=hello, got key=%v val=%v evicted=%v", evKey, evVal, evicted)
	}
}

func TestUpdateExisting(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	// Update "a" — should not evict anything
	_, _, evicted := c.Put("a", 10)
	if evicted {
		t.Fatal("update should not evict")
	}

	if v, _ := c.Get("a"); v != 10 {
		t.Fatalf("expected a=10 after update, got %v", v)
	}
	if c.Len() != 2 {
		t.Fatalf("expected len=2, got %d", c.Len())
	}
}

func TestDelete(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	if !c.Delete("a") {
		t.Fatal("expected delete to return true")
	}
	if c.Delete("a") {
		t.Fatal("expected delete of missing key to return false")
	}
	if c.Len() != 1 {
		t.Fatalf("expected len=1 after delete, got %d", c.Len())
	}
}

func TestPeek(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	// Peek "a" — should NOT change order
	if v, ok := c.Peek("a"); !ok || v != 1 {
		t.Fatalf("expected peek a=1, got %v %v", v, ok)
	}

	// Insert "c" — "a" should be evicted (still LRU since Peek doesn't promote)
	c.Put("c", 3)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' evicted after peek (no promotion)")
	}
}

func TestKeys(t *testing.T) {
	c := New[string, int](3)

	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)
	c.Get("a") // promote "a" to MRU

	keys := c.Keys()
	// Expected order: a (MRU), c, b (LRU)
	expected := []string{"a", "c", "b"}
	if len(keys) != len(expected) {
		t.Fatalf("expected %d keys, got %d", len(expected), len(keys))
	}
	for i, k := range expected {
		if keys[i] != k {
			t.Fatalf("keys[%d] expected %s, got %s", i, k, keys[i])
		}
	}
}

func TestClear(t *testing.T) {
	c := New[string, int](3)

	c.Put("a", 1)
	c.Put("b", 2)
	c.Clear()

	if c.Len() != 0 {
		t.Fatalf("expected len=0 after clear, got %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected empty cache after clear")
	}
}

func TestCapacityOne(t *testing.T) {
	c := New[string, int](1)

	c.Put("a", 1)
	c.Put("b", 2) // evicts "a"

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' evicted with capacity=1")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("expected b=2, got %v %v", v, ok)
	}
}

func TestPanicOnZeroCapacity(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on zero capacity")
		}
	}()
	New[string, int](0)
}

// --- TTL Tests ---

func TestTTLExpiration(t *testing.T) {
	now := time.Now()
	c := New[string, int](10, WithTTL[string, int](100*time.Millisecond))
	c.now = func() time.Time { return now }

	c.Put("a", 1)

	// Not expired yet
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("expected a=1 before expiry, got %v %v", v, ok)
	}

	// Advance time past TTL
	c.now = func() time.Time { return now.Add(200 * time.Millisecond) }

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be expired")
	}
}

func TestTTLPerEntry(t *testing.T) {
	now := time.Now()
	c := New[string, int](10)
	c.now = func() time.Time { return now }

	c.PutWithTTL("short", 1, 50*time.Millisecond)
	c.PutWithTTL("long", 2, 500*time.Millisecond)
	c.Put("forever", 3) // no TTL

	// Advance 100ms — "short" expired, "long" and "forever" alive
	c.now = func() time.Time { return now.Add(100 * time.Millisecond) }

	if _, ok := c.Get("short"); ok {
		t.Fatal("expected 'short' expired")
	}
	if v, ok := c.Get("long"); !ok || v != 2 {
		t.Fatalf("expected long=2, got %v %v", v, ok)
	}
	if v, ok := c.Get("forever"); !ok || v != 3 {
		t.Fatalf("expected forever=3, got %v %v", v, ok)
	}
}

func TestTTLUpdateResetsTTL(t *testing.T) {
	now := time.Now()
	c := New[string, int](10, WithTTL[string, int](100*time.Millisecond))
	c.now = func() time.Time { return now }

	c.Put("a", 1)

	// Advance 80ms and update
	c.now = func() time.Time { return now.Add(80 * time.Millisecond) }
	c.Put("a", 2)

	// Advance to 150ms total — original would have expired, but update reset TTL
	c.now = func() time.Time { return now.Add(150 * time.Millisecond) }
	if v, ok := c.Get("a"); !ok || v != 2 {
		t.Fatalf("expected a=2 after TTL reset, got %v %v", v, ok)
	}
}

func TestPeekRespectsExpiration(t *testing.T) {
	now := time.Now()
	c := New[string, int](10, WithTTL[string, int](100*time.Millisecond))
	c.now = func() time.Time { return now }

	c.Put("a", 1)
	c.now = func() time.Time { return now.Add(200 * time.Millisecond) }

	if _, ok := c.Peek("a"); ok {
		t.Fatal("expected Peek to return false for expired entry")
	}
}

func TestKeysExcludesExpired(t *testing.T) {
	now := time.Now()
	c := New[string, int](10)
	c.now = func() time.Time { return now }

	c.PutWithTTL("expired", 1, 50*time.Millisecond)
	c.Put("alive", 2)

	c.now = func() time.Time { return now.Add(100 * time.Millisecond) }

	keys := c.Keys()
	if len(keys) != 1 || keys[0] != "alive" {
		t.Fatalf("expected only 'alive', got %v", keys)
	}
}

// --- OnEvict Callback Tests ---

func TestOnEvictCallback(t *testing.T) {
	var evictedKeys []string
	var evictedVals []int

	c := New[string, int](2, WithOnEvict[string, int](func(k string, v int) {
		evictedKeys = append(evictedKeys, k)
		evictedVals = append(evictedVals, v)
	}))

	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3) // evicts "a"

	if len(evictedKeys) != 1 || evictedKeys[0] != "a" || evictedVals[0] != 1 {
		t.Fatalf("expected eviction callback for a=1, got keys=%v vals=%v", evictedKeys, evictedVals)
	}
}

func TestOnEvictCalledOnTTLExpiry(t *testing.T) {
	now := time.Now()
	var evictedKey string

	c := New[string, int](10,
		WithTTL[string, int](100*time.Millisecond),
		WithOnEvict[string, int](func(k string, v int) {
			evictedKey = k
		}),
	)
	c.now = func() time.Time { return now }

	c.Put("a", 1)
	c.now = func() time.Time { return now.Add(200 * time.Millisecond) }

	c.Get("a") // triggers lazy expiration

	if evictedKey != "a" {
		t.Fatalf("expected OnEvict for 'a' on TTL expiry, got '%s'", evictedKey)
	}
}

// --- Metrics Tests ---

func TestMetrics(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	c.Get("a")        // hit
	c.Get("b")        // hit
	c.Get("missing")  // miss
	c.Put("c", 3)     // evicts "a" (was promoted, so actually evicts oldest LRU)

	m := c.Metrics()
	if m.Hits != 2 {
		t.Fatalf("expected 2 hits, got %d", m.Hits)
	}
	if m.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", m.Misses)
	}
	if m.Evictions != 1 {
		t.Fatalf("expected 1 eviction, got %d", m.Evictions)
	}
}

func TestMetricsHitRate(t *testing.T) {
	c := New[string, int](10)

	c.Put("a", 1)
	c.Get("a")        // hit
	c.Get("a")        // hit
	c.Get("a")        // hit
	c.Get("missing")  // miss

	m := c.Metrics()
	rate := m.HitRate()
	if rate < 0.74 || rate > 0.76 {
		t.Fatalf("expected ~0.75 hit rate, got %f", rate)
	}
}

func TestMetricsTTLExpiration(t *testing.T) {
	now := time.Now()
	c := New[string, int](10, WithTTL[string, int](100*time.Millisecond))
	c.now = func() time.Time { return now }

	c.Put("a", 1)
	c.now = func() time.Time { return now.Add(200 * time.Millisecond) }

	c.Get("a") // miss due to expiration

	m := c.Metrics()
	if m.Expirations != 1 {
		t.Fatalf("expected 1 expiration, got %d", m.Expirations)
	}
	if m.Misses != 1 {
		t.Fatalf("expected 1 miss on expired get, got %d", m.Misses)
	}
}

// --- Concurrency Tests ---

func TestConcurrentAccess(t *testing.T) {
	c := New[int, int](100)
	var wg sync.WaitGroup

	// 10 goroutines writing
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.Put(offset*1000+i, i)
			}
		}(g)
	}

	// 10 goroutines reading
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.Get(offset*1000 + i)
			}
		}(g)
	}

	wg.Wait()

	if c.Len() > 100 {
		t.Fatalf("cache exceeded capacity: %d", c.Len())
	}
}

func TestConcurrentWithTTL(t *testing.T) {
	c := New[int, int](100, WithTTL[int, int](50*time.Millisecond))
	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				c.Put(offset*500+i, i)
				c.Get(offset*500 + i)
			}
		}(g)
	}

	wg.Wait()

	if c.Len() > 100 {
		t.Fatalf("cache exceeded capacity: %d", c.Len())
	}
}

// --- Benchmarks ---

func BenchmarkPut(b *testing.B) {
	c := New[int, int](1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Put(i, i)
	}
}

func BenchmarkPutWithTTL(b *testing.B) {
	c := New[int, int](1000, WithTTL[int, int](5*time.Minute))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Put(i, i)
	}
}

func BenchmarkGet_Hit(b *testing.B) {
	c := New[int, int](1000)
	for i := 0; i < 1000; i++ {
		c.Put(i, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get(i % 1000)
	}
}

func BenchmarkGet_Miss(b *testing.B) {
	c := New[int, int](1000)
	for i := 0; i < 1000; i++ {
		c.Put(i, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get(i + 1000)
	}
}

func BenchmarkMixed(b *testing.B) {
	c := New[int, int](1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%3 == 0 {
			c.Put(i, i)
		} else {
			c.Get(i)
		}
	}
}

func BenchmarkConcurrent(b *testing.B) {
	c := New[int, int](1000)
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				c.Put(i, i)
			} else {
				c.Get(i)
			}
			i++
		}
	})
}

func ExampleCache() {
	cache := New[string, int](2)

	cache.Put("a", 1)
	cache.Put("b", 2)

	v, _ := cache.Get("a") // promotes "a"
	fmt.Println(v)

	cache.Put("c", 3) // evicts "b" (LRU)

	_, ok := cache.Get("b")
	fmt.Println(ok)

	// Output:
	// 1
	// false
}

func ExampleCache_withTTL() {
	cache := New[string, int](100, WithTTL[string, int](5*time.Minute))

	cache.Put("session:abc", 42)
	cache.PutWithTTL("temp", 1, 30*time.Second) // override default TTL

	m := cache.Metrics()
	fmt.Printf("hit rate: %.0f%%\n", m.HitRate()*100)

	// Output:
	// hit rate: 0%
}
