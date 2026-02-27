package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTool is a simple mock Tool for testing.
type fakeTool struct {
	name   string
	result string
}

func (f *fakeTool) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name:        f.name,
		Description: "fake",
		InputSchema: MustSchema(map[string]interface{}{"type": "object"}),
	}
}

func (f *fakeTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return f.result, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "tool_a", result: "ok"})

	got, ok := r.Get("tool_a")
	require.True(t, ok)
	assert.Equal(t, "tool_a", got.Schema().Name)
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("missing")
	assert.False(t, ok)
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "dup"})
	assert.Panics(t, func() {
		r.Register(&fakeTool{name: "dup"})
	})
}

func TestRegistry_Schemas(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "t1"})
	r.Register(&fakeTool{name: "t2"})

	schemas := r.Schemas()
	assert.Len(t, schemas, 2)
}

func TestRegistry_Execute(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "greet", result: "hello world"})

	out, err := r.Execute(context.Background(), "greet", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "hello world", out)
}

func TestRegistry_ExecuteUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "ghost", json.RawMessage(`{}`))
	assert.Error(t, err)
}
