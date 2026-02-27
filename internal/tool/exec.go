package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/p-blackswan/platform-agent/internal/llm"
)

// ExecTool executes shell commands. Use with caution — apply allowlisting in production.
type ExecTool struct {
	workDir string
	timeout time.Duration
	logger  *slog.Logger
}

// NewExecTool creates an ExecTool.
// workDir is the working directory for commands ("" = process cwd).
// timeout is the max duration per command (0 = 30s default).
func NewExecTool(workDir string, timeout time.Duration, logger *slog.Logger) *ExecTool {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ExecTool{workDir: workDir, timeout: timeout, logger: logger}
}

type execInput struct {
	Command string `json:"command"`
	WorkDir string `json:"work_dir,omitempty"`
}

func (t *ExecTool) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name:        "exec",
		Description: "Execute a shell command and return stdout+stderr. Use for file operations, git commands, running tests, etc.",
		InputSchema: MustSchema(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]string{
					"type":        "string",
					"description": "Shell command to execute (passed to /bin/sh -c)",
				},
				"work_dir": map[string]string{
					"type":        "string",
					"description": "Optional working directory override",
				},
			},
			"required": []string{"command"},
		}),
	}
}

func (t *ExecTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var inp execInput
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", fmt.Errorf("exec: unmarshal input: %w", err)
	}
	if inp.Command == "" {
		return "", fmt.Errorf("exec: command is required")
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	dir := t.workDir
	if inp.WorkDir != "" {
		dir = inp.WorkDir
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", inp.Command)
	cmd.Dir = dir

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	t.logger.Debug("exec tool", "command", inp.Command, "dir", dir)

	err := cmd.Run()
	output := strings.TrimSpace(buf.String())

	if err != nil {
		// Return combined output + error (agent needs to see stderr)
		return fmt.Sprintf("ERROR: %v\n%s", err, output), nil
	}
	return output, nil
}
