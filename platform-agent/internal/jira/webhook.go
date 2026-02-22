package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog"
)

// WebhookEvent represents a Jira webhook event.
type WebhookEvent struct {
	WebhookEvent string `json:"webhookEvent"`
	IssueEvent   string `json:"issue_event_type_name,omitempty"`
	Issue        *Issue `json:"issue,omitempty"`
	User         *User  `json:"user,omitempty"`
	Changelog    *struct {
		Items []ChangelogItem `json:"items"`
	} `json:"changelog,omitempty"`
}

// ChangelogItem represents a field change in a Jira event.
type ChangelogItem struct {
	Field      string `json:"field"`
	FromString string `json:"fromString"`
	ToString   string `json:"toString"`
}

// WebhookHandler handles Jira webhook events.
type WebhookHandler struct {
	logger    zerolog.Logger
	onCreated func(ctx context.Context, event *WebhookEvent)
	onUpdated func(ctx context.Context, event *WebhookEvent)
}

// NewWebhookHandler creates a new Jira webhook handler.
func NewWebhookHandler(logger zerolog.Logger) *WebhookHandler {
	return &WebhookHandler{
		logger: logger.With().Str("component", "jira.webhook").Logger(),
	}
}

// OnIssueCreated sets the handler for issue created events.
func (w *WebhookHandler) OnIssueCreated(fn func(ctx context.Context, event *WebhookEvent)) {
	w.onCreated = fn
}

// OnIssueUpdated sets the handler for issue updated events.
func (w *WebhookHandler) OnIssueUpdated(fn func(ctx context.Context, event *WebhookEvent)) {
	w.onUpdated = fn
}

// ServeHTTP handles incoming Jira webhook requests.
func (w *WebhookHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		w.logger.Error().Err(err).Msg("failed to parse webhook event")
		http.Error(rw, "invalid payload", http.StatusBadRequest)
		return
	}

	w.logger.Info().
		Str("event", event.WebhookEvent).
		Str("issue_event", event.IssueEvent).
		Msg("jira webhook received")

	switch event.WebhookEvent {
	case "jira:issue_created":
		if w.onCreated != nil {
			go w.onCreated(ctx, &event)
		}
	case "jira:issue_updated":
		if w.onUpdated != nil {
			go w.onUpdated(ctx, &event)
		}
	default:
		w.logger.Debug().Str("event", event.WebhookEvent).Msg("unhandled event")
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "ok")
}
