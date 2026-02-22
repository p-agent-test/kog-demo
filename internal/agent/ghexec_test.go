package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyOperation_Read(t *testing.T) {
	readOps := []string{
		"pr.list", "pr.get", "pr.diff", "pr.files", "pr.checks",
		"issue.list", "issue.get",
		"repo.get", "repo.list",
		"run.list", "run.get",
	}
	for _, op := range readOps {
		t.Run(op, func(t *testing.T) {
			assert.Equal(t, classRead, classifyOperation(op), "expected read for: %s", op)
		})
	}
}

func TestClassifyOperation_Write(t *testing.T) {
	writeOps := []string{
		"pr.create", "pr.comment", "pr.review",
		"issue.create", "issue.comment",
		"repo.create",
	}
	for _, op := range writeOps {
		t.Run(op, func(t *testing.T) {
			assert.Equal(t, classWrite, classifyOperation(op), "expected write for: %s", op)
		})
	}
}

func TestClassifyOperation_Dangerous(t *testing.T) {
	dangerousOps := []string{
		"pr.merge", "pr.close", "issue.close", "repo.delete",
		"unknown.thing", "", "repo.settings", "org.delete",
	}
	for _, op := range dangerousOps {
		t.Run(op, func(t *testing.T) {
			assert.Equal(t, classDangerous, classifyOperation(op), "expected dangerous for: %s", op)
		})
	}
}

func TestListAllowedOps(t *testing.T) {
	result := listAllowedOps()
	assert.Contains(t, result, "pr.list")
	assert.Contains(t, result, "pr.create")
	assert.Contains(t, result, "read:")
	assert.Contains(t, result, "write")
}
