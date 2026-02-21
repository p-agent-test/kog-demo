package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noopAuth struct{}

func (n *noopAuth) Apply(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer test-token")
	return nil
}

func setupTestServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := NewClient(server.URL, &noopAuth{}, zerolog.Nop())
	client.SetHTTPClient(server.Client())
	return client, server
}

func TestClient_GetIssue(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/rest/api/3/issue/PLAT-123")
		json.NewEncoder(w).Encode(Issue{
			Key: "PLAT-123",
			Fields: IssueFields{
				Summary: "Test issue",
				Status:  &Status{Name: "In Progress"},
			},
		})
	})
	defer server.Close()

	issue, err := client.GetIssue(context.Background(), "PLAT-123")
	require.NoError(t, err)
	assert.Equal(t, "PLAT-123", issue.Key)
	assert.Equal(t, "Test issue", issue.Fields.Summary)
}

func TestClient_CreateIssue(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Issue{
			ID:  "10001",
			Key: "PLAT-124",
		})
	})
	defer server.Close()

	req := &CreateIssueRequest{}
	req.Fields.Project = Project{Key: "PLAT"}
	req.Fields.Summary = "New task"
	req.Fields.IssueType = IssueType{Name: "Task"}

	issue, err := client.CreateIssue(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "PLAT-124", issue.Key)
}

func TestClient_SearchIssues(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		json.NewEncoder(w).Encode(SearchResult{
			Total: 2,
			Issues: []Issue{
				{Key: "PLAT-1", Fields: IssueFields{Summary: "First"}},
				{Key: "PLAT-2", Fields: IssueFields{Summary: "Second"}},
			},
		})
	})
	defer server.Close()

	result, err := client.SearchIssues(context.Background(), "project = PLAT", 50)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)
	assert.Len(t, result.Issues, 2)
}

func TestClient_TransitionIssue(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/transitions")
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	err := client.TransitionIssue(context.Background(), "PLAT-123", "31")
	require.NoError(t, err)
}

func TestBasicAuth_Apply(t *testing.T) {
	auth := &BasicAuth{Email: "user@example.com", APIToken: "token123"}
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	err := auth.Apply(req)
	require.NoError(t, err)
	assert.Contains(t, req.Header.Get("Authorization"), "Basic ")
}

func TestOAuthAuth_Apply(t *testing.T) {
	auth := NewOAuthAuth("my-access-token", nil)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	err := auth.Apply(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-access-token", req.Header.Get("Authorization"))
}

func TestClient_GetIssue_Error(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errorMessages":["Issue does not exist"]}`))
	})
	defer server.Close()

	_, err := client.GetIssue(context.Background(), "NOPE-999")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestClient_UpdateIssue(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	err := client.UpdateIssue(context.Background(), "PLAT-123", map[string]interface{}{"summary": "Updated"})
	assert.NoError(t, err)
}

func TestClient_GetTransitions(t *testing.T) {
	client, server := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"transitions": []map[string]interface{}{
				{"id": "31", "name": "Done", "to": map[string]string{"name": "Done", "id": "3"}},
			},
		})
	})
	defer server.Close()

	transitions, err := client.GetTransitions(context.Background(), "PLAT-123")
	require.NoError(t, err)
	assert.Len(t, transitions, 1)
	assert.Equal(t, "Done", transitions[0].Name)
}

func TestOAuthAuth_NoToken_NoRefresh(t *testing.T) {
	auth := NewOAuthAuth("", nil)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	err := auth.Apply(req)
	assert.Error(t, err)
}

func TestOAuthAuth_RefreshSuccess(t *testing.T) {
	auth := NewOAuthAuth("", func() (string, error) {
		return "refreshed-token", nil
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	err := auth.Apply(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer refreshed-token", req.Header.Get("Authorization"))
}

func TestOAuthAuth_RefreshError(t *testing.T) {
	auth := NewOAuthAuth("", func() (string, error) {
		return "", fmt.Errorf("refresh failed")
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	err := auth.Apply(req)
	assert.Error(t, err)
}

func TestClient_BaseURL(t *testing.T) {
	client := NewClient("https://test.atlassian.net/", &noopAuth{}, zerolog.Nop())
	assert.Equal(t, "https://test.atlassian.net", client.BaseURL())
}

func TestWebhookHandler_InvalidJSON(t *testing.T) {
	handler := NewWebhookHandler(zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestWebhookHandler_UnhandledEvent(t *testing.T) {
	handler := NewWebhookHandler(zerolog.Nop())
	payload := `{"webhookEvent":"jira:issue_deleted"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(payload))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWebhookHandler_IssueUpdated(t *testing.T) {
	handler := NewWebhookHandler(zerolog.Nop())
	updated := make(chan bool, 1)
	handler.OnIssueUpdated(func(ctx context.Context, event *WebhookEvent) {
		updated <- true
	})

	payload := `{"webhookEvent":"jira:issue_updated","issue":{"key":"PLAT-1"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(payload))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWebhookHandler_IssueCreated(t *testing.T) {
	handler := NewWebhookHandler(zerolog.Nop())
	created := make(chan string, 1)
	handler.OnIssueCreated(func(ctx context.Context, event *WebhookEvent) {
		created <- event.Issue.Key
	})

	payload := `{"webhookEvent":"jira:issue_created","issue":{"key":"PLAT-100","fields":{"summary":"New"}}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
