package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyCommand_Read(t *testing.T) {
	tests := []string{
		"gh pr list --repo p-agent-test/p-agent",
		"gh pr view 1 --repo p-agent-test/p-agent",
		"gh pr diff 1",
		"gh pr checks 1",
		"gh pr status",
		"gh issue list --repo p-agent-test/p-agent",
		"gh issue view 42",
		"gh issue status",
		"gh run list",
		"gh run view 12345",
		"gh repo view p-agent-test/p-agent",
		"gh repo list p-agent-test",
		"gh api /repos/p-agent-test/p-agent/pulls",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			assert.Equal(t, classRead, classifyCommand(cmd), "expected read for: %s", cmd)
		})
	}
}

func TestClassifyCommand_Write(t *testing.T) {
	tests := []string{
		"gh pr create --repo p-agent-test/p-agent --title test --body test",
		"gh pr comment 1 --body 'looks good'",
		"gh pr review 1 --approve",
		"gh issue create --title 'bug' --body 'desc'",
		"gh issue comment 42 --body 'fixed'",
		"gh repo create p-agent-test/new-repo --private",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			assert.Equal(t, classWrite, classifyCommand(cmd), "expected write for: %s", cmd)
		})
	}
}

func TestClassifyCommand_Dangerous(t *testing.T) {
	tests := []string{
		"gh pr merge 1",
		"gh pr close 1",
		"gh issue close 42",
		"gh repo delete p-agent-test/repo",
		"gh label create bug",
		"gh release create v1.0.0",
		"gh secret set MY_SECRET",
		"gh variable set MY_VAR",
		"rm -rf /",
		"ls -la",
		"gh auth login",
		"gh config set",
		"gh api /repos/owner/repo -X DELETE",
		"gh api /repos/owner/repo --method POST -f name=test",
		"gh api /repos/owner/repo -X PATCH",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			assert.Equal(t, classDangerous, classifyCommand(cmd), "expected dangerous for: %s", cmd)
		})
	}
}

func TestClassifyCommand_EdgeCases(t *testing.T) {
	assert.Equal(t, classDangerous, classifyCommand(""))
	assert.Equal(t, classDangerous, classifyCommand("gh"))
	assert.Equal(t, classDangerous, classifyCommand("not-gh pr list"))
	assert.Equal(t, classRead, classifyCommand("  gh pr list  "))
}

func TestShellSplit(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"gh pr list", []string{"gh", "pr", "list"}},
		{`gh pr create --title "my title" --body "desc"`, []string{"gh", "pr", "create", "--title", "my title", "--body", "desc"}},
		{`gh pr comment 1 --body 'looks good'`, []string{"gh", "pr", "comment", "1", "--body", "looks good"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := shellSplit(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
