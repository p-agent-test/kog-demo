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
	GetConversationInfo(input *slack.GetConversationInfoInput) (*slack.Channel, error)
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

// GetConversationInfo returns channel info (read-only, safe).
func (s *SafeSlackClient) GetConversationInfo(input *slack.GetConversationInfoInput) (*slack.Channel, error) {
	return s.inner.GetConversationInfo(input)
}

// AuthTest tests the bot token.
func (s *SafeSlackClient) AuthTest() (*slack.AuthTestResponse, error) {
	return s.inner.AuthTest()
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

	return &App{
		api:     api,
		socket:  socket,
		logger:  logger.With().Str("component", "slack").Logger(),
		handler: handler,
	}, nil
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
