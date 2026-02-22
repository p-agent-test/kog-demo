package bridge

import (
	"github.com/slack-go/slack"
)

// SlackAPI is the minimal Slack API surface needed by the bridge.
type SlackAPI interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	AddReaction(name string, item slack.ItemRef) error
	RemoveReaction(name string, item slack.ItemRef) error
}

// slackPosterAdapter wraps a Slack API to implement SlackPoster.
type slackPosterAdapter struct {
	api SlackAPI
}

// NewSlackPoster creates a SlackPoster from a Slack API client.
func NewSlackPoster(api SlackAPI) SlackPoster {
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

func (s *slackPosterAdapter) AddReaction(channelID, messageTS, emoji string) error {
	return s.api.AddReaction(emoji, slack.ItemRef{Channel: channelID, Timestamp: messageTS})
}

func (s *slackPosterAdapter) RemoveReaction(channelID, messageTS, emoji string) error {
	return s.api.RemoveReaction(emoji, slack.ItemRef{Channel: channelID, Timestamp: messageTS})
}
