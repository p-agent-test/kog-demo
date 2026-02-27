// Package llm defines the LLM provider interface and related types.
// Providers are interchangeable behind this interface — Claude today, anything tomorrow.
package llm

import (
	"context"
	"encoding/json"
)

// Role constants for Message.Role.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// StopReason describes why the LLM stopped generating.
const (
	StopReasonEndTurn  = "end_turn"
	StopReasonToolUse  = "tool_use"
	StopReasonMaxTokens = "max_tokens"
)

// ToolUse represents a tool call requested by the LLM.
type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Message is a single turn in the conversation.
type Message struct {
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	ToolUse    *ToolUse  `json:"tool_use,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

// ToolResult is the result returned to the LLM after executing a tool.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolSchema describes a tool's interface for the LLM.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"` // JSON Schema object
}

// CompletionRequest is the input to a provider's Complete() call.
type CompletionRequest struct {
	Messages     []Message
	SystemPrompt string
	Tools        []ToolSchema
	MaxTokens    int
	Temperature  float64
	Model        string // override provider default if set
}

// Token is a single streaming token.
type Token struct {
	Text  string
	Done  bool
	Error error
}

// CompletionResponse is returned by Complete().
type CompletionResponse struct {
	Text         string   // final text (for end_turn)
	StopReason   string   // StopReasonEndTurn | StopReasonToolUse | StopReasonMaxTokens
	ToolUse      *ToolUse // populated when StopReason == StopReasonToolUse
	InputTokens  int
	OutputTokens int
}

// LLMProvider is the core abstraction for language model backends.
// Implementations: AnthropicProvider, (future) OpenAIProvider, OllamaProvider.
type LLMProvider interface {
	// Complete sends a completion request and waits for the full response.
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)

	// Stream sends a completion request and streams tokens to out channel.
	// The caller must drain out until Done==true or an error token arrives.
	Stream(ctx context.Context, req CompletionRequest, out chan<- Token) error

	// ModelID returns the current model identifier string.
	ModelID() string

	// MaxTokens returns the provider's default max output token limit.
	MaxTokens() int
}

// ToolResultMessage creates a Message containing a tool result for the conversation.
func ToolResultMessage(toolUseID, content string, isError bool) Message {
	return Message{
		Role: RoleUser,
		ToolResult: &ToolResult{
			ToolUseID: toolUseID,
			Content:   content,
			IsError:   isError,
		},
	}
}
