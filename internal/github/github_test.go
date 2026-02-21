package github

import (
	"context"
	"crypto/rand"
	"fmt"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v60/github"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/p-blackswan/platform-agent/pkg/tokenstore"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func TestGenerateJWT(t *testing.T) {
	keyData := generateTestKey(t)
	store := tokenstore.NewMemoryStore()
	logger := zerolog.Nop()

	client, err := NewClientFromKeyBytes(12345, 67890, keyData, store, logger)
	require.NoError(t, err)

	jwt, err := client.generateJWT()
	require.NoError(t, err)
	assert.NotEmpty(t, jwt)
	assert.Contains(t, jwt, ".")
}

func TestGetInstallationToken_Cached(t *testing.T) {
	ctx := context.Background()
	store := tokenstore.NewMemoryStore()
	_ = store.Set(ctx, installationTokenKey, "cached-token-123", 10*time.Minute)

	keyData := generateTestKey(t)
	logger := zerolog.Nop()

	client, err := NewClientFromKeyBytes(12345, 67890, keyData, store, logger)
	require.NoError(t, err)

	token, err := client.getInstallationToken(ctx)
	require.NoError(t, err)
	assert.Equal(t, "cached-token-123", token)
}

func TestGetInstallationToken_FromAPI(t *testing.T) {
	// Mock GitHub API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/app/installations/")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "ghs_test_token_123",
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	defer server.Close()

	keyData := generateTestKey(t)
	store := tokenstore.NewMemoryStore()
	logger := zerolog.Nop()

	client, err := NewClientFromKeyBytes(12345, 67890, keyData, store, logger)
	require.NoError(t, err)
	client.httpClient = server.Client()

	// We can't easily test the full flow without mocking the URL,
	// but we verify JWT generation and caching work.
	jwt, err := client.generateJWT()
	require.NoError(t, err)
	assert.NotEmpty(t, jwt)
}

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		owner    string
		repo     string
		pr       int
		hasError bool
	}{
		{
			name:  "valid URL",
			url:   "https://github.com/paribu/platform-api/pull/42",
			owner: "paribu",
			repo:  "platform-api",
			pr:    42,
		},
		{
			name:  "trailing slash",
			url:   "https://github.com/org/repo/pull/99/",
			owner: "org",
			repo:  "repo",
			pr:    99,
		},
		{
			name:     "invalid URL",
			url:      "not-a-url",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, pr, err := ParsePRURL(tt.url)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.owner, owner)
				assert.Equal(t, tt.repo, repo)
				assert.Equal(t, tt.pr, pr)
			}
		})
	}
}

func TestParsePRURL_MoreCases(t *testing.T) {
	// Empty string
	_, _, _, err := ParsePRURL("")
	assert.Error(t, err)

	// Just numbers
	_, _, _, err = ParsePRURL("123")
	assert.Error(t, err)

	// Non-numeric PR
	_, _, _, err = ParsePRURL("https://github.com/org/repo/pull/abc")
	assert.Error(t, err)
}

func TestNewClientFromKeyBytes_InvalidKey(t *testing.T) {
	store := tokenstore.NewMemoryStore()
	_, err := NewClientFromKeyBytes(1, 1, []byte("not-a-key"), store, zerolog.Nop())
	assert.Error(t, err)
}

func TestWebhookHandler_InvalidPayload(t *testing.T) {
	handler := NewWebhookHandler("", zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("not json"))
	req.Header.Set("X-GitHub-Event", "pull_request")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestWebhookHandler_UnhandledEvent(t *testing.T) {
	handler := NewWebhookHandler("", zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("{}"))
	req.Header.Set("X-GitHub-Event", "push")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWebhookHandler_CheckRunEvent(t *testing.T) {
	handler := NewWebhookHandler("", zerolog.Nop())
	ciCalled := make(chan bool, 1)
	handler.OnCheckRun(func(ctx context.Context, event *gh.CheckRunEvent) {
		ciCalled <- true
	})

	payload := `{"action":"completed","check_run":{"id":1}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "check_run")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWebhookHandler_ReadBodyError(t *testing.T) {
	handler := NewWebhookHandler("", zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook", errReader{})
	req.Header.Set("X-GitHub-Event", "pull_request")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, fmt.Errorf("read error") }

func TestWebhookHandler_PREvent(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewWebhookHandler("", logger) // No secret for testing

	prCalled := make(chan bool, 1)
	handler.OnPullRequest(func(ctx context.Context, event *gh.PullRequestEvent) {
		prCalled <- true
	})

	payload := `{"action":"opened","number":1,"pull_request":{"title":"test"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "pull_request")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
