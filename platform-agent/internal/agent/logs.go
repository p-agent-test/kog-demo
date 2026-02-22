package agent

import (
	"context"

	"github.com/p-blackswan/platform-agent/internal/k8s"
)

const maxLogChars = 3000

// K8sClient abstracts Kubernetes operations for testing.
type K8sClient interface {
	GetPodLogs(ctx context.Context, namespace, podName string, tailLines int) (string, error)
	FindPods(ctx context.Context, namespace, labelSelector string) ([]k8s.PodInfo, error)
	GetEvents(ctx context.Context, namespace, podName string) ([]k8s.EventInfo, error)
}
