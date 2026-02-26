package bridge

import (
	"fmt"
	"regexp"
	"strings"
)

// formatForSlack converts standard Markdown to Slack mrkdwn format.
func formatForSlack(text string) string {
	if text == "" {
		return ""
	}

	// 1. Protect code blocks
	var codeBlocks []string
	codeBlockRe := regexp.MustCompile("(?s)```.*?```")
	text = codeBlockRe.ReplaceAllStringFunc(text, func(match string) string {
		idx := len(codeBlocks)
		codeBlocks = append(codeBlocks, match)
		return fmt.Sprintf("\x00CODEBLOCK_%d\x00", idx)
	})

	// 2. Tables → bullet lists (before other transforms since tables contain |)
	text = convertTables(text)

	// 3. Headers: lines starting with # → bold
	headerRe := regexp.MustCompile(`(?m)^#{1,3}\s+(.+)$`)
	text = headerRe.ReplaceAllStringFunc(text, func(match string) string {
		// Strip # and whitespace, extract content
		content := strings.TrimLeft(match, "#")
		content = strings.TrimSpace(content)
		// Remove any **bold** inside header to avoid double-processing
		boldRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
		content = boldRe.ReplaceAllString(content, "$1")
		return "*" + content + "*"
	})

	// 4. Bold: **text** → *text*
	boldRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
	text = boldRe.ReplaceAllString(text, "*$1*")

	// 5. Italic: _text_ stays as-is

	// 6. Strikethrough: ~~text~~ → ~text~
	strikeRe := regexp.MustCompile(`~~(.+?)~~`)
	text = strikeRe.ReplaceAllString(text, "~$1~")

	// 7. Images: ![alt](url) → <url|alt> (before links to avoid conflict)
	imgRe := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	text = imgRe.ReplaceAllString(text, "<$2|$1>")

	// 8. Links: [text](url) → <url|text>
	linkRe := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	text = linkRe.ReplaceAllString(text, "<$2|$1>")

	// 9. Restore code blocks
	for i, block := range codeBlocks {
		text = strings.Replace(text, fmt.Sprintf("\x00CODEBLOCK_%d\x00", i), block, 1)
	}

	return text
}

// convertTables finds markdown tables and converts them to bullet lists.
func convertTables(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	i := 0

	for i < len(lines) {
		// Detect table: line with | characters
		if isTableRow(lines[i]) && i+1 < len(lines) && isSeparatorRow(lines[i+1]) {
			// Parse header row
			headers := parseTableRow(lines[i])
			i++ // skip header
			i++ // skip separator

			// Parse data rows
			for i < len(lines) && isTableRow(lines[i]) && !isSeparatorRow(lines[i]) {
				cells := parseTableRow(lines[i])
				if len(headers) == 1 {
					// Single column: just bullet
					val := ""
					if len(cells) > 0 {
						val = cells[0]
					}
					result = append(result, "• "+val)
				} else {
					var pairs []string
					for j, h := range headers {
						val := ""
						if j < len(cells) {
							val = cells[j]
						}
						pairs = append(pairs, fmt.Sprintf("*%s:* %s", h, val))
					}
					result = append(result, "• "+strings.Join(pairs, " · "))
				}
				i++
			}
		} else {
			result = append(result, lines[i])
			i++
		}
	}

	return strings.Join(result, "\n")
}

func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "|")
}

func isSeparatorRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	// Must have | and consist mainly of |, -, :, and spaces
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	cleaned := strings.NewReplacer("|", "", "-", "", ":", "", " ", "").Replace(trimmed)
	return cleaned == ""
}

func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.Trim(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}
