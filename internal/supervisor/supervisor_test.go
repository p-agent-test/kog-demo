package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/p-blackswan/platform-agent/internal/models"
)

func newTestSupervisor() *Supervisor {
	logger := zerolog.Nop()
	policy := DefaultPolicy()
	audit := NewAuditLog(logger)
	return NewSupervisor(policy, audit, 10*time.Minute, logger)
}

func TestPolicy_Evaluate(t *testing.T) {
	p := DefaultPolicy()

	assert.Equal(t, models.AccessAutoApprove, p.Evaluate("github.pr.read"))
	assert.Equal(t, models.AccessRequireApproval, p.Evaluate("deploy.staging"))
	assert.Equal(t, models.AccessDenied, p.Evaluate("deploy.production"))
	assert.Equal(t, models.AccessRequireApproval, p.Evaluate("unknown.action"))
}

func TestSupervisor_AutoApprove(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	req, err := s.RequestAccess(ctx, "U123", "testuser", "github.pr.read", "org/repo#1")
	require.NoError(t, err)
	assert.Equal(t, models.StatusApproved, req.Status)
	assert.Equal(t, "auto", req.ResolvedBy)
}

func TestSupervisor_RequireApproval(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	req, err := s.RequestAccess(ctx, "U123", "testuser", "deploy.staging", "api-server")
	require.NoError(t, err)
	assert.Equal(t, models.StatusPending, req.Status)

	// Approve
	err = s.Approve(ctx, req.ID, "U456")
	require.NoError(t, err)

	approved, err := s.GetRequest(req.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusApproved, approved.Status)
	assert.Equal(t, "U456", approved.ResolvedBy)
}

func TestSupervisor_Deny(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	req, err := s.RequestAccess(ctx, "U123", "testuser", "jira.issue.create", "PLAT")
	require.NoError(t, err)

	err = s.Deny(ctx, req.ID, "U789")
	require.NoError(t, err)

	denied, err := s.GetRequest(req.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusDenied, denied.Status)
}

func TestSupervisor_DeniedByPolicy(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	req, err := s.RequestAccess(ctx, "U123", "testuser", "deploy.production", "api-server")
	require.NoError(t, err)
	assert.Equal(t, models.StatusDenied, req.Status)
	assert.Equal(t, "policy", req.ResolvedBy)
}

func TestSupervisor_ApproveNonexistent(t *testing.T) {
	s := newTestSupervisor()
	err := s.Approve(context.Background(), "nonexistent", "U456")
	assert.Error(t, err)
}

func TestSupervisor_DenyNonexistent(t *testing.T) {
	s := newTestSupervisor()
	err := s.Deny(context.Background(), "nonexistent", "U456")
	assert.Error(t, err)
}

func TestSupervisor_DoubleApprove(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()
	req, _ := s.RequestAccess(ctx, "U1", "user", "deploy.staging", "svc")
	_ = s.Approve(ctx, req.ID, "U2")
	err := s.Approve(ctx, req.ID, "U3")
	assert.Error(t, err) // already approved
}

func TestSupervisor_ApproveAlreadyDenied(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()
	req, _ := s.RequestAccess(ctx, "U1", "user", "deploy.staging", "svc")
	_ = s.Deny(ctx, req.ID, "U2")
	err := s.Approve(ctx, req.ID, "U3")
	assert.Error(t, err)
}

func TestSupervisor_GetRequest(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()
	req, _ := s.RequestAccess(ctx, "U1", "user", "deploy.staging", "svc")
	got, err := s.GetRequest(req.ID)
	require.NoError(t, err)
	assert.Equal(t, req.ID, got.ID)
}

func TestSupervisor_GetRequestNotFound(t *testing.T) {
	s := newTestSupervisor()
	_, err := s.GetRequest("nope")
	assert.Error(t, err)
}

func TestPolicy_AddRule(t *testing.T) {
	p := DefaultPolicy()
	p.AddRule("custom.action", models.AccessDenied)
	assert.Equal(t, models.AccessDenied, p.Evaluate("custom.action"))
}

func TestPolicy_ListRules(t *testing.T) {
	p := DefaultPolicy()
	rules := p.ListRules()
	assert.Greater(t, len(rules), 0)
	// Verify it's a copy
	rules["test"] = models.AccessDenied
	assert.NotEqual(t, len(rules), len(p.ListRules()))
}

func TestAccessLevel_String(t *testing.T) {
	assert.Equal(t, "auto_approve", models.AccessAutoApprove.String())
	assert.Equal(t, "require_approval", models.AccessRequireApproval.String())
	assert.Equal(t, "denied", models.AccessDenied.String())
	assert.Equal(t, "unknown", models.AccessLevel(99).String())
}

func TestAuditLog_EmptyFilter(t *testing.T) {
	logger := zerolog.Nop()
	audit := NewAuditLog(logger)
	entries := audit.GetEntries("nobody", 10)
	assert.Empty(t, entries)
}

