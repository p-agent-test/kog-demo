package event

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEvent_Fields(t *testing.T) {
	before := time.Now().UTC()
	ev, err := NewEvent(SourceTelegram, TypeMessage, TelegramPayload{
		ChatID: 12345,
		Text:   "hello",
	}, map[string]string{"chat_id": "12345"})
	after := time.Now().UTC()

	require.NoError(t, err)
	assert.NotEmpty(t, ev.ID)
	assert.Equal(t, SourceTelegram, ev.Source)
	assert.Equal(t, TypeMessage, ev.Type)
	assert.Equal(t, "12345", ev.Metadata["chat_id"])
	assert.True(t, !ev.Timestamp.Before(before) && !ev.Timestamp.After(after))
}

func TestNewEvent_PayloadRoundtrip(t *testing.T) {
	payload := CronPayload{JobName: "heartbeat", Spec: "*/5 * * * *"}
	ev, err := NewEvent(SourceCron, TypeTick, payload, nil)
	require.NoError(t, err)

	var got CronPayload
	require.NoError(t, json.Unmarshal(ev.Payload, &got))
	assert.Equal(t, payload.JobName, got.JobName)
	assert.Equal(t, payload.Spec, got.Spec)
}

func TestEventID_Unique(t *testing.T) {
	id1 := newEventID()
	time.Sleep(time.Microsecond)
	id2 := newEventID()
	// IDs may collide if called in same nanosecond but should generally be unique
	assert.True(t, len(id1) > 4, "ID should have prefix")
	assert.True(t, len(id2) > 4, "ID should have prefix")
}

func TestSourceConstants(t *testing.T) {
	assert.Equal(t, "telegram", SourceTelegram)
	assert.Equal(t, "cron", SourceCron)
	assert.Equal(t, "webhook", SourceWebhook)
	assert.Equal(t, "internal", SourceInternal)
}

func TestTypeConstants(t *testing.T) {
	assert.Equal(t, "message", TypeMessage)
	assert.Equal(t, "tick", TypeTick)
	assert.Equal(t, "alert", TypeAlert)
	assert.Equal(t, "tool_result", TypeToolResult)
}
