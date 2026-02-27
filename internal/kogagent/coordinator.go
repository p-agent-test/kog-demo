// Package kogagent — AgentCoordinator manages a pool of agents.
// Supports spawn, kill, get, list, and broadcast operations.
package kogagent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/p-blackswan/platform-agent/internal/event"
)

// Coordinator manages a dynamic pool of agents.
// It is safe for concurrent use.
type Coordinator struct {
	mu     sync.RWMutex
	agents map[string]Agent
	logger *slog.Logger
}

// NewCoordinator creates an empty Coordinator.
func NewCoordinator(logger *slog.Logger) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Coordinator{
		agents: make(map[string]Agent),
		logger: logger,
	}
}

// Register adds a pre-built agent to the pool.
// Returns an error if an agent with the same ID already exists.
func (c *Coordinator) Register(a Agent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.agents[a.ID()]; exists {
		return fmt.Errorf("coordinator: agent %q already registered", a.ID())
	}
	c.agents[a.ID()] = a
	c.logger.Info("agent registered", "id", a.ID())
	return nil
}

// Spawn creates a new baseAgent from a Spec, registers it, and returns it.
// Returns an error if an agent with Spec.ID already exists.
func (c *Coordinator) Spawn(spec Spec) (Agent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.agents[spec.ID]; exists {
		return nil, fmt.Errorf("coordinator: agent %q already exists", spec.ID)
	}

	a, err := New(spec)
	if err != nil {
		return nil, fmt.Errorf("coordinator: spawn %s: %w", spec.ID, err)
	}

	c.agents[spec.ID] = a
	c.logger.Info("agent spawned", "id", spec.ID)
	return a, nil
}

// SpawnPlanner creates a new PlannerAgent from a PlannerSpec, registers and returns it.
func (c *Coordinator) SpawnPlanner(spec PlannerSpec) (*PlannerAgent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := spec.Identity.ID
	if _, exists := c.agents[id]; exists {
		return nil, fmt.Errorf("coordinator: planner %q already exists", id)
	}

	pa, err := NewPlannerAgent(spec)
	if err != nil {
		return nil, fmt.Errorf("coordinator: spawn planner %s: %w", id, err)
	}

	c.agents[id] = pa
	c.logger.Info("planner agent spawned", "id", id)
	return pa, nil
}

// Kill removes an agent from the pool by ID.
// Returns an error if the agent does not exist.
func (c *Coordinator) Kill(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.agents[id]; !exists {
		return fmt.Errorf("coordinator: agent %q not found", id)
	}
	delete(c.agents, id)
	c.logger.Info("agent killed", "id", id)
	return nil
}

// Get returns the agent with the given ID, or false if not found.
func (c *Coordinator) Get(id string) (Agent, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	a, ok := c.agents[id]
	return a, ok
}

// List returns a snapshot of all registered agents.
func (c *Coordinator) List() []Agent {
	c.mu.RLock()
	defer c.mu.RUnlock()

	agents := make([]Agent, 0, len(c.agents))
	for _, a := range c.agents {
		agents = append(agents, a)
	}
	return agents
}

// Broadcast sends the event to all registered agents concurrently.
// Returns a combined error if any agents fail.
func (c *Coordinator) Broadcast(ctx context.Context, ev event.Event) error {
	agents := c.List()
	if len(agents) == 0 {
		return nil
	}

	type result struct {
		id  string
		err error
	}
	results := make(chan result, len(agents))

	for _, a := range agents {
		a := a
		go func() {
			err := a.Handle(ctx, ev)
			results <- result{id: a.ID(), err: err}
		}()
	}

	var errs []string
	for range agents {
		r := <-results
		if r.err != nil {
			c.logger.Error("broadcast: agent error",
				"agent", r.id,
				"event_id", ev.ID,
				"err", r.err,
			)
			errs = append(errs, fmt.Sprintf("%s: %v", r.id, r.err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("broadcast errors: %v", errs)
	}
	return nil
}

// Send delivers the event to a single agent by ID.
func (c *Coordinator) Send(ctx context.Context, agentID string, ev event.Event) error {
	a, ok := c.Get(agentID)
	if !ok {
		return fmt.Errorf("coordinator: agent %q not found", agentID)
	}
	return a.Handle(ctx, ev)
}

// Count returns the number of registered agents.
func (c *Coordinator) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.agents)
}
