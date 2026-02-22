package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/models"
)

// PermissionResult is returned by RequestPermissions.
type PermissionResult struct {
	Granted    []Permission      // auto-approved
	Pending    []PendingApproval // waiting for human
	Denied     []Permission      // always-deny
	AllGranted bool              // true if all permissions are granted
}

// PendingApproval represents a permission waiting for human approval.
type PendingApproval struct {
	Permission Permission
	RequestID  string
	Channel    string
	MessageTS  string
}

// Supervisor manages access control and approval flows.
type Supervisor struct {
	policy     *Policy
	audit      *AuditLog
	grants     *GrantStore
	requests   sync.Map // map[string]*models.AccessRequest
	tokenTTL   time.Duration
	logger     zerolog.Logger
	adminUsers map[string]bool // user IDs allowed to change policies
}

// NewSupervisor creates a new supervisor.
func NewSupervisor(policy *Policy, audit *AuditLog, tokenTTL time.Duration, logger zerolog.Logger) *Supervisor {
	return &Supervisor{
		policy:     policy,
		audit:      audit,
		grants:     NewGrantStore(logger),
		tokenTTL:   tokenTTL,
		logger:     logger.With().Str("component", "supervisor").Logger(),
		adminUsers: make(map[string]bool),
	}
}

// SetAdminUsers sets the list of user IDs allowed to change policies.
func (s *Supervisor) SetAdminUsers(userIDs []string) {
	s.adminUsers = make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		s.adminUsers[id] = true
	}
}

// IsAdmin returns true if the user is an admin.
func (s *Supervisor) IsAdmin(userID string) bool {
	// If no admins configured, everyone is admin (backward compat)
	if len(s.adminUsers) == 0 {
		return true
	}
	return s.adminUsers[userID]
}

// Policy returns the supervisor's policy (for reading permission policies).
func (s *Supervisor) Policy() *Policy {
	return s.policy
}

// Grants returns the supervisor's grant store.
func (s *Supervisor) Grants() *GrantStore {
	return s.grants
}

// RequestAccess evaluates an action and returns the result or creates a pending request.
func (s *Supervisor) RequestAccess(ctx context.Context, userID, userName, action, resource string) (*models.AccessRequest, error) {
	level := s.policy.Evaluate(action)

	req := &models.AccessRequest{
		ID:          uuid.New().String(),
		UserID:      userID,
		UserName:    userName,
		Action:      action,
		Resource:    resource,
		Level:       level,
		RequestedAt: time.Now(),
		TTL:         s.tokenTTL,
	}

	switch level {
	case models.AccessAutoApprove:
		req.Status = models.StatusApproved
		now := time.Now()
		req.ResolvedAt = &now
		req.ResolvedBy = "auto"

		s.audit.Record(models.AuditEntry{
			ID:       req.ID,
			UserID:   userID,
			UserName: userName,
			Action:   action,
			Resource: resource,
			Result:   "auto_approved",
		})

		s.logger.Info().
			Str("user", userID).
			Str("action", action).
			Msg("auto-approved")

	case models.AccessRequireApproval:
		req.Status = models.StatusPending
		s.requests.Store(req.ID, req)

		s.audit.Record(models.AuditEntry{
			ID:       req.ID,
			UserID:   userID,
			UserName: userName,
			Action:   action,
			Resource: resource,
			Result:   "pending_approval",
		})

		s.logger.Info().
			Str("user", userID).
			Str("action", action).
			Str("request_id", req.ID).
			Msg("approval required")

	case models.AccessDenied:
		req.Status = models.StatusDenied
		now := time.Now()
		req.ResolvedAt = &now
		req.ResolvedBy = "policy"

		s.audit.Record(models.AuditEntry{
			ID:       req.ID,
			UserID:   userID,
			UserName: userName,
			Action:   action,
			Resource: resource,
			Result:   "denied_by_policy",
		})

		s.logger.Warn().
			Str("user", userID).
			Str("action", action).
			Msg("denied by policy")
	}

	return req, nil
}

