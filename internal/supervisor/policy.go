package supervisor

import (
	"sync"
	"time"

	"github.com/p-blackswan/platform-agent/internal/models"
)

// Permission represents a specific capability.
type Permission string

const (
	// GitHub permissions
	PermGithubPRRead  Permission = "github.pr.read"
	PermGithubPRWrite Permission = "github.pr.write"
	PermGithubPRMerge Permission = "github.pr.merge" // always-deny
	PermGithubCIRead  Permission = "github.ci.read"
	PermGithubRepoRead Permission = "github.repo.read"

	// Kubernetes permissions
	PermK8sRead   Permission = "k8s.read"
	PermK8sWrite  Permission = "k8s.write"
	PermK8sDelete Permission = "k8s.delete" // always-deny
	PermK8sExec   Permission = "k8s.exec"   // always-deny

	// Jira permissions
	PermJiraRead  Permission = "jira.read"
	PermJiraWrite Permission = "jira.write"

	// Slack permissions
	PermSlackRead  Permission = "slack.read"
	PermSlackWrite Permission = "slack.write"

	// GitHub exec permissions
	PermGithubExecRead  Permission = "github.exec.read"
	PermGithubExecWrite Permission = "github.exec.write"

	// Deploy permissions
	PermDeployTest Permission = "deploy.test"
	PermDeployProd Permission = "deploy.production" // always-deny
)

// PolicyLevel defines how a permission is granted.
type PolicyLevel string

const (
	PolicyAutoApprove    PolicyLevel = "auto-approve"      // Do it, don't ask
	PolicyNotifyThenDo   PolicyLevel = "notify-then-do"    // Notify, then do it
	PolicyRequireApproval PolicyLevel = "require-approval"  // Ask, wait for human
	PolicyAlwaysDeny     PolicyLevel = "always-deny"        // Never allow
)

// DefaultAutoApproveTTL is the TTL for auto-approved grants.
const DefaultAutoApproveTTL = 5 * time.Minute

// DefaultHumanApprovedTTL is the TTL for human-approved grants.
const DefaultHumanApprovedTTL = 15 * time.Minute

