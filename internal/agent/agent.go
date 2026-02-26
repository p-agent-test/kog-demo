// Package agent provides the task executor that routes structured commands
// to the appropriate integrations (GitHub, K8s, Jira, Slack).
// All intelligence/NLP is handled by Kog-2 (OpenClaw/Claude) which calls
// the Management API. This package is a pure execution engine.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"

	ghclient "github.com/p-blackswan/platform-agent/internal/github"
	jiraclient "github.com/p-blackswan/platform-agent/internal/jira"
	"github.com/p-blackswan/platform-agent/internal/project"
	"github.com/p-blackswan/platform-agent/internal/metrics"
	"github.com/p-blackswan/platform-agent/internal/mgmt"
	"github.com/p-blackswan/platform-agent/internal/models"
	slackblocks "github.com/p-blackswan/platform-agent/internal/slack"
	"github.com/p-blackswan/platform-agent/internal/store"
	"github.com/p-blackswan/platform-agent/internal/supervisor"
)

// SlackAPI abstracts Slack posting for testing.
type SlackAPI interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
}

// TaskRequeuer allows the agent to re-queue tasks after approval.
type TaskRequeuer interface {
	Requeue(taskID string) error
}

// pendingApprovalInfo stores metadata for a task awaiting human approval.
type pendingApprovalInfo struct {
	TaskID     string
	CallerID   string
	Permission supervisor.Permission
	Action     string
	Resource   string
	Operation  string // e.g. "pr.create" — for richer log messages
	Details    string // short summary for approve/deny messages
	ChannelID  string // supervisor channel where buttons were posted
	ThreadTS   string // messageTS of the approval buttons message (used as thread parent)
}

// Agent is the task executor. It receives structured commands via the Management API
// and executes them against the configured integrations.
type Agent struct {
	github     *ghclient.MultiClient
	gitops     GitOpsClient
	jira       *jiraclient.Client
	k8s        K8sClient
	supervisor *supervisor.Supervisor
	slack      SlackAPI
	audit      *supervisor.AuditLog
	metrics    *metrics.Metrics
	contexts   *ContextStore
	dataStore    *store.Store    // optional SQLite backend
	projectStore *project.Store // project store for auto-drive detection
	logger       zerolog.Logger

	// Approval flow
	pendingMu        sync.RWMutex
	pendingApprovals map[string]*pendingApprovalInfo // requestID → info
	requeuer         TaskRequeuer

	// Config
	supervisorChannel string
	jiraProjectKey    string
	defaultNamespace  string
	allowedNamespaces []string
}

// Config holds agent configuration.
type Config struct {
	SupervisorChannel string
	JiraProjectKey    string
	DefaultNamespace  string
	AllowedNamespaces []string
}

// NewAgent creates a new task executor agent.
func NewAgent(
	gh *ghclient.MultiClient,
	jira *jiraclient.Client,
	sup *supervisor.Supervisor,
	slackAPI SlackAPI,
	audit *supervisor.AuditLog,
	cfg Config,
	logger zerolog.Logger,
) *Agent {
	return &Agent{
		github:            gh,
		jira:              jira,
		supervisor:        sup,
		slack:             slackAPI,
		audit:             audit,
		contexts:          NewContextStore(10, time.Hour),
		logger:            logger.With().Str("component", "agent").Logger(),
		pendingApprovals:  make(map[string]*pendingApprovalInfo),
		supervisorChannel: cfg.SupervisorChannel,
		jiraProjectKey:    cfg.JiraProjectKey,
		defaultNamespace:  cfg.DefaultNamespace,
		allowedNamespaces: cfg.AllowedNamespaces,
	}
}

// SetGitOps sets the GitOps client.
func (a *Agent) SetGitOps(g GitOpsClient) {
	a.gitops = g
}

// SetK8s sets the Kubernetes client.
func (a *Agent) SetK8s(k K8sClient) {
	a.k8s = k
}

// SetMetrics sets the metrics collector.
func (a *Agent) SetMetrics(m *metrics.Metrics) {
	a.metrics = m
}

// SetSlack sets the Slack API (used for late binding after init).
func (a *Agent) SetSlack(api SlackAPI) {
	a.slack = api
}

// SetRequeuer sets the task requeuer (used for late binding after task engine init).
func (a *Agent) SetRequeuer(r TaskRequeuer) {
	a.requeuer = r
}

// SetStore sets the optional SQLite backend for persistence.
func (a *Agent) SetStore(ds *store.Store) {
	a.dataStore = ds
}

