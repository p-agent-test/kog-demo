package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	gh "github.com/google/go-github/v60/github"
	"gopkg.in/yaml.v3"
)

// DeployPRRequest contains deploy PR parameters.
type DeployPRRequest struct {
	Service     string
	Version     string
	Environment string
	RequestedBy string
}

// DeployPRResult contains the result of creating a deploy PR.
type DeployPRResult struct {
	PRNumber int
	PRURL    string
	PRTitle  string
	Branch   string
}

// ServiceConfig defines per-service GitOps configuration.
type ServiceConfig struct {
	ValuesFile string // e.g. "services/market-api/values.yaml"
	ImageKey   string // e.g. "image.tag" (dot-separated path)
}

// GitOpsConfig holds configuration for GitOps PR creation.
type GitOpsConfig struct {
	Owner          string
	Repo           string
	BaseBranch     string
	Services       map[string]ServiceConfig
	DefaultKeyPath string // default: "image.tag"
}

// GitOps handles GitOps PR creation for deploys.
type GitOps struct {
	client *Client
	config GitOpsConfig
}

// NewGitOps creates a new GitOps handler.
func NewGitOps(client *Client, config GitOpsConfig) *GitOps {
	if config.BaseBranch == "" {
		config.BaseBranch = "main"
	}
	if config.DefaultKeyPath == "" {
		config.DefaultKeyPath = "image.tag"
	}
	return &GitOps{client: client, config: config}
}

// CreateDeployPR creates a PR that updates the Helm values for a service deploy.
func (g *GitOps) CreateDeployPR(ctx context.Context, req DeployPRRequest) (*DeployPRResult, error) {
	ghClient, err := g.client.GetInstallationClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting github client: %w", err)
	}

	svcConfig := g.getServiceConfig(req.Service)
	branchName := fmt.Sprintf("agent/deploy-%s-%s-%d", req.Service, req.Environment, time.Now().Unix())
	title := fmt.Sprintf("chore: deploy %s %s to %s", req.Service, req.Version, req.Environment)

	// Get base branch ref
	baseRef, _, err := ghClient.Git.GetRef(ctx, g.config.Owner, g.config.Repo, "refs/heads/"+g.config.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("getting base ref: %w", err)
	}

	// Create branch
	newRef := &gh.Reference{
		Ref:    gh.String("refs/heads/" + branchName),
		Object: &gh.GitObject{SHA: baseRef.Object.SHA},
	}
	_, _, err = ghClient.Git.CreateRef(ctx, g.config.Owner, g.config.Repo, newRef)
	if err != nil {
		return nil, fmt.Errorf("creating branch: %w", err)
	}

	// Get current file content
	fileContent, _, _, err := ghClient.Repositories.GetContents(ctx, g.config.Owner, g.config.Repo, svcConfig.ValuesFile, &gh.RepositoryContentGetOptions{Ref: branchName})
	if err != nil {
		return nil, fmt.Errorf("getting values file: %w", err)
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return nil, fmt.Errorf("decoding file content: %w", err)
	}

	// Update YAML
	updatedContent, err := UpdateYAMLValue(content, svcConfig.ImageKey, req.Version)
	if err != nil {
		return nil, fmt.Errorf("updating values: %w", err)
	}

	// Commit updated file
	commitMsg := fmt.Sprintf("chore: update %s image tag to %s for %s", req.Service, req.Version, req.Environment)
	_, _, err = ghClient.Repositories.UpdateFile(ctx, g.config.Owner, g.config.Repo, svcConfig.ValuesFile, &gh.RepositoryContentFileOptions{
		Message: &commitMsg,
		Content: []byte(updatedContent),
		SHA:     fileContent.SHA,
		Branch:  &branchName,
	})
	if err != nil {
		return nil, fmt.Errorf("committing file: %w", err)
	}

	// Create PR
	body := buildDeployPRBody(req)
	pr, _, err := ghClient.PullRequests.Create(ctx, g.config.Owner, g.config.Repo, &gh.NewPullRequest{
		Title: &title,
		Body:  &body,
		Head:  &branchName,
		Base:  &g.config.BaseBranch,
	})
	if err != nil {
		return nil, fmt.Errorf("creating PR: %w", err)
	}

	return &DeployPRResult{
		PRNumber: pr.GetNumber(),
		PRURL:    pr.GetHTMLURL(),
		PRTitle:  title,
		Branch:   branchName,
	}, nil
}

func (g *GitOps) getServiceConfig(service string) ServiceConfig {
	if cfg, ok := g.config.Services[service]; ok {
		return cfg
	}
	return ServiceConfig{
		ValuesFile: fmt.Sprintf("services/%s/values.yaml", service),
		ImageKey:   g.config.DefaultKeyPath,
	}
}

func buildDeployPRBody(req DeployPRRequest) string {
	return fmt.Sprintf(`## Deploy Request

| Field | Value |
|-------|-------|
| **Service** | %s |
| **Version** | %s |
| **Environment** | %s |
| **Requested By** | <@%s> |
| **Timestamp** | %s |

---
_This PR was created automatically by Platform Agent. Review and merge to complete the deployment._
_⚠️ Do NOT merge without verification._`,
		req.Service, req.Version, req.Environment, req.RequestedBy, time.Now().UTC().Format(time.RFC3339))
}

// UpdateYAMLValue updates a dot-separated key path in a YAML string.
func UpdateYAMLValue(yamlContent, keyPath, newValue string) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return "", fmt.Errorf("parsing YAML: %w", err)
	}

	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("invalid YAML document")
	}

	keys := strings.Split(keyPath, ".")
	if err := setNestedValue(root.Content[0], keys, newValue); err != nil {
		return "", err
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("marshaling YAML: %w", err)
	}

	return string(out), nil
}

func setNestedValue(node *yaml.Node, keys []string, value string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %v", node.Kind)
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		if keyNode.Value == keys[0] {
			if len(keys) == 1 {
				valNode.Value = value
				valNode.Tag = "!!str"
				valNode.Kind = yaml.ScalarNode
				return nil
			}
			return setNestedValue(valNode, keys[1:], value)
		}
	}

	return fmt.Errorf("key %q not found in YAML", keys[0])
}
