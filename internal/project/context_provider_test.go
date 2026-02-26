package project

import (
	"testing"
)

func TestExtractSlugFromSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"agent:main:project-idp", "idp"},
		{"agent:main:project-my-cool-app", "my-cool-app"},
		{"agent:main:project-idp-v2", "idp"},
		{"agent:main:project-my-app-v12", "my-app"},
		{"agent:main:slack-C123", ""},
		{"", ""},
		{"agent:main:project-", ""},
	}
	for _, tt := range tests {
		got := extractSlugFromSessionKey(tt.key)
		if got != tt.want {
			t.Errorf("extractSlugFromSessionKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