func TestAuditLog(t *testing.T) {
	logger := zerolog.Nop()
	audit := NewAuditLog(logger)

	audit.Record(models.AuditEntry{UserID: "U1", Action: "read", Result: "ok"})
	audit.Record(models.AuditEntry{UserID: "U2", Action: "write", Result: "ok"})
	audit.Record(models.AuditEntry{UserID: "U1", Action: "deploy", Result: "denied"})

	assert.Equal(t, 3, audit.Count())

	// Filter by user
	u1Entries := audit.GetEntries("U1", 10)
	assert.Len(t, u1Entries, 2)

	// All entries
	all := audit.GetEntries("", 10)
	assert.Len(t, all, 3)

	// Limit
	limited := audit.GetEntries("", 1)
	assert.Len(t, limited, 1)
}

// --- JIT Permission System Tests ---

func TestSupervisor_RequestPermissions_AgentChat(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	result, err := s.RequestPermissions(ctx, "policy.list", "user1", "task-1")
	require.NoError(t, err)
	assert.True(t, result.AllGranted)
	assert.Empty(t, result.Granted)
	assert.Empty(t, result.Pending)
	assert.Empty(t, result.Denied)
}

func TestSupervisor_RequestPermissions_ReadTask(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	// github.review-pr requires PermGithubPRRead, which is auto-approve by default
	result, err := s.RequestPermissions(ctx, "github.review-pr", "user1", "task-1")
	require.NoError(t, err)
	assert.True(t, result.AllGranted)
	assert.Contains(t, result.Granted, PermGithubPRRead)
	assert.Empty(t, result.Pending)
	assert.Empty(t, result.Denied)

	// Grant should be in store
	assert.True(t, s.grants.Check(PermGithubPRRead, "task-1"))
}

func TestSupervisor_RequestPermissions_WriteTask(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	// jira.create-issue requires PermJiraWrite, which is require-approval by default
	result, err := s.RequestPermissions(ctx, "jira.create-issue", "user1", "task-1")
	require.NoError(t, err)
	assert.False(t, result.AllGranted)
	assert.Empty(t, result.Denied)
	assert.NotEmpty(t, result.Pending)

	// Check that the pending approval is for PermJiraWrite
	found := false
	for _, p := range result.Pending {
		if p.Permission == PermJiraWrite {
			found = true
		}
	}
	assert.True(t, found)
}

func TestSupervisor_RequestPermissions_MixedPermissions(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	// github.create-pr requires PermGithubPRRead (auto) + PermGithubPRWrite (require-approval)
	result, err := s.RequestPermissions(ctx, "github.create-pr", "user1", "task-1")
	require.NoError(t, err)
	assert.False(t, result.AllGranted)
	assert.Contains(t, result.Granted, PermGithubPRRead)
	assert.NotEmpty(t, result.Pending)

	pendingPerms := make([]Permission, 0)
	for _, p := range result.Pending {
		pendingPerms = append(pendingPerms, p.Permission)
	}
	assert.Contains(t, pendingPerms, PermGithubPRWrite)
}

func TestSupervisor_RequestPermissions_DeniedPermission(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	// Manually set up a task type that requires an always-deny permission
	TaskPermissionMap["test.dangerous"] = []Permission{PermK8sDelete}
	defer delete(TaskPermissionMap, "test.dangerous")

	result, err := s.RequestPermissions(ctx, "test.dangerous", "user1", "task-1")
	require.NoError(t, err)
	assert.False(t, result.AllGranted)
	assert.Contains(t, result.Denied, PermK8sDelete)
}

func TestSupervisor_RequestPermissions_UnknownTaskType(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	_, err := s.RequestPermissions(ctx, "unknown.task", "user1", "task-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown task type")
}

func TestSupervisor_RequestPermissions_ExistingGrant(t *testing.T) {
	s := newTestSupervisor()
	ctx := context.Background()

	// Pre-issue a grant
	s.grants.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)

	result, err := s.RequestPermissions(ctx, "github.review-pr", "user1", "task-1")
	require.NoError(t, err)
	assert.True(t, result.AllGranted)
	assert.Contains(t, result.Granted, PermGithubPRRead)
}

func TestSupervisor_GrantPermission(t *testing.T) {
	s := newTestSupervisor()

	grant := s.GrantPermission(PermJiraWrite, "user1", "admin1", "task-1")
	assert.NotEmpty(t, grant.ID)
	assert.Equal(t, PermJiraWrite, grant.Permission)
	assert.Equal(t, "admin1", grant.GrantedBy)
	assert.True(t, s.grants.Check(PermJiraWrite, "task-1"))
}

