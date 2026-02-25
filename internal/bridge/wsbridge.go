package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const defaultSplitMaxLen = 3000

// IsTokenLimitError checks if an error message indicates a context length / token limit error.
func IsTokenLimitError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "too many tokens")
}

// splitMessage splits a long message into chunks suitable for Slack posting.
// It tries to split on markdown headers, code blocks, and paragraph breaks.
func splitMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = defaultSplitMaxLen
	}
	if len(text) <= maxLen {
		return []string{text}
	}

	// Strategy 1: Split on ## or ### headers
	if chunks := splitOnHeaders(text, maxLen); len(chunks) > 1 {
		return chunks
	}

	// Strategy 2: Split on ``` code blocks
	if chunks := splitOnCodeBlocks(text, maxLen); len(chunks) > 1 {
		return chunks
	}

	// Strategy 3: Split on \n\n paragraph breaks
	if chunks := splitOnParagraphs(text, maxLen); len(chunks) > 1 {
		return chunks
	}

	// Strategy 4: Hard split at maxLen on newline boundaries
	return hardSplit(text, maxLen)
}

// splitOnHeaders splits text on lines starting with ## or ###.
func splitOnHeaders(text string, maxLen int) []string {
	lines := strings.Split(text, "\n")
	var chunks []string
	var current strings.Builder

	for _, line := range lines {
		isHeader := strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ")
		if isHeader && current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	if len(chunks) <= 1 {
		return chunks
	}

	// Re-split any chunks that are still too long
	return reSplitChunks(chunks, maxLen)
}

// splitOnCodeBlocks splits text so each ```...``` block is its own chunk.
func splitOnCodeBlocks(text string, maxLen int) []string {
	parts := strings.Split(text, "```")
	if len(parts) < 3 {
		return nil // no complete code block
	}

	var chunks []string
	for i, part := range parts {
		if i%2 == 0 {
			// Text between code blocks
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				chunks = append(chunks, trimmed)
			}
		} else {
			// Code block content — re-wrap with ```
			chunks = append(chunks, "```"+part+"```")
		}
	}

	if len(chunks) <= 1 {
		return chunks
	}

	return reSplitChunks(chunks, maxLen)
}

// splitOnParagraphs splits text on double newlines for chunks > 2000 chars.
func splitOnParagraphs(text string, maxLen int) []string {
	paragraphs := strings.Split(text, "\n\n")
	if len(paragraphs) <= 1 {
		return nil
	}

	var chunks []string
	var current strings.Builder

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		// If adding this paragraph would exceed maxLen, flush current
		if current.Len() > 0 && current.Len()+len(para)+2 > maxLen {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	if len(chunks) <= 1 {
		return nil
	}
	return chunks
}

// hardSplit splits text at maxLen boundaries, preferring newline breaks.
func hardSplit(text string, maxLen int) []string {
	var chunks []string
	for len(text) > maxLen {
		// Find last newline before maxLen
		cut := strings.LastIndex(text[:maxLen], "\n")
		if cut <= 0 {
			cut = maxLen
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

// reSplitChunks takes chunks and re-splits any that exceed maxLen.
func reSplitChunks(chunks []string, maxLen int) []string {
	var result []string
	for _, chunk := range chunks {
		if len(chunk) <= maxLen {
			result = append(result, chunk)
		} else {
			result = append(result, hardSplit(chunk, maxLen)...)
		}
	}
	return result
}

// WSBridge wraps WSClient and implements MessageForwarder (slack.MessageForwarder).
// Uses persistent WebSocket for OpenClaw communication with streaming updates.
type WSBridge struct {
	ws              *WSClient
	poster          SlackPoster
	botUserID       string
	logger          zerolog.Logger
	historyProvider ThreadHistoryProvider
	warmTracker     *WarmTracker

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
		ws:          ws,
		poster:      poster,
		botUserID:   botUserID,
		logger:      logger.With().Str("component", "ws-bridge").Logger(),
		Bridge:      base,
		warmTracker: NewWarmTracker(),
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

// SetThreadHistoryProvider sets the provider for auto-injecting thread history on cold sessions.
func (b *WSBridge) SetThreadHistoryProvider(p ThreadHistoryProvider) {
	b.historyProvider = p
}

// HandleMessageWithSession processes a message with an explicit session key (for project routing).
func (b *WSBridge) HandleMessageWithSession(ctx context.Context, channelID, userID, text, threadTS, messageTS, sessionKey string) {
	b.handleMessageInternal(ctx, channelID, userID, text, threadTS, messageTS, sessionKey)
}

// HandleMessage processes an inbound Slack message via WebSocket + streaming.
func (b *WSBridge) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
	b.handleMessageInternal(ctx, channelID, userID, text, threadTS, messageTS, "")
}

func (b *WSBridge) handleMessageInternal(ctx context.Context, channelID, userID, text, threadTS, messageTS, overrideSessionKey string) {
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

		sessionKey := overrideSessionKey
		if sessionKey == "" {
			sessionKey = fmt.Sprintf("agent:main:slack-%s", channelID)
			if threadTS != "" {
				sessionKey = fmt.Sprintf("agent:main:slack-%s-%s", channelID, threadTS)
			}
		}

		// Auto-inject thread history on cold sessions
		messageToSend := text
		if threadTS != "" && b.historyProvider != nil && !b.warmTracker.IsWarm(sessionKey) {
			history, histErr := b.historyProvider.GetThreadHistory(channelID, threadTS, maxHistoryMessages)
			if histErr != nil {
				b.logger.Warn().Err(histErr).Str("thread", threadTS).Msg("failed to fetch thread history")
			} else {
				formatted := FormatThreadHistory(history, messageTS)
				if formatted != "" {
					messageToSend = formatted + "\n\n" + text
				}
			}
		}
		b.warmTracker.MarkWarm(sessionKey)

		contextMessage := fmt.Sprintf("[platform:slack user:<@%s> channel:%s thread:%s] %s",
			userID, channelID, threadTS, messageToSend)

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

		// Final update — split long responses into multiple messages
		if response != "" && response != "NO_REPLY" && response != "HEARTBEAT_OK" {
			chunks := splitMessage(response, defaultSplitMaxLen)
			if len(chunks) <= 1 {
				// Single message — just finalize the streaming message
				sm.update(response, true)
			} else {
				// Multiple chunks — finalize first chunk in streaming message, post rest as new messages
				sm.update(chunks[0], true)
				for _, chunk := range chunks[1:] {
					time.Sleep(300 * time.Millisecond)
					b.poster.PostMessage(channelID, chunk, replyThread)
				}
			}
		}

		if replyThread != "" {
			b.trackThread(channelID, replyThread)
		}
	}()
}
