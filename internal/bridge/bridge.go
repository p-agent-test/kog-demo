// Package bridge forwards Slack messages to Kog-2 (OpenClaw) via the
// `openclaw agent` CLI and relays responses back to Slack.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// SlackPoster abstracts posting messages to Slack.
type SlackPoster interface {
	PostMessage(channelID string, text string, threadTS string) (string, error)
}

// Config holds bridge configuration.
type Config struct {
	// OpenClawBin is the path to the openclaw binary.
	// Default: "openclaw"
	OpenClawBin string

	// GatewayURL overrides the gateway WebSocket URL.
	// If empty, openclaw uses its default config.
	GatewayURL string

	// GatewayToken is the auth token for the gateway.
	// If empty, openclaw uses its stored device identity.
	GatewayToken string

	// DefaultTimeout is the max wait for an agent response.
	DefaultTimeout time.Duration

	// SessionPrefix is prepended to Slack user/channel IDs to create session keys.
	// Default: "slack"
	SessionPrefix string

	// BotUserID is the Slack bot's own user ID (e.g. "U0123ABC").
	// Used to filter out the bot's own messages.
	BotUserID string

	// MaxConcurrent limits parallel openclaw agent calls.
	MaxConcurrent int
}

// DefaultConfig returns sane defaults.
func DefaultConfig() Config {
	return Config{
		OpenClawBin:    "openclaw",
		DefaultTimeout: 120 * time.Second,
		SessionPrefix:  "slack",
		MaxConcurrent:  5,
	}
}

// Bridge forwards Slack messages to Kog-2 and relays responses.
type Bridge struct {
	cfg    Config
	poster SlackPoster
	sem    chan struct{}
	logger zerolog.Logger
	mu     sync.Mutex
}

// New creates a new Bridge.
func New(cfg Config, poster SlackPoster, logger zerolog.Logger) *Bridge {
	if cfg.OpenClawBin == "" {
		cfg.OpenClawBin = "openclaw"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 120 * time.Second
	}
	if cfg.SessionPrefix == "" {
		cfg.SessionPrefix = "slack"
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 5
	}

	return &Bridge{
		cfg:    cfg,
		poster: poster,
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		logger: logger.With().Str("component", "bridge").Logger(),
	}
}

// AgentResponse is the JSON output from `openclaw agent --json`.
type AgentResponse struct {
	RunID   string `json:"runId"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Result  struct {
		Payloads []struct {
			Text     string  `json:"text"`
			MediaURL *string `json:"mediaUrl"`
		} `json:"payloads"`
		Meta json.RawMessage `json:"meta"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// HandleMessage processes an inbound Slack message and routes it to Kog-2.
// This is meant to be called from the Slack event handler.
// It runs asynchronously — the response is posted back to the Slack channel.
func (b *Bridge) HandleMessage(ctx context.Context, channelID, userID, text, threadTS string) {
	// Skip bot's own messages
	if userID == b.cfg.BotUserID {
		return
	}

	// Skip empty messages
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Strip bot mention prefix (e.g. "<@U0123ABC> do something" → "do something")
	if b.cfg.BotUserID != "" {
		mention := fmt.Sprintf("<@%s>", b.cfg.BotUserID)
		text = strings.TrimPrefix(text, mention)
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
	}

	// Acquire semaphore (non-blocking if full)
	select {
	case b.sem <- struct{}{}:
	default:
		b.logger.Warn().
			Str("channel", channelID).
			Str("user", userID).
			Msg("bridge at capacity, dropping message")
		return
	}

	go func() {
		defer func() { <-b.sem }()

		b.logger.Info().
			Str("channel", channelID).
			Str("user", userID).
			Str("text", truncate(text, 100)).
			Msg("forwarding to Kog-2")

		// Build session ID from channel
		sessionID := fmt.Sprintf("%s-%s", b.cfg.SessionPrefix, channelID)

		resp, err := b.callAgent(ctx, sessionID, text)
		if err != nil {
			b.logger.Error().Err(err).Msg("openclaw agent call failed")
			if _, postErr := b.poster.PostMessage(channelID, "⚠️ Kog geçici olarak yanıt veremiyor. Tekrar deneyin.", threadTS); postErr != nil {
				b.logger.Error().Err(postErr).Msg("failed to post error message")
			}
			return
		}

		// Post response payloads (reply in thread if original was in thread)
		for _, payload := range resp.Result.Payloads {
			if payload.Text == "" {
				continue
			}
			if _, err := b.poster.PostMessage(channelID, payload.Text, threadTS); err != nil {
				b.logger.Error().Err(err).
					Str("channel", channelID).
					Msg("failed to post response to Slack")
			}
		}
	}()
}

// callAgent invokes `openclaw agent` CLI and returns the parsed response.
func (b *Bridge) callAgent(ctx context.Context, sessionID, message string) (*AgentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.DefaultTimeout+10*time.Second)
	defer cancel()

	args := []string{
		"agent",
		"--message", message,
		"--session-id", sessionID,
		"--json",
		"--timeout", fmt.Sprintf("%d", int(b.cfg.DefaultTimeout.Seconds())),
	}

	if b.cfg.GatewayURL != "" {
		args = append(args, "--url", b.cfg.GatewayURL)
	}
	if b.cfg.GatewayToken != "" {
		args = append(args, "--token", b.cfg.GatewayToken)
	}

	cmd := exec.CommandContext(ctx, b.cfg.OpenClawBin, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	b.logger.Debug().
		Str("session", sessionID).
		Str("message", truncate(message, 80)).
		Msg("calling openclaw agent")

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("openclaw agent failed: %w (stderr: %s)", err, truncate(stderr.String(), 500))
	}

	var resp AgentResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse openclaw response: %w (stdout: %s)", err, truncate(stdout.String(), 500))
	}

	if resp.Status != "ok" {
		errMsg := resp.Summary
		if resp.Error != nil {
			errMsg = resp.Error.Message
		}
		return nil, fmt.Errorf("agent returned error: %s", errMsg)
	}

	b.logger.Info().
		Str("session", sessionID).
		Str("runId", resp.RunID).
		Int("payloads", len(resp.Result.Payloads)).
		Msg("agent response received")

	return &resp, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
