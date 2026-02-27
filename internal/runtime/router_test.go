package runtime_test

import (
	"context"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/p-blackswan/platform-agent/internal/kogagent"
	"github.com/p-blackswan/platform-agent/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAgent is a minimal Agent stub for routing tests.
type stubAgent struct{ id string }

func (s *stubAgent) ID() string                                    { return s.id }
func (s *stubAgent) Status() kogagent.Status                       { return kogagent.StatusIdle }
func (s *stubAgent) Handle(_ context.Context, _ event.Event) error { return nil }

func makeAgents(ids ...string) []kogagent.Agent {
	agents := make([]kogagent.Agent, len(ids))
	for i, id := range ids {
		agents[i] = &stubAgent{id: id}
	}
	return agents
}

func TestSmartRouter_MatchBySource(t *testing.T) {
	agents := makeAgents("telegram-handler", "cron-handler", "general")
	rules := []runtime.Rule{
		{Source: event.SourceTelegram, Agents: []string{"telegram-handler"}},
		{Source: event.SourceCron, Agents: []string{"cron-handler"}},
	}
	r := runtime.NewSmartRouter(rules, agents, nil)

	ev := event.Event{Source: event.SourceTelegram, Type: event.TypeMessage}
	targets := r.Route(ev)
	require.Len(t, targets, 1)
	assert.Equal(t, "telegram-handler", targets[0].ID())
}

func TestSmartRouter_MatchByType(t *testing.T) {
	agents := makeAgents("tick-handler", "other")
	rules := []runtime.Rule{
		{Type: event.TypeTick, Agents: []string{"tick-handler"}},
	}
	r := runtime.NewSmartRouter(rules, agents, nil)

	ev := event.Event{Source: event.SourceCron, Type: event.TypeTick}
	targets := r.Route(ev)
	require.Len(t, targets, 1)
	assert.Equal(t, "tick-handler", targets[0].ID())
}

func TestSmartRouter_MatchBySourceAndType(t *testing.T) {
	agents := makeAgents("specific", "catch-all")
	rules := []runtime.Rule{
		{Source: event.SourceTelegram, Type: event.TypeMessage, Agents: []string{"specific"}},
		{Type: event.TypeMessage, Agents: []string{"catch-all"}},
	}
	r := runtime.NewSmartRouter(rules, agents, nil)

	// Should match first rule (source+type).
	ev := event.Event{Source: event.SourceTelegram, Type: event.TypeMessage}
	targets := r.Route(ev)
	require.Len(t, targets, 1)
	assert.Equal(t, "specific", targets[0].ID())

	// Different source → falls to second rule.
	ev2 := event.Event{Source: event.SourceWebhook, Type: event.TypeMessage}
	targets2 := r.Route(ev2)
	require.Len(t, targets2, 1)
	assert.Equal(t, "catch-all", targets2[0].ID())
}

func TestSmartRouter_MetaFilter(t *testing.T) {
	agents := makeAgents("vip-handler", "default")
	rules := []runtime.Rule{
		{
			MetaKey:   "priority",
			MetaValue: "high",
			Agents:    []string{"vip-handler"},
		},
	}
	r := runtime.NewSmartRouter(rules, agents, nil)

	// Event with priority=high → vip-handler.
	ev := event.Event{
		Source:   event.SourceTelegram,
		Type:     event.TypeMessage,
		Metadata: map[string]string{"priority": "high"},
	}
	targets := r.Route(ev)
	require.Len(t, targets, 1)
	assert.Equal(t, "vip-handler", targets[0].ID())

	// Event without matching meta → broadcast fallback.
	ev2 := event.Event{Source: event.SourceTelegram, Type: event.TypeMessage}
	targets2 := r.Route(ev2)
	assert.Len(t, targets2, 2) // all agents
}

func TestSmartRouter_BroadcastFallback(t *testing.T) {
	agents := makeAgents("a", "b", "c")
	r := runtime.NewSmartRouter(nil, agents, nil)

	ev := event.Event{Source: "unknown", Type: "unknown"}
	targets := r.Route(ev)
	assert.Len(t, targets, 3)
}

func TestSmartRouter_ExplicitBroadcastRule(t *testing.T) {
	agents := makeAgents("x", "y")
	rules := []runtime.Rule{
		{Source: event.SourceInternal, Agents: nil}, // empty Agents = broadcast
	}
	r := runtime.NewSmartRouter(rules, agents, nil)

	ev := event.Event{Source: event.SourceInternal}
	targets := r.Route(ev)
	assert.Len(t, targets, 2)
}

func TestSmartRouter_AddAgentAndRule(t *testing.T) {
	r := runtime.NewSmartRouter(nil, nil, nil)

	r.AddAgent(&stubAgent{id: "late-agent"})
	r.AddRule(runtime.Rule{Source: "x", Agents: []string{"late-agent"}})

	ev := event.Event{Source: "x"}
	targets := r.Route(ev)
	require.Len(t, targets, 1)
	assert.Equal(t, "late-agent", targets[0].ID())
}

func TestSmartRouter_PrependRule(t *testing.T) {
	agents := makeAgents("first", "second")
	rules := []runtime.Rule{
		{Source: "any", Agents: []string{"second"}},
	}
	r := runtime.NewSmartRouter(rules, agents, nil)
	r.PrependRule(runtime.Rule{Source: "any", Agents: []string{"first"}})

	ev := event.Event{Source: "any"}
	targets := r.Route(ev)
	require.Len(t, targets, 1)
	assert.Equal(t, "first", targets[0].ID())
}

func TestSmartRouter_UnknownAgentInRule(t *testing.T) {
	agents := makeAgents("real")
	rules := []runtime.Rule{
		{Source: "x", Agents: []string{"ghost", "real"}},
	}
	r := runtime.NewSmartRouter(rules, agents, nil)

	ev := event.Event{Source: "x"}
	targets := r.Route(ev)
	// "ghost" is unknown, only "real" returned.
	require.Len(t, targets, 1)
	assert.Equal(t, "real", targets[0].ID())
}
