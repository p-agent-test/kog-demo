package jira

import (
	"encoding/base64"
	"fmt"
	"net/http"
)

// BasicAuth implements Authenticator with email + API token (development).
type BasicAuth struct {
	Email    string
	APIToken string
}

func (b *BasicAuth) Apply(req *http.Request) error {
	cred := base64.StdEncoding.EncodeToString([]byte(b.Email + ":" + b.APIToken))
	req.Header.Set("Authorization", "Basic "+cred)
	return nil
}

// OAuthAuth implements Authenticator with OAuth 2.0 bearer token.
type OAuthAuth struct {
	AccessToken string
	onRefresh   func() (string, error)
}

// NewOAuthAuth creates an OAuth authenticator.
func NewOAuthAuth(accessToken string, refreshFn func() (string, error)) *OAuthAuth {
	return &OAuthAuth{
		AccessToken: accessToken,
		onRefresh:   refreshFn,
	}
}

func (o *OAuthAuth) Apply(req *http.Request) error {
	if o.AccessToken == "" {
		if o.onRefresh != nil {
			token, err := o.onRefresh()
			if err != nil {
				return fmt.Errorf("refreshing token: %w", err)
			}
			o.AccessToken = token
		} else {
			return fmt.Errorf("no access token available")
		}
	}
	req.Header.Set("Authorization", "Bearer "+o.AccessToken)
	return nil
}
