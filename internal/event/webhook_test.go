package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookSource_Name(t *testing.T) {
	src := event.NewWebhookSource(event.WebhookConfig{})
	assert.Equal(t, event.SourceWebhook, src.Name())
}

func TestWebhookSource_Ack(t *testing.T) {
	src := event.NewWebhookSource(event.WebhookConfig{})
	require.NoError(t, src.Ack(context.Background(), "any-id"))
}

// TestWebhookSource_Subscribe starts a real HTTP server and posts to it.
func TestWebhookSource_Subscribe(t *testing.T) {
	eventCh := make(chan event.Event, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := event.NewWebhookSource(event.WebhookConfig{
		Addr: "127.0.0.1:19877",
		Path: "/hook",
	})

	require.NoError(t, src.Subscribe(ctx, eventCh))

	// POST a JSON body.
	body := `{"type":"alert","data":"test payload"}`
	resp, err := http.Post("http://127.0.0.1:19877/hook", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Wait for event.
	select {
	case ev := <-eventCh:
		assert.Equal(t, event.SourceWebhook, ev.Source)
		assert.Equal(t, "alert", ev.Type)

		var pl event.WebhookPayload
		require.NoError(t, json.Unmarshal(ev.Payload, &pl))
		assert.Equal(t, "alert", pl.Type)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for webhook event")
	}
}

func TestWebhookSource_SecretValidation(t *testing.T) {
	eventCh := make(chan event.Event, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := event.NewWebhookSource(event.WebhookConfig{
		Addr:   "127.0.0.1:19878",
		Path:   "/secure",
		Secret: "s3cr3t",
	})
	require.NoError(t, src.Subscribe(ctx, eventCh))

	// Request without secret → 401.
	resp, err := http.Post("http://127.0.0.1:19878/secure", "application/json", bytes.NewBufferString(`{}`))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Request with correct secret → 202.
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:19878/secure", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", "s3cr3t")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp2.StatusCode)
}

func TestWebhookSource_MethodNotAllowed(t *testing.T) {
	eventCh := make(chan event.Event, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := event.NewWebhookSource(event.WebhookConfig{
		Addr: "127.0.0.1:19879",
		Path: "/wh",
	})
	require.NoError(t, src.Subscribe(ctx, eventCh))

	resp, err := http.Get("http://127.0.0.1:19879/wh")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestWebhookSource_PlainTextBody(t *testing.T) {
	eventCh := make(chan event.Event, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := event.NewWebhookSource(event.WebhookConfig{
		Addr: "127.0.0.1:19880",
		Path: "/text",
	})
	require.NoError(t, src.Subscribe(ctx, eventCh))

	// POST plain text (non-JSON) — should be wrapped.
	resp, err := http.Post("http://127.0.0.1:19880/text", "text/plain", bytes.NewBufferString("hello world"))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	select {
	case ev := <-eventCh:
		assert.Equal(t, event.SourceWebhook, ev.Source)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}
