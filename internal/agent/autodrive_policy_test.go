package agent

import (
	"encoding/json"
	"testing"
)

func TestSlugFromSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"agent:main:project-my-app", "my-app"},
		{"agent:main:project-my-app-v1", "my-app"},
		{"agent:main:project-my-app-v12", "my-app"},
		{"agent:main:project-krakend-request-log-plugin", "krakend-request-log-plugin"},
		{"agent:main:project-krakend-request-log-plugin-v3", "krakend-request-log-plugin"},
		{"agent:main:main", ""},          // not a project session
		{"other:prefix:project-x", ""},   // wrong prefix
		{"", ""},
		{"agent:main:project-", ""},      // empty slug
		{"agent:main:project-a-vx", "a-vx"}, // -vx is not version (x not digit)
	}

	for _, tt := range tests {
		got := slugFromSessionKey(tt.key)
		if got != tt.want {
			t.Errorf("slugFromSessionKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestGetAutoDrivePolicy(t *testing.T) {
	tests := []struct {
		op     string
		params string
		want   autoDrivePolicy
	}{
		// Auto-approve feature branch commits
		{"git.commit", `{"branch":"feat/new-feature"}`, adPolicyAutoApprove},
		{"git.commit", `{"branch":"project/my-app/impl"}`, adPolicyAutoApprove},
		{"git.commit", `{"ref":"develop"}`, adPolicyAutoApprove},

		// Deny main/master commits
		{"git.commit", `{"branch":"main"}`, adPolicyDeny},
		{"git.commit", `{"branch":"master"}`, adPolicyDeny},
		{"git.commit", `{"ref":"main"}`, adPolicyDeny},

		// Auto-approve branch creation
		{"git.create-branch", `{}`, adPolicyAutoApprove},

		// Auto-approve PR/issue operations
		{"pr.create", `{}`, adPolicyAutoApprove},
		{"pr.comment", `{}`, adPolicyAutoApprove},
		{"issue.create", `{}`, adPolicyAutoApprove},
		{"issue.comment", `{}`, adPolicyAutoApprove},

		// Require approval
		{"pr.review", `{}`, adPolicyRequireApproval},
		{"repo.create", `{}`, adPolicyRequireApproval},
		{"repo.token", `{}`, adPolicyRequireApproval},

		// Unknown operations require approval
		{"unknown.op", `{}`, adPolicyRequireApproval},

		// Empty/nil params for git.commit — auto-approve (no branch = not main)
		{"git.commit", `{}`, adPolicyAutoApprove},
		{"git.commit", ``, adPolicyAutoApprove},
	}

	for _, tt := range tests {
		var params json.RawMessage
		if tt.params != "" {
			params = json.RawMessage(tt.params)
		}
		got := getAutoDrivePolicy(tt.op, params)
		if got != tt.want {
			t.Errorf("getAutoDrivePolicy(%q, %s) = %q, want %q", tt.op, tt.params, got, tt.want)
		}
	}
}

func TestExtractBranch(t *testing.T) {
	tests := []struct {
		params string
		want   string
	}{
		{`{"branch":"main"}`, "main"},
		{`{"ref":"develop"}`, "develop"},
		{`{"base_branch":"release"}`, "release"},
		{`{"branch":"feat","ref":"ignored"}`, "feat"}, // branch takes precedence
		{`{}`, ""},
		{``, ""},
	}

	for _, tt := range tests {
		got := extractBranch(json.RawMessage(tt.params))
		if got != tt.want {
			t.Errorf("extractBranch(%s) = %q, want %q", tt.params, got, tt.want)
		}
	}
}
