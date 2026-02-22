package mgmt

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/health"
)

// Handlers holds dependencies for HTTP handlers.
type Handlers struct {
	engine    *TaskEngine
	checker   *health.Checker
	logger    zerolog.Logger
	startTime time.Time

	// Runtime config (mutable)
	runtimeConfig *RuntimeConfig
}

// RuntimeConfig holds mutable runtime configuration.
type RuntimeConfig struct {
	Environment    string
	LogLevel       string
	HTTPPort       int
	MgmtListenAddr string
	RateLimitRPS   int
	RateLimitBurst int
	AuthMode       string
	WorkerCount    int
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(engine *TaskEngine, checker *health.Checker, rtCfg *RuntimeConfig, logger zerolog.Logger) *Handlers {
	return &Handlers{
		engine:        engine,
		checker:       checker,
		logger:        logger.With().Str("component", "handlers").Logger(),
		startTime:     time.Now(),
		runtimeConfig: rtCfg,
	}
}

// SubmitTask handles POST /api/v1/tasks.
func (h *Handlers) SubmitTask(c *fiber.Ctx) error {
	var req SubmitTaskRequest
	if err := c.BodyParser(&req); err != nil {
		return problemResponse(c, fiber.StatusBadRequest,
			"invalid_body", "Bad Request",
			"Invalid request body: "+err.Error())
	}

	if req.Type == "" {
		return problemResponse(c, fiber.StatusBadRequest,
			"missing_type", "Bad Request",
			"Task type is required")
	}

	if !IsValidTaskType(req.Type) {
		return problemResponse(c, fiber.StatusBadRequest,
			"invalid_task_type", "Bad Request",
			"Unknown task type: "+req.Type)
	}

	task, err := h.engine.Submit(req)
	if err != nil {
		return problemResponse(c, fiber.StatusServiceUnavailable,
			"queue_full", "Service Unavailable",
			err.Error())
	}

	return c.Status(fiber.StatusAccepted).JSON(TaskResponse{Task: task})
}

// ListTasks handles GET /api/v1/tasks.
func (h *Handlers) ListTasks(c *fiber.Ctx) error {
	q := ListTasksQuery{
		Status:   c.Query("status"),
		Type:     c.Query("type"),
		CallerID: c.Query("caller_id"),
		Limit:    c.QueryInt("limit", 50),
		Offset:   c.QueryInt("offset", 0),
	}

	tasks, total := h.engine.List(q)
	if tasks == nil {
		tasks = []*Task{}
	}

	return c.JSON(TaskListResponse{
		Tasks:  tasks,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	})
}

// GetTask handles GET /api/v1/tasks/:id.
func (h *Handlers) GetTask(c *fiber.Ctx) error {
	id := c.Params("id")
	task, ok := h.engine.Get(id)
	if !ok {
		return problemResponse(c, fiber.StatusNotFound,
			"task_not_found", "Not Found",
			"Task not found: "+id)
	}

	return c.JSON(TaskResponse{Task: task})
}

// CancelTask handles DELETE /api/v1/tasks/:id.
func (h *Handlers) CancelTask(c *fiber.Ctx) error {
	id := c.Params("id")
	task, err := h.engine.Cancel(id)
	if err != nil {
		if task == nil {
			return problemResponse(c, fiber.StatusNotFound,
				"task_not_found", "Not Found",
				err.Error())
		}
		return problemResponse(c, fiber.StatusConflict,
			"invalid_state", "Conflict",
			err.Error())
	}

	return c.JSON(TaskResponse{Task: task})
}

// Chat handles POST /api/v1/chat.
func (h *Handlers) Chat(c *fiber.Ctx) error {
	var req ChatRequest
	if err := c.BodyParser(&req); err != nil {
		return problemResponse(c, fiber.StatusBadRequest,
			"invalid_body", "Bad Request",
			"Invalid request body: "+err.Error())
	}

	if req.Message == "" {
		return problemResponse(c, fiber.StatusBadRequest,
			"missing_message", "Bad Request",
			"Message is required")
	}

	// Submit as a slack.send-message task and return the task ID
	submitReq := SubmitTaskRequest{
		Type:   TaskTypeSlackSendMsg,
		Params: mustMarshal(map[string]string{"message": req.Message, "channel_id": req.ChannelContext}),
	}

	task, err := h.engine.Submit(submitReq)
	if err != nil {
		return problemResponse(c, fiber.StatusServiceUnavailable,
			"queue_full", "Service Unavailable",
			err.Error())
	}

	return c.Status(fiber.StatusAccepted).JSON(ChatResponse{
		Reply:  "Message submitted for processing",
		TaskID: task.ID,
	})
}

// HealthDetail handles GET /api/v1/health.
func (h *Handlers) HealthDetail(c *fiber.Ctx) error {
	results := h.checker.RunAll(c.Context())

	integrations := make(map[string]string, len(results))
	overall := "ok"
	for name, status := range results {
		integrations[name] = string(status)
		if status == health.StatusDown {
			overall = "degraded"
		}
	}

	uptime := time.Since(h.startTime).Round(time.Second).String()

	return c.JSON(HealthDetailResponse{
		Status:       overall,
		Integrations: integrations,
		Uptime:       uptime,
		Version:      "1.0.0",
	})
}

// GetConfig handles GET /api/v1/config.
func (h *Handlers) GetConfig(c *fiber.Ctx) error {
	cfg := h.runtimeConfig
	return c.JSON(ConfigResponse{
		Environment:    cfg.Environment,
		LogLevel:       cfg.LogLevel,
		HTTPPort:       cfg.HTTPPort,
		MgmtListenAddr: cfg.MgmtListenAddr,
		RateLimitRPS:   cfg.RateLimitRPS,
		RateLimitBurst: cfg.RateLimitBurst,
		AuthMode:       cfg.AuthMode,
		WorkerCount:    cfg.WorkerCount,
	})
}

// PatchConfig handles PATCH /api/v1/config.
func (h *Handlers) PatchConfig(c *fiber.Ctx) error {
	var req ConfigPatchRequest
	if err := c.BodyParser(&req); err != nil {
		return problemResponse(c, fiber.StatusBadRequest,
			"invalid_body", "Bad Request",
			"Invalid request body: "+err.Error())
	}

	if req.LogLevel != nil {
		h.runtimeConfig.LogLevel = *req.LogLevel
	}
	if req.RateLimitRPS != nil {
		h.runtimeConfig.RateLimitRPS = *req.RateLimitRPS
	}

	return h.GetConfig(c)
}

// MetricsSummary handles GET /api/v1/metrics/summary.
func (h *Handlers) MetricsSummary(c *fiber.Ctx) error {
	stats := h.engine.Stats()
	return c.JSON(stats)
}

// Liveness handles GET /healthz.
func (h *Handlers) Liveness(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// Readiness handles GET /readyz.
func (h *Handlers) Readiness(c *fiber.Ctx) error {
	ready := h.checker.IsReady(c.Context())
	if !ready {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"status": "not_ready",
		})
	}
	return c.JSON(fiber.Map{"status": "ready"})
}

// mustMarshal marshals v to JSON, panicking on failure (for known-good inputs).
func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mgmt: failed to marshal: " + err.Error())
	}
	return b
}
