package slack

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// PRReviewBlocks creates rich blocks for a PR review result.
func PRReviewBlocks(owner, repo string, prNumber, fileCount int, files []PRFileInfo) []slack.Block {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", fmt.Sprintf("📝 PR Review: %s/%s#%d", owner, repo, prNumber), false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Files changed:* %d", fileCount), false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
	}

	// File list (max 10)
	limit := 10
	if len(files) < limit {
		limit = len(files)
	}
	for _, f := range files[:limit] {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("• `%s` (+%d/-%d)", f.Name, f.Additions, f.Deletions),
				false, false),
			nil, nil,
		))
	}

	if len(files) > 10 {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("_...and %d more files_", len(files)-10),
				false, false),
			nil, nil,
		))
	}

	return blocks
}

// PRFileInfo is a simplified file info for blocks.
type PRFileInfo struct {
	Name      string
	Additions int
	Deletions int
}

// DeployConfirmBlocks creates a confirmation dialog for deploy.
func DeployConfirmBlocks(requestID, service, version, env, userID string) []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("🚀 *Deploy Confirmation*\n\n*Service:* `%s`\n*Version:* `%s`\n*Environment:* `%s`\n*Requested by:* <@%s>",
					service, version, env, userID),
				false, false),
			nil, nil,
		),
		slack.NewActionBlock(
			"deploy_confirm",
			slack.NewButtonBlockElement(
				fmt.Sprintf("approve_%s", requestID), "approve",
				slack.NewTextBlockObject("plain_text", "✅ Approve Deploy", false, false),
			),
			slack.NewButtonBlockElement(
				fmt.Sprintf("deny_%s", requestID), "deny",
				slack.NewTextBlockObject("plain_text", "❌ Deny", false, false),
			),
		),
	}
}

// ApprovalBlocks creates approval request blocks with reason field.
func ApprovalBlocks(requestID, userID, action, resource string) []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("🔐 *Approval Required*\n\n*User:* <@%s>\n*Action:* `%s`\n*Resource:* `%s`",
					userID, action, resource),
				false, false),
			nil, nil,
		),
		slack.NewActionBlock(
			"approval_actions",
			slack.NewButtonBlockElement(
				fmt.Sprintf("approve_%s", requestID), "approve",
				slack.NewTextBlockObject("plain_text", "✅ Approve", false, false),
			),
			slack.NewButtonBlockElement(
				fmt.Sprintf("deny_%s", requestID), "deny",
				slack.NewTextBlockObject("plain_text", "❌ Deny", false, false),
			),
		),
	}
}

// LogOutputBlocks creates blocks for log output with code block format.
func LogOutputBlocks(podName, namespace, logs string, truncated bool) []slack.Block {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", fmt.Sprintf("📋 Logs: %s/%s", namespace, podName), false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("```\n%s\n```", logs), false, false),
			nil, nil,
		),
	}

	if truncated {
		blocks = append(blocks, slack.NewContextBlock(
			"",
			slack.NewTextBlockObject("mrkdwn", "⚠️ _Output truncated. Use `--tail N` for more lines._", false, false),
		))
	}

	return blocks
}

// StatusDashboardBlocks creates a status dashboard summary.
func StatusDashboardBlocks(actions []StatusAction) []slack.Block {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "📊 Agent Status Dashboard", false, false),
		),
	}

	if len(actions) == 0 {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "_No recent actions._", false, false),
			nil, nil,
		))
		return blocks
	}

	var sb strings.Builder
	for _, a := range actions {
		icon := "✅"
		if a.Status == "error" {
			icon = "❌"
		} else if a.Status == "pending" {
			icon = "⏳"
		}
		sb.WriteString(fmt.Sprintf("%s *%s* — %s (%s)\n", icon, a.Action, a.Details, a.Time))
	}

	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", sb.String(), false, false),
		nil, nil,
	))

	return blocks
}

// StatusAction represents a recent agent action for the dashboard.
type StatusAction struct {
	Action  string
	Details string
	Status  string
	Time    string
}

// HelpBlocks creates rich help message blocks.
func HelpBlocks() []slack.Block {
	return []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "👋 Platform Agent — Help", false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", strings.Join([]string{
				"• `review <pr-url>` or `review org/repo#123` — Review a GitHub PR",
				"• `deploy <service> [version] [env]` — Deploy a service (creates GitOps PR)",
				"• `logs <pod-or-service> [namespace] [--tail N]` — Fetch pod logs",
				"• `task create <summary>` — Create a Jira task",
				"• `task <JIRA-ID>` — Check Jira task status",
				"• `status` — Agent health & recent actions",
				"• `help` — This message",
			}, "\n"), false, false),
			nil, nil,
		),
	}
}
