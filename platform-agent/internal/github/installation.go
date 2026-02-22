package github

import (
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
