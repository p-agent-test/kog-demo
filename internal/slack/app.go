package slack

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// BotAPI abstracts the Slack API client for testing.
type BotAPI interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	GetConversationInfo(input *slack.GetConversationInfoInput) (*slack.Channel, error)
	AuthTest() (*slack.AuthTestResponse, error)
}

// App is the Slack bot application using Socket Mode.
type App struct {
	api    BotAPI
	socket *socketmode.Client
	logger zerolog.Logger
	handler *Handler
}

// NewApp creates a new Slack bot app.
func NewApp(botToken, appToken string, logger zerolog.Logger, handler *Handler) (*App, error) {
	api := slack.New(
		botToken,
		slack.OptionAppLevelToken(appToken),
	)

	socket := socketmode.New(api)
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