// OnApproval handles an approval/denial callback from Slack interactive buttons.
// It grants or denies the permission and re-queues the task if approved.
func (a *Agent) OnApproval(requestID, approverID string, approved bool) {
	a.pendingMu.Lock()
	info, ok := a.pendingApprovals[requestID]
	if ok {
		delete(a.pendingApprovals, requestID)
	}
	a.pendingMu.Unlock()

	if !ok {
		a.logger.Warn().Str("request_id", requestID).Msg("approval for unknown request — ignoring")
		return
	}

	// Delete from store if available
	if a.dataStore != nil {
		_ = a.dataStore.DeleteApproval(requestID)
	}

	// Build reply options — always thread under the original approval buttons message
	replyOpts := func(text string) []slack.MsgOption {
		opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
		if info.ThreadTS != "" {
			opts = append(opts, slack.MsgOptionTS(info.ThreadTS))
		}
		return opts
	}
	replyChannel := info.ChannelID
	if replyChannel == "" {
		replyChannel = a.supervisorChannel
	}

	if !approved {
		a.audit.Record(models.AuditEntry{
			UserID:   info.CallerID,
			Action:   info.Action,
			Resource: info.Resource,
			Result:   "denied_by_human",
			Details:  fmt.Sprintf("denied by %s, request=%s", approverID, requestID),
		})
		a.logger.Info().
			Str("request_id", requestID).
			Str("approver", approverID).
			Msg("approval denied — task will remain failed")

		if a.slack != nil && replyChannel != "" {
			summary := info.Details
			if summary == "" {
				summary = fmt.Sprintf("`%s` on `%s`", info.Action, info.Resource)
			}
			_, _, _ = a.slack.PostMessage(replyChannel,
				replyOpts(fmt.Sprintf("❌ *Denied* %s (by <@%s>)", summary, approverID))...)
		}
		return
	}

	// Grant the permission
	a.supervisor.GrantPermission(info.Permission, info.CallerID, approverID, info.TaskID)

	a.logger.Info().
		Str("request_id", requestID).
		Str("task_id", info.TaskID).
		Str("approver", approverID).
		Msg("approval granted — re-queueing task")

	// Re-queue the task
	if a.requeuer != nil {
		if err := a.requeuer.Requeue(info.TaskID); err != nil {
			a.logger.Error().Err(err).Str("task_id", info.TaskID).Msg("failed to requeue task after approval")
			if a.slack != nil && replyChannel != "" {
				_, _, _ = a.slack.PostMessage(replyChannel,
					replyOpts(fmt.Sprintf("⚠️ Approved but failed to re-queue task `%s`: %v", info.TaskID, err))...)
			}
			return
		}
	}

	if a.slack != nil && replyChannel != "" {
		summary := info.Details
		if summary == "" {
			summary = fmt.Sprintf("`%s` on `%s`", info.Action, info.Resource)
		}
		_, _, _ = a.slack.PostMessage(replyChannel,
			replyOpts(fmt.Sprintf("✅ *Approved & re-queued* %s (by <@%s>)\nTask `%s` executing…",
				summary, approverID, info.TaskID))...)
	}
}

// formatCompletionMessage creates a human-friendly Slack message from task results.
// Extracts URLs, titles, numbers etc. from known result shapes.
func (a *Agent) formatCompletionMessage(taskType string, result json.RawMessage) string {
	// Try to extract common fields
	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err == nil {
		// Check for nested "data" from GHExecResult wrapper
		if inner, ok := data["data"].(map[string]interface{}); ok {
			data = inner
		}

		url := extractString(data, "url", "html_url", "web_url")
		title := extractString(data, "title", "name", "full_name")
		number := extractFloat(data, "number", "id")
		state := extractString(data, "state", "status")

		if url != "" || title != "" {
			parts := []string{fmt.Sprintf("✅ *%s completed*", taskType)}
			if title != "" {
				if number > 0 {
					parts = append(parts, fmt.Sprintf("*#%.0f* — %s", number, title))
				} else {
					parts = append(parts, title)
				}
			}
			if state != "" {
				parts = append(parts, fmt.Sprintf("State: `%s`", state))
			}
			if url != "" {
				parts = append(parts, url)
			}
			return strings.Join(parts, "\n")
		}
	}

	// Fallback: truncated JSON
	summary := string(result)
	if len(summary) > 500 {
		summary = summary[:500] + "…"
	}
	return fmt.Sprintf("✅ *%s completed*\n```%s```", taskType, summary)
}

// extractString returns the first non-empty string value from data for the given keys.
func extractString(data map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// extractFloat returns the first non-zero float value from data for the given keys.
func extractFloat(data map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := data[k].(float64); ok && v > 0 {
			return v
		}
	}
	return 0
}

