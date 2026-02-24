package bridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
)

type mockPoster struct {
	mu       sync.Mutex
	messages []struct {
		channel  string
		text     string
		threadTS string
	}
}

func (m *mockPoster) PostMessage(channelID, text, threadTS string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, struct {
		channel  string
		text     string
		threadTS string
	}{channelID, text, threadTS})
	return "1234.5678", nil
}

func (m *mockPoster) PostBlocks(channelID, threadTS, fallbackText string, blocks ...slack.Block) (string, error) {
	return "1234.5678", nil
}
func (m *mockPoster) UpdateMessage(_, _, _ string) error   { return nil }
func (m *mockPoster) AddReaction(_, _, _ string) error    { return nil }
func (m *mockPoster) RemoveReaction(_, _, _ string) error { return nil }

func (m *mockPoster) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

func TestBridgeSkipsBotMessages(t *testing.T) {
	poster := &mockPoster{}
	b := New(Config{
		BotUserID:     "U_BOT",
		MaxConcurrent: 1,
	}, poster, zerolog.Nop())

	b.HandleMessage(context.Background(), "C123", "U_BOT", "hello", "", "")
	time.Sleep(100 * time.Millisecond)

	if poster.count() != 0 {
		t.Error("should not post for bot's own messages")
	}
}

func TestBridgeSkipsEmptyMessage(t *testing.T) {
	poster := &mockPoster{}
	b := New(Config{MaxConcurrent: 1}, poster, zerolog.Nop())

	b.HandleMessage(context.Background(), "C123", "U_USER", "", "", "")
	b.HandleMessage(context.Background(), "C123", "U_USER", "   ", "", "")
	time.Sleep(100 * time.Millisecond)

	if poster.count() != 0 {
		t.Error("should not post for empty messages")
	}
}

func TestBridgeStripsMention(t *testing.T) {
	poster := &mockPoster{}
	b := New(Config{
		BotUserID:     "U_BOT",
		MaxConcurrent: 1,
	}, poster, zerolog.Nop())

	// Message that's only a mention with no actual text → skip
	b.HandleMessage(context.Background(), "C123", "U_USER", "<@U_BOT>", "", "")
	b.HandleMessage(context.Background(), "C123", "U_USER", "<@U_BOT>  ", "", "")
	time.Sleep(100 * time.Millisecond)

	if poster.count() != 0 {
		t.Error("should skip mention-only messages")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello…"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", cfg.MaxConcurrent)
	}
	if cfg.DefaultTimeout != 120*time.Second {
		t.Errorf("DefaultTimeout = %v, want 120s", cfg.DefaultTimeout)
	}
	if cfg.SessionPrefix != "slack" {
		t.Errorf("SessionPrefix = %q, want slack", cfg.SessionPrefix)
	}
}
