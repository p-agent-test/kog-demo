package agent

import (
	"encoding/json"
	"strings"

	"github.com/p-blackswan/platform-agent/internal/project"
)

// autoDrivePolicy represents the policy decision for an auto-drive operation.
type autoDrivePolicy string

const (
	adPolicyAutoApprove      autoDrivePolicy = "auto-approve"
	adPolicyDeny             autoDrivePolicy = "deny"
	adPolicyRequireApproval  autoDrivePolicy = "require-approval"
)

// SetProjectStore sets the project store for auto-drive session detection.
func (a *Agent) SetProjectStore(ps *project.Store) {
	a.projectStore = ps
}

// isAutoDriveSession checks if the sessionKey belongs to an auto-drive project.
// Session keys follow the pattern "agent:main:project-{slug}" or "agent:main:project-{slug}-v{N}".
func (a *Agent) isAutoDriveSession(sessionKey string) bool {
	if a.projectStore == nil || sessionKey == "" {
		return false
	}

	slug := slugFromSessionKey(sessionKey)
	if slug == "" {
		return false
	}

	p, err := a.projectStore.GetProject(slug)
	if err != nil || p == nil {
		return false
	}

	return p.AutoDrive && p.Status == "active"
}

// slugFromSessionKey extracts the project slug from a session key.
// Patterns: "agent:main:project-{slug}" or "agent:main:project-{slug}-v{N}"
func slugFromSessionKey(sessionKey string) string {
	const prefix = "agent:main:project-"
	if !strings.HasPrefix(sessionKey, prefix) {
		return ""
	}
	slug := sessionKey[len(prefix):]
	// Strip version suffix: "-v1", "-v12", etc.
	if idx := strings.LastIndex(slug, "-v"); idx > 0 {
		suffix := slug[idx+2:]
		allDigits := len(suffix) > 0
		for _, c := range suffix {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			slug = slug[:idx]
		}
	}
	return slug
}

// getAutoDrivePolicy determines the policy for an operation in auto-drive mode.
func getAutoDrivePolicy(operation string, params json.RawMessage) autoDrivePolicy {
	switch operation {
	case "git.commit":
		branch := extractBranch(params)
		if branch == "main" || branch == "master" {
			return adPolicyDeny
		}
		return adPolicyAutoApprove
	case "git.create-branch":
		return adPolicyAutoApprove
	case "pr.create", "pr.comment", "issue.create", "issue.comment":
		return adPolicyAutoApprove
	case "pr.review":
		return adPolicyRequireApproval
	case "repo.create", "repo.token":
		return adPolicyRequireApproval
	default:
		return adPolicyRequireApproval
	}
}

// extractBranch extracts the branch name from operation params.
// Checks "branch", "ref", and "base_branch" fields.
func extractBranch(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var p struct {
		Branch     string `json:"branch"`
		Ref        string `json:"ref"`
		BaseBranch string `json:"base_branch"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	if p.Branch != "" {
		return p.Branch
	}
	if p.Ref != "" {
		return p.Ref
	}
	return p.BaseBranch
}

// projectSlugFromSessionKey is exported for testing.
func projectSlugFromSessionKey(sessionKey string) string {
	return slugFromSessionKey(sessionKey)
}
