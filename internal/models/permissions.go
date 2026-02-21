package models

import "time"

// AccessLevel defines the permission level for an action.
type AccessLevel int

const (
	AccessAutoApprove AccessLevel = iota // Low-risk read operations
	AccessRequireApproval                // Needs supervisor approval
	AccessDenied                         // Always denied
)

func (a AccessLevel) String() string {
	switch a {
	case AccessAutoApprove:
		return "auto_approve"
	case AccessRequireApproval:
		return "require_approval"
	case AccessDenied:
		return "denied"
	default:
		return "unknown"
	}
}

// ActionCategory groups related actions.
type ActionCategory string

const (
	CategoryRead    ActionCategory = "read"
	CategoryWrite   ActionCategory = "write"
	CategoryDeploy  ActionCategory = "deploy"
	CategoryAdmin   ActionCategory = "admin"
)

// Permission represents an access control entry.
type Permission struct {
	Action   string       `json:"action"`
	Category ActionCategory `json:"category"`
	Level    AccessLevel  `json:"level"`
}

// AccessRequest represents a pending approval request.
type AccessRequest struct {
	ID          string       `json:"id"`
	UserID      string       `json:"user_id"`
	UserName    string       `json:"user_name"`
	Action      string       `json:"action"`
	Resource    string       `json:"resource"`
	Level       AccessLevel  `json:"level"`
	Status      RequestStatus `json:"status"`
	RequestedAt time.Time    `json:"requested_at"`
	ResolvedAt  *time.Time   `json:"resolved_at,omitempty"`
	ResolvedBy  string       `json:"resolved_by,omitempty"`
	TTL         time.Duration `json:"ttl"`
}

// RequestStatus represents the status of an access request.
type RequestStatus string

const (
	StatusPending  RequestStatus = "pending"
	StatusApproved RequestStatus = "approved"
	StatusDenied   RequestStatus = "denied"
	StatusExpired  RequestStatus = "expired"
)

// AuditEntry records an action for audit purposes.
type AuditEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	Result    string    `json:"result"`
	Details   string    `json:"details,omitempty"`
}
