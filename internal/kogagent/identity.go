// Package kogagent — AgentIdentity defines who an agent is and what it can do.
// Identity is loaded once at construction and shapes the agent's system prompt,
// tool access, memory scope, and coordination role.
package kogagent

import (
	"fmt"
	"strings"
	"time"
)

// Role classifies an agent's function in a multi-agent network.
type Role string

const (
	RolePlanner    Role = "planner"    // decomposes goals into sub-tasks
	RoleExecutor   Role = "executor"   // runs individual tasks / tools
	RoleReviewer   Role = "reviewer"   // validates outputs, QA
	RoleOrchestrator Role = "orchestrator" // coordinates other agents
	RoleGeneral    Role = "general"    // no specific role (standalone)
)

// AgentIdentity describes an agent's persistent self.
// It is used to generate the system prompt and configure runtime behaviour.
type AgentIdentity struct {
	// ID is the unique agent identifier. Must be stable across restarts.
	ID string `yaml:"id" json:"id"`

	// Name is a human-readable display name, e.g. "Kog-Alpha".
	Name string `yaml:"name" json:"name"`

	// Role determines the agent's function in a multi-agent system.
	Role Role `yaml:"role" json:"role"`

	// Description is a one-line summary of the agent's purpose.
	Description string `yaml:"description" json:"description"`

	// Persona is the first-person identity injected into the system prompt.
	// If empty, a default is generated from Name + Role + Description.
	Persona string `yaml:"persona" json:"persona"`

	// Capabilities is a list of high-level skills (used in prompts / routing).
	Capabilities []string `yaml:"capabilities" json:"capabilities"`

	// MemoryScope controls which memory entries this agent reads.
	// "own" = only entries saved by this agent.
	// "shared" = all entries (default).
	// "none" = memory disabled.
	MemoryScope string `yaml:"memory_scope" json:"memory_scope"`

	// MaxConcurrentTasks limits simultaneous in-flight Handle() calls.
	// Default: 1 (sequential).
	MaxConcurrentTasks int `yaml:"max_concurrent_tasks" json:"max_concurrent_tasks"`

	// CreatedAt is populated automatically when Validate() is called.
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
}

// Validate checks required fields and sets defaults.
func (id *AgentIdentity) Validate() error {
	if id.ID == "" {
		return fmt.Errorf("identity: id is required")
	}
	if id.Name == "" {
		id.Name = id.ID
	}
	if id.Role == "" {
		id.Role = RoleGeneral
	}
	if id.MemoryScope == "" {
		id.MemoryScope = "shared"
	}
	if id.MaxConcurrentTasks <= 0 {
		id.MaxConcurrentTasks = 1
	}
	if id.CreatedAt.IsZero() {
		id.CreatedAt = time.Now().UTC()
	}
	return nil
}

// SystemPrompt generates a system prompt from the identity.
// If Persona is set, it is used verbatim (caller may append tool/memory info).
// Otherwise a structured prompt is generated from Name, Role, Description, Capabilities.
func (id *AgentIdentity) SystemPrompt() string {
	if id.Persona != "" {
		return id.Persona
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are %s", id.Name)
	if id.Role != RoleGeneral {
		fmt.Fprintf(&b, ", a %s agent", id.Role)
	}
	b.WriteString(".\n")

	if id.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", id.Description)
	}

	if len(id.Capabilities) > 0 {
		b.WriteString("\nYour capabilities:\n")
		for _, c := range id.Capabilities {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}

	b.WriteString("\nBe precise, concise, and tool-first. Prefer action over explanation.")
	return b.String()
}

// String implements Stringer for logging.
func (id *AgentIdentity) String() string {
	return fmt.Sprintf("AgentIdentity{id=%s name=%s role=%s}", id.ID, id.Name, id.Role)
}
