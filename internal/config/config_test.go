// Package config tests.
package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequiredEnvs(t *testing.T) {
	t.Helper()
	envs := map[string]string{
		"SLACK_BOT_TOKEN":        "xoxb-test",
		"SLACK_APP_TOKEN":        "xapp-test",
		"GITHUB_APP_ID":          "12345",
		"GITHUB_INSTALLATION_ID": "67890",
		"GITHUB_PRIVATE_KEY_PATH": "/tmp/test.pem",
		"JIRA_BASE_URL":          "https://test.atlassian.net",
	}
	for k, v := range envs {
		t.Setenv(k, v)
	}
}

func TestLoad_Success(t *testing.T) {
	setRequiredEnvs(t)
	cfg, err := LoadWithPrefix("")
	require.NoError(t, err)
	assert.Equal(t, "xoxb-test", cfg.SlackBotToken)
	assert.Equal(t, int64(12345), cfg.GitHubAppID)
	assert.Equal(t, "https://test.atlassian.net", cfg.JiraBaseURL)
	assert.Equal(t, "development", cfg.Environment)
	assert.Equal(t, 8080, cfg.HTTPPort)
}

func TestLoad_MissingRequired(t *testing.T) {
	// Clear all envs — now everything is optional, so Load should succeed
	os.Clearenv()
	cfg, err := Load()
	require.NoError(t, err)
	// Defaults should be set
	assert.Equal(t, "development", cfg.Environment)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, ":8090", cfg.MgmtListenAddr)
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnvs(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "#platform-approvals", cfg.SupervisorChannel)
}

func TestLoad_CustomPort(t *testing.T) {
	setRequiredEnvs(t)
	t.Setenv("HTTP_PORT", "9090")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.HTTPPort)
}

func TestConfig_EnabledFlags(t *testing.T) {
	cfg := &Config{}
	assert.False(t, cfg.SlackEnabled())
	assert.False(t, cfg.GitHubEnabled())
	assert.False(t, cfg.JiraEnabled())

	cfg.SlackBotToken = "xoxb-test"
	cfg.SlackAppToken = "xapp-test"
	assert.True(t, cfg.SlackEnabled())

	cfg.GitHubAppID = 123
	cfg.GitHubPrivateKeyPath = "/tmp/test.pem"
	assert.True(t, cfg.GitHubEnabled())

	cfg.JiraBaseURL = "https://test.atlassian.net"
	assert.True(t, cfg.JiraEnabled())
}

func TestLoad_MgmtDefaults(t *testing.T) {
	os.Clearenv()
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ":8090", cfg.MgmtListenAddr)
	assert.Equal(t, "api-key", cfg.MgmtAuthMode)
	assert.Equal(t, 100, cfg.MgmtRateLimitRPS)
	assert.Equal(t, 200, cfg.MgmtRateLimitBurst)
	assert.Equal(t, 4, cfg.MgmtWorkers)
}