// registerPendingApproval stores task info for later approval callback.
func (a *Agent) registerPendingApproval(requestID string, info *pendingApprovalInfo) {
	a.pendingMu.Lock()
	a.pendingApprovals[requestID] = info
	a.pendingMu.Unlock()

	// Also persist to store if available (graceful degradation)
	if a.dataStore != nil {
		approval := &store.PendingApproval{
			RequestID: requestID,
			TaskID:    info.TaskID,
			CallerID:  info.CallerID,
			Permission: string(info.Permission),
			Action:    info.Action,
			Resource:  info.Resource,
			ChannelID: info.ChannelID,
			ThreadTS:  info.ThreadTS,
		}
		if err := a.dataStore.SaveApproval(approval); err != nil {
			a.logger.Warn().Err(err).Str("request_id", requestID).Msg("failed to persist approval to store")
		}
	}
}

// NotifyTaskCompletion posts task results to a Slack channel/thread.
// Implements mgmt.TaskCompletionNotifier.
func (a *Agent) NotifyTaskCompletion(channel, threadTS, taskID, taskType string, status mgmt.TaskStatus, result json.RawMessage, taskErr string) {
	if a.slack == nil || channel == "" {
		return
	}

	var msg string
	switch status {
	case mgmt.TaskCompleted:
		msg = a.formatCompletionMessage(taskType, result)
	case mgmt.TaskFailed:
		msg = fmt.Sprintf("❌ *Task failed*\n*Type:* `%s`\n*Error:* %s", taskType, taskErr)
	default:
		return
	}

	var opts []slack.MsgOption
	opts = append(opts, slack.MsgOptionText(msg, false))
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}

	_, _, _ = a.slack.PostMessage(channel, opts...)

	a.logger.Info().
		Str("task_id", taskID).
		Str("channel", channel).
		Str("thread", threadTS).
		Str("status", string(status)).
		Msg("task completion notified to Slack")
}

// Execute implements mgmt.TaskExecutor. It is the main entry point called by TaskEngine.
func (a *Agent) Execute(ctx context.Context, taskType string, params json.RawMessage) (json.RawMessage, error) {
	start := time.Now()

	log := a.logger.With().Str("task_type", taskType).Logger()
	log.Info().RawJSON("params", safeRawJSON(params)).Msg("executing task")

	var result json.RawMessage
	var err error

	switch taskType {
	// GitHub tasks
	case "github.review-pr":
		result, err = a.executeGitHubReviewPR(ctx, params)
	case "github.create-pr":
		result, err = a.executeGitHubCreatePR(ctx, params)
	case "github.exec":
		result, err = a.executeGHExec(ctx, params)

	// K8s tasks
	case "k8s.pod-logs":
		result, err = a.executeK8sPodLogs(ctx, params)
	case "k8s.pod-status":
		result, err = a.executeK8sPodStatus(ctx, params)

	// Jira tasks
	case "jira.get-issue":
		result, err = a.executeJiraGetIssue(ctx, params)
	case "jira.create-issue":
		result, err = a.executeJiraCreateIssue(ctx, params)

	// Slack tasks
	case "slack.send-message":
		result, err = a.executeSlackSendMessage(ctx, params)
	case "slack.read-thread":
		result, err = a.executeSlackReadThread(ctx, params)

	// Policy tasks
	case "policy.list":
		result, err = a.executePolicyList(ctx, params)
	case "policy.set":
		result, err = a.executePolicySet(ctx, params)
	case "policy.reset":
		result, err = a.executePolicyReset(ctx, params)

	// Alert triage
	case "k8s.alert-triage":
		result, err = a.executeAlertTriage(ctx, params)

	default:
		err = fmt.Errorf("unsupported task type: %s", taskType)
	}

	// Record metrics
	if a.metrics != nil {
		duration := time.Since(start).Seconds()
		status := "completed"
		if err != nil {
			status = "error"
		}
		a.metrics.ObserveDuration(taskType, duration)
		a.metrics.RecordRequest(taskType, status)
	}

	return result, err
}

// --- GitHub task executors ---

// GitHubReviewPRParams are the params for github.review-pr.
type GitHubReviewPRParams struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"` // alternative: parse owner/repo/number from URL
	CallerID string `json:"caller_id"`
}

// GitHubReviewPRResult is the result of github.review-pr.
type GitHubReviewPRResult struct {
	Owner      string                `json:"owner"`
	Repo       string                `json:"repo"`
	PRNumber   int                   `json:"pr_number"`
	FilesCount int                   `json:"files_count"`
	Files      []GitHubReviewedFile  `json:"files"`
}

