// Package event — HTTP webhook EventSource.
// Listens on a configurable path and converts incoming POST requests to Events.
package event

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// WebhookConfig configures the HTTP webhook source.
type WebhookConfig struct {
	// Addr is the listen address, e.g. ":8080" or "127.0.0.1:9000".
	Addr string

	// Path is the URL path to accept webhooks on, e.g. "/webhook".
	// Defaults to "/webhook" if empty.
	Path string

	// Secret is an optional shared secret. When set, the source validates
	// the X-Webhook-Secret header against this value.
	Secret string

	// ReadTimeout for individual HTTP connections. Default: 10s.
	ReadTimeout time.Duration

	// MaxBodyBytes limits the request body size. Default: 1 MiB.
	MaxBodyBytes int64

	// Logger. Defaults to slog.Default().
	Logger *slog.Logger
}

// WebhookPayload is the parsed body of a webhook POST request.
// Callers may send any JSON; unrecognised fields are preserved as-is.
type WebhookPayload struct {
	// Type is an optional hint for the event type (defaults to "webhook").
	Type string `json:"type,omitempty"`

	// Body is the raw request body (set regardless of Content-Type).
	Body json.RawMessage `json:"body"`

	// Headers carries selected request headers (Content-Type, User-Agent, etc.).
	Headers map[string]string `json:"headers,omitempty"`

	// RemoteAddr is the caller's IP:port.
	RemoteAddr string `json:"remote_addr,omitempty"`
}

// WebhookSource is an EventSource that receives HTTP POST requests.
type WebhookSource struct {
	cfg    WebhookConfig
	logger *slog.Logger
}

// NewWebhookSource creates a WebhookSource. Call Subscribe() to start listening.
func NewWebhookSource(cfg WebhookConfig) *WebhookSource {
	if cfg.Path == "" {
		cfg.Path = "/webhook"
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 10 * time.Second
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = 1 << 20 // 1 MiB
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookSource{cfg: cfg, logger: logger}
}

// Name implements EventSource.
func (w *WebhookSource) Name() string { return SourceWebhook }

// Ack implements EventSource (no-op for webhooks — HTTP response is the ack).
func (w *WebhookSource) Ack(_ context.Context, _ string) error { return nil }

// Subscribe starts the HTTP server and forwards incoming webhooks to out.
// The server runs until ctx is cancelled.
func (w *WebhookSource) Subscribe(ctx context.Context, out chan<- Event) error {
	mux := http.NewServeMux()
	mux.HandleFunc(w.cfg.Path, w.handler(out))

	srv := &http.Server{
		Addr:        w.cfg.Addr,
		Handler:     mux,
		ReadTimeout: w.cfg.ReadTimeout,
	}

	w.logger.Info("webhook source starting",
		"addr", w.cfg.Addr,
		"path", w.cfg.Path,
	)

	// Start server in background goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Shut down when context is done.
	go func() {
		select {
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutCtx); err != nil {
				w.logger.Error("webhook: shutdown error", "err", err)
			}
		case err := <-errCh:
			w.logger.Error("webhook: server error", "err", err)
		}
	}()

	// Give the server a moment to start, return any immediate bind error.
	select {
	case err := <-errCh:
		return fmt.Errorf("webhook source: %w", err)
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// handler returns an http.HandlerFunc that parses POST requests into Events.
func (w *WebhookSource) handler(out chan<- Event) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Secret validation.
		if w.cfg.Secret != "" {
			if r.Header.Get("X-Webhook-Secret") != w.cfg.Secret {
				w.logger.Warn("webhook: invalid secret", "remote", r.RemoteAddr)
				http.Error(rw, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// Read body.
		body, err := io.ReadAll(io.LimitReader(r.Body, w.cfg.MaxBodyBytes))
		if err != nil {
			http.Error(rw, "read error", http.StatusInternalServerError)
			return
		}

		// Ensure body is valid JSON; wrap plain text if needed.
		rawBody := json.RawMessage(body)
		if !json.Valid(body) {
			quoted, _ := json.Marshal(string(body))
			rawBody = quoted
		}

		evType := TypeAlert
		// Try to extract a "type" field from the body.
		var hint struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(body, &hint) == nil && hint.Type != "" {
			evType = hint.Type
		}

		payload := WebhookPayload{
			Type:       evType,
			Body:       rawBody,
			RemoteAddr: r.RemoteAddr,
			Headers: map[string]string{
				"Content-Type": r.Header.Get("Content-Type"),
				"User-Agent":   r.Header.Get("User-Agent"),
			},
		}

		ev, err := NewEvent(SourceWebhook, evType, payload, map[string]string{
			"remote_addr": r.RemoteAddr,
			"path":        r.URL.Path,
		})
		if err != nil {
			w.logger.Error("webhook: build event", "err", err)
			http.Error(rw, "internal error", http.StatusInternalServerError)
			return
		}

		select {
		case out <- ev:
			w.logger.Debug("webhook event queued", "event_id", ev.ID)
			rw.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(rw, `{"event_id":%q}`, ev.ID)
		default:
			w.logger.Warn("webhook: event channel full, dropping", "event_id", ev.ID)
			http.Error(rw, "service busy", http.StatusServiceUnavailable)
		}
	}
}
