package lru

import (
	"fmt"
	"sync"
	"testing"
)

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
	evKey, evicted := c.Put("c", 3)
	if !evicted || evKey != "b" {
		t.Fatalf("expected eviction of 'b', got key=%v evicted=%v", evKey, evicted)
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

func TestUpdateExisting(t *testing.T) {
	c := New[string, int](2)

	c.Put("a", 1)
	c.Put("b", 2)

	// Update "a" — should not evict anything
	_, evicted := c.Put("a", 10)
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

// --- Benchmarks ---

func BenchmarkPut(b *testing.B) {
	c := New[int, int](1000)
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