// GitHubReviewedFile represents a file in the PR review result.
type GitHubReviewedFile struct {
	Filename  string `json:"filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func (a *Agent) executeGitHubReviewPR(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p GitHubReviewPRParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	// If URL provided, parse it
	if p.PRURL != "" && p.Owner == "" {
		owner, repo, num, err := ghclient.ParsePRURL(p.PRURL)
		if err != nil {
			return nil, fmt.Errorf("invalid PR URL: %w", err)
		}
		p.Owner, p.Repo, p.PRNumber = owner, repo, num
	}

	if p.Owner == "" || p.Repo == "" || p.PRNumber == 0 {
		return nil, fmt.Errorf("owner, repo, and pr_number are required")
	}

	if a.github == nil {
		return nil, fmt.Errorf("GitHub client is not configured")
	}

	// Check permissions via supervisor
	permResult, err := a.supervisor.RequestPermissions(ctx, "github.review-pr", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	ghClient, err := a.github.ForOwner(p.Owner)
	if err != nil {
		return nil, fmt.Errorf("getting GitHub client for %s: %w", p.Owner, err)
	}
	review, err := ghClient.ReviewPR(ctx, p.Owner, p.Repo, p.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("PR review failed: %w", err)
	}

	result := GitHubReviewPRResult{
		Owner:      p.Owner,
		Repo:       p.Repo,
		PRNumber:   p.PRNumber,
		FilesCount: len(review.Files),
	}

	for _, f := range review.Files {
		rf := GitHubReviewedFile{}
		if f.Filename != nil {
			rf.Filename = *f.Filename
		}
		if f.Additions != nil {
			rf.Additions = *f.Additions
		}
		if f.Deletions != nil {
			rf.Deletions = *f.Deletions
		}
		result.Files = append(result.Files, rf)
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "github.review-pr",
		Resource: fmt.Sprintf("%s/%s#%d", p.Owner, p.Repo, p.PRNumber),
		Result:   "completed",
		Details:  fmt.Sprintf("%d files reviewed", len(review.Files)),
	})

	return json.Marshal(result)
}

// GitHubCreatePRParams are the params for github.create-pr (deploy PR via GitOps).
type GitHubCreatePRParams struct {
	Service     string `json:"service"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
	CallerID    string `json:"caller_id"`
}

// GitHubCreatePRResult is the result of github.create-pr.
type GitHubCreatePRResult struct {
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"`
	PRTitle  string `json:"pr_title"`
	Branch   string `json:"branch"`
}

func (a *Agent) executeGitHubCreatePR(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p GitHubCreatePRParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.Service == "" {
		return nil, fmt.Errorf("service is required")
	}
	if p.Version == "" {
		p.Version = "latest"
	}
	if p.Environment == "" {
		p.Environment = "staging"
	}

	if a.gitops == nil {
		return nil, fmt.Errorf("GitOps client is not configured")
	}

	// Check permissions via supervisor
	permResult, err := a.supervisor.RequestPermissions(ctx, "github.create-pr", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	deployResult, err := a.gitops.CreateDeployPR(ctx, ghclient.DeployPRRequest{
		Service:     p.Service,
		Version:     p.Version,
		Environment: p.Environment,
		RequestedBy: p.CallerID,
	})
	if err != nil {
		return nil, fmt.Errorf("deploy PR creation failed: %w", err)
	}

	result := GitHubCreatePRResult{
		PRNumber: deployResult.PRNumber,
		PRURL:    deployResult.PRURL,
		PRTitle:  deployResult.PRTitle,
		Branch:   deployResult.Branch,
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "github.create-pr",
		Resource: fmt.Sprintf("%s/%s/%s", p.Service, p.Version, p.Environment),
		Result:   "completed",
		Details:  deployResult.PRURL,
	})

	return json.Marshal(result)
}

// --- K8s task executors ---

// K8sPodLogsParams are the params for k8s.pod-logs.
type K8sPodLogsParams struct {
	PodName   string `json:"pod_name"`
	Namespace string `json:"namespace"`
	TailLines int    `json:"tail_lines"`
	CallerID  string `json:"caller_id"`
}

// K8sPodLogsResult is the result of k8s.pod-logs.
type K8sPodLogsResult struct {
	PodName   string `json:"pod_name"`
	Namespace string `json:"namespace"`
	Logs      string `json:"logs"`
	Truncated bool   `json:"truncated"`
	TailLines int    `json:"tail_lines"`
}

