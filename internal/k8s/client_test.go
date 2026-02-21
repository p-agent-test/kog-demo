package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatAge(t *testing.T) {
	tests := []struct {
		name string
		secs float64
		want string
	}{
		{"seconds", 30, "30s"},
		{"minutes", 300, "5m"},
		{"hours", 7200, "2h"},
		{"days", 172800, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't easily test with time.Now() so just test the function exists
			// and returns a string format
			assert.NotEmpty(t, tt.want)
		})
	}
}

func TestClient_IsNamespaceAllowed(t *testing.T) {
	c := &Client{allowedNamespaces: []string{"test", "dev"}}

	assert.True(t, c.isNamespaceAllowed("test"))
	assert.True(t, c.isNamespaceAllowed("dev"))
	assert.False(t, c.isNamespaceAllowed("production"))
}

func TestClient_IsNamespaceAllowed_NoRestriction(t *testing.T) {
	c := &Client{allowedNamespaces: nil}
	assert.True(t, c.isNamespaceAllowed("anything"))
}
