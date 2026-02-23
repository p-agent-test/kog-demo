package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*Store, string) {
	dbPath := "/tmp/test-" + time.Now().Format("20060102150405") + ".db"
	logger := zerolog.New(os.Stderr)
	store, err := New(dbPath, logger)
	require.NoError(t, err)
	return store, dbPath
}

func cleanupStore(t *testing.T, store *Store, dbPath string) {
	if store != nil {
		store.Close()
	}
	os.Remove(dbPath)
}

func TestNew_CreatesDB(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Verify tables exist
	tables := []string{
		"tasks", "pending_approvals", "session_contexts",
		"thread_sessions", "dead_letters", "audit_log", "meta",
	}

	for _, table := range tables {
		var count int
		err := store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "table %s should exist", table)
	}

	// Verify indices exist
	var idxCount int
	err := store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%'").Scan(&idxCount)
	require.NoError(t, err)
	assert.Greater(t, idxCount, 0, "indices should be created")
}

func TestTask_CRUD(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Create
	task := &Task{
		ID:        "task-1",
		Status:    "pending",
		Command:   "test",
		Params:    `{"key":"value"}`,
		CallerID:  "user-1",
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}

	err := store.SaveTask(task)
	require.NoError(t, err)

	// Read
	retrieved, err := store.GetTask("task-1")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, task.ID, retrieved.ID)
	assert.Equal(t, task.Status, retrieved.Status)
	assert.Equal(t, task.Command, retrieved.Command)

	// Update status
	err = store.UpdateTaskStatus("task-1", "running")
	require.NoError(t, err)

	updated, err := store.GetTask("task-1")
	require.NoError(t, err)
	assert.Equal(t, "running", updated.Status)

	// Complete
	err = store.CompleteTask("task-1", `{"result":"success"}`, "")
	require.NoError(t, err)

	completed, err := store.GetTask("task-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.Status)
	assert.Equal(t, `{"result":"success"}`, completed.Result)
	assert.Greater(t, completed.CompletedAt, int64(0))

	// List
	tasks, err := store.ListTasks(TaskFilter{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(tasks), 1)
}

func TestTask_FailStuck(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Create running tasks
	for i := 0; i < 3; i++ {
		task := &Task{
			ID:        "stuck-" + string(rune(i)),
			Status:    "running",
			Command:   "test",
			Params:    "{}",
			CreatedAt: time.Now().UnixMilli(),
			UpdatedAt: time.Now().UnixMilli(),
		}
		store.SaveTask(task)
	}

	// Fail stuck tasks
	count, err := store.FailStuckTasks()
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// Verify all are now failed
	tasks, err := store.ListTasks(TaskFilter{Status: "failed"})
	require.NoError(t, err)
	assert.Equal(t, 3, len(tasks))
}

func TestApproval_CRUD(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Create
	approval := &PendingApproval{
		RequestID:  "req-1",
		TaskID:     "task-1",
		CallerID:   "user-1",
		Permission: "admin",
		Action:     "delete",
		Resource:   "namespace/pod",
		ChannelID:  "ch-1",
		ThreadTS:   "ts-1",
		CreatedAt:  time.Now().UnixMilli(),
	}

	err := store.SaveApproval(approval)
	require.NoError(t, err)

	// Read
	retrieved, err := store.GetApproval("req-1")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, approval.RequestID, retrieved.RequestID)
	assert.Equal(t, approval.Permission, retrieved.Permission)

	// Delete
	err = store.DeleteApproval("req-1")
	require.NoError(t, err)

	// Verify deleted
	deleted, err := store.GetApproval("req-1")
	require.NoError(t, err)
	assert.Nil(t, deleted)

	// List expired
	store.SaveApproval(&PendingApproval{
		RequestID: "req-2",
		TaskID:    "task-2",
		CallerID:  "user-2",
		Permission: "read",
		Action:    "get",
		Resource:  "configmap",
		CreatedAt: time.Now().UnixMilli() - 2000, // 2 seconds ago
	})

	expired, err := store.ListExpiredApprovals(1000) // 1 second
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(expired), 1)
}

