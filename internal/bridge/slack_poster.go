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

func (s *slackPosterAdapter) PostMessage(channelID, text, threadTS string) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := s.api.PostMessage(channelID, opts...)
	return ts, err
}
