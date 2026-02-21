package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateYAMLValue_Simple(t *testing.T) {
	input := `image:
  repository: myrepo/myservice
  tag: v1.0.0
replicas: 3
`
	result, err := UpdateYAMLValue(input, "image.tag", "v2.0.0")
	require.NoError(t, err)
	assert.Contains(t, result, "v2.0.0")
	assert.Contains(t, result, "myrepo/myservice")
	assert.Contains(t, result, "replicas")
}

func TestUpdateYAMLValue_TopLevel(t *testing.T) {
	input := `version: old
name: test
`
	result, err := UpdateYAMLValue(input, "version", "new")
	require.NoError(t, err)
	assert.Contains(t, result, "new")
}

func TestUpdateYAMLValue_DeepNested(t *testing.T) {
	input := `app:
  container:
    image:
      tag: v1.0.0
`
	result, err := UpdateYAMLValue(input, "app.container.image.tag", "v3.0.0")
	require.NoError(t, err)
	assert.Contains(t, result, "v3.0.0")
}

func TestUpdateYAMLValue_KeyNotFound(t *testing.T) {
	input := `image:
  tag: v1.0.0
`
	_, err := UpdateYAMLValue(input, "image.nonexistent", "v2.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUpdateYAMLValue_InvalidYAML(t *testing.T) {
	_, err := UpdateYAMLValue(":::invalid", "key", "val")
	assert.Error(t, err)
}

func TestUpdateYAMLValue_EmptyDoc(t *testing.T) {
	_, err := UpdateYAMLValue("", "key", "val")
	assert.Error(t, err)
}

func TestBuildDeployPRBody(t *testing.T) {
	body := buildDeployPRBody(DeployPRRequest{
		Service:     "market-api",
		Version:     "v1.2.3",
		Environment: "staging",
		RequestedBy: "U123",
	})
	assert.Contains(t, body, "market-api")
	assert.Contains(t, body, "v1.2.3")
	assert.Contains(t, body, "staging")
	assert.Contains(t, body, "U123")
}

func TestGetServiceConfig_Default(t *testing.T) {
	g := NewGitOps(nil, GitOpsConfig{DefaultKeyPath: "image.tag"})
	cfg := g.getServiceConfig("unknown-svc")
	assert.Equal(t, "services/unknown-svc/values.yaml", cfg.ValuesFile)
	assert.Equal(t, "image.tag", cfg.ImageKey)
}

func TestGetServiceConfig_Custom(t *testing.T) {
	g := NewGitOps(nil, GitOpsConfig{
		Services: map[string]ServiceConfig{
			"market-api": {
				ValuesFile: "helm/market-api/values-staging.yaml",
				ImageKey:   "app.image.tag",
			},
		},
	})
	cfg := g.getServiceConfig("market-api")
	assert.Equal(t, "helm/market-api/values-staging.yaml", cfg.ValuesFile)
	assert.Equal(t, "app.image.tag", cfg.ImageKey)
}
