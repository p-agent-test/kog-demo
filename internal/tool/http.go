package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/p-blackswan/platform-agent/internal/llm"
)

// HTTPTool makes HTTP requests. Useful for calling APIs, webhooks, etc.
type HTTPTool struct {
	client  *http.Client
	maxBody int
	logger  *slog.Logger
}

// NewHTTPTool creates an HTTPTool.
func NewHTTPTool(logger *slog.Logger) *HTTPTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPTool{
		client:  &http.Client{Timeout: 30 * time.Second},
		maxBody: 64 * 1024, // 64KB response limit
		logger:  logger,
	}
}

type httpInput struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type httpOutput struct {
	Status  int    `json:"status"`
	Body    string `json:"body"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (t *HTTPTool) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name:        "http_request",
		Description: "Make an HTTP request (GET, POST, PUT, DELETE, PATCH). Returns status code and response body.",
		InputSchema: MustSchema(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"method": map[string]string{
					"type":        "string",
					"description": "HTTP method: GET, POST, PUT, DELETE, PATCH",
				},
				"url": map[string]string{
					"type":        "string",
					"description": "Full URL including query parameters",
				},
				"headers": map[string]interface{}{
					"type":        "object",
					"description": "Optional request headers",
					"additionalProperties": map[string]string{"type": "string"},
				},
				"body": map[string]string{
					"type":        "string",
					"description": "Request body (for POST/PUT/PATCH)",
				},
			},
			"required": []string{"method", "url"},
		}),
	}
}

func (t *HTTPTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var inp httpInput
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", fmt.Errorf("http: unmarshal input: %w", err)
	}

	method := strings.ToUpper(inp.Method)
	var bodyReader io.Reader
	if inp.Body != "" {
		bodyReader = bytes.NewBufferString(inp.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, inp.URL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("http: create request: %w", err)
	}

	for k, v := range inp.Headers {
		req.Header.Set(k, v)
	}

	t.logger.Debug("http tool", "method", method, "url", inp.URL)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, int64(t.maxBody))
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("http: read body: %w", err)
	}

	out := httpOutput{
		Status: resp.StatusCode,
		Body:   string(body),
	}
	result, _ := json.Marshal(out)
	return string(result), nil
}
