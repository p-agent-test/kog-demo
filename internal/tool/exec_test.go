package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecTool_Schema(t *testing.T) {
	tool := NewExecTool("", 0, nil)
	schema := tool.Schema()
	assert.Equal(t, "exec", schema.Name)
	assert.NotEmpty(t, schema.Description)
	assert.NotEmpty(t, schema.InputSchema)
}

func TestExecTool_SimpleCommand(t *testing.T) {
	tool := NewExecTool("", 0, nil)
	input, _ := json.Marshal(execInput{Command: "echo hello"})
	out, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, "hello", strings.TrimSpace(out))
}

func TestExecTool_Stderr(t *testing.T) {
	tool := NewExecTool("", 0, nil)
	input, _ := json.Marshal(execInput{Command: "ls /nonexistent_path_xyz 2>&1"})
	out, err := tool.Execute(context.Background(), input)
	require.NoError(t, err) // ExecTool never returns error, embeds in output
	assert.Contains(t, out, "ERROR")
}

func TestExecTool_WorkDir(t *testing.T) {
	tool := NewExecTool("", 0, nil)
	input, _ := json.Marshal(execInput{Command: "pwd", WorkDir: "/tmp"})
	out, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, "/tmp", strings.TrimSpace(out))
}

func TestExecTool_InvalidInput(t *testing.T) {
	tool := NewExecTool("", 0, nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`invalid`))
	assert.Error(t, err)
}

func TestExecTool_EmptyCommand(t *testing.T) {
	tool := NewExecTool("", 0, nil)
	input, _ := json.Marshal(execInput{Command: ""})
	_, err := tool.Execute(context.Background(), input)
	assert.Error(t, err)
}
