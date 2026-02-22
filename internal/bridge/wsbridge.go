package bridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// WSBridge wraps WSClient and implements the same message handling as Bridge,
// but uses a persistent WebSocket connection instead of CLI exec.
type WSBridge struct {
	ws     *WSClient
	poster SlackPoster
	logger zerolog.Logger
	cfg    Config

	// Reuse Bridge's thread tracking and semaphore
	*Bridge
}

// NewWSBridge creates a bridge that uses WebSocket for OpenClaw communication.
// It wraps a regular Bridge for thread tracking and Slack posting, but overrides
// the agent call to use WebSocket.
func NewWSBridge(wsCfg WSConfig, bridgeCfg Config, poster SlackPoster, logger zerolog.Logger) *WSBridge {
	ws := NewWSClient(wsCfg, logger)
	base := New(bridgeCfg, poster, logger)

	return &WSBridge{
		ws:     ws,
		poster: poster,
		logger: logger.With().Str("component", "ws-bridge").Logger(),
		cfg:    bridgeCfg,
		Bridge: base,
	}
}

// Connect establishes the WebSocket connection.
func (b *WSBridge) Connect(ctx context.Context) error {
	return b.ws.Connect(ctx)
}

// Close shuts down the WebSocket connection.
func (b *WSBridge) CloseWS() error {
	return b.ws.Close()
}

// IsConnected returns the WebSocket connection status.
func (b *WSBridge) IsConnected() bool {
	return b.ws.IsConnected()
}

// HandleMessage processes an inbound Slack message via WebSocket.
func (b *WSBridge) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
	// Skip bot's own messages
	if userID == b.cfg.BotUserID {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Strip bot mention
	if b.cfg.BotUserID != "" {
		mention := fmt.Sprintf("<@%s>", b.cfg.BotUserID)
		text = strings.TrimPrefix(text, mention)
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
	}

	// Acquire semaphore
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
			Msg("forwarding to Kog-2 via WS")

		// Show thinking indicator
		if messageTS != "" {
			_ = b.poster.AddReaction(channelID, messageTS, "hourglass_flowing_sand")
		}

		sessionID := fmt.Sprintf("%s-%s", b.cfg.SessionPrefix, channelID)
		contextMessage := fmt.Sprintf("[platform:slack user:<@%s>] %s", userID, text)

		resp, err := b.ws.SendAgent(ctx, sessionID, contextMessage)

		// Remove thinking indicator
		if messageTS != "" {
			_ = b.poster.RemoveReaction(channelID, messageTS, "hourglass_flowing_sand")
		}

		if err != nil {
			b.logger.Error().Err(err).Msg("ws agent call failed")
			if _, postErr := b.poster.PostMessage(channelID, "⚠️ Kog geçici olarak yanıt veremiyor. Tekrar deneyin.", threadTS); postErr != nil {
				b.logger.Error().Err(postErr).Msg("failed to post error message")
			}
			return
		}

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

		if replyThread != "" {
			b.trackThread(channelID, replyThread)
		}
	}()
}
