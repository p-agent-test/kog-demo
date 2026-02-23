package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	installationTokenKey = "github_installation_token"
	tokenTTL             = 55 * time.Minute // Tokens last 1 hour, refresh at 55 min
)

// installationTokenResponse mirrors the GitHub API response.
type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// getInstallationToken returns a cached or freshly generated installation token.
func (c *Client) getInstallationToken(ctx context.Context) (string, error) {
	// Check cache first
	tok, err := c.tokenStore.Get(ctx, installationTokenKey)
	if err == nil {
		c.logger.Debug().Msg("using cached installation token")
		return tok.Value, nil
	}

	// Generate new token via GitHub API
	c.logger.Info().Msg("generating new installation token")
	jwtToken, err := c.generateJWT()
	if err != nil {
		return "", fmt.Errorf("generating JWT: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", c.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("installation token request failed (status %d): %s", resp.StatusCode, body)
	}

	var tokenResp installationTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	// Cache the token
	if err := c.tokenStore.Set(ctx, installationTokenKey, tokenResp.Token, tokenTTL); err != nil {
		c.logger.Warn().Err(err).Msg("failed to cache installation token")
	}

	return tokenResp.Token, nil
}

// ScopedToken holds a scoped installation token with metadata.
type ScopedToken struct {
	Token       string            `json:"token"`
	ExpiresAt   time.Time         `json:"expires_at"`
	Permissions map[string]string `json:"permissions"`
	Repository  string            `json:"repository"`
}

// CreateScopedToken generates a short-lived installation token scoped to a single repo
// with specific permissions. Does NOT use the token cache (each call = fresh token).
func (c *Client) CreateScopedToken(ctx context.Context, repo string, permissions map[string]string) (*ScopedToken, error) {
	jwtToken, err := c.generateJWT()
	if err != nil {
		return nil, fmt.Errorf("generating JWT: %w", err)
	}

	body := struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}{
		Repositories: []string{repo},
		Permissions:  permissions,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", c.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting scoped token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("scoped token request failed (status %d): %s", resp.StatusCode, respBody)
	}

	var tokenResp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	c.logger.Info().
		Str("repo", repo).
		Str("expires", tokenResp.ExpiresAt.Format(time.RFC3339)).
		Msg("created scoped installation token")

	return &ScopedToken{
		Token:       tokenResp.Token,
		ExpiresAt:   tokenResp.ExpiresAt,
		Permissions: permissions,
		Repository:  repo,
	}, nil
}
