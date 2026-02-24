package project

import (
	"regexp"
	"strings"
)

// CommandType represents the type of project command.
type CommandType int

const (
	CmdUnknown CommandType = iota
	CmdListProjects
	CmdNewProject
	CmdContinueProject
	CmdMessageProject
	CmdDecide
	CmdBlocker
	CmdArchive
	CmdResume
)

// ProjectCommand represents a parsed project command.
type ProjectCommand struct {
	Type    CommandType
	Slug    string
	Message string
	Name    string
	RepoURL string
}

var mentionRe = regexp.MustCompile(`<@[A-Z0-9]+>`)
var quoteRe = regexp.MustCompile(`"([^"]+)"`)

// ParseCommand parses a raw message text into a ProjectCommand.
// The text should already have the bot mention stripped.
func ParseCommand(text string) *ProjectCommand {
	// Strip bot mention if still present
	text = mentionRe.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)

	if text == "" {
		return nil
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return nil
	}

	first := strings.ToLower(parts[0])

	// Built-in commands
	switch first {
	case "projects", "projeler":
		return &ProjectCommand{Type: CmdListProjects}

	case "new":
		return parseNewCommand(text, parts)

	case "decide":
		if len(parts) >= 3 {
			slug := strings.ToLower(parts[1])
			msg := strings.Join(parts[2:], " ")
			return &ProjectCommand{Type: CmdDecide, Slug: slug, Message: msg}
		}
		return nil

	case "blocker":
		if len(parts) >= 3 {
			slug := strings.ToLower(parts[1])
			msg := strings.Join(parts[2:], " ")
			return &ProjectCommand{Type: CmdBlocker, Slug: slug, Message: msg}
		}
		return nil

	case "archive":
		if len(parts) >= 2 {
			return &ProjectCommand{Type: CmdArchive, Slug: strings.ToLower(parts[1])}
		}
		return nil

	case "resume":
		if len(parts) >= 2 {
			return &ProjectCommand{Type: CmdResume, Slug: strings.ToLower(parts[1])}
		}
		return nil
	}

	// Not a built-in command → could be a slug reference
	// Will be resolved by the router checking against known slugs
	slug := strings.ToLower(parts[0])
	if len(parts) == 1 {
		return &ProjectCommand{Type: CmdContinueProject, Slug: slug}
	}
	msg := strings.Join(parts[1:], " ")
	return &ProjectCommand{Type: CmdMessageProject, Slug: slug, Message: msg}
}

func parseNewCommand(text string, parts []string) *ProjectCommand {
	// Expected: new project "Name" [--repo URL]
	if len(parts) < 3 || strings.ToLower(parts[1]) != "project" {
		return nil
	}

	cmd := &ProjectCommand{Type: CmdNewProject}

	// Extract quoted name
	matches := quoteRe.FindStringSubmatch(text)
	if len(matches) >= 2 {
		cmd.Name = matches[1]
	} else {
		// No quotes — take the third word as name
		cmd.Name = parts[2]
	}

	// Extract --repo
	for i, p := range parts {
		if p == "--repo" && i+1 < len(parts) {
			cmd.RepoURL = parts[i+1]
			break
		}
	}

	return cmd
}
