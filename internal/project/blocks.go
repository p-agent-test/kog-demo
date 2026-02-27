package project

import (
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// StatusEmoji returns a status emoji based on last activity time and project status.
func StatusEmoji(p *Project) string {
	if p.Status == "archived" {
		return "📦"
	}
	if p.AutoDrive {
		return "🔄"
	}
	if p.Status == "paused" {
		return "⏸️"
	}
	elapsed := time.Since(time.UnixMilli(p.UpdatedAt))
	switch {
	case elapsed < 6*time.Hour:
		return "🟢"
	case elapsed < 3*24*time.Hour:
		return "🟡"
	default:
		return "🔵"
	}
}

// truncateStr truncates a string to maxLen and appends "…" if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

// timeAgo returns a human-readable relative time string.
func timeAgo(ms int64) string {
	if ms == 0 {
		return "never"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// DashboardBlocks builds the Block Kit blocks for `@kog projects`.
func DashboardBlocks(projects []*Project, statsMap map[string]*ProjectStats, eventsMap map[string]*ProjectEvent) []slack.Block {
	if len(projects) == 0 {
		return []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "No active projects.\nCreate one with `new project \"Name\"`", false, false),
				nil, nil,
			),
		}
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", fmt.Sprintf("📂 %d Active Projects", len(projects)), false, false),
		),
	}

	for i, p := range projects {
		if i > 0 {
			blocks = append(blocks, slack.NewDividerBlock())
		}

		emoji := StatusEmoji(p)
		stats := statsMap[p.ID]

		// Build stats line
		var statsLine strings.Builder
		if stats != nil {
			if stats.Decisions > 0 {
				statsLine.WriteString(fmt.Sprintf("📌 %d decisions", stats.Decisions))
			}
			if stats.Blockers > 0 {
				if statsLine.Len() > 0 {
					statsLine.WriteString(" · ")
				}
				statsLine.WriteString(fmt.Sprintf("🚧 %d blockers", stats.Blockers))
			}
			if stats.Tasks > 0 {
				if statsLine.Len() > 0 {
					statsLine.WriteString(" · ")
				}
				statsLine.WriteString(fmt.Sprintf("%d tasks", stats.Tasks))
			}
		}

		// Build last activity line
		lastLine := fmt.Sprintf("Last: %s", timeAgo(p.UpdatedAt))
		if evt, ok := eventsMap[p.ID]; ok && evt != nil {
			lastLine += fmt.Sprintf(" — \"%s\"", truncateStr(evt.Summary, 60))
		}

		slugLabel := p.Slug
		if p.AutoDrive {
			slugLabel += " (auto-driving)"
		}
		text := fmt.Sprintf("%s *%s*\n%s", emoji, slugLabel, p.Name)
		if p.CurrentPhase != "" {
			if statsLine.Len() > 0 {
				statsLine.WriteString(fmt.Sprintf(" · Phase: %s", p.CurrentPhase))
			} else {
				statsLine.WriteString(fmt.Sprintf("Phase: %s", p.CurrentPhase))
			}
		}
		if statsLine.Len() > 0 {
			text += "\n" + statsLine.String()
		}
		if p.AutoDrive {
			driveLine := fmt.Sprintf("⏱️ Drive: every %s", FormatDurationMs(p.DriveIntervalMs))
			if p.ReportIntervalMs > 0 {
				driveLine += fmt.Sprintf(" · Report: every %s", FormatDurationMs(p.ReportIntervalMs))
			}
			text += "\n" + driveLine
			if p.AutoDriveUntil > 0 {
				text += fmt.Sprintf(" · %s", TimeLeftStr(p.AutoDriveUntil))
			}
		}
		text += "\n" + lastLine

		continueBtn := slack.NewButtonBlockElement(
			fmt.Sprintf("project_continue_%s", p.Slug), p.Slug,
			slack.NewTextBlockObject("plain_text", "▶️ Continue", false, false),
		)
		archiveBtn := slack.NewButtonBlockElement(
			fmt.Sprintf("project_archive_%s", p.Slug), p.Slug,
			slack.NewTextBlockObject("plain_text", "📦 Archive", false, false),
		)

		section := slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", text, false, false),
			nil,
			slack.NewAccessory(continueBtn),
		)
		blocks = append(blocks, section)

		var actionElements []slack.BlockElement
		actionElements = append(actionElements, continueBtn)
		if p.AutoDrive {
			pauseBtn := slack.NewButtonBlockElement(
				fmt.Sprintf("project_pause_%s", p.Slug), p.Slug,
				slack.NewTextBlockObject("plain_text", "⏸️ Pause", false, false),
			)
			actionElements = append(actionElements, pauseBtn)
		}
		actionElements = append(actionElements, archiveBtn)
		blocks = append(blocks, slack.NewActionBlock(
			fmt.Sprintf("project_actions_%s", p.Slug),
			actionElements...,
		))
	}

	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewActionBlock(
		"project_footer_actions",
		slack.NewButtonBlockElement(
			"project_show_archived", "show_archived",
			slack.NewTextBlockObject("plain_text", "📦 Show archived", false, false),
		),
	))
	blocks = append(blocks, slack.NewContextBlock("",
		slack.NewTextBlockObject("mrkdwn", "`<slug>` to continue · `new project \"Name\"` to create", false, false),
	))

	return blocks
}

