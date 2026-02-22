package bridge

import (
	"github.com/slack-go/slack"
)

// slackPosterAdapter wraps a Slack BotAPI to implement SlackPoster.
type slackPosterAdapter struct {
	api interface {
		PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	}
}

// NewSlackPoster creates a SlackPoster from a Slack API client.
func NewSlackPoster(api interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}) SlackPoster {
	return &slackPosterAdapter{api: api}
}

func (s *slackPosterAdapter) PostMessage(channelID, text string) (string, error) {
	_, ts, err := s.api.PostMessage(channelID, slack.MsgOptionText(text, false))
	return ts, err
}