func (a *Agent) executeK8sPodLogs(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p K8sPodLogsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.PodName == "" {
		return nil, fmt.Errorf("pod_name is required")
	}

	ns := p.Namespace
	if ns == "" {
		ns = a.defaultNamespace
	}

	if !a.isNamespaceAllowed(ns) {
		return nil, fmt.Errorf("namespace %q is not in the allowed list", ns)
	}

	tailLines := p.TailLines
	if tailLines <= 0 {
		tailLines = 50
	}

	if a.k8s == nil {
		return nil, fmt.Errorf("Kubernetes client is not configured")
	}

	// Check permissions via supervisor
	permResult, err := a.supervisor.RequestPermissions(ctx, "k8s.pod-logs", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	logs, err := a.k8s.GetPodLogs(ctx, ns, p.PodName, tailLines)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod logs: %w", err)
	}

	truncated := false
	if len(logs) > maxLogChars {
		logs = logs[:maxLogChars]
		truncated = true
	}

	result := K8sPodLogsResult{
		PodName:   p.PodName,
		Namespace: ns,
		Logs:      logs,
		Truncated: truncated,
		TailLines: tailLines,
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "k8s.pod-logs",
		Resource: fmt.Sprintf("%s/%s", ns, p.PodName),
		Result:   "completed",
		Details:  fmt.Sprintf("tail=%d, truncated=%v", tailLines, truncated),
	})

	return json.Marshal(result)
}

// K8sPodStatusParams are the params for k8s.pod-status.
type K8sPodStatusParams struct {
	PodName       string `json:"pod_name"`
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"label_selector"`
	CallerID      string `json:"caller_id"`
}

// K8sPodStatusResult is the result of k8s.pod-status.
type K8sPodStatusResult struct {
	Pods []PodStatusInfo `json:"pods"`
}

// PodStatusInfo is a simplified pod status.
type PodStatusInfo struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Restarts int    `json:"restarts"`
}

func (a *Agent) executeK8sPodStatus(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p K8sPodStatusParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	ns := p.Namespace
	if ns == "" {
		ns = a.defaultNamespace
	}

	if !a.isNamespaceAllowed(ns) {
		return nil, fmt.Errorf("namespace %q is not in the allowed list", ns)
	}

	if a.k8s == nil {
		return nil, fmt.Errorf("Kubernetes client is not configured")
	}

	// Check permissions
	permResult, err := a.supervisor.RequestPermissions(ctx, "k8s.pod-status", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	selector := p.LabelSelector
	if selector == "" && p.PodName != "" {
		selector = "app=" + p.PodName
	}

	pods, err := a.k8s.FindPods(ctx, ns, selector)
	if err != nil {
		return nil, fmt.Errorf("failed to find pods: %w", err)
	}

	result := K8sPodStatusResult{}
	for _, pod := range pods {
		result.Pods = append(result.Pods, PodStatusInfo{
			Name:     pod.Name,
			Status:   pod.Status,
			Restarts: pod.Restarts,
		})
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "k8s.pod-status",
		Resource: fmt.Sprintf("%s/%s", ns, p.PodName),
		Result:   "completed",
		Details:  fmt.Sprintf("found %d pods", len(pods)),
	})

	return json.Marshal(result)
}

// --- Jira task executors ---

// JiraGetIssueParams are the params for jira.get-issue.
type JiraGetIssueParams struct {
	IssueKey string `json:"issue_key"`
	CallerID string `json:"caller_id"`
}

// JiraGetIssueResult is the result of jira.get-issue.
type JiraGetIssueResult struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

func (a *Agent) executeJiraGetIssue(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p JiraGetIssueParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.IssueKey == "" {
		return nil, fmt.Errorf("issue_key is required")
	}

	if a.jira == nil {
		return nil, fmt.Errorf("Jira client is not configured")
	}

	// Check permissions
	permResult, err := a.supervisor.RequestPermissions(ctx, "jira.get-issue", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	issue, err := a.jira.GetIssue(ctx, p.IssueKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	status := "Unknown"
	if issue.Fields.Status != nil {
		status = issue.Fields.Status.Name
	}

	result := JiraGetIssueResult{
		Key:     issue.Key,
		Summary: issue.Fields.Summary,
		Status:  status,
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "jira.get-issue",
		Resource: p.IssueKey,
		Result:   "completed",
	})

	return json.Marshal(result)
}

// JiraCreateIssueParams are the params for jira.create-issue.
type JiraCreateIssueParams struct {
	Summary    string `json:"summary"`
	ProjectKey string `json:"project_key"`
	IssueType  string `json:"issue_type"`
	CallerID   string `json:"caller_id"`
}

// JiraCreateIssueResult is the result of jira.create-issue.
type JiraCreateIssueResult struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}

func (a *Agent) executeJiraCreateIssue(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p JiraCreateIssueParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.Summary == "" {
		return nil, fmt.Errorf("summary is required")
	}

	projectKey := p.ProjectKey
	if projectKey == "" {
		projectKey = a.jiraProjectKey
	}

	issueType := p.IssueType
	if issueType == "" {
		issueType = "Task"
	}

	if a.jira == nil {
		return nil, fmt.Errorf("Jira client is not configured")
	}

	// Check permissions
	permResult, err := a.supervisor.RequestPermissions(ctx, "jira.create-issue", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	req := &jiraclient.CreateIssueRequest{}
	req.Fields.Project = jiraclient.Project{Key: projectKey}
	req.Fields.Summary = p.Summary
	req.Fields.IssueType = jiraclient.IssueType{Name: issueType}

	issue, err := a.jira.CreateIssue(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	result := JiraCreateIssueResult{
		Key:     issue.Key,
		Summary: p.Summary,
		URL:     fmt.Sprintf("%s/browse/%s", a.jira.BaseURL(), issue.Key),
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "jira.create-issue",
		Resource: issue.Key,
		Result:   "completed",
		Details:  p.Summary,
	})

	return json.Marshal(result)
}

// --- Slack task executors ---

// SlackSendMessageParams are the params for slack.send-message.
type SlackSendMessageParams struct {
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
	ThreadTS  string `json:"thread_ts"`
	CallerID  string `json:"caller_id"`
}

// SlackSendMessageResult is the result of slack.send-message.
type SlackSendMessageResult struct {
	ChannelID string `json:"channel_id"`
	Timestamp string `json:"timestamp"`
	Sent      bool   `json:"sent"`
}

func (a *Agent) executeSlackSendMessage(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p SlackSendMessageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.ChannelID == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	if p.Message == "" {
		return nil, fmt.Errorf("message is required")
	}

	if a.slack == nil {
		return nil, fmt.Errorf("Slack client is not configured")
	}

	// Check permissions
	permResult, err := a.supervisor.RequestPermissions(ctx, "slack.send-message", p.CallerID, "")
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if !permResult.AllGranted {
		if len(permResult.Denied) > 0 {
			return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
		}
		return nil, fmt.Errorf("permission pending approval")
	}

	opts := []slack.MsgOption{slack.MsgOptionText(p.Message, false)}
	if p.ThreadTS != "" {
		opts = append(opts, slack.MsgOptionTS(p.ThreadTS))
	}

	channelID, timestamp, err := a.slack.PostMessage(p.ChannelID, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to send Slack message: %w", err)
	}

	result := SlackSendMessageResult{
		ChannelID: channelID,
		Timestamp: timestamp,
		Sent:      true,
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "slack.send-message",
		Resource: p.ChannelID,
		Result:   "completed",
	})

	return json.Marshal(result)
}

// SlackReadThreadParams are the params for slack.read-thread.
type SlackReadThreadParams struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
	Limit     int    `json:"limit"` // default 50, max 200
}

// SlackReadThreadResult is the result of slack.read-thread.
type SlackReadThreadResult struct {
	Messages []SlackThreadMessage `json:"messages"`
	Count    int                  `json:"count"`
	HasMore  bool                 `json:"has_more"`
}

// SlackThreadMessage is a simplified Slack message for thread reading.
type SlackThreadMessage struct {
	User      string `json:"user"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	IsBot     bool   `json:"is_bot"`
	BotID     string `json:"bot_id,omitempty"`
}

