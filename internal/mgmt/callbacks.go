package mgmt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// CallbackDelivery handles async webhook callbacks for completed tasks.
type CallbackDelivery struct {
	client  *http.Client
	retries int
	delay   time.Duration
	logger  zerolog.Logger
}

// CallbackPayload is the JSON body sent to callback URLs.
type CallbackPayload struct {
	TaskID      string          `json:"task_id"`
	Type        string          `json:"type"`
	Status      TaskStatus      `json:"status"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// NewCallbackDelivery creates a new callback delivery service.
func NewCallbackDelivery(timeout time.Duration, retries int, logger zerolog.Logger) *CallbackDelivery {
	return &CallbackDelivery{
		client: &http.Client{
			Timeout: timeout,
		},
		retries: retries,
		delay:   2 * time.Second,
		logger:  logger.With().Str("component", "callbacks").Logger(),
	}
}

// Deliver sends a callback with retries. Returns nil if URL is empty.
func (cd *CallbackDelivery) Deliver(ctx context.Context, url string, task *Task) error {
	if url == "" {
		return nil
	}

	payload := CallbackPayload{
		TaskID:      task.ID,
		Type:        task.Type,
		Status:      task.Status,
		Result:      task.Result,
		Error:       task.Error,
		CompletedAt: task.CompletedAt,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling callback payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= cd.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cd.delay * time.Duration(attempt)):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("creating callback request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "platform-agent-callback/1.0")

		resp, err := cd.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("callback delivery attempt %d: %w", attempt+1, err)
			cd.logger.Warn().Err(lastErr).
				Str("url", url).
				Str("task_id", task.ID).
				Int("attempt", attempt+1).
				Msg("callback delivery failed")
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			cd.logger.Info().
				Str("url", url).
				Str("task_id", task.ID).
				Int("status_code", resp.StatusCode).
				Msg("callback delivered")
			return nil
		}

		lastErr = fmt.Errorf("callback returned status %d on attempt %d", resp.StatusCode, attempt+1)
		cd.logger.Warn().
			Str("url", url).
			Str("task_id", task.ID).
			Int("status_code", resp.StatusCode).
			Int("attempt", attempt+1).
			Msg("callback returned non-2xx")
	}

	return fmt.Errorf("callback delivery failed after %d attempts: %w", cd.retries+1, lastErr)
}
