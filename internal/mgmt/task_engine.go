package mgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/store"
)

type contextKey string

// TaskIDContextKey is the context key for passing task ID to executors.
const TaskIDContextKey contextKey = "task_id"

// ProjectIDContextKey is the context key for passing project ID to executors.
const ProjectIDContextKey contextKey = "project_id"

// SessionKeyContextKey is the context key for passing session key to executors.
const SessionKeyContextKey contextKey = "session_key"

// ProjectIDFromContext extracts the project ID from context.
func ProjectIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ProjectIDContextKey).(string); ok {
		return v
	}
	return ""
}

// SessionKeyFromContext extracts the session key from context.
func SessionKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(SessionKeyContextKey).(string); ok {
		return v
	}
	return ""
}

// TaskIDFromContext extracts the task ID from context.
func TaskIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(TaskIDContextKey).(string); ok {
		return v
	}
	return ""
}

// TaskExecutor defines the interface for executing tasks.
// This allows the task engine to remain decoupled from the agent implementation.
type TaskExecutor interface {
	Execute(ctx context.Context, taskType string, params json.RawMessage) (json.RawMessage, error)
}

// TaskCompletionNotifier is called after a task completes (success or failure)
// when the task has a ResponseChannel set. Used to post results back to Slack.
type TaskCompletionNotifier interface {
	NotifyTaskCompletion(channel, threadTS, taskID, taskType string, status TaskStatus, result json.RawMessage, taskErr string)
}

// TaskEngine manages the lifecycle of async tasks.
type TaskEngine struct {
	tasks     sync.Map // id → *Task
	taskList  []*Task  // ordered list for iteration
	listMu    sync.RWMutex
	queue     chan *Task
	workers   int
	executor  TaskExecutor
	callbacks *CallbackDelivery
	notifier  TaskCompletionNotifier
	dataStore *store.Store // optional SQLite backend
	logger    zerolog.Logger
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   atomic.Bool
	taskMu    sync.Mutex // protects individual task field mutations
}

// SetNotifier sets the completion notifier (for posting results to Slack).
func (te *TaskEngine) SetNotifier(n TaskCompletionNotifier) {
	te.notifier = n
}

// SetStore sets the optional SQLite backend for task persistence.
func (te *TaskEngine) SetStore(ds *store.Store) {
	te.dataStore = ds
}

// TaskEngineConfig holds configuration for the task engine.
type TaskEngineConfig struct {
	Workers   int
	QueueSize int
}

// NewTaskEngine creates a new task engine.
func NewTaskEngine(cfg TaskEngineConfig, executor TaskExecutor, callbacks *CallbackDelivery, logger zerolog.Logger) *TaskEngine {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}

	return &TaskEngine{
		queue:     make(chan *Task, cfg.QueueSize),
		workers:   cfg.Workers,
		executor:  executor,
		callbacks: callbacks,
		logger:    logger.With().Str("component", "task_engine").Logger(),
	}
}

// Start launches worker goroutines.
func (te *TaskEngine) Start(ctx context.Context) {
	if te.running.Swap(true) {
		return // already running
	}

	ctx, te.cancel = context.WithCancel(ctx)

	for i := 0; i < te.workers; i++ {
		te.wg.Add(1)
		go te.worker(ctx, i)
	}

	te.logger.Info().Int("workers", te.workers).Msg("task engine started")
}

// Stop gracefully shuts down the task engine.
func (te *TaskEngine) Stop() {
	if !te.running.Swap(false) {
		return
	}
	if te.cancel != nil {
		te.cancel()
	}
	te.wg.Wait()
	te.logger.Info().Msg("task engine stopped")
}

