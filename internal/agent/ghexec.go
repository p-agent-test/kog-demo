package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/p-blackswan/platform-agent/internal/models"
)

// ghExecTimeout is the maximum duration for a gh CLI command.
const ghExecTimeout = 30 * time.Second

// commandClass defines the security classification of a gh command.
type commandClass int

const (
	classRead      commandClass = iota // auto-approve
	classWrite                         // require-approval
	classDangerous                     // always-deny
)

// GHExecParams are the params for github.exec.
type GHExecParams struct {
	Command  string `json:"command"`  // e.g. "gh pr list --repo p-agent-test/p-agent"
	CallerID string `json:"caller_id"`
}

// GHExecResult is the result of github.exec.
type GHExecResult struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// approvedWriteCommands are the only write commands allowed (with approval).
// Format: "gh <subcommand> <action>"
var approvedWriteCommands = map[string]bool{
	"gh pr create":      true,
	"gh pr comment":     true,
	"gh pr review":      true,
	"gh issue create":   true,
	"gh issue comment":  true,
	"gh repo create":    true,
}

// readCommands are safe read-only commands (auto-approved).
var readCommands = map[string]bool{
	"gh pr list":      true,
	"gh pr view":      true,
	"gh pr diff":      true,
	"gh pr checks":    true,
	"gh pr status":    true,
	"gh issue list":   true,
	"gh issue view":   true,
	"gh issue status": true,
	"gh run list":     true,
	"gh run view":     true,
	"gh run log":      true, // typo-friendly alias
	"gh run watch":    true,
	"gh repo view":    true,
	"gh repo list":    true,
	"gh api":          true, // GET requests only, checked separately
}

// classifyCommand determines the security class of a gh command.
func classifyCommand(command string) commandClass {
	cmd := strings.TrimSpace(command)

	// Must start with "gh"
	if !strings.HasPrefix(cmd, "gh ") && cmd != "gh" {
		return classDangerous
	}

	// Extract "gh <subcommand> <action>" (first 3 tokens)
	parts := strings.Fields(cmd)
	if len(parts) < 2 {
		return classDangerous
	}

	// Check 3-token match first: "gh pr list"
	if len(parts) >= 3 {
		key3 := strings.Join(parts[:3], " ")
		if readCommands[key3] {
			return classRead
		}
		if approvedWriteCommands[key3] {
			return classWrite
		}
	}

	// Check 2-token match: "gh api"
	key2 := strings.Join(parts[:2], " ")
	if readCommands[key2] {
		// Special case: "gh api" — only allow GET (no -X POST/PUT/DELETE/PATCH)
		if key2 == "gh api" {
			// Check each arg for -X or --method with a mutating HTTP method
			for i, arg := range parts {
				upperArg := strings.ToUpper(arg)
				if (upperArg == "-X" || upperArg == "--METHOD") && i+1 < len(parts) {
					method := strings.ToUpper(parts[i+1])
					if method == "POST" || method == "PUT" || method == "DELETE" || method == "PATCH" {
						return classDangerous
					}
				}
			}
		}
		return classRead
	}

	// Everything else is dangerous
	return classDangerous
}

func (a *Agent) executeGHExec(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p GHExecParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Classify the command
	class := classifyCommand(p.Command)

	switch class {
	case classDangerous:
		a.audit.Record(models.AuditEntry{
			UserID:   p.CallerID,
			Action:   "github.exec",
			Resource: p.Command,
			Result:   "denied",
			Details:  "command not in allowlist",
		})
		return nil, fmt.Errorf("command not allowed: only specific read/write gh commands are permitted. Allowed writes: pr create/comment/review, issue create/comment, repo create")

	case classWrite:
		// Check supervisor approval
		permResult, err := a.supervisor.RequestPermissions(ctx, "github.exec.write", p.CallerID, "")
		if err != nil {
			return nil, fmt.Errorf("permission check failed: %w", err)
		}
		if !permResult.AllGranted {
			// Send approval buttons
			if a.slack != nil && a.supervisorChannel != "" {
				reqID := ""
			if len(permResult.Pending) > 0 {
				reqID = permResult.Pending[0].RequestID
			}
			a.sendApprovalButtons(a.supervisorChannel, "", reqID, p.CallerID, "github.exec", p.Command)
			}

			a.audit.Record(models.AuditEntry{
				UserID:   p.CallerID,
				Action:   "github.exec",
				Resource: p.Command,
				Result:   "pending_approval",
			})

			if len(permResult.Denied) > 0 {
				return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
			}
			return nil, fmt.Errorf("permission pending approval")
		}

	case classRead:
		// Auto-approve, just log it
		a.logger.Debug().Str("command", p.Command).Msg("auto-approved read command")
	}

	// Execute the command
	execCtx, cancel := context.WithTimeout(ctx, ghExecTimeout)
	defer cancel()

	// Parse command into args (shell-like splitting)
	args := shellSplit(p.Command)
	if len(args) < 1 {
		return nil, fmt.Errorf("empty command")
	}

	// args[0] should be "gh"
	cmd := exec.CommandContext(execCtx, args[0], args[1:]...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to run command: %w", err)
		}
	}

	output := stdout.String()
	if output == "" && stderr.String() != "" {
		output = stderr.String()
	}

	// Truncate output to prevent huge responses
	const maxOutput = 50000
	truncated := false
	if len(output) > maxOutput {
		output = output[:maxOutput]
		truncated = true
	}

	result := GHExecResult{
		Command:  p.Command,
		Output:   output,
		ExitCode: exitCode,
	}
	if truncated {
		result.Output += "\n... (truncated)"
	}

	status := "completed"
	if exitCode != 0 {
		status = fmt.Sprintf("exit_code_%d", exitCode)
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "github.exec",
		Resource: p.Command,
		Result:   status,
		Details:  fmt.Sprintf("exit=%d, output_len=%d", exitCode, len(output)),
	})

	return json.Marshal(result)
}

// shellSplit splits a command string into tokens, respecting quotes.
func shellSplit(s string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}
