package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// ApprovalContext carries operation-specific details for rich approval messages.
type ApprovalContext struct {
	Action    string          // e.g. "github.exec"
	Operation string          // e.g. "pr.create", "git.commit"
	CallerID  string          // Slack user ID
	Params    json.RawMessage // raw operation params
	TaskID    string          // task ID for reference
}

// truncate shortens s to max chars, appending "…" if truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// BuildApprovalBlocks creates operation-specific rich Block Kit blocks for approval requests.
func BuildApprovalBlocks(requestID string, ctx ApprovalContext) []slack.Block {
	detail := buildOperationDetail(ctx)

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", detail, false, false),
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
	return blocks
}

// ApprovalSummary returns a short one-line summary for log/notification messages.
func ApprovalSummary(ctx ApprovalContext) string {
	var p map[string]interface{}
	_ = json.Unmarshal(ctx.Params, &p)

	switch ctx.Operation {
	case "pr.create":
		return fmt.Sprintf("pr.create on %s/%s %q", str(p, "owner"), str(p, "repo"), truncate(str(p, "title"), 60))
	case "git.commit":
		return fmt.Sprintf("git.commit on %s/%s %q", str(p, "owner"), str(p, "repo"), truncate(str(p, "message"), 60))
	case "git.create-branch":
		return fmt.Sprintf("git.create-branch %s on %s/%s", str(p, "branch"), str(p, "owner"), str(p, "repo"))
	case "issue.create":
		return fmt.Sprintf("issue.create on %s/%s %q", str(p, "owner"), str(p, "repo"), truncate(str(p, "title"), 60))
	case "repo.create":
		org := str(p, "org")
		if org == "" {
			org = str(p, "owner")
		}
		return fmt.Sprintf("repo.create %s/%s", org, str(p, "name"))
	case "repo.token":
		return fmt.Sprintf("repo.token for %s/%s", str(p, "owner"), str(p, "repo"))
	default:
		return fmt.Sprintf("%s on %s", ctx.Operation, ctx.Action)
	}
}

