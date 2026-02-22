package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// WSBridge wraps WSClient and implements MessageForwarder (slack.MessageForwarder).
// Uses persistent WebSocket for OpenClaw communication with streaming updates.
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

// streamingMessage manages a Slack message that updates as streaming progresses.
type streamingMessage struct {
	mu          sync.Mutex
	poster      SlackPoster
	channelID   string
	threadTS    string
	messageTS   string // Slack timestamp of the posted message
	lastText    string
	lastUpdate  time.Time
	minInterval time.Duration // minimum time between Slack API updates
}

func newStreamingMessage(poster SlackPoster, channelID, threadTS string) *streamingMessage {
	return &streamingMessage{
		poster:      poster,
		channelID:   channelID,
		threadTS:    threadTS,
		minInterval: 1500 * time.Millisecond, // Slack rate limit friendly
	}
}

// update posts or edits the Slack message with the latest text.
func (sm *streamingMessage) update(text string, isFinal bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Skip if text hasn't changed
	if text == sm.lastText && !isFinal {
		return
	}

	// Throttle non-final updates
	if !isFinal && time.Since(sm.lastUpdate) < sm.minInterval {
		return
	}

	displayText := text
	if !isFinal {
		displayText = text + " ▍" // typing indicator
	}

	if sm.messageTS == "" {
		// First update — post new message
		ts, err := sm.poster.PostMessage(sm.channelID, displayText, sm.threadTS)
		if err != nil {
			return
		}
		sm.messageTS = ts
	} else {
		// Subsequent updates — edit existing message
		_ = sm.poster.UpdateMessage(sm.channelID, sm.messageTS, displayText)
	}

	sm.lastText = text
	sm.lastUpdate = time.Now()
}

// HandleMessage processes an inbound Slack message via WebSocket + streaming.
func (b *WSBridge) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
	if userID == b.botUserID {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if b.botUserID != "" {
		mention := fmt.Sprintf("<@%s>", b.botUserID)
		text = strings.TrimPrefix(text, mention)
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
	}

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

		sessionKey := fmt.Sprintf("agent:main:slack-%s", channelID)
		if threadTS != "" {
			sessionKey = fmt.Sprintf("agent:main:slack-%s-%s", channelID, threadTS)
		}

		contextMessage := fmt.Sprintf("[platform:slack user:<@%s> channel:%s thread:%s] %s",
			userID, channelID, threadTS, text)

		replyThread := threadTS
		if replyThread == "" {
			replyThread = messageTS
		}

		// Streaming message — posts initial, then edits with deltas
		sm := newStreamingMessage(b.poster, channelID, replyThread)

		response, err := b.ws.SendChatStream(ctx, sessionKey, contextMessage, func(deltaText string, isFinal bool) {
			if deltaText == "" || deltaText == "NO_REPLY" || deltaText == "HEARTBEAT_OK" {
				return
			}
			sm.update(deltaText, isFinal)
		})

		if err != nil {
			b.logger.Error().Err(err).Msg("WS chat.send failed")
			if sm.messageTS == "" {
				// No streaming message was posted yet
				b.poster.PostMessage(channelID, "⚠️ Kog geçici olarak yanıt veremiyor.", replyThread)
			}
			return
		}

		// Final update (in case the last delta was throttled)
		if response != "" && response != "NO_REPLY" && response != "HEARTBEAT_OK" {
			sm.update(response, true)
		}

		if replyThread != "" {
			b.trackThread(channelID, replyThread)
		}
	}()
}
