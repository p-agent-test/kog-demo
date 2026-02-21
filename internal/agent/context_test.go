package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextStore_AddAndGet(t *testing.T) {
	cs := NewContextStore(10, time.Hour)

	cs.Add("C1", "ts1", "U1", "review https://github.com/org/repo/pull/42", map[string]string{"pr_url": "https://github.com/org/repo/pull/42"})
	cs.Add("C1", "ts1", "bot", "Reviewing PR...", nil)

	ctx := cs.Get("C1", "ts1")
	require.NotNil(t, ctx)
	assert.Equal(t, 2, len(ctx.Messages))
	assert.Equal(t, "C1", ctx.ChannelID)
}

func TestContextStore_MaxMessages(t *testing.T) {
	cs := NewContextStore(3, time.Hour)

	for i := 0; i < 5; i++ {
		cs.Add("C1", "ts1", "U1", "msg", nil)
	}

	ctx := cs.Get("C1", "ts1")
	require.NotNil(t, ctx)
	assert.Equal(t, 3, len(ctx.Messages))
}

func TestContextStore_TTLExpiry(t *testing.T) {
	cs := NewContextStore(10, 50*time.Millisecond)

	cs.Add("C1", "ts1", "U1", "hello", nil)
	assert.NotNil(t, cs.Get("C1", "ts1"))

	time.Sleep(60 * time.Millisecond)
	assert.Nil(t, cs.Get("C1", "ts1"))
}

func TestContextStore_FindMetadata(t *testing.T) {
	cs := NewContextStore(10, time.Hour)

	cs.Add("C1", "ts1", "U1", "review PR", map[string]string{"pr_url": "https://github.com/org/repo/pull/42"})
	cs.Add("C1", "ts1", "bot", "reviewing...", nil)
	cs.Add("C1", "ts1", "U1", "approve that PR", nil)

	pr := cs.FindMetadata("C1", "ts1", "pr_url")
	assert.Equal(t, "https://github.com/org/repo/pull/42", pr)
}

func TestContextStore_FindMetadata_NotFound(t *testing.T) {
	cs := NewContextStore(10, time.Hour)
	cs.Add("C1", "ts1", "U1", "hello", nil)
	assert.Equal(t, "", cs.FindMetadata("C1", "ts1", "pr_url"))
}

func TestContextStore_Cleanup(t *testing.T) {
	cs := NewContextStore(10, 50*time.Millisecond)

	cs.Add("C1", "ts1", "U1", "old", nil)
	time.Sleep(60 * time.Millisecond)
	cs.Add("C1", "ts2", "U1", "new", nil)

	removed := cs.Cleanup()
	assert.Equal(t, 1, removed)
	assert.Equal(t, 1, cs.Size())
}

func TestContextStore_DifferentThreads(t *testing.T) {
	cs := NewContextStore(10, time.Hour)

	cs.Add("C1", "ts1", "U1", "thread1", nil)
	cs.Add("C1", "ts2", "U1", "thread2", nil)

	assert.Equal(t, 2, cs.Size())
	assert.NotNil(t, cs.Get("C1", "ts1"))
	assert.NotNil(t, cs.Get("C1", "ts2"))
	assert.Nil(t, cs.Get("C1", "ts3"))
}

func TestContextStore_GetNonExistent(t *testing.T) {
	cs := NewContextStore(10, time.Hour)
	assert.Nil(t, cs.Get("C1", "ts1"))
}
