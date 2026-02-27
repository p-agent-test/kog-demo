// Package runtime implements the Kog event loop — the heart of the system.
package runtime

import (
	"context"
	"log/slog"
	"sync"

	"github.com/p-blackswan/platform-agent/internal/consciousness"
	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/p-blackswan/platform-agent/internal/kogagent"
)

// Config holds runtime configuration.
type Config struct {
	// MaxConcurrency limits how many events are handled in parallel.
	MaxConcurrency int

	// EventBufferSize is the capacity of the internal event channel.
	EventBufferSize int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxConcurrency:  4,
		EventBufferSize: 256,
	}
}

// Router decides which agents should handle a given event.
// The default implementation sends every event to all registered agents.
type Router interface {
	Route(ev event.Event) []kogagent.Agent
}

// broadcastRouter sends every event to every agent.
type broadcastRouter struct {
	agents []kogagent.Agent
}

func (r *broadcastRouter) Route(_ event.Event) []kogagent.Agent { return r.agents }

// Runtime is the main event loop. It wires sources → router → agents.
type Runtime struct {
	config           Config
	sources          []event.EventSource
	router           Router
	agents           []kogagent.Agent
	consciousnessLoop *consciousness.Loop
	logger           *slog.Logger
}

// New creates a Runtime.
func New(cfg Config, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{config: cfg, logger: logger}
}

// AddSource registers an event source. Must be called before Run().
func (r *Runtime) AddSource(src event.EventSource) {
	r.sources = append(r.sources, src)
}

// AddAgent registers an agent. Must be called before Run().
func (r *Runtime) AddAgent(a kogagent.Agent) {
	r.agents = append(r.agents, a)
}

// SetRouter sets a custom event router. If not set, defaults to broadcast.
func (r *Runtime) SetRouter(router Router) {
	r.router = router
}

// SetConsciousnessLoop attaches an autonomous thought loop to the runtime.
// When set, the loop is started (via loop.Start) at the beginning of Run().
func (r *Runtime) SetConsciousnessLoop(loop *consciousness.Loop) {
	r.consciousnessLoop = loop
}

// Run starts the event loop. Blocks until ctx is cancelled.
func (r *Runtime) Run(ctx context.Context) error {
	if r.router == nil {
		r.router = &broadcastRouter{agents: r.agents}
	}

	eventCh := make(chan event.Event, r.config.EventBufferSize)

	// Start consciousness loop (autonomous thought) if configured.
	if r.consciousnessLoop != nil {
		if err := r.consciousnessLoop.Start(ctx); err != nil {
			r.logger.Error("consciousness loop failed to start", "err", err)
		} else {
			r.logger.Info("consciousness loop started")
		}
	}

	// Start all event sources.
	for _, src := range r.sources {
		r.logger.Info("starting event source", "source", src.Name())
		if err := src.Subscribe(ctx, eventCh); err != nil {
			return err
		}
	}

	// Worker pool via semaphore.
	sem := make(chan struct{}, r.config.MaxConcurrency)
	var wg sync.WaitGroup

	r.logger.Info("kog runtime started",
		"sources", len(r.sources),
		"agents", len(r.agents),
		"concurrency", r.config.MaxConcurrency,
	)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("runtime shutting down, waiting for in-flight handlers")
			wg.Wait()
			return ctx.Err()

		case ev := <-eventCh:
			targets := r.router.Route(ev)
			if len(targets) == 0 {
				r.logger.Debug("event routed to no agents", "event_id", ev.ID)
				continue
			}

			for _, ag := range targets {
				// Capture loop variables.
				ag := ag
				ev := ev

				sem <- struct{}{} // acquire concurrency slot
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-sem }() // release slot

					if err := ag.Handle(ctx, ev); err != nil {
						r.logger.Error("agent handle error",
							"agent", ag.ID(),
							"event_id", ev.ID,
							"err", err,
						)
					}
				}()
			}
		}
	}
}
