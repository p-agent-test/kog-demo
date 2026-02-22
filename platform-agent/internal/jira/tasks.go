package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Issue represents a Jira issue.
type Issue struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Self   string      `json:"self"`
	Fields IssueFields `json:"fields"`
}

// IssueFields contains Jira issue field data.
type IssueFields struct {
	Summary     string       `json:"summary"`
	Description string       `json:"description,omitempty"`
	Status      *Status      `json:"status,omitempty"`
	Assignee    *User        `json:"assignee,omitempty"`
	Reporter    *User        `json:"reporter,omitempty"`
	Project     *Project     `json:"project,omitempty"`
	IssueType   *IssueType   `json:"issuetype,omitempty"`
	Priority    *Priority    `json:"priority,omitempty"`
	Labels      []string     `json:"labels,omitempty"`
	Sprint      *Sprint      `json:"sprint,omitempty"`
}

type Status struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"emailAddress,omitempty"`
}

type Project struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

type IssueType struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

type Priority struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

type Sprint struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// SearchResult contains JQL search results.
type SearchResult struct {
	Total      int     `json:"total"`
	MaxResults int     `json:"maxResults"`
	StartAt    int     `json:"startAt"`
	Issues     []Issue `json:"issues"`
}

// Transition represents a Jira issue transition.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   Status `json:"to"`
}

// CreateIssueRequest is the payload for creating an issue.
type CreateIssueRequest struct {
	Fields struct {
		Project     Project    `json:"project"`
		Summary     string     `json:"summary"`
		Description string     `json:"description,omitempty"`
		IssueType   IssueType  `json:"issuetype"`
		Priority    *Priority  `json:"priority,omitempty"`
		Labels      []string   `json:"labels,omitempty"`
		Assignee    *User      `json:"assignee,omitempty"`
	} `json:"fields"`
}

// GetIssue fetches an issue by key.
func (c *Client) GetIssue(ctx context.Context, issueKey string) (*Issue, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), nil)
	if err != nil {
		return nil, fmt.Errorf("getting issue %s: %w", issueKey, err)
	}

	var issue Issue
	if err := decodeResponse(resp, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// CreateIssue creates a new issue.
func (c *Client) CreateIssue(ctx context.Context, req *CreateIssueRequest) (*Issue, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	resp, err := c.do(ctx, "POST", "/rest/api/3/issue", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating issue: %w", err)
	}

	var issue Issue
	if err := decodeResponse(resp, &issue); err != nil {
		return nil, err
	}

	c.logger.Info().Str("key", issue.Key).Msg("issue created")
	return &issue, nil
}

// UpdateIssue updates an existing issue.
func (c *Client) UpdateIssue(ctx context.Context, issueKey string, fields map[string]interface{}) error {
	body, err := json.Marshal(map[string]interface{}{"fields": fields})
	if err != nil {
		return fmt.Errorf("marshaling update: %w", err)
	}

	_, err = c.do(ctx, "PUT", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("updating issue %s: %w", issueKey, err)
	}
	return nil
}

// TransitionIssue transitions an issue to a new status.
func (c *Client) TransitionIssue(ctx context.Context, issueKey, transitionID string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"transition": map[string]string{"id": transitionID},
	})

	_, err := c.do(ctx, "POST", fmt.Sprintf("/rest/api/3/issue/%s/transitions", issueKey), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("transitioning issue %s: %w", issueKey, err)
	}
	return nil
}

// GetTransitions lists available transitions for an issue.
func (c *Client) GetTransitions(ctx context.Context, issueKey string) ([]Transition, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/rest/api/3/issue/%s/transitions", issueKey), nil)
	if err != nil {
		return nil, fmt.Errorf("getting transitions for %s: %w", issueKey, err)
	}

	var result struct {
		Transitions []Transition `json:"transitions"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Transitions, nil
}

// SearchIssues performs a JQL search.
func (c *Client) SearchIssues(ctx context.Context, jql string, maxResults int) (*SearchResult, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "status", "assignee", "priority", "labels", "sprint"},
	})

	resp, err := c.do(ctx, "POST", "/rest/api/3/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("searching issues: %w", err)
	}

	var result SearchResult
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
