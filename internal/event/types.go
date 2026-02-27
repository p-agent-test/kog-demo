// Package event defines the Event type and EventSource interface.
// All runtime stimuli — messages, ticks, webhooks — flow as Events.
package event

import (
	"context"
	"encoding/json"
	"time"
)

// Source identifiers for well-known event sources.
const (
	SourceTelegram = "telegram"
	SourceCron     = "cron"
	SourceWebhook  = "webhook"
	SourceInternal = "internal"
)

// Type identifiers for well-known event types.
const (
	TypeMessage    = "message"
	TypeTick       = "tick"
	TypeAlert      = "alert"
	TypeToolResult = "tool_result"
	TypeSystem     = "system"
)

// Event is the fundamental unit of work in the Kog runtime.
// Every external stimulus and internal notification becomes an Event.
type Event struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`    // e.g. "telegram", "cron"
	Type      string            `json:"type"`      // e.g. "message", "tick"
	Payload   json.RawMessage   `json:"payload"`   // source-specific JSON
	Metadata  map[string]string `json:"metadata"`  // arbitrary key-value pairs
	Timestamp time.Time         `json:"timestamp"`
}

// TelegramPayload is the decoded Payload for SourceTelegram events.
type TelegramPayload struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Text      string `json:"text"`
	ReplyTo   int    `json:"reply_to,omitempty"`
}

// CronPayload is the decoded Payload for SourceCron events.
type CronPayload struct {
	JobName string `json:"job_name"`
	Spec    string `json:"spec"`
}

// EventSource is implemented by anything that can emit events.
// The runtime starts each source in its own goroutine.
type EventSource interface {
	// Name returns the source identifier (e.g. "telegram").
	Name() string

	// Subscribe starts delivering events to out until ctx is cancelled.
	// Subscribe must be non-blocking; it should start a goroutine internally.
	Subscribe(ctx context.Context, out chan<- Event) error

	// Ack acknowledges a processed event (for at-least-once sources).
	// Sources that don't need acking can implement this as a no-op.
	Ack(ctx context.Context, eventID string) error
}

// NewEvent constructs an Event with a generated ID and current timestamp.
func NewEvent(source, evType string, payload interface{}, meta map[string]string) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:        newEventID(),
		Source:    source,
		Type:      evType,
		Payload:   raw,
		Metadata:  meta,
		Timestamp: time.Now().UTC(),
	}, nil
}

func newEventID() string {
	return "evt_" + time.Now().Format("20060102150405.000000000")
}
