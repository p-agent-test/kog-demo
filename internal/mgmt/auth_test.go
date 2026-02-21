package mgmt

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuth_NoAuth_Mode(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/api/v1/config", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuth_APIKey_Valid(t *testing.T) {
	app := testApp(t, "api-key", "test-secret-key")

	req, _ := http.NewRequest("GET", "/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer test-secret-key")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuth_APIKey_Missing(t *testing.T) {
	app := testApp(t, "api-key", "test-secret-key")

	req, _ := http.NewRequest("GET", "/api/v1/config", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var problem ProblemDetail
	json.NewDecoder(resp.Body).Decode(&problem)
	assert.Equal(t, "missing_auth", problem.Type)
}

func TestAuth_APIKey_Invalid(t *testing.T) {
	app := testApp(t, "api-key", "test-secret-key")

	req, _ := http.NewRequest("GET", "/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var problem ProblemDetail
	json.NewDecoder(resp.Body).Decode(&problem)
	assert.Equal(t, "invalid_api_key", problem.Type)
}

func TestAuth_APIKey_InvalidScheme(t *testing.T) {
	app := testApp(t, "api-key", "test-secret-key")

	req, _ := http.NewRequest("GET", "/api/v1/config", nil)
	req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuth_ProbeEndpoints_NoAuth(t *testing.T) {
	app := testApp(t, "api-key", "test-secret-key")

	// Probe endpoints should NOT require auth
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		req, _ := http.NewRequest("GET", path, nil)
		resp, err := app.Test(req, -1)
		require.NoError(t, err, "path: %s", path)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
	}
}

func TestAuth_RoleRequired_Admin(t *testing.T) {
	app := testApp(t, "api-key", "admin-key")

	// PATCH /api/v1/config requires admin role
	body := `{"log_level":"warn"}`
	req, _ := http.NewRequest("PATCH", "/api/v1/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer admin-key")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
