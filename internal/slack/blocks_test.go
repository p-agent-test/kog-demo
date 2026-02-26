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

func TestBuildApprovalBlocks_PRCreate(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "pr.create",
		CallerID:  "U123",
		TaskID:    "task-1",
		Params:    []byte(`{"owner":"org","repo":"myrepo","title":"fix: bug","head":"fix/bug","base":"main","body":"This fixes a nasty bug in the system"}`),
	}
	blocks := BuildApprovalBlocks("req-1", ctx)
	assert.Equal(t, 2, len(blocks)) // section + actions
	// Verify summary
	summary := ApprovalSummary(ctx)
	assert.Contains(t, summary, "pr.create")
	assert.Contains(t, summary, "org/myrepo")
	assert.Contains(t, summary, "fix: bug")
}

func TestBuildApprovalBlocks_GitCommit(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "git.commit",
		CallerID:  "U123",
		Params:    []byte(`{"owner":"org","repo":"myrepo","branch":"feat/x","message":"feat: new","files":["a.go","b.go","c.go"]}`),
	}
	blocks := BuildApprovalBlocks("req-2", ctx)
	assert.Equal(t, 2, len(blocks))
	summary := ApprovalSummary(ctx)
	assert.Contains(t, summary, "git.commit")
	assert.Contains(t, summary, "feat: new")
}

func TestBuildApprovalBlocks_CreateBranch(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "git.create-branch",
		CallerID:  "U123",
		Params:    []byte(`{"owner":"org","repo":"myrepo","branch":"feat/new","base":"main"}`),
	}
	blocks := BuildApprovalBlocks("req-3", ctx)
	assert.Equal(t, 2, len(blocks))
}

func TestBuildApprovalBlocks_IssueCreate(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "issue.create",
		CallerID:  "U123",
		Params:    []byte(`{"owner":"org","repo":"myrepo","title":"Bug: crash","labels":["bug","critical"],"body":"Steps to reproduce..."}`),
	}
	blocks := BuildApprovalBlocks("req-4", ctx)
	assert.Equal(t, 2, len(blocks))
	summary := ApprovalSummary(ctx)
	assert.Contains(t, summary, "issue.create")
	assert.Contains(t, summary, "Bug: crash")
}

func TestBuildApprovalBlocks_RepoCreate(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "repo.create",
		CallerID:  "U123",
		Params:    []byte(`{"org":"myorg","name":"new-svc","private":true,"description":"A new service"}`),
	}
	blocks := BuildApprovalBlocks("req-5", ctx)
	assert.Equal(t, 2, len(blocks))
	summary := ApprovalSummary(ctx)
	assert.Contains(t, summary, "repo.create")
	assert.Contains(t, summary, "myorg/new-svc")
}

func TestBuildApprovalBlocks_RepoToken(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "repo.token",
		CallerID:  "U123",
		Params:    []byte(`{"owner":"org","repo":"myrepo","permissions":{"contents":"write","pull_requests":"write"}}`),
	}
	blocks := BuildApprovalBlocks("req-6", ctx)
	assert.Equal(t, 2, len(blocks))
}

func TestBuildApprovalBlocks_Default(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "unknown.op",
		CallerID:  "U123",
		Params:    []byte(`{"foo":"bar"}`),
	}
	blocks := BuildApprovalBlocks("req-7", ctx)
	assert.Equal(t, 2, len(blocks))
	summary := ApprovalSummary(ctx)
	assert.Contains(t, summary, "unknown.op")
}

func TestBuildApprovalBlocks_PRComment(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "pr.comment",
		CallerID:  "U123",
		Params:    []byte(`{"owner":"org","repo":"myrepo","number":42,"body":"LGTM!"}`),
	}
	blocks := BuildApprovalBlocks("req-8", ctx)
	assert.Equal(t, 2, len(blocks))
}

func TestBuildApprovalBlocks_PRReview(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "pr.review",
		CallerID:  "U123",
		Params:    []byte(`{"owner":"org","repo":"myrepo","number":42,"event":"APPROVE","body":"Looks good"}`),
	}
	blocks := BuildApprovalBlocks("req-9", ctx)
	assert.Equal(t, 2, len(blocks))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hel…", truncate("hello", 3))
	assert.Equal(t, "hello", truncate("hello", 5))
}

func TestBuildApprovalBlocks_NilParams(t *testing.T) {
	ctx := ApprovalContext{
		Action:    "github.exec",
		Operation: "pr.create",
		CallerID:  "U123",
		Params:    nil,
	}
	blocks := BuildApprovalBlocks("req-10", ctx)
	assert.Equal(t, 2, len(blocks))
}
