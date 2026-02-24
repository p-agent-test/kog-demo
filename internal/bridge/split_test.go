package bridge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitMessage_Short(t *testing.T) {
	result := splitMessage("hello world", 3000)
	assert.Equal(t, []string{"hello world"}, result)
}

func TestSplitMessage_ExactLimit(t *testing.T) {
	text := strings.Repeat("a", 3000)
	result := splitMessage(text, 3000)
	assert.Equal(t, []string{text}, result)
}

func TestSplitMessage_Headers(t *testing.T) {
	text := "## Introduction\nSome intro text here.\n\n## Details\nDetails about the thing.\n\n### Sub-section\nMore details here."
	result := splitMessage(text, 50)
	assert.True(t, len(result) > 1, "should split on headers")
	assert.True(t, strings.HasPrefix(result[0], "## Introduction"), "first chunk should start with header")
}

func TestSplitMessage_CodeBlocks(t *testing.T) {
	text := "Here is some code:\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\nAnd more text after.\n```python\nprint('hi')\n```\nDone."
	result := splitMessage(text, 30)
	assert.True(t, len(result) > 1, "should split on code blocks")
	// At least one chunk should contain a code block
	hasCodeBlock := false
	for _, chunk := range result {
		if strings.Contains(chunk, "```") {
			hasCodeBlock = true
			break
		}
	}
	assert.True(t, hasCodeBlock, "should preserve code blocks")
}

func TestSplitMessage_Paragraphs(t *testing.T) {
	// No headers or code blocks, just paragraphs
	para1 := strings.Repeat("word ", 100) // ~500 chars
	para2 := strings.Repeat("text ", 100)
	para3 := strings.Repeat("more ", 100)
	text := para1 + "\n\n" + para2 + "\n\n" + para3
	result := splitMessage(text, 600)
	assert.True(t, len(result) > 1, "should split on paragraphs, got %d chunks", len(result))
}

func TestSplitMessage_HardSplit(t *testing.T) {
	// Single long line — no natural break points
	text := strings.Repeat("x", 5000)
	result := splitMessage(text, 2000)
	assert.True(t, len(result) >= 3, "should hard split")
	for _, chunk := range result {
		assert.True(t, len(chunk) <= 2000, "chunk should not exceed maxLen")
	}
}

func TestSplitMessage_NoOverSplit(t *testing.T) {
	// Under limit with headers — should not split
	text := "## Title\nShort text."
	result := splitMessage(text, 3000)
	assert.Equal(t, 1, len(result))
}

func TestSplitMessage_Empty(t *testing.T) {
	result := splitMessage("", 3000)
	assert.Equal(t, []string{""}, result)
}

func TestSplitMessage_NewlineHardSplit(t *testing.T) {
	// Long text with newlines — should prefer splitting at newlines
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = strings.Repeat("a", 50)
	}
	text := strings.Join(lines, "\n") // ~5100 chars
	result := splitMessage(text, 2000)
	assert.True(t, len(result) > 1)
	for _, chunk := range result {
		assert.True(t, len(chunk) <= 2000)
	}
}
