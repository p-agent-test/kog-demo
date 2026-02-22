package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_New(t *testing.T) {
	m := New()
	assert.NotNil(t, m.RequestsTotal)
	assert.NotNil(t, m.RequestDuration)
	assert.NotNil(t, m.ApprovalsTotal)
	assert.NotNil(t, m.GitHubTokensActive)
	assert.NotNil(t, m.ErrorsTotal)
}

func TestMetrics_RecordRequest(t *testing.T) {
	m := New()
	m.RecordRequest("review", "completed")
	m.RecordRequest("review", "completed")
	m.RecordRequest("deploy", "error")

	// Verify via handler
	body := getMetricsBody(t, m)
	assert.Contains(t, body, `agent_requests_total{intent="review",status="completed"} 2`)
	assert.Contains(t, body, `agent_requests_total{intent="deploy",status="error"} 1`)
}

func TestMetrics_RecordError(t *testing.T) {
	m := New()
	m.RecordError("github", "auth_failure")

	body := getMetricsBody(t, m)
	assert.Contains(t, body, `agent_errors_total{module="github",type="auth_failure"} 1`)
}

func TestMetrics_RecordApproval(t *testing.T) {
	m := New()
	m.RecordApproval("deploy", "approved")
	m.RecordApproval("deploy", "denied")

	body := getMetricsBody(t, m)
	assert.Contains(t, body, `agent_approvals_total{action="deploy",result="approved"} 1`)
	assert.Contains(t, body, `agent_approvals_total{action="deploy",result="denied"} 1`)
}

func TestMetrics_ObserveDuration(t *testing.T) {
	m := New()
	m.ObserveDuration("review", 1.5)

	body := getMetricsBody(t, m)
	assert.Contains(t, body, "agent_request_duration_seconds")
}

func TestMetrics_SetGitHubTokens(t *testing.T) {
	m := New()
	m.SetGitHubTokens(3)

	body := getMetricsBody(t, m)
	assert.Contains(t, body, "agent_github_tokens_active 3")
}

func TestMetrics_Handler(t *testing.T) {
	m := New()
	handler := m.Handler()
	assert.NotNil(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func getMetricsBody(t *testing.T, m *Metrics) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	return strings.TrimSpace(string(body))
}
