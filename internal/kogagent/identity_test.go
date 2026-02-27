package kogagent_test

import (
	"strings"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/kogagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentIdentity_Validate(t *testing.T) {
	t.Run("valid identity", func(t *testing.T) {
		id := &kogagent.AgentIdentity{
			ID:          "kog-1",
			Name:        "Kog-1",
			Role:        kogagent.RoleExecutor,
			Description: "Does things",
		}
		require.NoError(t, id.Validate())
		assert.False(t, id.CreatedAt.IsZero())
	})

	t.Run("missing ID returns error", func(t *testing.T) {
		id := &kogagent.AgentIdentity{Name: "Kog"}
		err := id.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "id is required")
	})

	t.Run("defaults applied", func(t *testing.T) {
		id := &kogagent.AgentIdentity{ID: "x"}
		require.NoError(t, id.Validate())
		assert.Equal(t, "x", id.Name)
		assert.Equal(t, kogagent.RoleGeneral, id.Role)
		assert.Equal(t, "shared", id.MemoryScope)
		assert.Equal(t, 1, id.MaxConcurrentTasks)
	})
}

func TestAgentIdentity_SystemPrompt_WithPersona(t *testing.T) {
	id := &kogagent.AgentIdentity{
		ID:      "kog",
		Name:    "Kog",
		Persona: "You are Kog, the supreme executor.",
	}
	require.NoError(t, id.Validate())
	assert.Equal(t, "You are Kog, the supreme executor.", id.SystemPrompt())
}

func TestAgentIdentity_SystemPrompt_Generated(t *testing.T) {
	id := &kogagent.AgentIdentity{
		ID:           "kog-alpha",
		Name:         "Kog-Alpha",
		Role:         kogagent.RolePlanner,
		Description:  "Plans and coordinates complex workflows.",
		Capabilities: []string{"task decomposition", "agent coordination"},
	}
	require.NoError(t, id.Validate())

	prompt := id.SystemPrompt()
	assert.Contains(t, prompt, "Kog-Alpha")
	assert.Contains(t, prompt, "planner")
	assert.Contains(t, prompt, "task decomposition")
	assert.Contains(t, prompt, "agent coordination")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(prompt), "over explanation."))
}

func TestAgentIdentity_SystemPrompt_GeneralRole(t *testing.T) {
	id := &kogagent.AgentIdentity{
		ID:   "simple",
		Name: "Simple",
		Role: kogagent.RoleGeneral,
	}
	require.NoError(t, id.Validate())
	prompt := id.SystemPrompt()
	// RoleGeneral should not include the "a <role> agent" phrase.
	assert.NotContains(t, prompt, "a general agent")
	assert.Contains(t, prompt, "Simple")
}

func TestAgentIdentity_String(t *testing.T) {
	id := &kogagent.AgentIdentity{ID: "a", Name: "A", Role: kogagent.RoleReviewer}
	s := id.String()
	assert.Contains(t, s, "a")
	assert.Contains(t, s, "reviewer")
}