// RequestPermissions checks all required permissions for a task type.
// Returns which are auto-approved, which need human approval, which are denied.
func (s *Supervisor) RequestPermissions(ctx context.Context, taskType, callerID, taskID string) (*PermissionResult, error) {
	perms, ok := TaskPermissionMap[taskType]
	if !ok {
		return nil, fmt.Errorf("unknown task type: %s", taskType)
	}

	// No permissions needed
	if len(perms) == 0 {
		return &PermissionResult{AllGranted: true}, nil
	}

	result := &PermissionResult{}

	for _, perm := range perms {
		// Check if there's already an active grant
		if s.grants.Check(perm, taskID) {
			result.Granted = append(result.Granted, perm)
			continue
		}

		level := s.policy.GetPermissionPolicy(perm)

		switch level {
		case PolicyAutoApprove:
			// Issue grant immediately
			grant := s.grants.Issue(perm, callerID, "policy", taskID, DefaultAutoApproveTTL)
			result.Granted = append(result.Granted, perm)

			s.audit.Record(models.AuditEntry{
				ID:       grant.ID,
				UserID:   callerID,
				Action:   "permission.grant",
				Resource: string(perm),
				Result:   "auto_approved",
				Details:  fmt.Sprintf("task=%s, ttl=%s", taskID, DefaultAutoApproveTTL),
			})

			s.logger.Info().
				Str("permission", string(perm)).
				Str("task_id", taskID).
				Str("caller", callerID).
				Msg("permission auto-approved")

		case PolicyNotifyThenDo:
			// Issue grant immediately (notification happens at caller level)
			grant := s.grants.Issue(perm, callerID, "policy", taskID, DefaultAutoApproveTTL)
			result.Granted = append(result.Granted, perm)

			s.audit.Record(models.AuditEntry{
				ID:       grant.ID,
				UserID:   callerID,
				Action:   "permission.grant",
				Resource: string(perm),
				Result:   "notify_then_do",
				Details:  fmt.Sprintf("task=%s", taskID),
			})

		case PolicyRequireApproval:
			reqID := uuid.New().String()
			result.Pending = append(result.Pending, PendingApproval{
				Permission: perm,
				RequestID:  reqID,
			})

			s.audit.Record(models.AuditEntry{
				ID:       reqID,
				UserID:   callerID,
				Action:   "permission.request",
				Resource: string(perm),
				Result:   "pending_approval",
				Details:  fmt.Sprintf("task=%s", taskID),
			})

			s.logger.Info().
				Str("permission", string(perm)).
				Str("task_id", taskID).
				Str("request_id", reqID).
				Msg("permission requires approval")

		case PolicyAlwaysDeny:
			result.Denied = append(result.Denied, perm)

			s.audit.Record(models.AuditEntry{
				ID:       uuid.New().String(),
				UserID:   callerID,
				Action:   "permission.denied",
				Resource: string(perm),
				Result:   "always_deny",
				Details:  fmt.Sprintf("task=%s", taskID),
			})

			s.logger.Warn().
				Str("permission", string(perm)).
				Str("task_id", taskID).
				Msg("permission denied by policy")
		}
	}

	result.AllGranted = len(result.Pending) == 0 && len(result.Denied) == 0

	return result, nil
}

// GrantPermission manually grants a permission (e.g., after human approval).
func (s *Supervisor) GrantPermission(perm Permission, callerID, approverID, taskID string) *PermissionGrant {
	grant := s.grants.Issue(perm, callerID, approverID, taskID, DefaultHumanApprovedTTL)

	s.audit.Record(models.AuditEntry{
		ID:       grant.ID,
		UserID:   callerID,
		Action:   "permission.grant",
		Resource: string(perm),
		Result:   "human_approved",
		Details:  fmt.Sprintf("approved_by=%s, task=%s, ttl=%s", approverID, taskID, DefaultHumanApprovedTTL),
	})

	s.logger.Info().
		Str("permission", string(perm)).
		Str("task_id", taskID).
		Str("approver", approverID).
		Msg("permission granted by human")

	return grant
}

// ApplyPolicyChange applies a PolicyChange and records it in the audit log.
func (s *Supervisor) ApplyPolicyChange(change PolicyChange, appliedBy string) {
	s.policy.SetPermissionPolicy(change.Permission, change.NewLevel)

	s.audit.Record(models.AuditEntry{
		ID:       uuid.New().String(),
		UserID:   appliedBy,
		Action:   "policy.change",
		Resource: string(change.Permission),
		Result:   string(change.NewLevel),
		Details:  change.Reason,
	})

	s.logger.Info().
		Str("permission", string(change.Permission)).
		Str("new_level", string(change.NewLevel)).
		Str("applied_by", appliedBy).
		Str("reason", change.Reason).
		Msg("policy changed")
}

// Approve approves a pending request.
func (s *Supervisor) Approve(ctx context.Context, requestID, approverID string) error {
	val, ok := s.requests.Load(requestID)
	if !ok {
		return fmt.Errorf("request %s not found", requestID)
	}

	req := val.(*models.AccessRequest)
	if req.Status != models.StatusPending {
		return fmt.Errorf("request %s is not pending (status: %s)", requestID, req.Status)
	}

	now := time.Now()
	req.Status = models.StatusApproved
	req.ResolvedAt = &now
	req.ResolvedBy = approverID

	s.audit.Record(models.AuditEntry{
		ID:       req.ID,
		UserID:   req.UserID,
		UserName: req.UserName,
		Action:   req.Action,
		Resource: req.Resource,
		Result:   "approved",
		Details:  fmt.Sprintf("approved by %s", approverID),
	})

	s.logger.Info().
		Str("request_id", requestID).
		Str("approver", approverID).
		Msg("request approved")

	return nil
}

// Deny denies a pending request.
func (s *Supervisor) Deny(ctx context.Context, requestID, denierID string) error {
	val, ok := s.requests.Load(requestID)
	if !ok {
		return fmt.Errorf("request %s not found", requestID)
	}

	req := val.(*models.AccessRequest)
	if req.Status != models.StatusPending {
		return fmt.Errorf("request %s is not pending (status: %s)", requestID, req.Status)
	}

	now := time.Now()
	req.Status = models.StatusDenied
	req.ResolvedAt = &now
	req.ResolvedBy = denierID

	s.audit.Record(models.AuditEntry{
		ID:       req.ID,
		UserID:   req.UserID,
		UserName: req.UserName,
		Action:   req.Action,
		Resource: req.Resource,
		Result:   "denied",
		Details:  fmt.Sprintf("denied by %s", denierID),
	})

	return nil
}

// GetRequest returns a request by ID.
func (s *Supervisor) GetRequest(requestID string) (*models.AccessRequest, error) {
	val, ok := s.requests.Load(requestID)
	if !ok {
		return nil, fmt.Errorf("request %s not found", requestID)
	}
	return val.(*models.AccessRequest), nil
}
