// Package bridge forwards Slack messages to Kog-2 (OpenClaw) via the
// `openclaw agent` CLI and relays responses back to Slack.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// SlackPoster abstracts posting messages to Slack.
type SlackPoster interface {
	PostMessage(channelID string, text string, threadTS string) (string, error)
	UpdateMessage(channelID string, messageTS string, text string) error
	AddReaction(channelID string, messageTS string, emoji string) error
	RemoveReaction(channelID string, messageTS string, emoji string) error
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

	// MgmtURL is the Management API base URL for registering session context.
	// Default: "http://localhost:8090"
	MgmtURL string

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

// ThreadLookup is an optional function to check if a thread exists in persistent storage.
// Used for restart recovery — when in-memory activeThreads is empty.
type ThreadLookup func(channel, threadTS string) bool

// Bridge forwards Slack messages to Kog-2 and relays responses.
type Bridge struct {
	cfg           Config
	poster        SlackPoster
	sem           chan struct{}
	logger        zerolog.Logger
	mu            sync.Mutex
	activeThreads map[string]bool // channel:threadTS → tracked
	threadLookup  ThreadLookup    // optional persistent fallback
	threadSaver   ThreadSaver     // optional persistent writer
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
		cfg:           cfg,
		poster:        poster,
		sem:           make(chan struct{}, cfg.MaxConcurrent),
		logger:        logger.With().Str("component", "bridge").Logger(),
		activeThreads: make(map[string]bool),
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

// SetThreadLookup sets an optional persistent fallback for thread tracking.
// Used after restart when in-memory activeThreads is empty.
func (b *Bridge) SetThreadLookup(fn ThreadLookup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.threadLookup = fn
}

// IsActiveThread returns true if the given thread is being tracked (Kog has responded in it).
func (b *Bridge) IsActiveThread(channelID, threadTS string) bool {
	if threadTS == "" {
		return false
	}
	key := channelID + ":" + threadTS

	b.mu.Lock()
	defer b.mu.Unlock()

	// Check in-memory first
	if b.activeThreads[key] {
		return true
	}

	// Fallback to persistent store (restart recovery)
	if b.threadLookup != nil && b.threadLookup(channelID, threadTS) {
		// Promote to in-memory cache
		b.activeThreads[key] = true
		return true
	}

	return false
}

// ThreadSaver is an optional function to persist thread tracking to storage.
type ThreadSaver func(channel, threadTS, sessionKey string)

// SetThreadSaver sets an optional function to persist tracked threads.
func (b *Bridge) SetThreadSaver(fn ThreadSaver) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.threadSaver = fn
}

func (b *Bridge) trackThread(channelID, threadTS string) {
	if threadTS == "" {
		return
	}
	b.mu.Lock()
	b.activeThreads[channelID+":"+threadTS] = true
	saver := b.threadSaver
	b.mu.Unlock()

	// Persist to store (non-blocking)
	if saver != nil {
		sessionKey := fmt.Sprintf("agent:main:slack-%s-%s", channelID, threadTS)
		saver(channelID, threadTS, sessionKey)
	}
}

// HandleMessage processes an inbound Slack message and routes it to Kog-2.
// This is meant to be called from the Slack event handler.
// It runs asynchronously — the response is posted back to the Slack channel.
// messageTS is the timestamp of the triggering message (for reactions).
func (b *Bridge) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
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

		// Show thinking indicator — defer removal so it's cleaned up on panic/restart too
		if messageTS != "" {
			_ = b.poster.AddReaction(channelID, messageTS, "hourglass_flowing_sand")
			defer func() {
				_ = b.poster.RemoveReaction(channelID, messageTS, "hourglass_flowing_sand")
			}()
		}

		// Determine the thread TS for context injection
		ctxThread := threadTS
		if ctxThread == "" {
			ctxThread = messageTS
		}

		// Build session ID: each thread gets its own isolated session
		// Thread messages → "slack-{channel}-{threadTS}" (isolated context)
		// Top-level DMs  → "slack-{channel}" (shared channel session)
		var sessionID string
		if threadTS != "" {
			sessionID = fmt.Sprintf("%s-%s-%s", b.cfg.SessionPrefix, channelID, threadTS)
		} else {
			sessionID = fmt.Sprintf("%s-%s", b.cfg.SessionPrefix, channelID)
		}

		// Register Slack context with agent so async task completions route back here
		b.registerSessionContext(sessionID, channelID, ctxThread)

		resp, err := b.callAgent(ctx, sessionID, channelID, ctxThread, userID, text)

		if err != nil {
			b.logger.Error().Err(err).Msg("openclaw agent call failed")
			if _, postErr := b.poster.PostMessage(channelID, "⚠️ Kog geçici olarak yanıt veremiyor. Tekrar deneyin.", threadTS); postErr != nil {
				b.logger.Error().Err(postErr).Msg("failed to post error message")
			}
			return
		}

		// Always reply in thread:
		// - If already in a thread, use that threadTS
		// - If top-level message, use the message's own TS to create a new thread
		replyThread := threadTS
		if replyThread == "" {
			replyThread = messageTS
		}

		for _, payload := range resp.Result.Payloads {
			if payload.Text == "" {
				continue
			}
			_, err := b.poster.PostMessage(channelID, payload.Text, replyThread)
			if err != nil {
				b.logger.Error().Err(err).
					Str("channel", channelID).
					Msg("failed to post response to Slack")
				continue
			}
		}

		// Track thread for follow-up replies
		if replyThread != "" {
			b.trackThread(channelID, replyThread)
		}
	}()
}

// callAgent invokes `openclaw agent` CLI and returns the parsed response.
func (b *Bridge) callAgent(ctx context.Context, sessionID, channelID, threadTS, userID, message string) (*AgentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.DefaultTimeout+10*time.Second)
	defer cancel()

	// Prepend Slack context so Kog-2 knows the platform and can route async responses
	contextMessage := fmt.Sprintf("[slack_context: channel=%s thread=%s user=<@%s>]\n%s", channelID, threadTS, userID, message)

	args := []string{
		"agent",
		"--message", contextMessage,
		"--session-id", sessionID,
		"--json",
		"--timeout", fmt.Sprintf("%d", int(b.cfg.DefaultTimeout.Seconds())),
	}

	// Gateway URL and token are read from openclaw config automatically.
	// No CLI flags needed — openclaw agent uses the local gateway.

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

// registerSessionContext calls POST /api/v1/context on the Management API
// to register the Slack routing context for this session.
func (b *Bridge) registerSessionContext(sessionID, channelID, threadTS string) {
	mgmtURL := b.cfg.MgmtURL
	if mgmtURL == "" {
		mgmtURL = "http://localhost:8090"
	}

	body := fmt.Sprintf(`{"session_id":"%s","channel":"%s","thread_ts":"%s"}`, sessionID, channelID, threadTS)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", mgmtURL+"/api/v1/context", strings.NewReader(body))
	if err != nil {
		b.logger.Warn().Err(err).Msg("failed to create context registration request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.logger.Warn().Err(err).Msg("failed to register session context")
		return
	}
	resp.Body.Close()

	b.logger.Debug().
		Str("session", sessionID).
		Str("channel", channelID).
		Str("thread", threadTS).
		Msg("session context registered with agent")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
