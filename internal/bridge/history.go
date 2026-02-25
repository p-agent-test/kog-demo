package bridge

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const (
	maxHistoryMessages = 20
	maxMessageChars    = 500
	maxTotalChars      = 4000
)

// ThreadMessage represents a single message from thread history.
type ThreadMessage struct {
	UserID    string
	Text      string
	Timestamp string
	IsBotMsg  bool
}

// ThreadHistoryProvider fetches Slack thread history for context injection.
type ThreadHistoryProvider interface {
	GetThreadHistory(channelID, threadTS string, limit int) ([]ThreadMessage, error)
}

// SlackThreadReader is the minimal interface needed to read thread replies.
type SlackThreadReader interface {
	GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
}

// slackThreadProvider adapts a SlackThreadReader into a ThreadHistoryProvider.
type slackThreadProvider struct {
	client    SlackThreadReader
	botUserID string
}

// NewSlackThreadProvider creates a ThreadHistoryProvider from a Slack API client.
func NewSlackThreadProvider(client SlackThreadReader, botUserID string) ThreadHistoryProvider {
	return &slackThreadProvider{client: client, botUserID: botUserID}
}

func (p *slackThreadProvider) GetThreadHistory(channelID, threadTS string, limit int) ([]ThreadMessage, error) {
	if limit <= 0 {
		limit = maxHistoryMessages
	}

	msgs, _, _, err := p.client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     limit + 5, // fetch a few extra to account for filtered messages
	})
	if err != nil {
		return nil, fmt.Errorf("GetConversationReplies: %w", err)
	}

	var result []ThreadMessage
	for _, m := range msgs {
		// Skip bot error messages
		if m.User == p.botUserID && strings.HasPrefix(strings.TrimSpace(m.Text), "⚠️") {
			continue
		}

		text := m.Text
		if len(text) > maxMessageChars {
			text = text[:maxMessageChars] + "…"
		}

		isBotMsg := m.User == p.botUserID || m.BotID != ""

		result = append(result, ThreadMessage{
			UserID:    m.User,
			Text:      text,
			Timestamp: m.Timestamp,
			IsBotMsg:  isBotMsg,
		})
	}

	// Keep only the most recent `limit` messages
	if len(result) > limit {
		result = result[len(result)-limit:]
	}

	return result, nil
}

// FormatThreadHistory formats thread messages into a context block string.
// It excludes the last message (the current one being processed) if excludeLastTS is set.
func FormatThreadHistory(messages []ThreadMessage, excludeLastTS string) string {
	if len(messages) == 0 {
		return ""
	}

	// Filter out the current message
	var filtered []ThreadMessage
	for _, m := range messages {
		if excludeLastTS != "" && m.Timestamp == excludeLastTS {
			continue
		}
		filtered = append(filtered, m)
	}

	if len(filtered) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Thread History — auto-injected context]\n")

	totalChars := sb.Len()
	now := time.Now()

	// Build lines, tracking total size
	var lines []string
	for _, m := range filtered {
		ago := formatTimeAgo(m.Timestamp, now)
		line := fmt.Sprintf("<@%s> (%s): %s", m.UserID, ago, m.Text)
		lines = append(lines, line)
	}

	// Truncate oldest lines if total exceeds budget
	budgetRemaining := maxTotalChars - totalChars - len("[End Thread History]\n") - 2
	var kept []string
	usedChars := 0
	// Start from newest (end of slice) and work backwards
	for i := len(lines) - 1; i >= 0; i-- {
		lineLen := len(lines[i]) + 1 // +1 for newline
		if usedChars+lineLen > budgetRemaining {
			break
		}
		kept = append([]string{lines[i]}, kept...)
		usedChars += lineLen
	}

	if len(kept) == 0 {
		return ""
	}

	for _, line := range kept {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString("[End Thread History]")

	return sb.String()
}

func formatTimeAgo(ts string, now time.Time) string {
	// Slack timestamps are like "1234567890.123456"
	// Parse seconds part
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 {
		return "unknown"
	}
	var sec int64
	for _, c := range parts[0] {
		if c >= '0' && c <= '9' {
			sec = sec*10 + int64(c-'0')
		}
	}
	if sec == 0 {
		return "unknown"
	}

	t := time.Unix(sec, 0)
	d := now.Sub(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

// WarmTracker tracks which sessions have been active in this process lifetime.
type WarmTracker struct {
	mu       sync.RWMutex
	sessions map[string]bool
}

// NewWarmTracker creates a new warm session tracker.
func NewWarmTracker() *WarmTracker {
	return &WarmTracker{sessions: make(map[string]bool)}
}

// IsWarm returns true if the session has been active this process lifetime.
func (w *WarmTracker) IsWarm(sessionKey string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sessions[sessionKey]
}

// MarkWarm marks a session as warm.
func (w *WarmTracker) MarkWarm(sessionKey string) {
	w.mu.Lock()
	w.sessions[sessionKey] = true
	w.mu.Unlock()
}