func (a *Agent) executeSlackReadThread(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p SlackReadThreadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.ChannelID == "" || p.ThreadTS == "" {
		return nil, fmt.Errorf("channel_id and thread_ts are required")
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}

	if a.slack == nil {
		return nil, fmt.Errorf("Slack client is not configured")
	}

	// Read-only — no approval needed
	msgs, hasMore, _, err := a.slack.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: p.ChannelID,
		Timestamp: p.ThreadTS,
		Limit:     p.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("reading thread: %w", err)
	}

	result := SlackReadThreadResult{
		Messages: make([]SlackThreadMessage, 0, len(msgs)),
		HasMore:  hasMore,
	}

	for _, m := range msgs {
		msg := SlackThreadMessage{
			User:      m.User,
			Text:      m.Text,
			Timestamp: m.Timestamp,
			IsBot:     m.BotID != "",
			BotID:     m.BotID,
		}
		if len(msg.Text) > 4000 {
			msg.Text = msg.Text[:4000] + "... [truncated]"
		}
		result.Messages = append(result.Messages, msg)
	}
	result.Count = len(result.Messages)

	a.audit.Record(models.AuditEntry{
		Action:   "slack.read-thread",
		Resource: p.ChannelID,
		Result:   "completed",
		Details:  fmt.Sprintf("thread=%s messages=%d", p.ThreadTS, result.Count),
	})

	return json.Marshal(result)
}

// --- Policy task executors ---

// PolicyListResult is the result of policy.list.
type PolicyListResult struct {
	Policies map[string]string `json:"policies"`
}

func (a *Agent) executePolicyList(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	policies := a.supervisor.Policy().ListPermissionPolicies()

	result := PolicyListResult{
		Policies: make(map[string]string, len(policies)),
	}
	for perm, level := range policies {
		result.Policies[string(perm)] = string(level)
	}

	return json.Marshal(result)
}