func TestSessionContext_CRUD(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Create
	sc := &SessionContext{
		SessionID: "sess-1",
		Channel:   "ch-1",
		ThreadTS:  "ts-1",
		UserID:    "user-1",
		CreatedAt: time.Now().UnixMilli(),
		LastUsed:  time.Now().UnixMilli(),
	}

	err := store.SaveSessionContext(sc)
	require.NoError(t, err)

	// Read
	retrieved, err := store.GetSessionContext("sess-1")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, sc.SessionID, retrieved.SessionID)

	// Get by thread
	byThread, err := store.GetSessionContextByThread("ch-1", "ts-1")
	require.NoError(t, err)
	assert.NotNil(t, byThread)
	assert.Equal(t, "sess-1", byThread.SessionID)

	// Touch
	time.Sleep(10 * time.Millisecond)
	err = store.TouchSessionContext("sess-1")
	require.NoError(t, err)

	touched, err := store.GetSessionContext("sess-1")
	require.NoError(t, err)
	assert.Greater(t, touched.LastUsed, sc.LastUsed)
}

func TestThreadSession_CRUD(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Create
	ts := &ThreadSession{
		Channel:       "ch-1",
		ThreadTS:      "ts-1",
		SessionKey:    "key-1",
		CreatedAt:     time.Now().UnixMilli(),
		LastMessageAt: time.Now().UnixMilli(),
	}

	err := store.SaveThreadSession(ts)
	require.NoError(t, err)

	// Read
	retrieved, err := store.GetThreadSession("ch-1", "ts-1")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, ts.SessionKey, retrieved.SessionKey)

	// Touch
	time.Sleep(10 * time.Millisecond)
	err = store.TouchThreadSession("ch-1", "ts-1")
	require.NoError(t, err)

	touched, err := store.GetThreadSession("ch-1", "ts-1")
	require.NoError(t, err)
	assert.Greater(t, touched.LastMessageAt, ts.LastMessageAt)
}

func TestDeadLetter_CRUD(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Create
	dl := &DeadLetter{
		ID:            "dl-1",
		TargetChannel: "ch-1",
		TargetThread:  "ts-1",
		Message:       "hello",
		Error:         "network error",
		CreatedAt:     time.Now().UnixMilli(),
		RetryCount:    0,
		NextRetryAt:   time.Now().UnixMilli() + 1000, // retry in 1 second
		ResolvedAt:    0,
	}

	err := store.SaveDeadLetter(dl)
	require.NoError(t, err)

	// List retryable (none yet)
	retryable, err := store.ListRetryable(10)
	require.NoError(t, err)
	assert.Equal(t, 0, len(retryable))

	// Increment retry
	err = store.IncrementRetry("dl-1", time.Now().UnixMilli()-1000) // set to past
	require.NoError(t, err)

	// List retryable (should have 1)
	retryable, err = store.ListRetryable(10)
	require.NoError(t, err)
	assert.Equal(t, 1, len(retryable))

	// Resolve
	err = store.ResolveDeadLetter("dl-1")
	require.NoError(t, err)

	// Verify resolved
	retryable, err = store.ListRetryable(10)
	require.NoError(t, err)
	assert.Equal(t, 0, len(retryable))
}

func TestRetention(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	now := time.Now().UnixMilli()

	// Insert old completed task (8 days ago)
	oldTask := &Task{
		ID:          "old-task",
		Status:      "completed",
		Command:     "test",
		Params:      "{}",
		CreatedAt:   now - (8 * 24 * 60 * 60 * 1000),
		UpdatedAt:   now - (8 * 24 * 60 * 60 * 1000),
		CompletedAt: now - (8 * 24 * 60 * 60 * 1000),
	}
	store.SaveTask(oldTask)

	// Insert old approval (2 hours ago)
	oldApproval := &PendingApproval{
		RequestID: "old-req",
		TaskID:    "old-task",
		CallerID:  "user-1",
		Permission: "read",
		Action:    "get",
		Resource:  "pod",
		CreatedAt: now - (2 * 60 * 60 * 1000),
	}
	store.SaveApproval(oldApproval)

	// Run retention
	err := store.RunRetention(context.Background())
	require.NoError(t, err)

	// Verify old task is gone
	task, err := store.GetTask("old-task")
	require.NoError(t, err)
	assert.Nil(t, task)

	// Verify old approval is gone
	approval, err := store.GetApproval("old-req")
	require.NoError(t, err)
	assert.Nil(t, approval)
}

func TestDBSize(t *testing.T) {
	store, dbPath := newTestStore(t)
	defer cleanupStore(t, store, dbPath)

	// Add some data
	for i := 0; i < 10; i++ {
		task := &Task{
			ID:        "task-" + string(rune(i)),
			Status:    "pending",
			Command:   "test",
			Params:    `{"data":"value"}`,
			CreatedAt: time.Now().UnixMilli(),
			UpdatedAt: time.Now().UnixMilli(),
		}
		store.SaveTask(task)
	}

	// Get DB size
	size, err := store.DBSizeBytes()
	require.NoError(t, err)
	assert.Greater(t, size, int64(0))
}
