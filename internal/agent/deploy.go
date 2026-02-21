package agent

import (
	"context"

	"github.com/p-blackswan/platform-agent/internal/github"
)

// GitOpsClient abstracts the GitOps PR creation for testing.
type GitOpsClient interface {
	CreateDeployPR(ctx context.Context, req github.DeployPRRequest) (*github.DeployPRResult, error)
}