// Submit creates a new task and enqueues it.
func (te *TaskEngine) Submit(req SubmitTaskRequest) (*Task, error) {
	if !IsValidTaskType(req.Type) {
		return nil, fmt.Errorf("unknown task type: %s", req.Type)
	}

	task := &Task{
		ID:              uuid.New().String(),
		Type:            req.Type,
		Status:          TaskPending,
		Params:          req.Params,
		CallerID:        req.CallerID,
		CallbackURL:     req.CallbackURL,
		ResponseChannel: req.ResponseChannel,
		ResponseThread:  req.ResponseThread,
		ProjectID:       req.ProjectID,
		SessionKey:      req.SessionKey,
		CreatedAt:       time.Now().UTC(),
	}

	if req.TTLSeconds > 0 {
		task.TTL = time.Duration(req.TTLSeconds) * time.Second
	}

	te.tasks.Store(task.ID, task)
	te.listMu.Lock()
	te.taskList = append(te.taskList, task)
	te.listMu.Unlock()

	// Persist to store if available (graceful degradation)
	if te.dataStore != nil {
		storeTask := &store.Task{
			ID:              task.ID,
			Status:          string(task.Status),
			Command:         task.Type,
			Params:          string(task.Params),
			CallerID:        task.CallerID,
			ResponseChannel: task.ResponseChannel,
			ResponseThread:  task.ResponseThread,
			CreatedAt:       task.CreatedAt.UnixMilli(),
			UpdatedAt:       task.CreatedAt.UnixMilli(),
		}
		if err := te.dataStore.SaveTask(storeTask); err != nil {
			te.logger.Warn().Err(err).Str("task_id", task.ID).Msg("failed to persist task to store")
		}
	}

	// Take snapshot before enqueueing (worker may modify task immediately)
	snap := task.Snapshot()

	select {
	case te.queue <- task:
		logEvt := te.logger.Info().
			Str("task_id", task.ID).
			Str("type", task.Type)
		if task.ResponseChannel != "" {
			logEvt = logEvt.Str("response_channel", task.ResponseChannel).Str("response_thread", task.ResponseThread)
		}
		logEvt.Msg("task enqueued")
	default:
		task.Lock()
		task.Status = TaskFailed
		task.Error = "task queue is full"
		now := time.Now().UTC()
		task.CompletedAt = &now
		task.Unlock()
		snap = task.Snapshot()

		// Update store on failure
		if te.dataStore != nil {
			_ = te.dataStore.UpdateTaskStatus(task.ID, string(TaskFailed))
		}

		return &snap, fmt.Errorf("task queue is full")
	}

	return &snap, nil
}

// Get retrieves a task by ID. Returns a snapshot (copy) safe for concurrent use.
func (te *TaskEngine) Get(id string) (*Task, bool) {
	val, ok := te.tasks.Load(id)
	if !ok {
		return nil, false
	}
	t := val.(*Task)
	snap := t.Snapshot()
	return &snap, true
}

// Requeue re-queues a task that was awaiting approval.
func (te *TaskEngine) Requeue(id string) error {
	val, ok := te.tasks.Load(id)
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	task := val.(*Task)
	task.Lock()
	if task.Status != TaskAwaitingApproval {
		status := task.Status
		task.Unlock()
		return fmt.Errorf("task %s is in status %s, only awaiting_approval tasks can be requeued", id, status)
	}

	task.Status = TaskPending
	task.Error = ""
	task.CompletedAt = nil
	task.Unlock()

	select {
	case te.queue <- task:
		te.logger.Info().Str("task_id", id).Msg("task requeued after approval")
		return nil
	default:
		task.Lock()
		task.Status = TaskFailed
		task.Error = "task queue is full (requeue)"
		now := time.Now().UTC()
		task.CompletedAt = &now
		task.Unlock()
		return fmt.Errorf("task queue is full")
	}
}

