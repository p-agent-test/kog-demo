package bridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// WSBridge wraps WSClient and implements MessageForwarder (slack.MessageForwarder).
// Uses persistent WebSocket for OpenClaw communication instead of CLI exec.
type WSBridge struct {
	ws        *WSClient
	poster    SlackPoster
	botUserID string
	logger    zerolog.Logger

	// Embed Bridge for thread tracking and IsActiveThread
	*Bridge
}

// NewWSBridge creates a bridge that uses WebSocket for OpenClaw communication.
func NewWSBridge(ws *WSClient, poster SlackPoster, botUserID string, logger zerolog.Logger) *WSBridge {
	// Create a dummy CLI bridge config for thread tracking
	base := New(Config{
		BotUserID:     botUserID,
		MaxConcurrent: 5,
	}, poster, logger)

	return &WSBridge{
		ws:        ws,
		poster:    poster,
		botUserID: botUserID,
		logger:    logger.With().Str("component", "ws-bridge").Logger(),
		Bridge:    base,
	}
}

// HandleMessage processes an inbound Slack message via WebSocket + chat.send.
func (b *WSBridge) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
	if userID == b.botUserID {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Strip bot mention
	if b.botUserID != "" {
		mention := fmt.Sprintf("<@%s>", b.botUserID)
		text = strings.TrimPrefix(text, mention)
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
	}

	// Acquire semaphore (from embedded Bridge)
	select {
	case b.sem <- struct{}{}:
	default:
		b.logger.Warn().Str("channel", channelID).Msg("bridge at capacity, dropping message")
		return
	}

	go func() {
		defer func() { <-b.sem }()

		b.logger.Info().
			Str("channel", channelID).
			Str("user", userID).
			Str("text", truncate(text, 100)).
			Msg("forwarding to Kog-2 via WS")

		// Hourglass indicator
		if messageTS != "" {
			_ = b.poster.AddReaction(channelID, messageTS, "hourglass_flowing_sand")
			defer func() {
				_ = b.poster.RemoveReaction(channelID, messageTS, "hourglass_flowing_sand")
			}()
		}

		// Build session key — thread-per-session when threadTS available
		sessionKey := fmt.Sprintf("agent:main:slack-%s", channelID)
		if threadTS != "" {
			sessionKey = fmt.Sprintf("agent:main:slack-%s-%s", channelID, threadTS)
		}

		contextMessage := fmt.Sprintf("[platform:slack user:<@%s> channel:%s thread:%s] %s",
			userID, channelID, threadTS, text)

		// Send via WebSocket chat.send (streaming)
		response, err := b.ws.SendChat(ctx, sessionKey, contextMessage)
		if err != nil {
			b.logger.Error().Err(err).Msg("WS chat.send failed")
			if _, postErr := b.poster.PostMessage(channelID, "⚠️ Kog geçici olarak yanıt veremiyor.", threadTS); postErr != nil {
				b.logger.Error().Err(postErr).Msg("failed to post error message")
			}
			return
		}

		// Post response to Slack
		replyThread := threadTS
		if replyThread == "" {
			replyThread = messageTS
		}

		if response != "" && response != "NO_REPLY" && response != "HEARTBEAT_OK" {
			_, err := b.poster.PostMessage(channelID, response, replyThread)
			if err != nil {
				b.logger.Error().Err(err).Str("channel", channelID).Msg("failed to post to Slack")
			}
		}

		if replyThread != "" {
			b.trackThread(channelID, replyThread)
		}
	}()
}
