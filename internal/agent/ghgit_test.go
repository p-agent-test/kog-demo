package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyOperation_GitRead(t *testing.T) {
	readOps := []string{"git.get-file", "git.list-files", "git.get-tree", "git.get-files"}
	for _, op := range readOps {
		assert.Equal(t, classRead, classifyOperation(op), "expected read for: %s", op)
	}
}

func TestClassifyOperation_GitWrite(t *testing.T) {
	writeOps := []string{"git.commit", "git.create-branch"}
	for _, op := range writeOps {
		assert.Equal(t, classWrite, classifyOperation(op), "expected write for: %s", op)
	}
}

func TestListAllowedOps_IncludesGit(t *testing.T) {
	result := listAllowedOps()
	assert.Contains(t, result, "git.commit")
	assert.Contains(t, result, "git.get-file")
	assert.Contains(t, result, "git.get-tree")
	assert.Contains(t, result, "git.get-files")
}
