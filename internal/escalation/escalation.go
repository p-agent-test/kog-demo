// Package escalation handles notifying humans (Anıl) about critical events.
// Supports Telegram and is extensible to Slack, email, etc.
package escalation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Level describes the urgency of an escalation.
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Escalation represents a notification to a human.
type Escalation struct {
	Level   Level
	Title   string
	Message string
	Source  string // which agent or subsystem triggered it
	Error   error  // underlying error, if any
}

// Notifier sends escalation notifications.
type Notifier interface {
	Notify(ctx context.Context, e Escalation) error
}

// TelegramNotifier sends escalations via Telegram Bot API.
type TelegramNotifier struct {
	token   string
	chatID  int64
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// NewTelegramNotifier creates a notifier that sends to a specific Telegram chat.
func NewTelegramNotifier(token string, chatID int64, logger *slog.Logger) *TelegramNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &TelegramNotifier{
		token:   token,
		chatID:  chatID,
		baseURL: "https://api.telegram.org/bot" + token,
		client:  &http.Client{Timeout: 15 * time.Second},
		logger:  logger,
	}
}

// Notify sends the escalation as a Telegram message.
func (n *TelegramNotifier) Notify(ctx context.Context, e Escalation) error {
	emoji := levelEmoji(e.Level)
	text := fmt.Sprintf("%s *[%s] %s*\n\n%s", emoji, e.Level, e.Title, e.Message)
	if e.Source != "" {
		text += fmt.Sprintf("\n\n_Source: %s_", e.Source)
	}
	if e.Error != nil {
		text += fmt.Sprintf("\n\n```\n%v\n```", e.Error)
	}

	body, err := json.Marshal(map[string]interface{}{
		"chat_id":    n.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	if err != nil {
		return fmt.Errorf("escalation marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("escalation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("escalation send: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	n.logger.Info("escalation sent",
		"level", e.Level,
		"title", e.Title,
		"chat_id", n.chatID,
	)
	return nil
}

// MultiNotifier fans out to multiple notifiers.
type MultiNotifier struct {
	notifiers []Notifier
}

func NewMultiNotifier(ns ...Notifier) *MultiNotifier {
	return &MultiNotifier{notifiers: ns}
}

func (m *MultiNotifier) Notify(ctx context.Context, e Escalation) error {
	var lastErr error
	for _, n := range m.notifiers {
		if err := n.Notify(ctx, e); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// LogNotifier logs escalations (useful for testing/dev).
type LogNotifier struct {
	logger *slog.Logger
}

func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogNotifier{logger: logger}
}

func (l *LogNotifier) Notify(_ context.Context, e Escalation) error {
	l.logger.Warn("escalation",
		"level", e.Level,
		"title", e.Title,
		"message", e.Message,
		"source", e.Source,
		"error", e.Error,
	)
	return nil
}

func levelEmoji(l Level) string {
	switch l {
	case LevelCritical:
		return "🚨"
	case LevelWarning:
		return "⚠️"
	default:
		return "ℹ️"
	}
}