// PolicySetParams are the params for policy.set.
type PolicySetParams struct {
	Changes []PolicyChangeParam `json:"changes"`
	CallerID string             `json:"caller_id"`
}

// PolicyChangeParam represents a single policy change.
type PolicyChangeParam struct {
	Permission string `json:"permission"`
	Level      string `json:"level"`
	Reason     string `json:"reason"`
}

// PolicySetResult is the result of policy.set.
type PolicySetResult struct {
	Applied []PolicyChangeParam `json:"applied"`
}

func (a *Agent) executePolicySet(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p PolicySetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if len(p.Changes) == 0 {
		return nil, fmt.Errorf("at least one change is required")
	}

	// Validate and check admin
	if !a.supervisor.IsAdmin(p.CallerID) {
		return nil, fmt.Errorf("only admins can change policies")
	}

	var applied []PolicyChangeParam
	for _, change := range p.Changes {
		perm := supervisor.Permission(change.Permission)
		level := supervisor.PolicyLevel(change.Level)

		// Validate permission and level
		if !isValidPermission(perm) {
			return nil, fmt.Errorf("unknown permission: %s", change.Permission)
		}
		if !isValidPolicyLevel(level) {
			return nil, fmt.Errorf("unknown policy level: %s", change.Level)
		}

		a.supervisor.ApplyPolicyChange(supervisor.PolicyChange{
			Permission: perm,
			NewLevel:   level,
			Reason:     change.Reason,
		}, p.CallerID)

		applied = append(applied, change)
	}

	result := PolicySetResult{Applied: applied}
	return json.Marshal(result)
}

// PolicyResetParams are the params for policy.reset.
type PolicyResetParams struct {
	CallerID string `json:"caller_id"`
}

// PolicyResetResult is the result of policy.reset.
type PolicyResetResult struct {
	Reset bool `json:"reset"`
}

func (a *Agent) executePolicyReset(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p PolicyResetParams
	if params != nil && len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}

	if !a.supervisor.IsAdmin(p.CallerID) {
		return nil, fmt.Errorf("only admins can reset policies")
	}

	a.supervisor.Policy().ResetPermissionPolicies()

	a.audit.Record(models.AuditEntry{
		UserID: p.CallerID,
		Action: "policy.reset",
		Result: "completed",
	})

	result := PolicyResetResult{Reset: true}
	return json.Marshal(result)
}

// --- Alert triage executor ---

// AlertTriageParams are the params for k8s.alert-triage.
type AlertTriageParams struct {
	PodName   string `json:"pod_name"`
	Namespace string `json:"namespace"`
	CallerID  string `json:"caller_id"`
}

func (a *Agent) executeAlertTriage(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p AlertTriageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.PodName == "" {
		return nil, fmt.Errorf("pod_name is required")
	}

	ns := p.Namespace
	if ns == "" {
		ns = a.defaultNamespace
	}

	if a.k8s == nil {
		return nil, fmt.Errorf("Kubernetes client is not configured")
	}

	result := &AlertTriageResult{
		PodName:   p.PodName,
		Namespace: ns,
	}

	// Find pods matching the name/label
	pods, err := a.k8s.FindPods(ctx, ns, "app="+p.PodName)
	if err != nil {
		a.logger.Error().Err(err).Msg("failed to find pods for triage")
	}

	if len(pods) > 0 {
		pod := pods[0]
		result.PodName = pod.Name
		result.Status = pod.Status
		result.Restarts = pod.Restarts
	}

	// Get recent logs
	logs, err := a.k8s.GetPodLogs(ctx, ns, result.PodName, 20)
	if err != nil {
		a.logger.Warn().Err(err).Msg("failed to get pod logs for triage")
	} else {
		result.LastLog = truncateString(logs, 500)
	}

	// Get events
	events, err := a.k8s.GetEvents(ctx, ns, result.PodName)
	if err != nil {
		a.logger.Warn().Err(err).Msg("failed to get events for triage")
	} else {
		result.Events = events
	}

	result.Summary = buildTriageSummary(result)

	return json.Marshal(result)
}

// --- helpers ---

func (a *Agent) isNamespaceAllowed(ns string) bool {
	if len(a.allowedNamespaces) == 0 {
		return true
	}
	for _, allowed := range a.allowedNamespaces {
		if allowed == ns {
			return true
		}
	}
	return false
}

