package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// HTTPClient abstracts HTTP calls for testing.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client wraps the Jira REST API.
type Client struct {
	baseURL    string
	httpClient HTTPClient
	auth       Authenticator
	logger     zerolog.Logger
}

// Authenticator applies authentication to requests.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NewClient creates a new Jira API client.
func NewClient(baseURL string, auth Authenticator, logger zerolog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		auth:    auth,
		logger:  logger.With().Str("component", "jira").Logger(),
	}
}

// SetHTTPClient sets a custom HTTP client (for testing).
func (c *Client) SetHTTPClient(hc HTTPClient) {
	c.httpClient = hc
}

// BaseURL returns the base URL of the Jira instance.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// do executes an authenticated API request.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if err := c.auth.Apply(req); err != nil {
		return nil, fmt.Errorf("applying auth: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("jira API error (status %d): %s", resp.StatusCode, respBody)
	}

	return resp, nil
}

// decodeResponse reads and decodes a JSON response.
func decodeResponse(resp *http.Response, v interface{}) error {
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
