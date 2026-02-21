// Package k8s provides a Kubernetes client wrapper for pod logs, events, and listing.
package k8s

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/rs/zerolog"
)

// PodInfo contains basic pod information.
type PodInfo struct {
	Name      string
	Namespace string
	Status    string
	Restarts  int
	Age       string
	Labels    map[string]string
}

// EventInfo contains a Kubernetes event summary.
type EventInfo struct {
	Reason  string
	Message string
	Type    string
	Age     string
	Count   int
}

// Client wraps the Kubernetes API.
type Client struct {
	clientset         kubernetes.Interface
	allowedNamespaces []string
	logger            zerolog.Logger
}

// Config holds K8s client configuration.
type Config struct {
	KubeconfigPath    string
	AllowedNamespaces []string
}

// NewClient creates a K8s client from kubeconfig or in-cluster config.
func NewClient(cfg Config, logger zerolog.Logger) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if cfg.KubeconfigPath != "" {
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.KubeconfigPath)
	} else {
		restConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("building k8s config: %w", err)
	}

	cs, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating k8s clientset: %w", err)
	}

	return &Client{
		clientset:         cs,
		allowedNamespaces: cfg.AllowedNamespaces,
		logger:            logger.With().Str("component", "k8s").Logger(),
	}, nil
}

// NewClientFromInterface creates a client from an existing kubernetes.Interface (for testing).
func NewClientFromInterface(cs kubernetes.Interface, allowedNamespaces []string, logger zerolog.Logger) *Client {
	return &Client{
		clientset:         cs,
		allowedNamespaces: allowedNamespaces,
		logger:            logger.With().Str("component", "k8s").Logger(),
	}
}

func (c *Client) isNamespaceAllowed(ns string) bool {
	if len(c.allowedNamespaces) == 0 {
		return true
	}
	for _, a := range c.allowedNamespaces {
		if a == ns {
			return true
		}
	}
	return false
}

// GetPodLogs returns the last N lines of logs for a pod.
func (c *Client) GetPodLogs(ctx context.Context, namespace, podName string, tailLines int) (string, error) {
	if !c.isNamespaceAllowed(namespace) {
		return "", fmt.Errorf("namespace %q is not allowed", namespace)
	}

	tail := int64(tailLines)
	opts := &corev1.PodLogOptions{
		TailLines: &tail,
	}

	stream, err := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, opts).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("getting pod logs: %w", err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("reading pod logs: %w", err)
	}

	return string(data), nil
}

// FindPods finds pods matching a label selector.
func (c *Client) FindPods(ctx context.Context, namespace, labelSelector string) ([]PodInfo, error) {
	if !c.isNamespaceAllowed(namespace) {
		return nil, fmt.Errorf("namespace %q is not allowed", namespace)
	}

	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	var result []PodInfo
	for _, p := range pods.Items {
		restarts := 0
		for _, cs := range p.Status.ContainerStatuses {
			restarts += int(cs.RestartCount)
		}

		result = append(result, PodInfo{
			Name:      p.Name,
			Namespace: p.Namespace,
			Status:    string(p.Status.Phase),
			Restarts:  restarts,
			Age:       formatAge(p.CreationTimestamp.Time),
			Labels:    p.Labels,
		})
	}

	return result, nil
}

// GetEvents returns recent events for a pod.
func (c *Client) GetEvents(ctx context.Context, namespace, podName string) ([]EventInfo, error) {
	if !c.isNamespaceAllowed(namespace) {
		return nil, fmt.Errorf("namespace %q is not allowed", namespace)
	}

	fieldSelector := fmt.Sprintf("involvedObject.name=%s", podName)
	events, err := c.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}

	var result []EventInfo
	for _, e := range events.Items {
		result = append(result, EventInfo{
			Reason:  e.Reason,
			Message: e.Message,
			Type:    e.Type,
			Age:     formatAge(e.LastTimestamp.Time),
			Count:   int(e.Count),
		})
	}

	return result, nil
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// DescribePod returns detailed info about a pod.
func (c *Client) DescribePod(ctx context.Context, namespace, podName string) (*PodInfo, error) {
	if !c.isNamespaceAllowed(namespace) {
		return nil, fmt.Errorf("namespace %q is not allowed", namespace)
	}

	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting pod: %w", err)
	}

	restarts := 0
	var statusParts []string
	for _, cs := range pod.Status.ContainerStatuses {
		restarts += int(cs.RestartCount)
		if cs.State.Waiting != nil {
			statusParts = append(statusParts, cs.State.Waiting.Reason)
		}
	}

	status := string(pod.Status.Phase)
	if len(statusParts) > 0 {
		status = strings.Join(statusParts, ", ")
	}

	return &PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Status:    status,
		Restarts:  restarts,
		Age:       formatAge(pod.CreationTimestamp.Time),
		Labels:    pod.Labels,
	}, nil
}
