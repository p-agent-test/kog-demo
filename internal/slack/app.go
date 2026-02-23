package slack

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// BotAPI abstracts the Slack API client for testing.
// SECURITY: Only safe methods are exposed. No user enumeration APIs —
// users:read scope removed entirely. Bot uses Slack mention format (<@U123>)
// and never resolves user names.
type BotAPI interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
	GetConversationInfo(input *slack.GetConversationInfoInput) (*slack.Channel, error)
	GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
	AuthTest() (*slack.AuthTestResponse, error)
}

// SafeSlackClient wraps the Slack API client with security restrictions.
// It enforces channel allowlists and blocks bulk user enumeration APIs.
type SafeSlackClient struct {
	inner           *slack.Client
	allowedChannels map[string]bool
	logger          zerolog.Logger
}

// NewSafeSlackClient creates a restricted Slack client.
// allowedChannels is the list of channel IDs the bot is permitted to write to.
// If empty, all channels are denied (fail-closed).
func NewSafeSlackClient(client *slack.Client, allowedChannels []string, logger zerolog.Logger) *SafeSlackClient {
	allowed := make(map[string]bool, len(allowedChannels))
	for _, ch := range allowedChannels {
		allowed[ch] = true
	}
	return &SafeSlackClient{
		inner:           client,
		allowedChannels: allowed,
		logger:          logger.With().Str("component", "slack.safe_client").Logger(),
	}
}

// PostMessage sends a message only if the channel is in the allowlist.
func (s *SafeSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	if !s.allowedChannels[channelID] {
		s.logger.Warn().
			Str("channel_id", channelID).
			Msg("blocked PostMessage to non-allowlisted channel")
		return "", "", fmt.Errorf("channel %s is not in the allowed channels list", channelID)
	}
	return s.inner.PostMessage(channelID, options...)
}

// UpdateMessage updates an existing message (same channel allowlist enforcement).
func (s *SafeSlackClient) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	if !s.allowedChannels[channelID] {
		s.logger.Warn().
			Str("channel_id", channelID).
			Msg("blocked UpdateMessage to non-allowlisted channel")
		return "", "", "", fmt.Errorf("channel %s is not in the allowed channels list", channelID)
	}
	return s.inner.UpdateMessage(channelID, timestamp, options...)
}

// GetConversationInfo returns channel info (read-only, safe).
func (s *SafeSlackClient) GetConversationInfo(input *slack.GetConversationInfoInput) (*slack.Channel, error) {
	return s.inner.GetConversationInfo(input)
}

// GetConversationReplies reads thread history (read-only, safe — no allowlist check).
func (s *SafeSlackClient) GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error) {
	return s.inner.GetConversationReplies(params)
}

// AuthTest tests the bot token.
func (s *SafeSlackClient) AuthTest() (*slack.AuthTestResponse, error) {
	return s.inner.AuthTest()
}

// AddReaction adds a reaction to a message (read-level, safe).
func (s *SafeSlackClient) AddReaction(name string, item slack.ItemRef) error {
	return s.inner.AddReaction(name, item)
}

// RemoveReaction removes a reaction from a message (read-level, safe).
func (s *SafeSlackClient) RemoveReaction(name string, item slack.ItemRef) error {
	return s.inner.RemoveReaction(name, item)
}

// App is the Slack bot application using Socket Mode.
type App struct {
	api    BotAPI
	socket *socketmode.Client
	logger zerolog.Logger
	handler *Handler
}

// NewApp creates a new Slack bot app.
// allowedChannels restricts which channels the bot can write to (fail-closed if empty).
func NewApp(botToken, appToken string, allowedChannels []string, logger zerolog.Logger, handler *Handler) (*App, error) {
	rawAPI := slack.New(
		botToken,
		slack.OptionAppLevelToken(appToken),
	)

	api := NewSafeSlackClient(rawAPI, allowedChannels, logger)
	socket := socketmode.New(rawAPI)
	handler.api = api
	handler.SetSocket(socket)

	return &App{
		api:     api,
		socket:  socket,
		logger:  logger.With().Str("component", "slack").Logger(),
		handler: handler,
	}, nil
}

// AuthTest calls Slack's auth.test to get bot identity info.
func (a *App) AuthTest() (*slack.AuthTestResponse, error) {
	return a.api.AuthTest()
}

// PostMessage posts a message to a Slack channel (via SafeSlackClient).
func (a *App) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	return a.api.PostMessage(channelID, options...)
}

// UpdateMessage updates an existing Slack message.
func (a *App) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	return a.api.UpdateMessage(channelID, timestamp, options...)
}

// AddReaction adds an emoji reaction to a message.
func (a *App) AddReaction(name string, item slack.ItemRef) error {
	return a.api.(*SafeSlackClient).AddReaction(name, item)
}

// RemoveReaction removes an emoji reaction from a message.
func (a *App) RemoveReaction(name string, item slack.ItemRef) error {
	return a.api.(*SafeSlackClient).RemoveReaction(name, item)
}

// GetConversationReplies reads thread messages.
func (a *App) GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error) {
	return a.api.GetConversationReplies(params)
}

// Run starts the Socket Mode event loop. Blocks until context is cancelled.
func (a *App) Run(ctx context.Context) error {
	a.logger.Info().Msg("starting Slack Socket Mode connection")

	go func() {
		for evt := range a.socket.Events {
			a.handler.HandleEvent(ctx, evt)
		}
	}()

	go func() {
		<-ctx.Done()
		a.logger.Info().Msg("shutting down Slack Socket Mode")
	}()

	if err := a.socket.RunContext(ctx); err != nil {
		return fmt.Errorf("socket mode error: %w", err)
	}
	return nil
}
