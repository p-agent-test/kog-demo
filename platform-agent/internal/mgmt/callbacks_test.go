package mgmt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallbackDelivery_Success(t *testing.T) {
	var received atomic.Bool
	var receivedPayload CallbackPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	cd := NewCallbackDelivery(5*time.Second, 2, logger)

	now := time.Now().UTC()
	task := &Task{
		ID:          "task-123",
		Type:        TaskTypePolicyList,
		Status:      TaskCompleted,
		Result:      json.RawMessage(`{"reply":"hello"}`),
		CompletedAt: &now,
	}

	err := cd.Deliver(context.Background(), server.URL, task)
	require.NoError(t, err)
	assert.True(t, received.Load())
	assert.Equal(t, "task-123", receivedPayload.TaskID)
	assert.Equal(t, TaskCompleted, receivedPayload.Status)
}

func TestCallbackDelivery_EmptyURL(t *testing.T) {
	logger := zerolog.Nop()
	cd := NewCallbackDelivery(5*time.Second, 2, logger)

	err := cd.Deliver(context.Background(), "", &Task{ID: "test"})
	assert.NoError(t, err)
}

func TestCallbackDelivery_Retry(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	cd := NewCallbackDelivery(5*time.Second, 3, logger)
	cd.delay = 10 * time.Millisecond // speed up retries for test

	now := time.Now().UTC()
	task := &Task{
		ID:          "task-retry",
		Type:        TaskTypePolicyList,
		Status:      TaskCompleted,
		CompletedAt: &now,
	}

	err := cd.Deliver(context.Background(), server.URL, task)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, int(attempts.Load()), 3)
}

func TestCallbackDelivery_AllRetryFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	cd := NewCallbackDelivery(5*time.Second, 1, logger)
	cd.delay = 10 * time.Millisecond

	task := &Task{
		ID:     "task-fail",
		Type:   TaskTypePolicyList,
		Status: TaskFailed,
		Error:  "something went wrong",
	}

	err := cd.Deliver(context.Background(), server.URL, task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "callback delivery failed")
}

func TestCallbackDelivery_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	cd := NewCallbackDelivery(5*time.Second, 5, logger)
	cd.delay = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	task := &Task{
		ID:     "task-ctx",
		Type:   TaskTypePolicyList,
		Status: TaskFailed,
	}

	err := cd.Deliver(ctx, server.URL, task)
	// First attempt might succeed (with 500) or context might cancel during retry wait
	// Either way, it should error
	assert.Error(t, err)
}

func TestCallbackDelivery_FailedTask(t *testing.T) {
	var receivedPayload CallbackPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	cd := NewCallbackDelivery(5*time.Second, 2, logger)

	task := &Task{
		ID:     "task-err",
		Type:   TaskTypeK8sPodLogs,
		Status: TaskFailed,
		Error:  "pod not found",
	}

	err := cd.Deliver(context.Background(), server.URL, task)
	require.NoError(t, err)
	assert.Equal(t, TaskFailed, receivedPayload.Status)
	assert.Equal(t, "pod not found", receivedPayload.Error)
}
