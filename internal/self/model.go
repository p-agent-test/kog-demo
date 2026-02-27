// Package self defines SelfModel — Kog's introspective capability registry.
// It answers the question "can I do this?" and tracks current load and known limitations.
package self

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

// Capability describes a single thing the agent can do.
type Capability struct {
	// ID is a unique identifier, e.g. "exec_shell", "github_api".
	ID string `json:"id"`

	// Description is a human-readable summary.
	Description string `json:"description"`

	// Available indicates whether this capability is currently usable.
	Available bool `json:"available"`

	// Limitations is a list of known constraints or caveats.
	Limitations []string `json:"limitations,omitempty"`

	// LastUsed tracks when this capability was last exercised.
	LastUsed time.Time `json:"last_used,omitempty"`

	// UseCount tracks how many times this capability has been used.
	UseCount int `json:"use_count"`
}

// LoadSnapshot is a point-in-time snapshot of the agent's runtime load.
type LoadSnapshot struct {
	ActiveTasks   int       `json:"active_tasks"`
	GoroutineCount int      `json:"goroutine_count"`
	AllocMB       float64   `json:"alloc_mb"`
	SnapshotAt    time.Time `json:"snapshot_at"`
}

// SelfModel maintains a registry of capabilities and tracks current load.
// It is safe for concurrent use.
type SelfModel struct {
	mu           sync.RWMutex
	logger       *slog.Logger
	capabilities map[string]*Capability
	activeTasks  int
}

// New creates an empty SelfModel.
func New(logger *slog.Logger) *SelfModel {
	if logger == nil {
		logger = slog.Default()
	}
	return &SelfModel{
		capabilities: make(map[string]*Capability),
		logger:       logger,
	}
}

// Register adds a capability to the model.
// If a capability with the same ID already exists, it is replaced.
func (m *SelfModel) Register(c Capability) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.capabilities[c.ID] = &c
	m.logger.Debug("capability registered", "id", c.ID, "available", c.Available)
}

// RegisterAll registers multiple capabilities at once.
func (m *SelfModel) RegisterAll(caps []Capability) {
	for _, c := range caps {
		m.Register(c)
	}
}

// CanDo returns true if a capability with the given ID exists and is available.
func (m *SelfModel) CanDo(capabilityID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.capabilities[capabilityID]
	return ok && c.Available
}

// Get returns the capability with the given ID, or nil if not found.
func (m *SelfModel) Get(capabilityID string) *Capability {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := m.capabilities[capabilityID]
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// SetAvailable marks a capability as available or unavailable.
func (m *SelfModel) SetAvailable(capabilityID string, available bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.capabilities[capabilityID]
	if !ok {
		return fmt.Errorf("self: capability %q not registered", capabilityID)
	}
	c.Available = available
	m.logger.Info("capability availability changed",
		"id", capabilityID, "available", available)
	return nil
}

// RecordUse marks that a capability was used. Updates UseCount and LastUsed.
func (m *SelfModel) RecordUse(capabilityID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.capabilities[capabilityID]; ok {
		c.UseCount++
		c.LastUsed = time.Now().UTC()
	}
}

// List returns a snapshot of all registered capabilities.
func (m *SelfModel) List() []Capability {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Capability, 0, len(m.capabilities))
	for _, c := range m.capabilities {
		out = append(out, *c)
	}
	return out
}

// AvailableCapabilities returns only the capabilities that are currently available.
func (m *SelfModel) AvailableCapabilities() []Capability {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Capability, 0)
	for _, c := range m.capabilities {
		if c.Available {
			out = append(out, *c)
		}
	}
	return out
}

// IncrementActive increments the active task counter.
func (m *SelfModel) IncrementActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeTasks++
}

// DecrementActive decrements the active task counter.
func (m *SelfModel) DecrementActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeTasks > 0 {
		m.activeTasks--
	}
}

// Load returns the current load snapshot.
func (m *SelfModel) Load() LoadSnapshot {
	m.mu.RLock()
	active := m.activeTasks
	m.mu.RUnlock()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return LoadSnapshot{
		ActiveTasks:    active,
		GoroutineCount: runtime.NumGoroutine(),
		AllocMB:        float64(ms.Alloc) / (1024 * 1024),
		SnapshotAt:     time.Now().UTC(),
	}
}

// CanHandleMore returns true if the agent has capacity for more work.
// maxActive=0 means unlimited.
func (m *SelfModel) CanHandleMore(maxActive int) bool {
	if maxActive <= 0 {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeTasks < maxActive
}

// Summary returns a human-readable one-liner about current state.
func (m *SelfModel) Summary() string {
	load := m.Load()
	caps := m.AvailableCapabilities()
	return fmt.Sprintf("active_tasks=%d goroutines=%d alloc=%.1fMB available_caps=%d",
		load.ActiveTasks, load.GoroutineCount, load.AllocMB, len(caps))
}
