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
	CmdDrive
	CmdPause
	CmdPhase
	CmdReport
	CmdPhaseModel
)

// ProjectCommand represents a parsed project command.
type ProjectCommand struct {
	Type           CommandType
	Slug           string
	Message        string
	Name           string
	RepoURL        string
	DriveInterval  string
	ReportInterval string
	Phases         string
	PhaseModels    map[string]string // phase → model alias
	Duration       string
	PhaseModel     string // for CmdPhaseModel: the model alias
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

	case "drive":
		return parseDriveCommand(parts)

	case "pause":
		if len(parts) >= 2 {
			return &ProjectCommand{Type: CmdPause, Slug: strings.ToLower(parts[1])}
		}
		return nil

	case "phase":
		if len(parts) >= 3 {
			return &ProjectCommand{Type: CmdPhase, Slug: strings.ToLower(parts[1]), Message: parts[2]}
		}
		return nil

	case "report":
		// report <slug> [interval]
		if len(parts) >= 2 {
			cmd := &ProjectCommand{Type: CmdReport, Slug: strings.ToLower(parts[1])}
			if len(parts) >= 3 {
				cmd.ReportInterval = parts[2]
			}
			return cmd
		}
		return nil

	case "phase-model":
		// phase-model <slug> <phase> <model>
		if len(parts) >= 4 {
			return &ProjectCommand{
				Type:       CmdPhaseModel,
				Slug:       strings.ToLower(parts[1]),
				Message:    parts[2], // phase name
				PhaseModel: parts[3], // model alias
			}
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
	// Expected: new project "Name" [--repo URL] [--auto-drive 10m] [--report 1h] [--phases X,Y] [--duration 24h]
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

	// Extract flags
	for i, p := range parts {
		switch p {
		case "--repo":
			if i+1 < len(parts) {
				cmd.RepoURL = parts[i+1]
			}
		case "--auto-drive":
			if i+1 < len(parts) {
				cmd.DriveInterval = parts[i+1]
			}
		case "--report":
			if i+1 < len(parts) {
				cmd.ReportInterval = parts[i+1]
			}
		case "--phases":
			if i+1 < len(parts) {
				cmd.Phases, cmd.PhaseModels = parsePhaseModels(parts[i+1])
			}
		case "--duration":
			if i+1 < len(parts) {
				cmd.Duration = parts[i+1]
			}
		}
	}

	return cmd
}

func parseDriveCommand(parts []string) *ProjectCommand {
	// drive <slug> [interval] [--report interval] [--phases X,Y] [--duration 24h]
	if len(parts) < 2 {
		return nil
	}

	cmd := &ProjectCommand{
		Type: CmdDrive,
		Slug: strings.ToLower(parts[1]),
	}

	// Optional positional interval
	if len(parts) >= 3 && !strings.HasPrefix(parts[2], "--") {
		cmd.DriveInterval = parts[2]
	}

	for i, p := range parts {
		switch p {
		case "--report":
			if i+1 < len(parts) {
				cmd.ReportInterval = parts[i+1]
			}
		case "--phases":
			if i+1 < len(parts) {
				cmd.Phases, cmd.PhaseModels = parsePhaseModels(parts[i+1])
			}
		case "--duration":
			if i+1 < len(parts) {
				cmd.Duration = parts[i+1]
			}
		}
	}

	return cmd
}

// parsePhaseModels parses a phases string that may include model hints.
// Format: "Analysis:opus,Design:sonnet,Implement" → phases string + model map
// Phases without a model use the session default.
func parsePhaseModels(s string) (phases string, models map[string]string) {
	parts := strings.Split(s, ",")
	var phaseList []string
	models = make(map[string]string)

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if idx := strings.Index(p, ":"); idx != -1 {
			phase := strings.TrimSpace(p[:idx])
			model := strings.TrimSpace(p[idx+1:])
			phaseList = append(phaseList, phase)
			if model != "" {
				models[phase] = model
			}
		} else {
			phaseList = append(phaseList, p)
		}
	}

	if len(models) == 0 {
		models = nil
	}

	return strings.Join(phaseList, ","), models
}
