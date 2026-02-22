package mgmt

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/p-blackswan/platform-agent/internal/health"
)

// testApp creates a Fiber app with all routes for testing.
func testApp(t *testing.T, authMode string, apiKey string) *fiber.App {
	t.Helper()
	logger := zerolog.Nop()
	checker := health.NewChecker(logger)

	executor := &NoOpExecutor{}
	callbacks := NewCallbackDelivery(5, 1, logger)
	engine := NewTaskEngine(TaskEngineConfig{Workers: 2, QueueSize: 100}, executor, callbacks, logger)
	engine.Start(t.Context())
	t.Cleanup(func() { engine.Stop() })

	rtCfg := &RuntimeConfig{
		Environment:    "test",
		LogLevel:       "debug",
		HTTPPort:       8080,
		MgmtListenAddr: ":8090",
		RateLimitRPS:   100,
		RateLimitBurst: 200,
		AuthMode:       authMode,
		WorkerCount:    2,
	}

	srv := NewServer(ServerConfig{
		ListenAddr: ":0",
		AuthConfig: AuthConfig{
			Mode:   authMode,
			APIKey: apiKey,
		},
		RateLimit: RateLimitConfig{RPS: 100, Burst: 200},
	}, engine, checker, nil, rtCfg, logger)

	return srv.App()
}

func TestServer_HealthzEndpoint(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "ok", body["status"])
}

func TestServer_ReadyzEndpoint(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/readyz", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_SubmitTask(t *testing.T) {
	app := testApp(t, "none", "")

	body := `{"type":"policy.list","params":{"message":"hello"}}`
	req, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	var taskResp TaskResponse
	json.NewDecoder(resp.Body).Decode(&taskResp)
	assert.NotEmpty(t, taskResp.Task.ID)
	assert.Equal(t, "policy.list", taskResp.Task.Type)
	assert.Equal(t, TaskPending, taskResp.Task.Status)
}

func TestServer_SubmitTask_InvalidType(t *testing.T) {
	app := testApp(t, "none", "")

	body := `{"type":"invalid.type","params":{}}`
	req, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var problem ProblemDetail
	json.NewDecoder(resp.Body).Decode(&problem)
	assert.Equal(t, "invalid_task_type", problem.Type)
}

func TestServer_SubmitTask_MissingType(t *testing.T) {
	app := testApp(t, "none", "")

	body := `{"params":{}}`
	req, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_GetTask(t *testing.T) {
	app := testApp(t, "none", "")

	// Submit a task first
	body := `{"type":"jira.get-issue","params":{"key":"PLAT-123"}}`
	req, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	require.NoError(t, err)

	var taskResp TaskResponse
	json.NewDecoder(resp.Body).Decode(&taskResp)
	taskID := taskResp.Task.ID

	// Get the task
	req, _ = http.NewRequest("GET", "/api/v1/tasks/"+taskID, nil)
	resp, err = app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var getResp TaskResponse
	json.NewDecoder(resp.Body).Decode(&getResp)
	assert.Equal(t, taskID, getResp.Task.ID)
}

func TestServer_GetTask_NotFound(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/api/v1/tasks/nonexistent", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_ListTasks(t *testing.T) {
	app := testApp(t, "none", "")

	// Submit two tasks
	for _, taskType := range []string{"policy.list", "jira.get-issue"} {
		body := `{"type":"` + taskType + `","params":{}}`
		req, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	}

	// List all
	req, _ := http.NewRequest("GET", "/api/v1/tasks", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var listResp TaskListResponse
	json.NewDecoder(resp.Body).Decode(&listResp)
	assert.GreaterOrEqual(t, listResp.Total, 2)

	// List with type filter
	req, _ = http.NewRequest("GET", "/api/v1/tasks?type=policy.list", nil)
	resp, err = app.Test(req, -1)
	require.NoError(t, err)

	var filteredResp TaskListResponse
	json.NewDecoder(resp.Body).Decode(&filteredResp)
	for _, task := range filteredResp.Tasks {
		assert.Equal(t, "policy.list", task.Type)
	}
}

func TestServer_CancelTask(t *testing.T) {
	app := testApp(t, "none", "")

	// Submit a task
	body := `{"type":"policy.list","params":{}}`
	req, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	require.NoError(t, err)

	var taskResp TaskResponse
	json.NewDecoder(resp.Body).Decode(&taskResp)
	taskID := taskResp.Task.ID

	// Cancel it
	req, _ = http.NewRequest("DELETE", "/api/v1/tasks/"+taskID, nil)
	resp, err = app.Test(req, -1)
	require.NoError(t, err)
	// Task may already be running/completed since workers are active
	// So we accept either OK or Conflict
	assert.Contains(t, []int{http.StatusOK, http.StatusConflict}, resp.StatusCode)
}

func TestServer_Chat(t *testing.T) {
	app := testApp(t, "none", "")

	body := `{"message":"hello agent"}`
	req, _ := http.NewRequest("POST", "/api/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	var chatResp ChatResponse
	json.NewDecoder(resp.Body).Decode(&chatResp)
	assert.NotEmpty(t, chatResp.TaskID)
}

func TestServer_Chat_EmptyMessage(t *testing.T) {
	app := testApp(t, "none", "")

	body := `{"message":""}`
	req, _ := http.NewRequest("POST", "/api/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_HealthDetail(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/api/v1/health", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var healthResp HealthDetailResponse
	json.NewDecoder(resp.Body).Decode(&healthResp)
	assert.NotEmpty(t, healthResp.Status)
	assert.NotEmpty(t, healthResp.Uptime)
}

func TestServer_GetConfig(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/api/v1/config", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var cfgResp ConfigResponse
	json.NewDecoder(resp.Body).Decode(&cfgResp)
	assert.Equal(t, "test", cfgResp.Environment)
	assert.Equal(t, "debug", cfgResp.LogLevel)
}

func TestServer_PatchConfig(t *testing.T) {
	app := testApp(t, "none", "")

	body := `{"log_level":"warn"}`
	req, _ := http.NewRequest("PATCH", "/api/v1/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var cfgResp ConfigResponse
	json.NewDecoder(resp.Body).Decode(&cfgResp)
	assert.Equal(t, "warn", cfgResp.LogLevel)
}

func TestServer_MetricsSummary(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/api/v1/metrics/summary", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var summary MetricsSummaryResponse
	json.NewDecoder(resp.Body).Decode(&summary)
	assert.NotNil(t, summary.ByStatus)
	assert.NotNil(t, summary.ByType)
}

func TestServer_MetricsEndpoint(t *testing.T) {
	app := testApp(t, "none", "")

	req, _ := http.NewRequest("GET", "/metrics", nil)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(bodyBytes), "metrics")
}