// Cancel cancels a pending task.
func (te *TaskEngine) Cancel(id string) (*Task, error) {
	val, ok := te.tasks.Load(id)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	task := val.(*Task)
	task.Lock()
	if task.Status != TaskPending {
		status := task.Status
		task.Unlock()
		snap := task.Snapshot()
		return &snap, fmt.Errorf("task %s is in status %s, only pending tasks can be cancelled", id, status)
	}

	task.Status = TaskCancelled
	now := time.Now().UTC()
	task.CompletedAt = &now
	task.Unlock()

	te.logger.Info().Str("task_id", id).Msg("task cancelled")
	snap := task.Snapshot()
	return &snap, nil
}

// List returns tasks matching the given filters. Returns snapshots.
func (te *TaskEngine) List(q ListTasksQuery) ([]*Task, int) {
	te.listMu.RLock()
	defer te.listMu.RUnlock()

	// Apply filters
	var filtered []*Task
	for _, t := range te.taskList {
		t.RLock()
		status := t.Status
		taskType := t.Type
		callerID := t.CallerID
		t.RUnlock()

		if q.Status != "" && string(status) != q.Status {
			continue
		}
		if q.Type != "" && taskType != q.Type {
			continue
		}
		if q.CallerID != "" && callerID != q.CallerID {
			continue
		}
		filtered = append(filtered, t)
	}

	total := len(filtered)

	// Apply pagination — return in reverse order (newest first)
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	// Reverse to show newest first
	reversed := make([]*Task, len(filtered))
	for i, t := range filtered {
		reversed[len(filtered)-1-i] = t
	}

	if offset >= len(reversed) {
		return nil, total
	}

	end := offset + limit
	if end > len(reversed) {
		end = len(reversed)
	}

	// Return snapshots
	result := make([]*Task, end-offset)
	for i, t := range reversed[offset:end] {
		snap := t.Snapshot()
		result[i] = &snap
	}

	return result, total
}

// Stats returns summary statistics.
func (te *TaskEngine) Stats() MetricsSummaryResponse {
	te.listMu.RLock()
	defer te.listMu.RUnlock()

	resp := MetricsSummaryResponse{
		TotalTasks: len(te.taskList),
		ByStatus:   make(map[string]int),
		ByType:     make(map[string]int),
	}

	var totalDuration int64
	var completedCount int64

	for _, t := range te.taskList {
		t.RLock()
		resp.ByStatus[string(t.Status)]++
		resp.ByType[t.Type]++

		if t.Status == TaskCompleted && t.StartedAt != nil && t.CompletedAt != nil {
			totalDuration += t.CompletedAt.Sub(*t.StartedAt).Milliseconds()
			completedCount++
		}
		t.RUnlock()
	}

	if completedCount > 0 {
		resp.AvgDurationMs = totalDuration / completedCount
	}

	return resp
}

func (te *TaskEngine) worker(ctx context.Context, id int) {
	defer te.wg.Done()
	log := te.logger.With().Int("worker", id).Logger()
	log.Debug().Msg("worker started")

	for {
		select {
		case <-ctx.Done():
			log.Debug().Msg("worker stopping")
			return
		case task, ok := <-te.queue:
			if !ok {
				return
			}
			te.executeTask(ctx, task, log)
		}
	}
}

// NoOpExecutor is a task executor that returns a placeholder result.
// Used when no real agent is connected (e.g., mgmt-only mode).
type NoOpExecutor struct{}

// Execute returns a placeholder result.
func (n *NoOpExecutor) Execute(_ context.Context, taskType string, params json.RawMessage) (json.RawMessage, error) {
	result := map[string]string{
		"status":  "completed",
		"message": "Task type " + taskType + " executed (no-op)",
	}
	b, _ := json.Marshal(result)
	return b, nil
}

