package kogagent_test

import (
	"context"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/p-blackswan/platform-agent/internal/kogagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAgent is a simple test double that records Handle calls.
type mockAgent struct {
	id       string
	handled  []event.Event
	status   kogagent.Status
	failNext bool
}

func (m *mockAgent) ID() string         { return m.id }
func (m *mockAgent) Status() kogagent.Status { return m.status }
func (m *mockAgent) Handle(_ context.Context, ev event.Event) error {
	m.handled = append(m.handled, ev)
	return nil
}

func newMockAgent(id string) *mockAgent {
	return &mockAgent{id: id, status: kogagent.StatusIdle}
}

func TestCoordinator_RegisterAndGet(t *testing.T) {
	c := kogagent.NewCoordinator(nil)

	a := newMockAgent("agent-1")
	require.NoError(t, c.Register(a))

	got, ok := c.Get("agent-1")
	require.True(t, ok)
	assert.Equal(t, "agent-1", got.ID())

	_, ok = c.Get("non-existent")
	assert.False(t, ok)
}

func TestCoordinator_DuplicateRegisterErrors(t *testing.T) {
	c := kogagent.NewCoordinator(nil)

	a := newMockAgent("dup")
	require.NoError(t, c.Register(a))
	err := c.Register(newMockAgent("dup"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestCoordinator_Kill(t *testing.T) {
	c := kogagent.NewCoordinator(nil)
	require.NoError(t, c.Register(newMockAgent("x")))

	require.NoError(t, c.Kill("x"))
	assert.Equal(t, 0, c.Count())

	err := c.Kill("x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCoordinator_List(t *testing.T) {
	c := kogagent.NewCoordinator(nil)
	for _, id := range []string{"a1", "a2", "a3"} {
		require.NoError(t, c.Register(newMockAgent(id)))
	}
	assert.Equal(t, 3, c.Count())
	list := c.List()
	assert.Len(t, list, 3)
}

func TestCoordinator_Broadcast(t *testing.T) {
	c := kogagent.NewCoordinator(nil)
	agents := []*mockAgent{newMockAgent("b1"), newMockAgent("b2"), newMockAgent("b3")}
	for _, a := range agents {
		require.NoError(t, c.Register(a))
	}

	ev := event.Event{ID: "ev-test", Source: "internal", Type: "message"}
	require.NoError(t, c.Broadcast(context.Background(), ev))

	for _, a := range agents {
		require.Len(t, a.handled, 1, "agent %s should have received 1 event", a.id)
		assert.Equal(t, "ev-test", a.handled[0].ID)
	}
}

func TestCoordinator_Send(t *testing.T) {
	c := kogagent.NewCoordinator(nil)
	a := newMockAgent("target")
	other := newMockAgent("other")
	require.NoError(t, c.Register(a))
	require.NoError(t, c.Register(other))

	ev := event.Event{ID: "direct", Source: "internal", Type: "tick"}
	require.NoError(t, c.Send(context.Background(), "target", ev))

	assert.Len(t, a.handled, 1)
	assert.Empty(t, other.handled)

	err := c.Send(context.Background(), "ghost", ev)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCoordinator_EmptyBroadcast(t *testing.T) {
	c := kogagent.NewCoordinator(nil)
	ev := event.Event{ID: "x"}
	require.NoError(t, c.Broadcast(context.Background(), ev))
}
