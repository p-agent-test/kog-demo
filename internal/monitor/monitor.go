// Package monitor defines the Monitor interface and a central Registry.
// Monitors observe external or internal state and emit Observations when
// something noteworthy is detected.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Severity levels for Observation.
const (
	SeverityInfo     = 1
	SeverityWarning  = 5
	SeverityError    = 7
	SeverityCritical = 10
)

// Observation is a single detected event from a monitor.
type Observation struct {
	// MonitorID identifies which monitor produced this observation.
	MonitorID string `json:"monitor_id"`

	// SituationID is a stable identifier for this type of situation (e.g. "disk_high").
	SituationID string `json:"situation_id"`

	// Severity is 0–10.
	Severity int `json:"severity"`

	// Message is a human-readable description of what was observed.
	Message string `json:"message"`

	// Details is arbitrary key-value context.
	Details map[string]string `json:"details,omitempty"`

	// ObservedAt is when the observation was made.
	ObservedAt time.Time `json:"observed_at"`
}

// Monitor is implemented by any component that watches something and can report Observations.
type Monitor interface {
	// ID returns a stable identifier for this monitor.
	ID() string

	// Check performs a single check and returns any observations.
	// An empty slice means nothing to report.
	Check(ctx context.Context) ([]Observation, error)
}

// Registry holds and runs a collection of monitors.
// It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	monitors map[string]Monitor
	logger   *slog.Logger
}

// NewRegistry creates an empty Registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		monitors: make(map[string]Monitor),
		logger:   logger,
	}
}

// Register adds a monitor to the registry.
// Returns an error if a monitor with the same ID already exists.
func (r *Registry) Register(m Monitor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.monitors[m.ID()]; exists {
		return fmt.Errorf("monitor: %q already registered", m.ID())
	}
	r.monitors[m.ID()] = m
	r.logger.Info("monitor registered", "id", m.ID())
	return nil
}

// Unregister removes a monitor by ID.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.monitors, id)
}

// Get returns a monitor by ID.
func (r *Registry) Get(id string) (Monitor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.monitors[id]
	return m, ok
}

// List returns all registered monitors.
func (r *Registry) List() []Monitor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Monitor, 0, len(r.monitors))
	for _, m := range r.monitors {
		out = append(out, m)
	}
	return out
}

// CheckAll runs all monitors concurrently and returns all observations.
func (r *Registry) CheckAll(ctx context.Context) ([]Observation, error) {
	monitors := r.List()
	if len(monitors) == 0 {
		return nil, nil
	}

	type result struct {
		obs []Observation
		err error
		id  string
	}
	results := make(chan result, len(monitors))

	for _, m := range monitors {
		m := m
		go func() {
			obs, err := m.Check(ctx)
			results <- result{obs: obs, err: err, id: m.ID()}
		}()
	}

	var all []Observation
	var errs []string
	for range monitors {
		res := <-results
		if res.err != nil {
			r.logger.Error("monitor check error", "monitor", res.id, "err", res.err)
			errs = append(errs, fmt.Sprintf("%s: %v", res.id, res.err))
		}
		all = append(all, res.obs...)
	}

	if len(errs) > 0 {
		return all, fmt.Errorf("monitor check errors: %v", errs)
	}
	return all, nil
}

// Count returns the number of registered monitors.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.monitors)
}