// ProjectCreatedBlocks builds blocks for a newly created project.
func ProjectCreatedBlocks(p *Project) []slack.Block {
	text := fmt.Sprintf("✅ *Project Created*\n\n*Name:* %s\n*Slug:* `%s`", p.Name, p.Slug)
	if p.RepoURL != "" {
		text += fmt.Sprintf("\n*Repo:* %s", p.RepoURL)
	}

	startBtn := slack.NewButtonBlockElement(
		fmt.Sprintf("project_start_%s", p.Slug), p.Slug,
		slack.NewTextBlockObject("plain_text", "🚀 Start Working", false, false),
	)

	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", text, false, false),
			nil,
			slack.NewAccessory(startBtn),
		),
	}
}

// ProjectContinueBlocks builds blocks for resuming a project session.
func ProjectContinueBlocks(p *Project, decisions []*ProjectMemory, blockers []*ProjectMemory, lastSummary string) []slack.Block {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text",
				fmt.Sprintf("🔄 %s — Resuming (v%d)", p.Name, p.SessionVersion), false, false),
		),
	}

	// Recent decisions
	if len(decisions) > 0 {
		limit := 3
		if len(decisions) < limit {
			limit = len(decisions)
		}
		recent := decisions[len(decisions)-limit:]
		var lines []string
		for _, d := range recent {
			lines = append(lines, fmt.Sprintf("📌 %s", truncateStr(d.Content, 80)))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Recent Decisions:*\n"+strings.Join(lines, "\n"), false, false),
			nil, nil,
		))
	}

	// Active blockers
	if len(blockers) > 0 {
		var lines []string
		for _, b := range blockers {
			lines = append(lines, fmt.Sprintf("🚧 %s", truncateStr(b.Content, 80)))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Blockers:*\n"+strings.Join(lines, "\n"), false, false),
			nil, nil,
		))
	}

	// Last session summary
	if lastSummary != "" {
		blocks = append(blocks, slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("_Last session: %s_", truncateStr(lastSummary, 120)), false, false),
		))
	}

	return blocks
}

// DecisionRecordedBlocks builds blocks for a recorded decision.
func DecisionRecordedBlocks(slug, content string, totalDecisions int) []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("📌 *Decision recorded* for `%s`:\n%s", slug, content), false, false),
			nil, nil,
		),
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("Total decisions: %d", totalDecisions), false, false),
		),
	}
}

// BlockerRecordedBlocks builds blocks for a recorded blocker.
func BlockerRecordedBlocks(slug, content string, totalBlockers int) []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("🚧 *Blocker recorded* for `%s`:\n%s", slug, content), false, false),
			nil, nil,
		),
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("Total blockers: %d", totalBlockers), false, false),
		),
	}
}