// PermissionGrant represents a time-limited permission.
type PermissionGrant struct {
	ID         string     `json:"id"`
	Permission Permission `json:"permission"`
	Level      PolicyLevel `json:"level"`
	GrantedTo  string     `json:"granted_to"`
	GrantedBy  string     `json:"granted_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	TaskID     string     `json:"task_id"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TaskPermissionMap maps task types to required permissions.
var TaskPermissionMap = map[string][]Permission{
	"github.review-pr":   {PermGithubPRRead},
	"github.create-pr":   {PermGithubPRRead, PermGithubPRWrite},
	"github.list-prs":    {PermGithubPRRead},
	"github.ci-status":   {PermGithubCIRead},
	"k8s.pod-logs":       {PermK8sRead},
	"k8s.pod-status":     {PermK8sRead},
	"k8s.deployments":    {PermK8sRead},
	"k8s.alert-triage":   {PermK8sRead},
	"jira.get-issue":     {PermJiraRead},
	"jira.create-issue":  {PermJiraWrite},
	"slack.send-message": {PermSlackWrite},
	"github.exec":        {PermGithubExecRead},
	"github.exec.write":  {PermGithubExecWrite},
	"policy.list":        {},
	"policy.set":         {},
	"policy.reset":       {},
}

// PolicyChange represents a single policy change request.
type PolicyChange struct {
	Permission Permission
	NewLevel   PolicyLevel
	Reason     string
}

// Policy defines access control rules.
type Policy struct {
	mu    sync.RWMutex
	rules map[string]models.AccessLevel

	// JIT permission policies: Permission → PolicyLevel
	permPolicies map[Permission]PolicyLevel
}

// DefaultPolicy returns the default access control policy.
func DefaultPolicy() *Policy {
	p := &Policy{
		rules: map[string]models.AccessLevel{
			// Auto-approve (read operations)
			"github.pr.read":       models.AccessAutoApprove,
			"github.pr.list":       models.AccessAutoApprove,
			"github.issues.read":   models.AccessAutoApprove,
			"github.checks.read":   models.AccessAutoApprove,
			"jira.issue.read":      models.AccessAutoApprove,
			"jira.issue.search":    models.AccessAutoApprove,
			"jira.sprint.read":     models.AccessAutoApprove,
			"logs.read":            models.AccessAutoApprove,
			"status.read":          models.AccessAutoApprove,

			// Require approval (write operations)
			"github.pr.review":      models.AccessRequireApproval,
			"github.pr.merge":       models.AccessRequireApproval,
			"jira.issue.create":     models.AccessRequireApproval,
			"jira.issue.update":     models.AccessRequireApproval,
			"jira.issue.transition": models.AccessRequireApproval,
			"deploy.staging":        models.AccessRequireApproval,

			// Denied (dangerous operations)
			"deploy.production":  models.AccessDenied,
			"github.repo.delete": models.AccessDenied,
			"admin.config.modify": models.AccessDenied,
		},
		permPolicies: defaultPermPolicies(),
	}
	return p
}

// defaultPermPolicies returns the default JIT permission policies.
func defaultPermPolicies() map[Permission]PolicyLevel {
	return map[Permission]PolicyLevel{
		// Reads → auto-approve
		PermGithubPRRead:  PolicyAutoApprove,
		PermGithubCIRead:  PolicyAutoApprove,
		PermGithubRepoRead: PolicyAutoApprove,
		PermK8sRead:       PolicyAutoApprove,
		PermJiraRead:      PolicyAutoApprove,
		PermSlackRead:      PolicyAutoApprove,
		PermGithubExecRead: PolicyAutoApprove,

		// Writes → require-approval
		PermGithubPRWrite: PolicyRequireApproval,
		PermJiraWrite:     PolicyRequireApproval,
		PermSlackWrite:    PolicyRequireApproval,
		PermK8sWrite:      PolicyRequireApproval,
		PermDeployTest:     PolicyRequireApproval,
		PermGithubExecWrite: PolicyRequireApproval,

		// Dangerous → always-deny
		PermGithubPRMerge: PolicyAlwaysDeny,
		PermK8sDelete:     PolicyAlwaysDeny,
		PermK8sExec:       PolicyAlwaysDeny,
		PermDeployProd:    PolicyAlwaysDeny,
	}
}

// Evaluate returns the access level for the given action.
func (p *Policy) Evaluate(action string) models.AccessLevel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if level, ok := p.rules[action]; ok {
		return level
	}
	// Default: require approval for unknown actions
	return models.AccessRequireApproval
}

// AddRule adds or updates a policy rule.
func (p *Policy) AddRule(action string, level models.AccessLevel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules[action] = level
}

// ListRules returns all policy rules.
func (p *Policy) ListRules() map[string]models.AccessLevel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copy := make(map[string]models.AccessLevel, len(p.rules))
	for k, v := range p.rules {
		copy[k] = v
	}
	return copy
}

// GetPermissionPolicy returns the PolicyLevel for a given Permission.
func (p *Policy) GetPermissionPolicy(perm Permission) PolicyLevel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if level, ok := p.permPolicies[perm]; ok {
		return level
	}
	return PolicyRequireApproval
}

// SetPermissionPolicy sets the PolicyLevel for a given Permission.
func (p *Policy) SetPermissionPolicy(perm Permission, level PolicyLevel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.permPolicies == nil {
		p.permPolicies = make(map[Permission]PolicyLevel)
	}
	p.permPolicies[perm] = level
}

// ListPermissionPolicies returns a copy of all permission policies.
func (p *Policy) ListPermissionPolicies() map[Permission]PolicyLevel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copy := make(map[Permission]PolicyLevel, len(p.permPolicies))
	for k, v := range p.permPolicies {
		copy[k] = v
	}
	return copy
}

// ResetPermissionPolicies resets all permission policies to defaults.
func (p *Policy) ResetPermissionPolicies() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.permPolicies = defaultPermPolicies()
}
