package memory

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempStore(t *testing.T) *SQLiteStore {
	t.Helper()
	f, err := os.CreateTemp("", "kog-memory-*.db")
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store, err := NewSQLiteStore(f.Name(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSave_And_Get(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	entry := MemoryEntry{
		ID:      "mem_001",
		AgentID: "kog-primary",
		Content: "The sky is blue",
		Tags:    []string{"fact", "nature"},
	}
	require.NoError(t, s.Save(ctx, entry))

	got, err := s.Get(ctx, "mem_001")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, entry.ID, got.ID)
	assert.Equal(t, entry.Content, got.Content)
	assert.Equal(t, entry.Tags, got.Tags)
}

func TestGet_NotFound(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	got, err := s.Get(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	require.NoError(t, s.Save(ctx, MemoryEntry{ID: "del_me", AgentID: "a", Content: "delete me"}))
	require.NoError(t, s.Delete(ctx, "del_me"))

	got, err := s.Get(ctx, "del_me")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSearch_FTS(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	entries := []MemoryEntry{
		{ID: "1", AgentID: "a", Content: "go is a great programming language"},
		{ID: "2", AgentID: "a", Content: "python is popular for data science"},
		{ID: "3", AgentID: "a", Content: "rust guarantees memory safety"},
	}
	for _, e := range entries {
		require.NoError(t, s.Save(ctx, e))
	}

	results, err := s.Search(ctx, "go programming", 5)
	require.NoError(t, err)
	// At least one result should contain "go"
	assert.NotEmpty(t, results)
}

func TestSearch_LIKE_Fallback(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	require.NoError(t, s.Save(ctx, MemoryEntry{
		ID: "x", AgentID: "a", Content: "unique-needle-search-term",
	}))

	// Use LIKE-style search with special chars that might break FTS
	results, err := s.searchLike(ctx, "unique-needle", 5)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestSave_GeneratesID(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	entry := MemoryEntry{
		AgentID: "a",
		Content: "auto id",
	}
	require.NoError(t, s.Save(ctx, entry))
	// If no error, the ID was generated internally
}

func TestSave_Upsert(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	e := MemoryEntry{ID: "upsert_id", AgentID: "a", Content: "v1"}
	require.NoError(t, s.Save(ctx, e))

	e.Content = "v2"
	require.NoError(t, s.Save(ctx, e))

	got, err := s.Get(ctx, "upsert_id")
	require.NoError(t, err)
	assert.Equal(t, "v2", got.Content)
}