// ProjectStatusBlocks builds a detailed project status view.
func ProjectStatusBlocks(p *Project, stats *ProjectStats, decisions []*ProjectMemory, blockers []*ProjectMemory, events []*ProjectEvent) []slack.Block {
	emoji := StatusEmoji(p)
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text",
				fmt.Sprintf("%s %s", emoji, p.Name), false, false),
		),
	}

	// Fields
	fields := []*slack.TextBlockObject{
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Status:* %s", p.Status), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Session:* v%d", p.SessionVersion), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Created:* %s", time.UnixMilli(p.CreatedAt).UTC().Format("2006-01-02")), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Last Active:* %s", timeAgo(p.UpdatedAt)), false, false),
	}
	if p.RepoURL != "" {
		fields = append(fields, slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Repo:* %s", p.RepoURL), false, false))
	}
	if p.OwnerID != "" {
		fields = append(fields, slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Owner:* <@%s>", p.OwnerID), false, false))
	}

	blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))

	// Stats summary
	if stats != nil {
		blocks = append(blocks, slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("📌 %d decisions · 🚧 %d blockers · %d tasks · %d events",
					stats.Decisions, stats.Blockers, stats.Tasks, stats.Events), false, false),
		))
	}

	blocks = append(blocks, slack.NewDividerBlock())

	// Recent decisions (last 5)
	if len(decisions) > 0 {
		limit := 5
		if len(decisions) < limit {
			limit = len(decisions)
		}
		recent := decisions[len(decisions)-limit:]
		var lines []string
		for _, d := range recent {
			ts := time.UnixMilli(d.CreatedAt).UTC().Format("Jan 2")
			lines = append(lines, fmt.Sprintf("📌 [%s] %s", ts, truncateStr(d.Content, 80)))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Decisions:*\n"+strings.Join(lines, "\n"), false, false),
			nil, nil,
		))
	}

	// Active blockers
	if len(blockers) > 0 {
		var lines []string
		for _, b := range blockers {
			lines = append(lines, fmt.Sprintf("🚧 %s", truncateStr(b.Content, 80)))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Blockers:*\n"+strings.Join(lines, "\n"), false, false),
			nil, nil,
		))
	}

	// Recent events (last 5)
	if len(events) > 0 {
		limit := 5
		if len(events) < limit {
			limit = len(events)
		}
		var lines []string
		for _, e := range events[:limit] {
			lines = append(lines, fmt.Sprintf("• %s — %s", timeAgo(e.CreatedAt), truncateStr(e.Summary, 60)))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Recent Activity:*\n"+strings.Join(lines, "\n"), false, false),
			nil, nil,
		))
	}

	// Action buttons
	continueBtn := slack.NewButtonBlockElement(
		fmt.Sprintf("project_continue_%s", p.Slug), p.Slug,
		slack.NewTextBlockObject("plain_text", "▶️ Continue", false, false),
	)
	archiveBtn := slack.NewButtonBlockElement(
		fmt.Sprintf("project_archive_%s", p.Slug), p.Slug,
		slack.NewTextBlockObject("plain_text", "📦 Archive", false, false),
	)

	blocks = append(blocks, slack.NewActionBlock(
		fmt.Sprintf("project_detail_actions_%s", p.Slug),
		continueBtn, archiveBtn,
	))

	return blocks
}

// ArchivedDashboardBlocks builds blocks for the archived projects list.
func ArchivedDashboardBlocks(projects []*Project) []slack.Block {
	if len(projects) == 0 {
		return []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "📦 No archived projects.", false, false),
				nil, nil,
			),
		}
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", fmt.Sprintf("📦 %d Archived Projects", len(projects)), false, false),
		),
	}

	for i, p := range projects {
		if i > 0 {
			blocks = append(blocks, slack.NewDividerBlock())
		}

		archivedAt := timeAgo(p.UpdatedAt)
		text := fmt.Sprintf("📦 *%s*\n_%s_ · Archived %s", p.Name, p.Slug, archivedAt)

		resumeBtn := slack.NewButtonBlockElement(
			fmt.Sprintf("project_resume_%s", p.Slug), p.Slug,
			slack.NewTextBlockObject("plain_text", "♻️ Resume", false, false),
		)

		section := slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", text, false, false),
			nil,
			slack.NewAccessory(resumeBtn),
		)
		blocks = append(blocks, section)
	}

	return blocks
}