func (te *TaskEngine) executeTask(ctx context.Context, task *Task, log zerolog.Logger) {
	// Skip cancelled tasks
	task.RLock()
	if task.Status == TaskCancelled {
		task.RUnlock()
		return
	}
	task.RUnlock()

	now := time.Now().UTC()
	task.Lock()
	task.Status = TaskRunning
	task.StartedAt = &now
	task.Unlock()

	// Update store status if available
	if te.dataStore != nil {
		_ = te.dataStore.UpdateTaskStatus(task.ID, string(TaskRunning))
	}

	log.Info().
		Str("task_id", task.ID).
		Str("type", task.Type).
		Msg("executing task")

	// Apply TTL if set
	taskCtx := ctx
	var taskCancel context.CancelFunc
	if task.TTL > 0 {
		taskCtx, taskCancel = context.WithTimeout(ctx, task.TTL)
	} else {
		taskCtx, taskCancel = context.WithTimeout(ctx, 5*time.Minute)
	}
	defer taskCancel()

	taskCtx = context.WithValue(taskCtx, TaskIDContextKey, task.ID)
	if task.SessionKey != "" {
		taskCtx = context.WithValue(taskCtx, SessionKeyContextKey, task.SessionKey)
	}
	if task.ProjectID != "" {
		taskCtx = context.WithValue(taskCtx, ProjectIDContextKey, task.ProjectID)
	}
	result, err := te.executor.Execute(taskCtx, task.Type, task.Params)
	completed := time.Now().UTC()

	task.Lock()

	if err != nil && strings.HasPrefix(err.Error(), "awaiting_approval:") {
		// Task is waiting for human approval — don't mark as failed
		task.Status = TaskAwaitingApproval
		task.Error = err.Error()
		task.CompletedAt = nil // not completed yet
		task.Unlock()
		log.Info().
			Str("task_id", task.ID).
			Msg("task awaiting approval")

		// Update store status
		if te.dataStore != nil {
			_ = te.dataStore.UpdateTaskStatus(task.ID, string(TaskAwaitingApproval))
		}
	} else if err != nil {
		task.CompletedAt = &completed
		task.Status = TaskFailed
		task.Error = err.Error()
		task.Unlock()
		log.Error().Err(err).
			Str("task_id", task.ID).
			Msg("task failed")

		// Update store with completion
		if te.dataStore != nil {
			_ = te.dataStore.CompleteTask(task.ID, "", task.Error)
		}
	} else {
		task.CompletedAt = &completed
		task.Status = TaskCompleted
		task.Result = result
		task.Unlock()
		log.Info().
			Str("task_id", task.ID).
			Msg("task completed")

		// Update store with completion
		if te.dataStore != nil {
			resultStr := ""
			if result != nil {
				resultStr = string(result)
			}
			_ = te.dataStore.CompleteTask(task.ID, resultStr, "")
		}
	}

	// Read final state for notifications
	task.RLock()
	callbackURL := task.CallbackURL
	taskID := task.ID
	taskType := task.Type
	taskStatus := task.Status
	taskResult := task.Result
	taskError := task.Error
	respChannel := task.ResponseChannel
	respThread := task.ResponseThread
	task.RUnlock()

	// Deliver callback
	if te.callbacks != nil && callbackURL != "" {
		snap := task.Snapshot()
		go func() {
			cbCtx, cbCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cbCancel()
			if err := te.callbacks.Deliver(cbCtx, callbackURL, &snap); err != nil {
				log.Error().Err(err).
					Str("task_id", taskID).
					Str("callback_url", callbackURL).
					Msg("callback delivery failed")
			}
		}()
	}

	// Notify response channel (Slack thread) if set and task is terminal
	if respChannel != "" {
		log.Info().
			Str("task_id", taskID).
			Str("response_channel", respChannel).
			Str("response_thread", respThread).
			Str("status", string(taskStatus)).
			Bool("has_notifier", te.notifier != nil).
			Msg("response channel routing check")
	}
	if te.notifier != nil && respChannel != "" && (taskStatus == TaskCompleted || taskStatus == TaskFailed) {
		go te.notifier.NotifyTaskCompletion(respChannel, respThread, taskID, taskType, taskStatus, taskResult, taskError)
	}
}
