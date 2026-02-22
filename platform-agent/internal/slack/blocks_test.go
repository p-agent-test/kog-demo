package slack

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPRReviewBlocks(t *testing.T) {
	files := []PRFileInfo{
		{Name: "main.go", Additions: 10, Deletions: 5},
		{Name: "test.go", Additions: 20, Deletions: 0},
	}
	blocks := PRReviewBlocks("org", "repo", 42, 2, files)
	assert.GreaterOrEqual(t, len(blocks), 3) // header + section + divider + files
}

func TestPRReviewBlocks_ManyFiles(t *testing.T) {
	files := make([]PRFileInfo, 15)
	for i := range files {
		files[i] = PRFileInfo{Name: "file.go", Additions: 1, Deletions: 1}
	}
	blocks := PRReviewBlocks("org", "repo", 1, 15, files)
	// Should have "and N more" block
	assert.GreaterOrEqual(t, len(blocks), 5)
}

func TestDeployConfirmBlocks(t *testing.T) {
	blocks := DeployConfirmBlocks("req-1", "api", "v1.0.0", "staging", "U123")
	assert.Equal(t, 2, len(blocks))
}

func TestApprovalBlocks(t *testing.T) {
	blocks := ApprovalBlocks("req-1", "U123", "deploy", "api")
	assert.Equal(t, 2, len(blocks))
}

func TestLogOutputBlocks(t *testing.T) {
	blocks := LogOutputBlocks("pod-1", "test", "some logs", false)
	assert.Equal(t, 2, len(blocks))

	blocks = LogOutputBlocks("pod-1", "test", "some logs", true)
	assert.Equal(t, 3, len(blocks)) // +truncation notice
}

func TestStatusDashboardBlocks_Empty(t *testing.T) {
	blocks := StatusDashboardBlocks(nil)
	assert.GreaterOrEqual(t, len(blocks), 2)
}

func TestStatusDashboardBlocks_WithActions(t *testing.T) {
	actions := []StatusAction{
		{Action: "review", Details: "org/repo#42", Status: "completed", Time: "2m ago"},
		{Action: "deploy", Details: "api v1.0", Status: "error", Time: "5m ago"},
	}
	blocks := StatusDashboardBlocks(actions)
	assert.GreaterOrEqual(t, len(blocks), 2)
}

func TestHelpBlocks(t *testing.T) {
	blocks := HelpBlocks()
	assert.Equal(t, 2, len(blocks))
}
