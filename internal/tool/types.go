// Package tool defines the Tool interface and a ToolRegistry.
// Every capability the agent has is expressed as a Tool.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/p-blackswan/platform-agent/internal/llm"
)

// Tool is the interface all agent tools must implement.
type Tool interface {
	// Schema returns the tool's name, description, and JSON Schema for inputs.
	Schema() llm.ToolSchema

	// Execute runs the tool with the given JSON input and returns a result string.
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry holds all registered tools and provides lookup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty ToolRegistry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry. Panics on duplicate name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Schema().Name
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("tool already registered: %s", name))
	}
	r.tools[name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Schemas returns all tool schemas (for passing to LLM).
func (r *Registry) Schemas() []llm.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schemas := make([]llm.ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		schemas = append(schemas, t.Schema())
	}
	return schemas
}

// Execute runs a tool by name with JSON input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, input)
}

// MustSchema builds a json.RawMessage from a Go value (panics on error).
func MustSchema(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("MustSchema: %v", err))
	}
	return b
}
