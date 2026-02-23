package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// OrgInstallation pairs an org name with its GitHub App installation ID.
type OrgInstallation struct {
	Owner          string
	InstallationID int64
}

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

	// Multi-org: comma-separated "owner:installationID" pairs
	// Example: "p-blackswan:111307878,p-backoffice:222408999"
	// If set, overrides GitHubInstallationID. If not set, falls back to single-org mode.
	GitHubOrgs string `envconfig:"GITHUB_ORGS"`

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
	BridgeMode       string `envconfig:"BRIDGE_MODE" default:"cli"` // "cli" or "ws"
	OpenClawBin      string `envconfig:"OPENCLAW_BIN" default:"openclaw"`
	OpenClawURL      string `envconfig:"OPENCLAW_GATEWAY_URL"`  // ws://localhost:18789/ws/gateway (WS mode) or http://localhost:18789 (CLI mode)
	OpenClawToken    string `envconfig:"OPENCLAW_GATEWAY_TOKEN"`

	// WS Bridge (device auth — required when BRIDGE_MODE=ws)
	WSDeviceID        string `envconfig:"WS_DEVICE_ID"`
	WSPublicKey       string `envconfig:"WS_PUBLIC_KEY"`        // base64url, raw 32 bytes
	WSPrivateKeyPath  string `envconfig:"WS_PRIVATE_KEY_PATH"`  // path to PEM file (Ed25519)

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

// GitHubMultiOrg returns true if multi-org mode is configured.
func (c *Config) GitHubMultiOrg() bool {
	return c.GitHubOrgs != ""
}

// ParseGitHubOrgs parses GITHUB_ORGS env var into OrgInstallation list.
// Format: "owner1:installationID1,owner2:installationID2"
// Falls back to single-org (GitHubInstallationID) if GITHUB_ORGS is empty.
func (c *Config) ParseGitHubOrgs() ([]OrgInstallation, error) {
	if c.GitHubOrgs != "" {
		return parseOrgInstallations(c.GitHubOrgs)
	}
	// Single-org fallback
	if c.GitHubInstallationID > 0 {
		return []OrgInstallation{{Owner: "default", InstallationID: c.GitHubInstallationID}}, nil
	}
	return nil, fmt.Errorf("no GitHub installations configured")
}

func parseOrgInstallations(raw string) ([]OrgInstallation, error) {
	parts := strings.Split(raw, ",")
	orgs := make([]OrgInstallation, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens := strings.SplitN(part, ":", 2)
		if len(tokens) != 2 {
			return nil, fmt.Errorf("invalid org format %q, expected owner:installationID", part)
		}
		owner := strings.TrimSpace(tokens[0])
		id, err := strconv.ParseInt(strings.TrimSpace(tokens[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid installation ID for %q: %w", owner, err)
		}
		orgs = append(orgs, OrgInstallation{Owner: owner, InstallationID: id})
	}
	if len(orgs) == 0 {
		return nil, fmt.Errorf("GITHUB_ORGS is set but contains no valid entries")
	}
	return orgs, nil
}

// JiraEnabled returns true if Jira base URL is configured.
func (c *Config) JiraEnabled() bool {
	return c.JiraBaseURL != ""
}

// WSBridgeEnabled returns true if WS bridge mode is configured with device auth.
func (c *Config) WSBridgeEnabled() bool {
	return strings.EqualFold(c.BridgeMode, "ws") && c.WSDeviceID != "" && c.WSPrivateKeyPath != ""
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
