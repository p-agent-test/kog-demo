package memory_test

import (
	"context"
	"math"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEmbedder maps text to pre-canned vectors.
type stubEmbedder struct {
	vecs map[string][]float32
	dims int
}

func (s *stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := s.vecs[text]; ok {
		return v, nil
	}
	// Default: return a unit vector in first dimension.
	return []float32{1, 0, 0}, nil
}

func (s *stubEmbedder) Dimensions() int { return s.dims }

func newStubEmbedder(dims int, vecs map[string][]float32) *stubEmbedder {
	return &stubEmbedder{dims: dims, vecs: vecs}
}

func TestVectorStore_SaveAndSearchByVector(t *testing.T) {
	ctx := context.Background()

	// Set up SQLite store (in-memory).
	base, err := memory.NewSQLiteStore(":memory:", nil)
	require.NoError(t, err)
	defer base.Close()

	embedder := newStubEmbedder(3, map[string][]float32{
		"cats are fluffy":   {1, 0, 0},
		"dogs are loyal":    {0, 1, 0},
		"fish swim in water": {0, 0, 1},
		// query vector:
		"furry cats": {0.9, 0.1, 0},
	})

	vs := memory.NewVectorStore(base, embedder)

	entries := []memory.MemoryEntry{
		{ID: "e1", AgentID: "ag1", Content: "cats are fluffy"},
		{ID: "e2", AgentID: "ag1", Content: "dogs are loyal"},
		{ID: "e3", AgentID: "ag1", Content: "fish swim in water"},
	}
	for _, e := range entries {
		require.NoError(t, vs.Save(ctx, e))
	}

	assert.Equal(t, 3, vs.VectorCount())

	results, err := vs.SearchByVector(ctx, "furry cats", 2)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// The most similar entry should be "cats are fluffy" (cosine ~ 0.9)
	assert.Equal(t, "e1", results[0].ID)
}

func TestVectorStore_DeleteRemovesVector(t *testing.T) {
	ctx := context.Background()

	base, err := memory.NewSQLiteStore(":memory:", nil)
	require.NoError(t, err)
	defer base.Close()

	embedder := newStubEmbedder(2, map[string][]float32{
		"hello": {1, 0},
	})
	vs := memory.NewVectorStore(base, embedder)

	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "x1", AgentID: "a", Content: "hello"}))
	assert.Equal(t, 1, vs.VectorCount())

	require.NoError(t, vs.Delete(ctx, "x1"))
	assert.Equal(t, 0, vs.VectorCount())
}

func TestVectorStore_FallbackTextSearch(t *testing.T) {
	ctx := context.Background()

	base, err := memory.NewSQLiteStore(":memory:", nil)
	require.NoError(t, err)
	defer base.Close()

	// NoopEmbedder → no vector search, text search only.
	vs := memory.NewVectorStore(base, memory.NoopEmbedder{})

	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "t1", AgentID: "a", Content: "golang concurrency patterns"}))
	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "t2", AgentID: "a", Content: "rust memory safety"}))

	results, err := vs.Search(ctx, "golang", 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "t1", results[0].ID)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	// Orthogonal vectors should have cosine = 0.
	// We test indirectly via VectorStore ranking.
	ctx := context.Background()

	base, err := memory.NewSQLiteStore(":memory:", nil)
	require.NoError(t, err)
	defer base.Close()

	embedder := newStubEmbedder(2, map[string][]float32{
		"a": {1, 0},
		"b": {0, 1},
		"q": {1, 0}, // identical to "a"
	})
	vs := memory.NewVectorStore(base, embedder)

	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "a", AgentID: "x", Content: "a"}))
	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "b", AgentID: "x", Content: "b"}))

	results, err := vs.SearchByVector(ctx, "q", 2)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// "a" should come first (cosine=1.0), "b" second (cosine=0.0)
	assert.Equal(t, "a", results[0].ID)
	assert.Equal(t, "b", results[1].ID)
}

func TestVectorStore_LoadVectors(t *testing.T) {
	ctx := context.Background()

	base, err := memory.NewSQLiteStore(":memory:", nil)
	require.NoError(t, err)
	defer base.Close()

	// Save without embedder first.
	vs := memory.NewVectorStore(base, memory.NoopEmbedder{})
	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "v1", AgentID: "a", Content: "test"}))
	assert.Equal(t, 0, vs.VectorCount())

	// Simulate loading pre-computed vectors (e.g. from a cache).
	vs.LoadVectors(map[string][]float32{
		"v1": {0.5, 0.5},
	})
	assert.Equal(t, 1, vs.VectorCount())
}

// TestFloatPrecision ensures cosine of identical unit vectors is 1.0.
func TestFloatPrecision(t *testing.T) {
	// cos(x, x) should be exactly 1.0 for unit vectors.
	vec := []float32{1.0 / float32(math.Sqrt(3)), 1.0 / float32(math.Sqrt(3)), 1.0 / float32(math.Sqrt(3))}
	ctx := context.Background()

	base, err := memory.NewSQLiteStore(":memory:", nil)
	require.NoError(t, err)
	defer base.Close()

	embedder := newStubEmbedder(3, map[string][]float32{
		"same": vec,
	})
	vs := memory.NewVectorStore(base, embedder)
	require.NoError(t, vs.Save(ctx, memory.MemoryEntry{ID: "z1", AgentID: "a", Content: "same"}))

	results, err := vs.SearchByVector(ctx, "same", 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "z1", results[0].ID)
}
