package bridge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatHeaders(t *testing.T) {
	assert.Equal(t, "*Hello*", formatForSlack("# Hello"))
	assert.Equal(t, "*Hello*", formatForSlack("## Hello"))
	assert.Equal(t, "*Hello*", formatForSlack("### Hello"))
	// Mid-line # should not be converted
	assert.Equal(t, "Use # wisely", formatForSlack("Use # wisely"))
}

func TestFormatBold(t *testing.T) {
	assert.Equal(t, "this is *bold* text", formatForSlack("this is **bold** text"))
	// Single * should remain
	assert.Equal(t, "a * b", formatForSlack("a * b"))
}

func TestFormatStrikethrough(t *testing.T) {
	assert.Equal(t, "this is ~struck~ text", formatForSlack("this is ~~struck~~ text"))
}

func TestFormatLinks(t *testing.T) {
	assert.Equal(t, "click <https://example.com|here>", formatForSlack("click [here](https://example.com)"))
}

func TestFormatImages(t *testing.T) {
	assert.Equal(t, "<https://img.png|photo>", formatForSlack("![photo](https://img.png)"))
}

func TestFormatTables(t *testing.T) {
	input := `| Col1 | Col2 | Col3 |
|------|------|------|
| a    | b    | c    |
| d    | e    | f    |`
	expected := `• *Col1:* a · *Col2:* b · *Col3:* c
• *Col1:* d · *Col2:* e · *Col3:* f`
	assert.Equal(t, expected, formatForSlack(input))
}

func TestFormatTablesSingleColumn(t *testing.T) {
	input := `| Name |
|------|
| Alice |
| Bob |`
	expected := `• Alice
• Bob`
	assert.Equal(t, expected, formatForSlack(input))
}

func TestFormatCodeBlockProtection(t *testing.T) {
	input := "before\n```\n# Not a header\n**not bold**\n~~not struck~~\n```\nafter"
	result := formatForSlack(input)
	assert.Contains(t, result, "# Not a header")
	assert.Contains(t, result, "**not bold**")
	assert.Contains(t, result, "~~not struck~~")
}

func TestFormatMixed(t *testing.T) {
	input := `# Title

This is **bold** and ~~struck~~.

| A | B |
|---|---|
| 1 | 2 |

` + "```go\nfunc main() {}\n```"
	result := formatForSlack(input)
	assert.Contains(t, result, "*Title*")
	assert.Contains(t, result, "*bold*")
	assert.Contains(t, result, "~struck~")
	assert.Contains(t, result, "• *A:* 1 · *B:* 2")
	assert.Contains(t, result, "func main() {}")
}

func TestFormatPassthrough(t *testing.T) {
	input := "just plain text with no markdown"
	assert.Equal(t, input, formatForSlack(input))
}

func TestFormatEmpty(t *testing.T) {
	assert.Equal(t, "", formatForSlack(""))
}
