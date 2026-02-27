// Package runtime — YAML config loading for the Kog runtime.
// Supports environment variable overrides via ${VAR} or $VAR syntax in values.
package runtime

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// KogConfig is the top-level configuration loaded from kog.yaml.
type KogConfig struct {
	// Runtime settings.
	Runtime RuntimeSettings `yaml:"runtime"`

	// Agents defined in config (static list; dynamic agents via Coordinator).
	Agents []AgentConfig `yaml:"agents"`

	// Memory configuration.
	Memory MemoryConfig `yaml:"memory"`

	// Sources configures event sources.
	Sources SourcesConfig `yaml:"sources"`

	// Routing rules for SmartRouter.
	Routing []Rule `yaml:"routing"`

	// LLM provider defaults.
	LLM LLMConfig `yaml:"llm"`
}

// RuntimeSettings maps to the runtime.Config fields in YAML.
type RuntimeSettings struct {
	MaxConcurrency  int `yaml:"max_concurrency"`
	EventBufferSize int `yaml:"event_buffer_size"`
}

// AgentConfig describes a statically-configured agent.
type AgentConfig struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	Role         string   `yaml:"role"`
	Description  string   `yaml:"description"`
	Persona      string   `yaml:"persona"`
	Capabilities []string `yaml:"capabilities"`
	MemoryScope  string   `yaml:"memory_scope"`
	// ModelOverride overrides the global LLM model for this agent.
	ModelOverride string `yaml:"model_override"`
	// MaxToolIter overrides the default tool iteration limit.
	MaxToolIter int `yaml:"max_tool_iter"`
}

// MemoryConfig configures the memory backend.
type MemoryConfig struct {
	// DSN is the SQLite DSN, e.g. "file:kog.db?cache=shared&_journal=WAL".
	DSN string `yaml:"dsn"`

	// EmbedderEndpoint enables semantic search. e.g. "http://localhost:11434/api/embeddings".
	// Leave empty to disable vector search.
	EmbedderEndpoint string `yaml:"embedder_endpoint"`

	// EmbedderModel is the model name for the embedding endpoint.
	EmbedderModel string `yaml:"embedder_model"`

	// EmbedderAPIKey is the API key for the embedding endpoint.
	EmbedderAPIKey string `yaml:"embedder_api_key"`

	// EmbedderDimensions is the expected embedding size. 0 = auto-detect.
	EmbedderDimensions int `yaml:"embedder_dimensions"`
}

// SourcesConfig holds all event source configurations.
type SourcesConfig struct {
	// Telegram event source.
	Telegram TelegramSourceConfig `yaml:"telegram"`

	// Webhook HTTP source.
	Webhook WebhookSourceConfig `yaml:"webhook"`

	// Cron jobs.
	Cron []CronJobConfig `yaml:"cron"`
}

// TelegramSourceConfig configures the Telegram EventSource.
type TelegramSourceConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`
}

// WebhookSourceConfig configures the HTTP webhook EventSource.
type WebhookSourceConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	Path    string `yaml:"path"`
	Secret  string `yaml:"secret"`
}

// CronJobConfig defines a single cron-triggered event.
type CronJobConfig struct {
	Name string `yaml:"name"`
	Spec string `yaml:"spec"` // cron expression, e.g. "@every 5m"
}

// LLMConfig holds LLM provider defaults.
type LLMConfig struct {
	// Provider: "anthropic" | "openai" | "ollama".
	Provider string `yaml:"provider"`

	// Model ID, e.g. "claude-opus-4-5" or "gpt-4o".
	Model string `yaml:"model"`

	// APIKey. Prefer ${ANTHROPIC_API_KEY} syntax for security.
	APIKey string `yaml:"api_key"`

	// BaseURL for OpenAI-compatible or self-hosted providers.
	BaseURL string `yaml:"base_url"`

	// MaxTokens for completions. Default: 4096.
	MaxTokens int `yaml:"max_tokens"`
}

// LoadConfig reads and parses a YAML config file, expanding env vars.
func LoadConfig(path string) (*KogConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Expand environment variables in the YAML before parsing.
	expanded := expandEnvVars(string(raw))

	var cfg KogConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadConfigBytes parses a YAML config from bytes (useful for testing).
func LoadConfigBytes(data []byte) (*KogConfig, error) {
	expanded := expandEnvVars(string(data))
	var cfg KogConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// ToRuntimeConfig converts the YAML runtime settings to runtime.Config.
func (kc *KogConfig) ToRuntimeConfig() Config {
	cfg := DefaultConfig()
	if kc.Runtime.MaxConcurrency > 0 {
		cfg.MaxConcurrency = kc.Runtime.MaxConcurrency
	}
	if kc.Runtime.EventBufferSize > 0 {
		cfg.EventBufferSize = kc.Runtime.EventBufferSize
	}
	return cfg
}

// applyDefaults fills in zero-value fields with sensible defaults.
func applyDefaults(cfg *KogConfig) {
	if cfg.Memory.DSN == "" {
		cfg.Memory.DSN = "file:kog.db?cache=shared&_journal=WAL&_timeout=5000"
	}
	if cfg.LLM.MaxTokens == 0 {
		cfg.LLM.MaxTokens = 4096
	}
	if cfg.Sources.Webhook.Addr == "" {
		cfg.Sources.Webhook.Addr = ":8088"
	}
	if cfg.Sources.Webhook.Path == "" {
		cfg.Sources.Webhook.Path = "/webhook"
	}
}

// envVarPattern matches ${VAR_NAME} and $VAR_NAME.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandEnvVars replaces ${VAR} and $VAR with the corresponding environment
// variable value. Missing vars are replaced with an empty string.
func expandEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// Extract variable name from ${VAR} or $VAR.
		name := strings.TrimPrefix(match, "${")
		name = strings.TrimSuffix(name, "}")
		name = strings.TrimPrefix(name, "$")
		return os.Getenv(name)
	})
}
