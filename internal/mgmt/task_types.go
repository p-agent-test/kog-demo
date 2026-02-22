package mgmt

// Supported task types.
const (
	TaskTypeGitHubCreatePR  = "github.create-pr"
	TaskTypeGitHubReviewPR  = "github.review-pr"
	TaskTypeGitHubListPRs   = "github.list-prs"
	TaskTypeGitHubCIStatus  = "github.ci-status"
	TaskTypeK8sPodLogs      = "k8s.pod-logs"
	TaskTypeK8sPodStatus    = "k8s.pod-status"
	TaskTypeK8sDeployments  = "k8s.deployments"
	TaskTypeK8sAlertTriage  = "k8s.alert-triage"
	TaskTypeJiraGetIssue    = "jira.get-issue"
	TaskTypeJiraCreateIssue = "jira.create-issue"
	TaskTypeSlackSendMsg    = "slack.send-message"
	TaskTypePolicyList      = "policy.list"
	TaskTypePolicySet       = "policy.set"
	TaskTypePolicyReset     = "policy.reset"
	TaskTypeGitHubExec      = "github.exec"
)

// ValidTaskTypes is the set of all supported task types.
var ValidTaskTypes = map[string]bool{
	TaskTypeGitHubCreatePR:  true,
	TaskTypeGitHubReviewPR:  true,
	TaskTypeGitHubListPRs:   true,
	TaskTypeGitHubCIStatus:  true,
	TaskTypeK8sPodLogs:      true,
	TaskTypeK8sPodStatus:    true,
	TaskTypeK8sDeployments:  true,
	TaskTypeK8sAlertTriage:  true,
	TaskTypeJiraGetIssue:    true,
	TaskTypeJiraCreateIssue: true,
	TaskTypeSlackSendMsg:    true,
	TaskTypePolicyList:      true,
	TaskTypePolicySet:       true,
	TaskTypePolicyReset:     true,
	TaskTypeGitHubExec:      true,
}

// IsValidTaskType returns true if the task type is recognized.
func IsValidTaskType(t string) bool {
	return ValidTaskTypes[t]
}
