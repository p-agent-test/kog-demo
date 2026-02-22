// Package mgmt provides the Management API for the platform agent.
package mgmt

import (
	"encoding/json"
	"sync"
	"time"
)

// --- Task types ---

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	TaskPending          TaskStatus = "pending"
	TaskRunning          TaskStatus = "running"
	TaskCompleted        TaskStatus = "completed"
	TaskFailed           TaskStatus = "failed"
	TaskCancelled        TaskStatus = "cancelled"
	TaskAwaitingApproval TaskStatus = "awaiting_approval"
)

// Task represents an async unit of work submitted to the agent.
type Task struct {
	mu              sync.RWMutex    `json:"-"`
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Status          TaskStatus      `json:"status"`
	Params          json.RawMessage `json:"params"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	CallerID        string          `json:"caller_id"`
	CallbackURL     string          `json:"callback_url,omitempty"`
	ResponseChannel string          `json:"response_channel,omitempty"`
	ResponseThread  string          `json:"response_thread,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	TTL             time.Duration   `json:"-"`
}

// Lock locks the task for writing.
func (t *Task) Lock()   { t.mu.Lock() }
// Unlock unlocks the task after writing.
func (t *Task) Unlock() { t.mu.Unlock() }
// RLock locks the task for reading.
func (t *Task) RLock()  { t.mu.RLock() }
// RUnlock unlocks the task after reading.
func (t *Task) RUnlock() { t.mu.RUnlock() }

// Snapshot returns a copy of the task that is safe to read without holding locks.
func (t *Task) Snapshot() Task {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Task{
		ID:              t.ID,
		Type:            t.Type,
		Status:          t.Status,
		Params:          t.Params,
		Result:          t.Result,
		Error:           t.Error,
		CallerID:        t.CallerID,
		CallbackURL:     t.CallbackURL,
		ResponseChannel: t.ResponseChannel,
		ResponseThread:  t.ResponseThread,
		CreatedAt:       t.CreatedAt,
		StartedAt:       t.StartedAt,
		CompletedAt:     t.CompletedAt,
		TTL:             t.TTL,
	}
}

// --- Request DTOs ---

// SubmitTaskRequest is the payload for POST /api/v1/tasks.
type SubmitTaskRequest struct {
	Type            string          `json:"type"`
	Params          json.RawMessage `json:"params"`
	CallerID        string          `json:"caller_id,omitempty"`
	CallbackURL     string          `json:"callback_url,omitempty"`
	ResponseChannel string          `json:"response_channel,omitempty"`
	ResponseThread  string          `json:"response_thread,omitempty"`
	TTLSeconds      int             `json:"ttl_seconds,omitempty"`
}

// ChatRequest is the payload for POST /api/v1/chat.
type ChatRequest struct {
	Message        string `json:"message"`
	ChannelContext string `json:"channel_context,omitempty"`
}

// ConfigPatchRequest is the payload for PATCH /api/v1/config.
type ConfigPatchRequest struct {
	LogLevel    *string `json:"log_level,omitempty"`
	RateLimitRPS *int   `json:"rate_limit_rps,omitempty"`
}

// ListTasksQuery holds query parameters for GET /api/v1/tasks.
type ListTasksQuery struct {
	Status   string `query:"status"`
	Type     string `query:"type"`
	CallerID string `query:"caller_id"`
	Limit    int    `query:"limit"`
	Offset   int    `query:"offset"`
}

// --- Response DTOs ---

// TaskResponse wraps a Task for API responses.
type TaskResponse struct {
	Task *Task `json:"task"`
}

// TaskListResponse wraps a list of tasks.
type TaskListResponse struct {
	Tasks  []*Task `json:"tasks"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// ChatResponse is the response for POST /api/v1/chat.
type ChatResponse struct {
	Reply   string `json:"reply"`
	TaskID  string `json:"task_id,omitempty"`
}

// HealthDetailResponse is the response for GET /api/v1/health.
type HealthDetailResponse struct {
	Status       string            `json:"status"`
	Integrations map[string]string `json:"integrations"`
	Uptime       string            `json:"uptime"`
	Version      string            `json:"version"`
}

// ConfigResponse is the response for GET /api/v1/config.
type ConfigResponse struct {
	Environment    string `json:"environment"`
	LogLevel       string `json:"log_level"`
	HTTPPort       int    `json:"http_port"`
	MgmtListenAddr string `json:"mgmt_listen_addr"`
	RateLimitRPS   int    `json:"rate_limit_rps"`
	RateLimitBurst int    `json:"rate_limit_burst"`
	AuthMode       string `json:"auth_mode"`
	WorkerCount    int    `json:"worker_count"`
}

// MetricsSummaryResponse is the response for GET /api/v1/metrics/summary.
type MetricsSummaryResponse struct {
	TotalTasks    int            `json:"total_tasks"`
	ByStatus      map[string]int `json:"by_status"`
	ByType        map[string]int `json:"by_type"`
	AvgDurationMs int64          `json:"avg_duration_ms"`
}

// ProblemDetail follows RFC 7807 for error responses.
type ProblemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}
