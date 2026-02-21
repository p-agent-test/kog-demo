package slack

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSlackAPI implements BotAPI for testing.
type mockSlackAPI struct {
	postedMessages []postedMessage
}

type postedMessage struct {
	ChannelID string
	Options   []slack.MsgOption
}

func (m *mockSlackAPI) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	m.postedMessages = append(m.postedMessages, postedMessage{
		ChannelID: channelID,
		Options:   options,
	})
	return channelID, "1234567890.123456", nil
}

func (m *mockSlackAPI) GetConversationInfo(_ *slack.GetConversationInfoInput) (*slack.Channel, error) {
	return &slack.Channel{}, nil
}

func (m *mockSlackAPI) AuthTest() (*slack.AuthTestResponse, error) {
	return &slack.AuthTestResponse{UserID: "U123BOT"}, nil
}

func TestHandler_SendApprovalRequest(t *testing.T) {
	logger := zerolog.Nop()
	mw := NewMiddleware(logger, 10, time.Minute)
	h := NewHandler(logger, mw)
	mock := &mockSlackAPI{}
	h.api = mock

	err := h.SendApprovalRequest(
		context.Background(),
		"C123CHANNEL",
		"req-001",
		"U123USER",
		"deploy",
		"production/api-server",
	)
	require.NoError(t, err)
	assert.Len(t, mock.postedMessages, 1)
	assert.Equal(t, "C123CHANNEL", mock.postedMessages[0].ChannelID)
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, time.Second)

	// First 3 should pass
	assert.True(t, rl.Allow("user1"))
	assert.True(t, rl.Allow("user1"))
	assert.True(t, rl.Allow("user1"))

	// 4th should fail
	assert.False(t, rl.Allow("user1"))

	// Different user should pass
	assert.True(t, rl.Allow("user2"))

	// After window expires, should pass again
	time.Sleep(1100 * time.Millisecond)
	assert.True(t, rl.Allow("user1"))
}

func TestMiddleware_CheckRateLimit(t *testing.T) {
	logger := zerolog.Nop()
	mw := NewMiddleware(logger, 2, time.Second)

	assert.True(t, mw.CheckRateLimit("user1"))
	assert.True(t, mw.CheckRateLimit("user1"))
	assert.False(t, mw.CheckRateLimit("user1"))
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	rl := NewRateLimiter(1, 50*time.Millisecond)
	assert.True(t, rl.Allow("u1"))
	assert.False(t, rl.Allow("u1"))
	time.Sleep(60 * time.Millisecond)
	assert.True(t, rl.Allow("u1"))
}

func TestRateLimiter_MultipleUsers(t *testing.T) {
	rl := NewRateLimiter(1, time.Second)
	assert.True(t, rl.Allow("u1"))
	assert.True(t, rl.Allow("u2"))
	assert.True(t, rl.Allow("u3"))
	assert.False(t, rl.Allow("u1"))
}

func TestNewHandler(t *testing.T) {
	logger := zerolog.Nop()
	mw := NewMiddleware(logger, 10, time.Minute)
	h := NewHandler(logger, mw)
	assert.NotNil(t, h)
	assert.NotNil(t, h.middleware)
}
