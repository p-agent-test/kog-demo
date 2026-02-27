package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicAPIBase    = "https://api.anthropic.com/v1"
	anthropicAPIVersion = "2023-06-01"
	defaultMaxTokens    = 4096
	defaultModel        = "claude-sonnet-4-5"
)

// AnthropicProvider implements LLMProvider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
	logger    *slog.Logger
}

// AnthropicOption configures the provider.
type AnthropicOption func(*AnthropicProvider)

func WithModel(model string) AnthropicOption {
	return func(p *AnthropicProvider) { p.model = model }
}

func WithMaxTokens(n int) AnthropicOption {
	return func(p *AnthropicProvider) { p.maxTokens = n }
}

func WithHTTPClient(c *http.Client) AnthropicOption {
	return func(p *AnthropicProvider) { p.client = c }
}

func WithLogger(l *slog.Logger) AnthropicOption {
	return func(p *AnthropicProvider) { p.logger = l }
}

// NewAnthropicProvider constructs a new Anthropic provider.
func NewAnthropicProvider(apiKey string, opts ...AnthropicOption) *AnthropicProvider {
	p := &AnthropicProvider{
		apiKey:    apiKey,
		model:     defaultModel,
		maxTokens: defaultMaxTokens,
		client:    &http.Client{Timeout: 120 * time.Second},
		logger:    slog.Default(),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *AnthropicProvider) ModelID() string  { return p.model }
func (p *AnthropicProvider) MaxTokens() int   { return p.maxTokens }

// ---- Anthropic wire types ----

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content interface{}             `json:"content"` // string or []contentBlock
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// buildMessages converts []Message to []anthropicMessage, handling tool results.
func buildMessages(msgs []Message) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.ToolResult != nil {
			// Tool results are user messages with content blocks
			block := map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": m.ToolResult.ToolUseID,
				"content":     m.ToolResult.Content,
			}
			if m.ToolResult.IsError {
				block["is_error"] = true
			}
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: []interface{}{block},
			})
		} else {
			out = append(out, anthropicMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}
	return out
}

func (p *AnthropicProvider) buildRequest(req CompletionRequest, stream bool) anthropicRequest {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}
	maxTok := p.maxTokens
	if req.MaxTokens > 0 {
		maxTok = req.MaxTokens
	}

	ar := anthropicRequest{
		Model:     model,
		MaxTokens: maxTok,
		System:    req.SystemPrompt,
		Messages:  buildMessages(req.Messages),
		Stream:    stream,
	}
	for _, t := range req.Tools {
		ar.Tools = append(ar.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return ar
}

func (p *AnthropicProvider) doRequest(ctx context.Context, ar anthropicRequest) (*http.Response, error) {
	body, err := json.Marshal(ar)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		anthropicAPIBase+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	return p.client.Do(httpReq)
}

// Complete sends a blocking completion request.
func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	ar := p.buildRequest(req, false)
	resp, err := p.doRequest(ctx, ar)
	if err != nil {
		return nil, fmt.Errorf("anthropic http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var ar2 anthropicResponse
	if err := json.Unmarshal(raw, &ar2); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if ar2.Error != nil {
		return nil, fmt.Errorf("anthropic api error %s: %s", ar2.Error.Type, ar2.Error.Message)
	}

	out := &CompletionResponse{
		StopReason:   ar2.StopReason,
		InputTokens:  ar2.Usage.InputTokens,
		OutputTokens: ar2.Usage.OutputTokens,
	}

	for _, block := range ar2.Content {
		switch block.Type {
		case "text":
			out.Text += block.Text
		case "tool_use":
			out.ToolUse = &ToolUse{
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			}
		}
	}

	p.logger.Debug("anthropic complete",
		"model", ar.Model,
		"stop_reason", out.StopReason,
		"in_tokens", out.InputTokens,
		"out_tokens", out.OutputTokens,
	)
	return out, nil
}

// Stream sends a completion request and streams Server-Sent Events to out.
func (p *AnthropicProvider) Stream(ctx context.Context, req CompletionRequest, out chan<- Token) error {
	ar := p.buildRequest(req, true)
	resp, err := p.doRequest(ctx, ar)
	if err != nil {
		return fmt.Errorf("anthropic stream http: %w", err)
	}

	go func() {
		defer resp.Body.Close()
		defer close(out)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				out <- Token{Done: true}
				return
			}

			var ev map[string]interface{}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}

			// Content block delta
			if evType, _ := ev["type"].(string); evType == "content_block_delta" {
				if delta, ok := ev["delta"].(map[string]interface{}); ok {
					if text, ok := delta["text"].(string); ok {
						select {
						case out <- Token{Text: text}:
						case <-ctx.Done():
							out <- Token{Error: ctx.Err()}
							return
						}
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- Token{Error: err}
		}
	}()

	return nil
}