func buildOperationDetail(ctx ApprovalContext) string {
	var p map[string]interface{}
	_ = json.Unmarshal(ctx.Params, &p)

	var sb strings.Builder
	sb.WriteString("🔐 *Approval Required*\n\n")

	switch ctx.Operation {
	case "pr.create":
		sb.WriteString("📝 *Create Pull Request*\n")
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		if t := str(p, "title"); t != "" {
			sb.WriteString(fmt.Sprintf("*Title:* %s\n", t))
		}
		head, base := str(p, "head"), str(p, "base")
		if head != "" || base != "" {
			if base == "" {
				base = "main"
			}
			sb.WriteString(fmt.Sprintf("*Branch:* %s → %s\n", head, base))
		}
		if body := str(p, "body"); body != "" {
			sb.WriteString(fmt.Sprintf("*Body:* %s\n", truncate(body, 200)))
		}

	case "git.commit":
		sb.WriteString("💾 *Git Commit*\n")
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		if br := str(p, "branch"); br != "" {
			sb.WriteString(fmt.Sprintf("*Branch:* %s\n", br))
		}
		if msg := str(p, "message"); msg != "" {
			sb.WriteString(fmt.Sprintf("*Message:* %s\n", msg))
		}
		if files := strSlice(p, "files"); len(files) > 0 {
			sb.WriteString(fmt.Sprintf("*Files:* %d files\n", len(files)))
			limit := 10
			if len(files) < limit {
				limit = len(files)
			}
			for _, f := range files[:limit] {
				// Extract just filename from file objects or strings
				sb.WriteString(fmt.Sprintf("  • `%s`\n", f))
			}
			if len(files) > 10 {
				sb.WriteString(fmt.Sprintf("  _+ %d more_\n", len(files)-10))
			}
		}

	case "git.create-branch":
		sb.WriteString("🌿 *Create Branch*\n")
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		branch := str(p, "branch")
		base := str(p, "base")
		if base == "" {
			base = "main"
		}
		sb.WriteString(fmt.Sprintf("*Branch:* %s (from %s)\n", branch, base))

	case "issue.create":
		sb.WriteString("📋 *Create Issue*\n")
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		if t := str(p, "title"); t != "" {
			sb.WriteString(fmt.Sprintf("*Title:* %s\n", t))
		}
		if labels := strSlice(p, "labels"); len(labels) > 0 {
			sb.WriteString(fmt.Sprintf("*Labels:* %s\n", strings.Join(labels, ", ")))
		}
		if body := str(p, "body"); body != "" {
			sb.WriteString(fmt.Sprintf("*Body:* %s\n", truncate(body, 200)))
		}

	case "issue.comment", "pr.comment":
		emoji := "💬"
		name := "Issue Comment"
		if ctx.Operation == "pr.comment" {
			name = "PR Comment"
		}
		sb.WriteString(fmt.Sprintf("%s *%s*\n", emoji, name))
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		if n := str(p, "number"); n != "" {
			sb.WriteString(fmt.Sprintf("*Number:* #%s\n", n))
		}
		if body := str(p, "body"); body != "" {
			sb.WriteString(fmt.Sprintf("*Body:* %s\n", truncate(body, 200)))
		}

	case "pr.review":
		sb.WriteString("👀 *PR Review*\n")
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		if n := str(p, "number"); n != "" {
			sb.WriteString(fmt.Sprintf("*PR:* #%s\n", n))
		}
		if ev := str(p, "event"); ev != "" {
			sb.WriteString(fmt.Sprintf("*Event:* %s\n", ev))
		}
		if body := str(p, "body"); body != "" {
			sb.WriteString(fmt.Sprintf("*Body:* %s\n", truncate(body, 200)))
		}

	case "repo.create":
		sb.WriteString("📦 *Create Repository*\n")
		org := str(p, "org")
		if org == "" {
			org = str(p, "owner")
		}
		sb.WriteString(fmt.Sprintf("*Org:* %s\n", org))
		sb.WriteString(fmt.Sprintf("*Name:* %s\n", str(p, "name")))
		if v, ok := p["private"]; ok {
			if b, ok := v.(bool); ok {
				if b {
					sb.WriteString("*Private:* yes\n")
				} else {
					sb.WriteString("*Private:* no\n")
				}
			}
		}
		if desc := str(p, "description"); desc != "" {
			sb.WriteString(fmt.Sprintf("*Description:* %s\n", truncate(desc, 200)))
		}

	case "repo.token":
		sb.WriteString("🔑 *Scoped Installation Token*\n")
		sb.WriteString(fmt.Sprintf("*Repo:* %s/%s\n", str(p, "owner"), str(p, "repo")))
		if perms := str(p, "permissions"); perms != "" {
			sb.WriteString(fmt.Sprintf("*Permissions:* %s\n", perms))
		} else if permsMap, ok := p["permissions"].(map[string]interface{}); ok {
			var parts []string
			for k, v := range permsMap {
				parts = append(parts, fmt.Sprintf("%s:%v", k, v))
			}
			sb.WriteString(fmt.Sprintf("*Permissions:* %s\n", strings.Join(parts, ", ")))
		}

	default:
		sb.WriteString(fmt.Sprintf("⚙️ *%s*\n", ctx.Action))
		sb.WriteString(fmt.Sprintf("*Operation:* %s\n", ctx.Operation))
		if len(ctx.Params) > 0 {
			raw := string(ctx.Params)
			sb.WriteString(fmt.Sprintf("*Params:* %s\n", truncate(raw, 300)))
		}
	}

	sb.WriteString(fmt.Sprintf("\n*Requested by:* <@%s>", ctx.CallerID))
	if ctx.TaskID != "" {
		sb.WriteString(fmt.Sprintf("\n*Task:* %s", ctx.TaskID))
	}

	return sb.String()
}

// str extracts a string value from a map.
func str(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%g", val)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// strSlice extracts a string slice from a map (handles []interface{}).
func strSlice(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []interface{}:
		var result []string
		for _, item := range val {
			switch s := item.(type) {
			case string:
				result = append(result, s)
			case map[string]interface{}:
				// file objects — try "path" or "name"
				if p, ok := s["path"].(string); ok {
					result = append(result, p)
				} else if n, ok := s["name"].(string); ok {
					result = append(result, n)
				}
			}
		}
		return result
	case []string:
		return val
	default:
		return nil
	}
}

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
