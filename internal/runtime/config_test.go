package runtime_test

import (
	"os"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleYAML = `
runtime:
  max_concurrency: 8
  event_buffer_size: 512

llm:
  provider: anthropic
  model: claude-opus-4-5
  api_key: ${TEST_API_KEY}
  max_tokens: 2048

memory:
  dsn: "file:test.db"
  embedder_endpoint: "http://localhost:11434/api/embeddings"
  embedder_model: "nomic-embed-text"

sources:
  telegram:
    enabled: true
    token: ${TEST_TG_TOKEN}
  webhook:
    enabled: true
    addr: ":9090"
    path: "/events"
    secret: "shh"
  cron:
    - name: heartbeat
      spec: "@every 5m"
    - name: daily-report
      spec: "0 9 * * *"

routing:
  - source: telegram
    type: message
    agents: [telegram-agent]
  - type: tick
    agents: [cron-agent]

agents:
  - id: telegram-agent
    name: Telegram Handler
    role: executor
    capabilities: [messaging, search]
  - id: cron-agent
    name: Cron Worker
    role: executor
`

func TestLoadConfigBytes(t *testing.T) {
	os.Setenv("TEST_API_KEY", "sk-test-123")
	os.Setenv("TEST_TG_TOKEN", "bot-token-456")
	defer os.Unsetenv("TEST_API_KEY")
	defer os.Unsetenv("TEST_TG_TOKEN")

	cfg, err := runtime.LoadConfigBytes([]byte(sampleYAML))
	require.NoError(t, err)

	// Runtime settings.
	assert.Equal(t, 8, cfg.Runtime.MaxConcurrency)
	assert.Equal(t, 512, cfg.Runtime.EventBufferSize)

	// LLM.
	assert.Equal(t, "anthropic", cfg.LLM.Provider)
	assert.Equal(t, "claude-opus-4-5", cfg.LLM.Model)
	assert.Equal(t, "sk-test-123", cfg.LLM.APIKey) // env var expanded
	assert.Equal(t, 2048, cfg.LLM.MaxTokens)

	// Memory.
	assert.Equal(t, "file:test.db", cfg.Memory.DSN)
	assert.Equal(t, "nomic-embed-text", cfg.Memory.EmbedderModel)

	// Sources.
	assert.True(t, cfg.Sources.Telegram.Enabled)
	assert.Equal(t, "bot-token-456", cfg.Sources.Telegram.Token) // env var
	assert.Equal(t, ":9090", cfg.Sources.Webhook.Addr)
	assert.Equal(t, "/events", cfg.Sources.Webhook.Path)
	assert.Equal(t, "shh", cfg.Sources.Webhook.Secret)
	require.Len(t, cfg.Sources.Cron, 2)
	assert.Equal(t, "heartbeat", cfg.Sources.Cron[0].Name)
	assert.Equal(t, "@every 5m", cfg.Sources.Cron[0].Spec)

	// Routing.
	require.Len(t, cfg.Routing, 2)
	assert.Equal(t, "telegram", cfg.Routing[0].Source)
	assert.Equal(t, []string{"telegram-agent"}, cfg.Routing[0].Agents)

	// Agents.
	require.Len(t, cfg.Agents, 2)
	assert.Equal(t, "telegram-agent", cfg.Agents[0].ID)
	assert.Equal(t, "executor", cfg.Agents[0].Role)
	assert.Equal(t, []string{"messaging", "search"}, cfg.Agents[0].Capabilities)
}

func TestLoadConfigBytes_Defaults(t *testing.T) {
	cfg, err := runtime.LoadConfigBytes([]byte(`{}`))
	require.NoError(t, err)

	// Defaults applied.
	assert.NotEmpty(t, cfg.Memory.DSN)
	assert.Equal(t, 4096, cfg.LLM.MaxTokens)
	assert.Equal(t, ":8088", cfg.Sources.Webhook.Addr)
	assert.Equal(t, "/webhook", cfg.Sources.Webhook.Path)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := runtime.LoadConfig("/nonexistent/path/kog.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	_, err := runtime.LoadConfigBytes([]byte(`{ invalid yaml :`))
	require.Error(t, err)
}

func TestToRuntimeConfig(t *testing.T) {
	cfg := &runtime.KogConfig{
		Runtime: runtime.RuntimeSettings{
			MaxConcurrency:  16,
			EventBufferSize: 1024,
		},
	}
	rc := cfg.ToRuntimeConfig()
	assert.Equal(t, 16, rc.MaxConcurrency)
	assert.Equal(t, 1024, rc.EventBufferSize)
}

func TestToRuntimeConfig_Defaults(t *testing.T) {
	cfg := &runtime.KogConfig{}
	rc := cfg.ToRuntimeConfig()
	// Should use DefaultConfig values when not set.
	def := runtime.DefaultConfig()
	assert.Equal(t, def.MaxConcurrency, rc.MaxConcurrency)
	assert.Equal(t, def.EventBufferSize, rc.EventBufferSize)
}

func TestEnvVarExpansion_MissingVarIsEmpty(t *testing.T) {
	os.Unsetenv("UNSET_VAR_XYZ")
	cfg, err := runtime.LoadConfigBytes([]byte(`
llm:
  api_key: ${UNSET_VAR_XYZ}
`))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.LLM.APIKey)
}