func TestSupervisor_ApplyPolicyChange(t *testing.T) {
	s := newTestSupervisor()

	// Change github.pr.read from auto-approve to require-approval
	change := PolicyChange{
		Permission: PermGithubPRRead,
		NewLevel:   PolicyRequireApproval,
		Reason:     "test",
	}
	s.ApplyPolicyChange(change, "admin1")

	assert.Equal(t, PolicyRequireApproval, s.policy.GetPermissionPolicy(PermGithubPRRead))
}

func TestSupervisor_IsAdmin(t *testing.T) {
	s := newTestSupervisor()

	// No admins configured → everyone is admin
	assert.True(t, s.IsAdmin("anyone"))

	// Configure admins
	s.SetAdminUsers([]string{"admin1", "admin2"})

	assert.True(t, s.IsAdmin("admin1"))
	assert.True(t, s.IsAdmin("admin2"))
	assert.False(t, s.IsAdmin("random_user"))
}

func TestSupervisor_Policy_PermissionPolicies(t *testing.T) {
	p := DefaultPolicy()

	// Default reads should be auto-approve
	assert.Equal(t, PolicyAutoApprove, p.GetPermissionPolicy(PermGithubPRRead))
	assert.Equal(t, PolicyAutoApprove, p.GetPermissionPolicy(PermK8sRead))
	assert.Equal(t, PolicyAutoApprove, p.GetPermissionPolicy(PermJiraRead))

	// Default writes should be require-approval
	assert.Equal(t, PolicyRequireApproval, p.GetPermissionPolicy(PermGithubPRWrite))
	assert.Equal(t, PolicyRequireApproval, p.GetPermissionPolicy(PermJiraWrite))
	assert.Equal(t, PolicyRequireApproval, p.GetPermissionPolicy(PermK8sWrite))

	// Dangerous should be always-deny
	assert.Equal(t, PolicyAlwaysDeny, p.GetPermissionPolicy(PermGithubPRMerge))
	assert.Equal(t, PolicyAlwaysDeny, p.GetPermissionPolicy(PermK8sDelete))
	assert.Equal(t, PolicyAlwaysDeny, p.GetPermissionPolicy(PermK8sExec))
	assert.Equal(t, PolicyAlwaysDeny, p.GetPermissionPolicy(PermDeployProd))
}

func TestSupervisor_Policy_SetAndGetPermissionPolicy(t *testing.T) {
	p := DefaultPolicy()

	// Change a policy
	p.SetPermissionPolicy(PermGithubPRRead, PolicyRequireApproval)
	assert.Equal(t, PolicyRequireApproval, p.GetPermissionPolicy(PermGithubPRRead))

	// Other policies unchanged
	assert.Equal(t, PolicyAutoApprove, p.GetPermissionPolicy(PermK8sRead))
}

func TestSupervisor_Policy_ListPermissionPolicies(t *testing.T) {
	p := DefaultPolicy()
	policies := p.ListPermissionPolicies()
	assert.Greater(t, len(policies), 0)

	// Should be a copy
	policies[PermGithubPRRead] = PolicyAlwaysDeny
	assert.Equal(t, PolicyAutoApprove, p.GetPermissionPolicy(PermGithubPRRead))
}

func TestSupervisor_Policy_ResetPermissionPolicies(t *testing.T) {
	p := DefaultPolicy()

	// Modify a policy
	p.SetPermissionPolicy(PermGithubPRRead, PolicyAlwaysDeny)
	assert.Equal(t, PolicyAlwaysDeny, p.GetPermissionPolicy(PermGithubPRRead))

	// Reset
	p.ResetPermissionPolicies()
	assert.Equal(t, PolicyAutoApprove, p.GetPermissionPolicy(PermGithubPRRead))
}

func TestSupervisor_Policy_UnknownPermission(t *testing.T) {
	p := DefaultPolicy()

	// Unknown permission defaults to require-approval
	assert.Equal(t, PolicyRequireApproval, p.GetPermissionPolicy(Permission("unknown.perm")))
}

func TestSupervisor_RequestPermissions_AuditRecorded(t *testing.T) {
	logger := zerolog.Nop()
	policy := DefaultPolicy()
	audit := NewAuditLog(logger)
	s := NewSupervisor(policy, audit, 10*time.Minute, logger)

	ctx := context.Background()
	_, err := s.RequestPermissions(ctx, "github.review-pr", "user1", "task-1")
	require.NoError(t, err)

	// Should have audit entries for auto-approved grant
	entries := audit.GetEntries("user1", 10)
	assert.NotEmpty(t, entries)

	found := false
	for _, e := range entries {
		if e.Action == "permission.grant" && e.Result == "auto_approved" {
			found = true
		}
	}
	assert.True(t, found, "should have audit entry for auto-approved grant")
}

func TestSupervisor_Grants_Accessor(t *testing.T) {
	s := newTestSupervisor()
	assert.NotNil(t, s.Grants())
}

func TestSupervisor_Policy_Accessor(t *testing.T) {
	s := newTestSupervisor()
	assert.NotNil(t, s.Policy())
}
