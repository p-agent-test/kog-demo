// Package kogagent implements the baseAgent — the core LLM+tool+memory loop.
package kogagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/p-blackswan/platform-agent/internal/llm"
	"github.com/p-blackswan/platform-agent/internal/memory"
	"github.com/p-blackswan/platform-agent/internal/tool"
)

// Status values for Agent.Status().
type Status int

const (
	StatusIdle    Status = iota
	StatusRunning Status = iota
	StatusError   Status = iota
)

// Spec describes how to construct an agent.
type Spec struct {
	ID           string
	SystemPrompt string
	Provider     llm.LLMProvider
	Registry     *tool.Registry
	Memory       memory.MemoryStore
	Logger       *slog.Logger
	MaxToolIter  int // max tool-use iterations per Handle call (default: 10)
}

// Agent is the runtime interface for an agent instance.
type Agent interface {
	ID() string
	Handle(ctx context.Context, ev event.Event) error
	Status() Status
}

// baseAgent is the canonical LLM+tool+memory loop implementation.
type baseAgent struct {
	spec   Spec
	logger *slog.Logger
	mu     sync.Mutex
	status Status
}

// New creates a new baseAgent from a Spec.
func New(spec Spec) (Agent, error) {
	if spec.Provider == nil {
		return nil, fmt.Errorf("agent: LLMProvider is required")
	}
	if spec.MaxToolIter == 0 {
		spec.MaxToolIter = 10
	}
	logger := spec.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &baseAgent{spec: spec, logger: logger, status: StatusIdle}, nil
}

func (a *baseAgent) ID() string     { return a.spec.ID }
func (a *baseAgent) Status() Status { a.mu.Lock(); defer a.mu.Unlock(); return a.status }

func (a *baseAgent) setStatus(s Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

// Handle processes a single event through the LLM+tool loop.
func (a *baseAgent) Handle(ctx context.Context, ev event.Event) error {
	a.setStatus(StatusRunning)
	defer a.setStatus(StatusIdle)

	a.logger.Info("agent handling event",
		"agent", a.spec.ID,
		"event_id", ev.ID,
		"event_type", ev.Type,
		"event_source", ev.Source,
	)

	// 1. Retrieve relevant memories as context.
	var memContext string
	if a.spec.Memory != nil {
		payload := string(ev.Payload)
		entries, err := a.spec.Memory.Search(ctx, payload, 5)
		if err == nil && len(entries) > 0 {
			memContext = "\n\n[Relevant memories:]\n"
			for _, e := range entries {
				memContext += fmt.Sprintf("- [%s] %s\n", e.CreatedAt.Format(time.RFC3339), e.Content)
			}
		}
	}

	// 2. Build initial messages.
	userContent := fmt.Sprintf("[Event: source=%s type=%s id=%s]\n%s",
		ev.Source, ev.Type, ev.ID, string(ev.Payload))
	if memContext != "" {
		userContent = memContext + "\n" + userContent
	}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: userContent},
	}

	// 3. Tool schemas.
	var schemas []llm.ToolSchema
	if a.spec.Registry != nil {
		schemas = a.spec.Registry.Schemas()
	}

	// 4. LLM + tool-use loop.
	for iter := 0; iter < a.spec.MaxToolIter; iter++ {
		resp, err := a.spec.Provider.Complete(ctx, llm.CompletionRequest{
			Messages:     msgs,
			SystemPrompt: a.spec.SystemPrompt,
			Tools:        schemas,
		})
		if err != nil {
			a.setStatus(StatusError)
			return fmt.Errorf("agent %s: llm complete: %w", a.spec.ID, err)
		}

		a.logger.Debug("llm response",
			"agent", a.spec.ID,
			"stop_reason", resp.StopReason,
			"iter", iter,
		)

		switch resp.StopReason {
		case llm.StopReasonEndTurn:
			// Save result to memory.
			if a.spec.Memory != nil && resp.Text != "" {
				_ = a.spec.Memory.Save(ctx, memory.MemoryEntry{
					AgentID: a.spec.ID,
					Content: fmt.Sprintf("[response to event %s]: %s", ev.ID, resp.Text),
					Tags:    []string{ev.Source, ev.Type},
				})
			}
			a.logger.Info("agent done", "agent", a.spec.ID, "event_id", ev.ID)
			return nil

		case llm.StopReasonToolUse:
			if resp.ToolUse == nil {
				return fmt.Errorf("agent: stop_reason=tool_use but no tool_use in response")
			}

			// Append assistant message with tool use.
			toolUseJSON, _ := json.Marshal(resp.ToolUse)
			msgs = append(msgs, llm.Message{
				Role:    llm.RoleAssistant,
				Content: fmt.Sprintf("[tool_use: %s]", string(toolUseJSON)),
				ToolUse: resp.ToolUse,
			})

			// Execute the tool.
			toolResult, toolErr := a.executeToolUse(ctx, resp.ToolUse)
			msgs = append(msgs, llm.ToolResultMessage(resp.ToolUse.ID, toolResult, toolErr != nil))

			// Persist tool execution to memory.
			if a.spec.Memory != nil {
				content := fmt.Sprintf("[tool %s called with %s] -> %s",
					resp.ToolUse.Name, string(resp.ToolUse.Input), truncate(toolResult, 200))
				_ = a.spec.Memory.Save(ctx, memory.MemoryEntry{
					AgentID: a.spec.ID,
					Content: content,
					Tags:    []string{"tool", resp.ToolUse.Name},
				})
			}

		case llm.StopReasonMaxTokens:
			return fmt.Errorf("agent %s: hit max tokens limit", a.spec.ID)

		default:
			return fmt.Errorf("agent %s: unknown stop_reason: %s", a.spec.ID, resp.StopReason)
		}
	}

	return fmt.Errorf("agent %s: exceeded max tool iterations (%d)", a.spec.ID, a.spec.MaxToolIter)
}

func (a *baseAgent) executeToolUse(ctx context.Context, tu *llm.ToolUse) (string, error) {
	if a.spec.Registry == nil {
		return "", fmt.Errorf("no tool registry")
	}
	a.logger.Info("executing tool", "tool", tu.Name, "agent", a.spec.ID)
	result, err := a.spec.Registry.Execute(ctx, tu.Name, tu.Input)
	if err != nil {
		a.logger.Error("tool execution error", "tool", tu.Name, "err", err)
		return fmt.Sprintf("tool error: %v", err), err
	}
	return result, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
