package github

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v60/github"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/pkg/tokenstore"
)

// Client wraps the GitHub API with App authentication.
type Client struct {
	appID          int64
	installationID int64
	privateKey     *rsa.PrivateKey
	tokenStore     tokenstore.Store
	httpClient     *http.Client
	logger         zerolog.Logger
}

// NewClient creates a new GitHub App client.
func NewClient(appID, installationID int64, privateKeyPath string, store tokenstore.Store, logger zerolog.Logger) (*Client, error) {
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading private key: %w", err)
	}
	return NewClientFromKeyBytes(appID, installationID, keyData, store, logger)
}

// NewClientFromKeyBytes creates a client from PEM key bytes (useful for testing).
func NewClientFromKeyBytes(appID, installationID int64, keyData []byte, store tokenstore.Store, logger zerolog.Logger) (*Client, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(keyData)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	return &Client{
		appID:          appID,
		installationID: installationID,
		privateKey:     key,
		tokenStore:     store,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		logger:         logger.With().Str("component", "github").Logger(),
	}, nil
}

// generateJWT creates a JWT for GitHub App authentication.
func (c *Client) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", c.appID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signed, nil
}

// GetInstallationClient returns a github.Client authenticated with an installation token.
// Uses JIT token generation with caching.
func (c *Client) GetInstallationClient(ctx context.Context) (*github.Client, error) {
	token, err := c.getInstallationToken(ctx)
	if err != nil {
		return nil, err
	}

	client := github.NewClient(&http.Client{
		Transport: &tokenTransport{token: token, base: http.DefaultTransport},
		Timeout:   30 * time.Second,
	})
	return client, nil
}

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "token "+t.token)
	return t.base.RoundTrip(req2)
}
