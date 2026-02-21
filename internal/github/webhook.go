package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	gh "github.com/google/go-github/v60/github"
	"github.com/rs/zerolog"
)

// WebhookHandler handles GitHub webhook events.
type WebhookHandler struct {
	secret []byte
	logger zerolog.Logger
	onPR   func(ctx context.Context, event *gh.PullRequestEvent)
	onCI   func(ctx context.Context, event *gh.CheckRunEvent)
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(secret string, logger zerolog.Logger) *WebhookHandler {
	return &WebhookHandler{
		secret: []byte(secret),
		logger: logger.With().Str("component", "github.webhook").Logger(),
	}
}

// OnPullRequest sets the handler for PR events.
func (w *WebhookHandler) OnPullRequest(fn func(ctx context.Context, event *gh.PullRequestEvent)) {
	w.onPR = fn
}

// OnCheckRun sets the handler for CI check run events.
func (w *WebhookHandler) OnCheckRun(fn func(ctx context.Context, event *gh.CheckRunEvent)) {
	w.onCI = fn
}

// ServeHTTP handles incoming webhook requests.
func (w *WebhookHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Validate signature if secret is set
	if len(w.secret) > 0 {
		sig := r.Header.Get("X-Hub-Signature-256")
		if err := gh.ValidateSignature(sig, payload, w.secret); err != nil {
			w.logger.Warn().Err(err).Msg("invalid webhook signature")
			http.Error(rw, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	eventType := r.Header.Get("X-GitHub-Event")
	w.logger.Info().Str("event", eventType).Msg("webhook received")

	switch eventType {
	case "pull_request":
		var event gh.PullRequestEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			http.Error(rw, "invalid payload", http.StatusBadRequest)
			return
		}
		if w.onPR != nil {
			go w.onPR(ctx, &event)
		}

	case "check_run":
		var event gh.CheckRunEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			http.Error(rw, "invalid payload", http.StatusBadRequest)
			return
		}
		if w.onCI != nil {
			go w.onCI(ctx, &event)
		}

	default:
		w.logger.Debug().Str("event", eventType).Msg("unhandled event type")
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "ok")
}
