package llm

import (
	"encoding/json"
	"testing"
)

func TestToolResultMessage(t *testing.T) {
	msg := ToolResultMessage("tool_123", "output text", false)
	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if msg.ToolResult == nil {
		t.Fatal("expected ToolResult, got nil")
	}
	if msg.ToolResult.ToolUseID != "tool_123" {
		t.Errorf("expected ToolUseID %q, got %q", "tool_123", msg.ToolResult.ToolUseID)
	}
	if msg.ToolResult.Content != "output text" {
		t.Errorf("expected content %q, got %q", "output text", msg.ToolResult.Content)
	}
	if msg.ToolResult.IsError {
		t.Error("expected IsError=false")
	}
}

func TestToolResultMessage_Error(t *testing.T) {
	msg := ToolResultMessage("t1", "fail", true)
	if !msg.ToolResult.IsError {
		t.Error("expected IsError=true")
	}
}

func TestToolSchema_JSONMarshal(t *testing.T) {
	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "test_tool" {
		t.Errorf("unexpected name: %v", m["name"])
	}
}

func TestMessage_Roles(t *testing.T) {
	for _, role := range []string{RoleUser, RoleAssistant, RoleSystem} {
		msg := Message{Role: role, Content: "hello"}
		if msg.Role != role {
			t.Errorf("expected %q, got %q", role, msg.Role)
		}
	}
}
