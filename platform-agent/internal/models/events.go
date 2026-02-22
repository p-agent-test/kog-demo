package models

import "time"

// EventType identifies the kind of internal event.
type EventType string

const (
	EventSlackMessage      EventType = "slack.message"
	EventSlackCommand      EventType = "slack.command"
	EventSlackInteraction  EventType = "slack.interaction"
	EventGitHubPROpened    EventType = "github.pr.opened"
	EventGitHubPRReview    EventType = "github.pr.review_requested"
	EventGitHubCIStatus    EventType = "github.ci.status"
	EventJiraIssueCreated  EventType = "jira.issue.created"
	EventJiraIssueUpdated  EventType = "jira.issue.updated"
	EventJiraTransition    EventType = "jira.issue.transitioned"
	EventAccessRequest     EventType = "supervisor.access_request"
	EventAccessApproved    EventType = "supervisor.access_approved"
	EventAccessDenied      EventType = "supervisor.access_denied"
)

// Event is an internal event passed between components.
type Event struct {
	ID        string            `json:"id"`
	Type      EventType         `json:"type"`
	Source    string            `json:"source"`
	Timestamp time.Time         `json:"timestamp"`
	UserID    string            `json:"user_id,omitempty"`
	Payload   map[string]string `json:"payload,omitempty"`
}

// SlackMessagePayload holds Slack-specific message data.
type SlackMessagePayload struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	UserID    string `json:"user_id"`
	Text      string `json:"text"`
	IsDM      bool   `json:"is_dm"`
}

// GitHubPRPayload holds GitHub PR event data.
type GitHubPRPayload struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	Action   string `json:"action"`
	HTMLURL  string `json:"html_url"`
}

// JiraIssuePayload holds Jira issue event data.
type JiraIssuePayload struct {
	IssueKey   string `json:"issue_key"`
	ProjectKey string `json:"project_key"`
	Summary    string `json:"summary"`
	Status     string `json:"status"`
	Assignee   string `json:"assignee,omitempty"`
}
