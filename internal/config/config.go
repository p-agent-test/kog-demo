package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// General
	Environment string `envconfig:"ENVIRONMENT" default:"development"`
	LogLevel    string `envconfig:"LOG_LEVEL" default:"info"`
	HTTPPort    int    `envconfig:"HTTP_PORT" default:"8080"`

	// Slack (optional — agent starts without Slack in mgmt-only mode)
	// Prefixed with AGENT_ to prevent OpenClaw from auto-detecting and subscribing
	SlackBotToken         string `envconfig:"AGENT_SLACK_BOT_TOKEN"`
	SlackAppToken         string `envconfig:"AGENT_SLACK_APP_TOKEN"` // xapp- token for Socket Mode
	SlackSigningSecret    string `envconfig:"AGENT_SLACK_SIGNING_SECRET"`
	SlackAllowedChannels  string `envconfig:"AGENT_SLACK_ALLOWED_CHANNELS"` // Comma-separated channel IDs the bot can write to (fail-closed if empty)

	// GitHub App (optional — agent starts without GitHub in mgmt-only mode)
	GitHubAppID          int64  `envconfig:"GITHUB_APP_ID"`
	GitHubInstallationID int64  `envconfig:"GITHUB_INSTALLATION_ID"`
	GitHubPrivateKeyPath string `envconfig:"GITHUB_PRIVATE_KEY_PATH"`
	GitHubWebhookSecret  string `envconfig:"GITHUB_WEBHOOK_SECRET"`

	// Jira (optional — agent starts without Jira in mgmt-only mode)
	JiraBaseURL      string `envconfig:"JIRA_BASE_URL"`
	JiraClientID     string `envconfig:"JIRA_CLIENT_ID"`
	JiraClientSecret string `envconfig:"JIRA_CLIENT_SECRET"`
	JiraAPIEmail     string `envconfig:"JIRA_API_EMAIL"`  // Basic auth (dev)
	JiraAPIToken     string `envconfig:"JIRA_API_TOKEN"`  // Basic auth (dev)
	JiraCloudID      string `envconfig:"JIRA_CLOUD_ID"`

	// Supervisor
	SupervisorChannel     string        `envconfig:"SUPERVISOR_CHANNEL" default:"#platform-approvals"`
	SupervisorAutoApprove bool          `envconfig:"SUPERVISOR_AUTO_APPROVE" default:"false"`
	TokenTTL              time.Duration `envconfig:"TOKEN_TTL" default:"10m"`
	ApprovalTimeout       time.Duration `envconfig:"APPROVAL_TIMEOUT" default:"30m"`

	// Bridge (Slack → Kog-2 via OpenClaw)
	OpenClawBin      string `envconfig:"OPENCLAW_BIN" default:"openclaw"`
	OpenClawURL      string `envconfig:"OPENCLAW_GATEWAY_URL"`  // ws://127.0.0.1:18789
	OpenClawToken    string `envconfig:"OPENCLAW_GATEWAY_TOKEN"`

	// Management API
	MgmtListenAddr   string        `envconfig:"MGMT_LISTEN_ADDR" default:":8090"`
	MgmtAuthMode     string        `envconfig:"MGMT_AUTH_MODE" default:"api-key"`
	MgmtAPIKey       string        `envconfig:"MGMT_API_KEY"`
	MgmtRateLimitRPS int           `envconfig:"MGMT_RATE_LIMIT_RPS" default:"100"`
	MgmtRateLimitBurst int         `envconfig:"MGMT_RATE_LIMIT_BURST" default:"200"`
	MgmtTLSCert      string        `envconfig:"MGMT_TLS_CERT"`
	MgmtTLSKey       string        `envconfig:"MGMT_TLS_KEY"`
	MgmtTLSCA        string        `envconfig:"MGMT_TLS_CA"`
	MgmtCORSOrigins  string        `envconfig:"MGMT_CORS_ORIGINS"`
	MgmtWorkers      int           `envconfig:"MGMT_WORKERS" default:"4"`
	CallbackTimeout  time.Duration `envconfig:"CALLBACK_TIMEOUT" default:"30s"`
	CallbackRetries  int           `envconfig:"CALLBACK_RETRIES" default:"3"`
}

// SlackEnabled returns true if Slack tokens are configured.
func (c *Config) SlackEnabled() bool {
	return c.SlackBotToken != "" && c.SlackAppToken != ""
}

// SlackAllowedChannelList returns the parsed list of allowed Slack channel IDs.
// Returns nil if not configured (fail-closed — no channels allowed).
func (c *Config) SlackAllowedChannelList() []string {
	if c.SlackAllowedChannels == "" {
		return nil
	}
	parts := strings.Split(c.SlackAllowedChannels, ",")
	channels := make([]string, 0, len(parts))
	for _, ch := range parts {
		ch = strings.TrimSpace(ch)
		if ch != "" {
			channels = append(channels, ch)
		}
	}
	return channels
}

// GitHubEnabled returns true if GitHub App credentials are configured.
func (c *Config) GitHubEnabled() bool {
	return c.GitHubAppID > 0 && c.GitHubPrivateKeyPath != ""
}

// JiraEnabled returns true if Jira base URL is configured.
func (c *Config) JiraEnabled() bool {
	return c.JiraBaseURL != ""
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return &cfg, nil
}

// LoadWithPrefix reads configuration with a prefix.
func LoadWithPrefix(prefix string) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(prefix, &cfg); err != nil {
		return nil, fmt.Errorf("loading config with prefix %s: %w", prefix, err)
	}
	return &cfg, nil
}
