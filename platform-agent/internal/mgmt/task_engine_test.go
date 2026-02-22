package mgmt

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) *TaskEngine {
	t.Helper()
	logger := zerolog.Nop()
	executor := &NoOpExecutor{}
	callbacks := NewCallbackDelivery(5, 1, logger)
	engine := NewTaskEngine(TaskEngineConfig{Workers: 2, QueueSize: 100}, executor, callbacks, logger)
	engine.Start(t.Context())
	t.Cleanup(func() { engine.Stop() })
	return engine
}

func TestTaskEngine_Submit(t *testing.T) {
	engine := newTestEngine(t)

	task, err := engine.Submit(SubmitTaskRequest{
		Type:     TaskTypePolicyList,
		Params:   json.RawMessage(`{"message":"hello"}`),
		CallerID: "test-user",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, task.ID)
	assert.Equal(t, TaskTypePolicyList, task.Type)
	assert.Equal(t, "test-user", task.CallerID)
	assert.False(t, task.CreatedAt.IsZero())
}

func TestTaskEngine_Submit_InvalidType(t *testing.T) {
	engine := newTestEngine(t)

	_, err := engine.Submit(SubmitTaskRequest{
		Type:   "invalid.type",
		Params: json.RawMessage(`{}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown task type")
}

func TestTaskEngine_Get(t *testing.T) {
	engine := newTestEngine(t)

	task, err := engine.Submit(SubmitTaskRequest{
		Type:   TaskTypeJiraGetIssue,
		Params: json.RawMessage(`{"key":"PLAT-123"}`),
	})
	require.NoError(t, err)

	found, ok := engine.Get(task.ID)
	assert.True(t, ok)
	assert.Equal(t, task.ID, found.ID)
}

func TestTaskEngine_Get_NotFound(t *testing.T) {
	engine := newTestEngine(t)

	_, ok := engine.Get("nonexistent")
	assert.False(t, ok)
}

func TestTaskEngine_Cancel_Pending(t *testing.T) {
	logger := zerolog.Nop()
	executor := &NoOpExecutor{}
	callbacks := NewCallbackDelivery(5, 1, logger)
	// Use a full queue with no workers to keep tasks pending
	engine := NewTaskEngine(TaskEngineConfig{Workers: 0, QueueSize: 100}, executor, callbacks, logger)
	// Don't start workers — tasks stay pending

	task, err := engine.Submit(SubmitTaskRequest{
		Type:   TaskTypePolicyList,
		Params: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Task should be pending
	assert.Equal(t, TaskPending, task.Status)

	cancelled, err := engine.Cancel(task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskCancelled, cancelled.Status)
	assert.NotNil(t, cancelled.CompletedAt)
}

func TestTaskEngine_Cancel_NotFound(t *testing.T) {
	engine := newTestEngine(t)

	_, err := engine.Cancel("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task not found")
}

func TestTaskEngine_List(t *testing.T) {
	engine := newTestEngine(t)

	// Submit several tasks
	for _, tt := range []string{TaskTypePolicyList, TaskTypeJiraGetIssue, TaskTypeK8sPodLogs} {
		_, err := engine.Submit(SubmitTaskRequest{
			Type:     tt,
			Params:   json.RawMessage(`{}`),
			CallerID: "list-test",
		})
		require.NoError(t, err)
	}

	// List all
	tasks, total := engine.List(ListTasksQuery{})
	assert.Equal(t, 3, total)
	assert.Len(t, tasks, 3)

	// Filter by type
	tasks, total = engine.List(ListTasksQuery{Type: TaskTypePolicyList})
	assert.Equal(t, 1, total)

	// Filter by caller
	tasks, total = engine.List(ListTasksQuery{CallerID: "list-test"})
	assert.Equal(t, 3, total)
	assert.LessOrEqual(t, len(tasks), 50)
}

func TestTaskEngine_List_Pagination(t *testing.T) {
	engine := newTestEngine(t)

	for i := 0; i < 10; i++ {
		_, err := engine.Submit(SubmitTaskRequest{
			Type:   TaskTypePolicyList,
			Params: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	tasks, total := engine.List(ListTasksQuery{Limit: 3, Offset: 0})
	assert.Equal(t, 10, total)
	assert.Len(t, tasks, 3)

	tasks2, _ := engine.List(ListTasksQuery{Limit: 3, Offset: 3})
	assert.Len(t, tasks2, 3)

	// No overlap
	assert.NotEqual(t, tasks[0].ID, tasks2[0].ID)
}

func TestTaskEngine_Execution(t *testing.T) {
	engine := newTestEngine(t)

	task, err := engine.Submit(SubmitTaskRequest{
		Type:   TaskTypePolicyList,
		Params: json.RawMessage(`{"message":"test"}`),
	})
	require.NoError(t, err)

	// Wait for task to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := engine.Stats()
		if stats.ByStatus[string(TaskCompleted)] > 0 {
			found, ok := engine.Get(task.ID)
			assert.True(t, ok)
			snap := found.Snapshot()
			assert.Equal(t, TaskCompleted, snap.Status)
			assert.NotNil(t, snap.Result)
			assert.NotNil(t, snap.StartedAt)
			assert.NotNil(t, snap.CompletedAt)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatal("task did not complete in time")
}

func TestTaskEngine_Stats(t *testing.T) {
	engine := newTestEngine(t)

	for i := 0; i < 5; i++ {
		_, err := engine.Submit(SubmitTaskRequest{
			Type:   TaskTypePolicyList,
			Params: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// Wait for tasks to complete by polling stats
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := engine.Stats()
		if stats.ByStatus[string(TaskCompleted)] >= 5 {
			assert.Equal(t, 5, stats.TotalTasks)
			assert.NotNil(t, stats.ByStatus)
			assert.NotNil(t, stats.ByType)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Fallback — check what we have
	stats := engine.Stats()
	assert.Equal(t, 5, stats.TotalTasks)
	assert.NotNil(t, stats.ByStatus)
	assert.NotNil(t, stats.ByType)
}

func TestNoOpExecutor(t *testing.T) {
	executor := &NoOpExecutor{}
	result, err := executor.Execute(context.Background(), TaskTypePolicyList, json.RawMessage(`{"message":"hi"}`))
	require.NoError(t, err)
	assert.NotNil(t, result)

	var parsed map[string]string
	err = json.Unmarshal(result, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "completed", parsed["status"])
}

func TestTaskEngine_AllTaskTypes(t *testing.T) {
	engine := newTestEngine(t)

	for taskType := range ValidTaskTypes {
		task, err := engine.Submit(SubmitTaskRequest{
			Type:   taskType,
			Params: json.RawMessage(`{}`),
		})
		require.NoError(t, err, "task type: %s", taskType)
		assert.NotEmpty(t, task.ID)
	}
}