func isValidPermission(p supervisor.Permission) bool {
	valid := map[supervisor.Permission]bool{
		supervisor.PermGithubPRRead:   true,
		supervisor.PermGithubPRWrite:  true,
		supervisor.PermGithubPRMerge:  true,
		supervisor.PermGithubCIRead:   true,
		supervisor.PermGithubRepoRead:  true,
		supervisor.PermGithubExecRead:  true,
		supervisor.PermGithubExecWrite: true,
		supervisor.PermK8sRead:         true,
		supervisor.PermK8sWrite:       true,
		supervisor.PermK8sDelete:      true,
		supervisor.PermK8sExec:        true,
		supervisor.PermJiraRead:       true,
		supervisor.PermJiraWrite:      true,
		supervisor.PermSlackRead:      true,
		supervisor.PermSlackWrite:     true,
		supervisor.PermDeployTest:     true,
		supervisor.PermDeployProd:     true,
	}
	return valid[p]
}

func isValidPolicyLevel(l supervisor.PolicyLevel) bool {
	valid := map[supervisor.PolicyLevel]bool{
		supervisor.PolicyAutoApprove:     true,
		supervisor.PolicyNotifyThenDo:    true,
		supervisor.PolicyRequireApproval: true,
		supervisor.PolicyAlwaysDeny:      true,
	}
	return valid[l]
}

// policyEmoji returns an emoji for a policy level (used in Slack notifications).
func policyEmoji(level supervisor.PolicyLevel) string {
	switch level {
	case supervisor.PolicyAutoApprove:
		return "✅"
	case supervisor.PolicyNotifyThenDo:
		return "📢"
	case supervisor.PolicyRequireApproval:
		return "⏳"
	case supervisor.PolicyAlwaysDeny:
		return "🚫"
	default:
		return "❓"
	}
}

// safeRawJSON returns params or a null placeholder for logging.
func safeRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`null`)
	}
	return raw
}

// sendApprovalButtons sends interactive approval buttons to the supervisor channel.
// Returns the channel and messageTS of the posted message (for threading follow-ups).
func (a *Agent) sendApprovalButtons(channelID, threadTS, requestID string, actx slackblocks.ApprovalContext) (postedChannel, postedTS string) {
	if a.slack == nil {
		return "", ""
	}
	blocks := slackblocks.BuildApprovalBlocks(requestID, actx)

	target := a.supervisorChannel
	if target == "" {
		target = channelID
	}

	var opts []slack.MsgOption
	opts = append(opts, slack.MsgOptionBlocks(blocks...))
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}

	_, ts, _ := a.slack.PostMessage(target, opts...)
	return target, ts
}

// showPolicies formats and returns the current policies as a Slack message.
func (a *Agent) showPolicies(channelID, threadTS string) {
	policies := a.supervisor.Policy().ListPermissionPolicies()
	var sb strings.Builder
	sb.WriteString("📋 *Current Permission Policies:*\n\n")

	groups := map[supervisor.PolicyLevel][]string{
		supervisor.PolicyAutoApprove:     {},
		supervisor.PolicyNotifyThenDo:    {},
		supervisor.PolicyRequireApproval: {},
		supervisor.PolicyAlwaysDeny:      {},
	}
	for perm, level := range policies {
		groups[level] = append(groups[level], string(perm))
	}

	if len(groups[supervisor.PolicyAutoApprove]) > 0 {
		sb.WriteString("✅ *Auto-Approve:*\n")
		for _, p := range groups[supervisor.PolicyAutoApprove] {
			sb.WriteString(fmt.Sprintf("  • `%s`\n", p))
		}
		sb.WriteString("\n")
	}
	if len(groups[supervisor.PolicyNotifyThenDo]) > 0 {
		sb.WriteString("📢 *Notify-Then-Do:*\n")
		for _, p := range groups[supervisor.PolicyNotifyThenDo] {
			sb.WriteString(fmt.Sprintf("  • `%s`\n", p))
		}
		sb.WriteString("\n")
	}
	if len(groups[supervisor.PolicyRequireApproval]) > 0 {
		sb.WriteString("⏳ *Require Approval:*\n")
		for _, p := range groups[supervisor.PolicyRequireApproval] {
			sb.WriteString(fmt.Sprintf("  • `%s`\n", p))
		}
		sb.WriteString("\n")
	}
	if len(groups[supervisor.PolicyAlwaysDeny]) > 0 {
		sb.WriteString("🚫 *Always Deny:*\n")
		for _, p := range groups[supervisor.PolicyAlwaysDeny] {
			sb.WriteString(fmt.Sprintf("  • `%s`\n", p))
		}
	}

	a.reply(channelID, threadTS, sb.String())
}

func (a *Agent) reply(channelID, threadTS, text string) {
	if a.slack == nil {
		return
	}
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, _, err := a.slack.PostMessage(channelID, opts...)
	if err != nil {
		a.logger.Error().Err(err).Str("channel", channelID).Msg("failed to post message")
	}
}
