package tokenstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_SetAndGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	err := store.Set(ctx, "test-key", "test-value", 5*time.Minute)
	require.NoError(t, err)

	tok, err := store.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Equal(t, "test-value", tok.Value)
	assert.False(t, tok.IsExpired())
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_, err := store.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestMemoryStore_GetExpired(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	err := store.Set(ctx, "expired", "val", 1*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)
	_, err = store.Get(ctx, "expired")
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestMemoryStore_Delete(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_ = store.Set(ctx, "del-key", "val", 5*time.Minute)
	err := store.Delete(ctx, "del-key")
	require.NoError(t, err)

	_, err = store.Get(ctx, "del-key")
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestToken_IsExpired(t *testing.T) {
	tok := &Token{ExpiresAt: time.Now().Add(-1 * time.Second)}
	assert.True(t, tok.IsExpired())

	tok2 := &Token{ExpiresAt: time.Now().Add(1 * time.Hour)}
	assert.False(t, tok2.IsExpired())
}

func TestMemoryStore_OverwriteKey(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	_ = store.Set(ctx, "key", "val1", 5*time.Minute)
	_ = store.Set(ctx, "key", "val2", 5*time.Minute)
	tok, err := store.Get(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, "val2", tok.Value)
}

func TestMemoryStore_DeleteNonexistent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	err := store.Delete(ctx, "nope")
	assert.NoError(t, err) // should not error
}

func TestMemoryStore_CleanupEmpty(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	count, err := store.Cleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestMemoryStore_Cleanup(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	_ = store.Set(ctx, "fresh", "val", 5*time.Minute)
	_ = store.Set(ctx, "stale1", "val", 1*time.Millisecond)
	_ = store.Set(ctx, "stale2", "val", 1*time.Millisecond)

	time.Sleep(5 * time.Millisecond)

	count, err := store.Cleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	_, err = store.Get(ctx, "fresh")
	assert.NoError(t, err)
}
