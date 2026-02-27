// Package memory — In-memory vector store with cosine similarity search.
// Wraps MemoryStore and adds semantic search via embedding vectors.
package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
)

// VectorStore extends MemoryStore with embedding-based semantic search.
// Embeddings are kept in memory (keyed by entry ID); the underlying store
// handles persistence of text and metadata.
type VectorStore struct {
	base     MemoryStore
	embedder Embedder

	mu      sync.RWMutex
	vectors map[string][]float32 // entryID → embedding
}

// NewVectorStore creates a VectorStore backed by base and using embedder.
// If embedder is nil or NoopEmbedder, SearchByVector returns an error.
func NewVectorStore(base MemoryStore, embedder Embedder) *VectorStore {
	return &VectorStore{
		base:     base,
		embedder: embedder,
		vectors:  make(map[string][]float32),
	}
}

// Save persists the entry and (if embedder is available) computes + caches its vector.
func (v *VectorStore) Save(ctx context.Context, entry MemoryEntry) error {
	if err := v.base.Save(ctx, entry); err != nil {
		return err
	}

	if v.canEmbed() {
		vec, err := v.embedder.Embed(ctx, entry.Content)
		if err == nil && len(vec) > 0 {
			v.mu.Lock()
			v.vectors[entry.ID] = vec
			v.mu.Unlock()
		}
		// Non-fatal: text search still works if embedding fails.
	}
	return nil
}

func (v *VectorStore) canEmbed() bool {
	if v.embedder == nil {
		return false
	}
	switch v.embedder.(type) {
	case NoopEmbedder, *NoopEmbedder:
		return false
	}
	return true
}

// Search delegates to the underlying text-based search.
func (v *VectorStore) Search(ctx context.Context, query string, topK int) ([]MemoryEntry, error) {
	return v.base.Search(ctx, query, topK)
}

// SearchByVector embeds the query text and returns the topK most similar entries
// using cosine similarity over cached in-memory vectors.
func (v *VectorStore) SearchByVector(ctx context.Context, query string, topK int) ([]MemoryEntry, error) {
	if !v.canEmbed() {
		return nil, fmt.Errorf("vectorstore: no embedder configured")
	}

	queryVec, err := v.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vectorstore: embed query: %w", err)
	}

	type scored struct {
		id    string
		score float64
	}

	v.mu.RLock()
	scores := make([]scored, 0, len(v.vectors))
	for id, vec := range v.vectors {
		sim := cosineSimilarity(queryVec, vec)
		scores = append(scores, scored{id: id, score: sim})
	}
	v.mu.RUnlock()

	// Sort descending by similarity.
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	if topK <= 0 {
		topK = 10
	}
	if topK > len(scores) {
		topK = len(scores)
	}

	results := make([]MemoryEntry, 0, topK)
	for _, s := range scores[:topK] {
		entry, err := v.base.Get(ctx, s.id)
		if err != nil || entry == nil {
			continue
		}
		results = append(results, *entry)
	}
	return results, nil
}

// Get delegates to the base store.
func (v *VectorStore) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	return v.base.Get(ctx, id)
}

// Delete removes from both base store and vector cache.
func (v *VectorStore) Delete(ctx context.Context, id string) error {
	v.mu.Lock()
	delete(v.vectors, id)
	v.mu.Unlock()
	return v.base.Delete(ctx, id)
}

// Close closes the underlying store.
func (v *VectorStore) Close() error { return v.base.Close() }

// cosineSimilarity returns the cosine similarity ∈ [-1, 1] between two vectors.
// Returns 0 if either vector has zero magnitude.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// LoadVectors re-populates the in-memory vector cache from a list of
// (id → vec) pairs. Useful after process restart if vectors are cached externally.
func (v *VectorStore) LoadVectors(vectors map[string][]float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, vec := range vectors {
		v.vectors[id] = vec
	}
}

// VectorCount returns the number of cached embedding vectors.
func (v *VectorStore) VectorCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.vectors)
}
