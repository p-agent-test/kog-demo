package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v60/github"
	"github.com/p-blackswan/platform-agent/internal/mgmt"
	"github.com/p-blackswan/platform-agent/internal/models"
	"github.com/p-blackswan/platform-agent/internal/supervisor"
)

// commandClass defines the security classification of a GitHub operation.
type commandClass int

const (
	classRead      commandClass = iota // auto-approve
	classWrite                         // require-approval
	classDangerous                     // always-deny
)

// GHExecParams are the params for github.exec.
type GHExecParams struct {
	// Operation is the GitHub operation to perform.
	// Read: pr.list, pr.get, pr.diff, pr.files, issue.list, issue.get, repo.get, repo.list, run.list, run.get
	// Write (approval required): pr.create, pr.comment, pr.review, issue.create, issue.comment, repo.create
	// Dangerous (denied): pr.merge, pr.close, issue.close, repo.delete, and anything else
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
	CallerID  string          `json:"caller_id"`

	// taskID is injected at runtime from context (not from JSON).
	taskID string
}

// GHExecResult is the result of github.exec.
type GHExecResult struct {
	Operation string      `json:"operation"`
	Data      interface{} `json:"data"`
}

// operationClassification maps operation names to security classes.
var operationClassification = map[string]commandClass{
	// Read operations — auto-approve
	"pr.list":    classRead,
	"pr.get":     classRead,
	"pr.diff":    classRead,
	"pr.files":   classRead,
	"pr.checks":  classRead,
	"issue.list": classRead,
	"issue.get":  classRead,
	"repo.get":   classRead,
	"repo.list":  classRead,
	"run.list":   classRead,
	"run.get":    classRead,

	// Write operations — require approval
	"pr.create":      classWrite,
	"pr.comment":     classWrite,
	"pr.review":      classWrite,
	"issue.create":   classWrite,
	"issue.comment":  classWrite,
	"repo.create":    classWrite,

	// Git operations
	"git.commit":        classWrite,
	"git.create-branch": classWrite,
	"git.get-file":      classRead,
	"git.list-files":    classRead,

	// Dangerous — always deny
	"pr.merge":    classDangerous,
	"pr.close":    classDangerous,
	"issue.close": classDangerous,
	"repo.delete": classDangerous,
}

// classifyOperation returns the security class of a GitHub operation.
func classifyOperation(op string) commandClass {
	if class, ok := operationClassification[op]; ok {
		return class
	}
	return classDangerous // unknown = dangerous
}

func (a *Agent) executeGHExec(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p GHExecParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	// Inject task ID from context (set by task engine) for approval tracking
	p.taskID = mgmt.TaskIDFromContext(ctx)

	if p.Operation == "" {
		return nil, fmt.Errorf("operation is required")
	}

	if a.github == nil {
		return nil, fmt.Errorf("GitHub client is not configured")
	}

	class := classifyOperation(p.Operation)

	switch class {
	case classDangerous:
		a.audit.Record(models.AuditEntry{
			UserID:   p.CallerID,
			Action:   "github.exec",
			Resource: p.Operation,
			Result:   "denied",
			Details:  "operation not allowed",
		})
		allowed := listAllowedOps()
		return nil, fmt.Errorf("operation %q not allowed. Allowed: %s", p.Operation, allowed)

	case classWrite:
		permResult, err := a.supervisor.RequestPermissions(ctx, "github.exec.write", p.CallerID, p.taskID)
		if err != nil {
			return nil, fmt.Errorf("permission check failed: %w", err)
		}
		if !permResult.AllGranted {
			if len(permResult.Denied) > 0 {
				return nil, fmt.Errorf("permission denied: %v", permResult.Denied)
			}
			// Register pending approval for re-queue on callback
			reqID := ""
			if len(permResult.Pending) > 0 {
				reqID = permResult.Pending[0].RequestID
				a.registerPendingApproval(reqID, &pendingApprovalInfo{
					TaskID:     p.taskID,
					CallerID:   p.CallerID,
					Permission: supervisor.PermGithubExecWrite,
					Action:     "github.exec",
					Resource:   p.Operation,
				})
			}
			// Send Slack buttons and capture thread for follow-ups
			if a.slack != nil && a.supervisorChannel != "" {
				postedCh, postedTS := a.sendApprovalButtons(a.supervisorChannel, "", reqID, p.CallerID, "github.exec", p.Operation)
				// Update pending info with thread context
				a.pendingMu.Lock()
				if info, ok := a.pendingApprovals[reqID]; ok {
					info.ChannelID = postedCh
					info.ThreadTS = postedTS
				}
				a.pendingMu.Unlock()
			}
			a.audit.Record(models.AuditEntry{
				UserID:   p.CallerID,
				Action:   "github.exec",
				Resource: p.Operation,
				Result:   "pending_approval",
				Details:  fmt.Sprintf("request_id=%s, task_id=%s", reqID, p.taskID),
			})
			return nil, fmt.Errorf("awaiting_approval: permission pending human approval (request=%s)", reqID)
		}

	case classRead:
		a.logger.Debug().Str("operation", p.Operation).Msg("auto-approved read operation")
	}

	// Get GitHub client
	client, err := a.github.GetInstallationClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub client: %w", err)
	}

	// Dispatch to handler
	data, err := a.dispatchGHOperation(ctx, client, p.Operation, p.Params)
	if err != nil {
		a.audit.Record(models.AuditEntry{
			UserID:   p.CallerID,
			Action:   "github.exec",
			Resource: p.Operation,
			Result:   "error",
			Details:  err.Error(),
		})
		return nil, err
	}

	a.audit.Record(models.AuditEntry{
		UserID:   p.CallerID,
		Action:   "github.exec",
		Resource: p.Operation,
		Result:   "completed",
	})

	result := GHExecResult{Operation: p.Operation, Data: data}
	return json.Marshal(result)
}

// --- operation dispatchers ---

func (a *Agent) dispatchGHOperation(ctx context.Context, client *gh.Client, op string, params json.RawMessage) (interface{}, error) {
	switch op {
	// PR operations
	case "pr.list":
		return a.ghPRList(ctx, client, params)
	case "pr.get":
		return a.ghPRGet(ctx, client, params)
	case "pr.files":
		return a.ghPRFiles(ctx, client, params)
	case "pr.create":
		return a.ghPRCreate(ctx, client, params)
	case "pr.comment":
		return a.ghPRComment(ctx, client, params)
	case "pr.review":
		return a.ghPRReview(ctx, client, params)

	// Issue operations
	case "issue.list":
		return a.ghIssueList(ctx, client, params)
	case "issue.get":
		return a.ghIssueGet(ctx, client, params)
	case "issue.create":
		return a.ghIssueCreate(ctx, client, params)
	case "issue.comment":
		return a.ghIssueComment(ctx, client, params)

	// Repo operations
	case "repo.get":
		return a.ghRepoGet(ctx, client, params)
	case "repo.list":
		return a.ghRepoList(ctx, client, params)
	case "repo.create":
		return a.ghRepoCreate(ctx, client, params)

	// Run operations
	case "run.list":
		return a.ghRunList(ctx, client, params)
	case "run.get":
		return a.ghRunGet(ctx, client, params)

	// Git operations
	case "git.commit":
		return a.ghGitCommit(ctx, client, params)
	case "git.create-branch":
		return a.ghGitCreateBranch(ctx, client, params)
	case "git.get-file":
		return a.ghGitGetFile(ctx, client, params)
	case "git.list-files":
		return a.ghGitListFiles(ctx, client, params)

	default:
		return nil, fmt.Errorf("unimplemented operation: %s", op)
	}
}

// --- param structs ---

type ghRepoParams struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

type ghPRParams struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
}

type ghIssueParams struct {
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issue_number"`
}

// --- PR handlers ---

func (a *Agent) ghPRList(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		State string `json:"state"` // open, closed, all
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	state := p.State
	if state == "" {
		state = "open"
	}
	prs, _, err := client.PullRequests.List(ctx, p.Owner, p.Repo, &gh.PullRequestListOptions{
		State:       state,
		ListOptions: gh.ListOptions{PerPage: 30},
	})
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	type prSummary struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		User   string `json:"user"`
		URL    string `json:"url"`
	}
	var result []prSummary
	for _, pr := range prs {
		user := ""
		if pr.User != nil {
			user = pr.User.GetLogin()
		}
		result = append(result, prSummary{
			Number: pr.GetNumber(),
			Title:  pr.GetTitle(),
			State:  pr.GetState(),
			User:   user,
			URL:    pr.GetHTMLURL(),
		})
	}
	return result, nil
}

func (a *Agent) ghPRGet(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p ghPRParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.PRNumber == 0 {
		return nil, fmt.Errorf("owner, repo, and pr_number are required")
	}
	pr, _, err := client.PullRequests.Get(ctx, p.Owner, p.Repo, p.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("getting PR: %w", err)
	}
	return pr, nil
}

func (a *Agent) ghPRFiles(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p ghPRParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.PRNumber == 0 {
		return nil, fmt.Errorf("owner, repo, and pr_number are required")
	}
	files, _, err := client.PullRequests.ListFiles(ctx, p.Owner, p.Repo, p.PRNumber, &gh.ListOptions{PerPage: 100})
	if err != nil {
		return nil, fmt.Errorf("listing PR files: %w", err)
	}
	return files, nil
}

func (a *Agent) ghPRCreate(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"` // source branch
		Base  string `json:"base"` // target branch
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.Title == "" || p.Head == "" {
		return nil, fmt.Errorf("owner, repo, title, and head are required")
	}
	if p.Base == "" {
		p.Base = "main"
	}
	pr, _, err := client.PullRequests.Create(ctx, p.Owner, p.Repo, &gh.NewPullRequest{
		Title: &p.Title,
		Body:  &p.Body,
		Head:  &p.Head,
		Base:  &p.Base,
	})
	if err != nil {
		return nil, fmt.Errorf("creating PR: %w", err)
	}
	return map[string]interface{}{
		"number": pr.GetNumber(),
		"url":    pr.GetHTMLURL(),
		"title":  pr.GetTitle(),
	}, nil
}

func (a *Agent) ghPRComment(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner    string `json:"owner"`
		Repo     string `json:"repo"`
		PRNumber int    `json:"pr_number"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.PRNumber == 0 || p.Body == "" {
		return nil, fmt.Errorf("owner, repo, pr_number, and body are required")
	}
	comment, _, err := client.Issues.CreateComment(ctx, p.Owner, p.Repo, p.PRNumber, &gh.IssueComment{
		Body: &p.Body,
	})
	if err != nil {
		return nil, fmt.Errorf("commenting on PR: %w", err)
	}
	return map[string]interface{}{
		"id":  comment.GetID(),
		"url": comment.GetHTMLURL(),
	}, nil
}

func (a *Agent) ghPRReview(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner    string `json:"owner"`
		Repo     string `json:"repo"`
		PRNumber int    `json:"pr_number"`
		Event    string `json:"event"` // APPROVE, REQUEST_CHANGES, COMMENT
		Body     string `json:"body"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.PRNumber == 0 {
		return nil, fmt.Errorf("owner, repo, and pr_number are required")
	}
	if p.Event == "" {
		p.Event = "COMMENT"
	}
	event := strings.ToUpper(p.Event)
	review, _, err := client.PullRequests.CreateReview(ctx, p.Owner, p.Repo, p.PRNumber, &gh.PullRequestReviewRequest{
		Body:  &p.Body,
		Event: &event,
	})
	if err != nil {
		return nil, fmt.Errorf("reviewing PR: %w", err)
	}
	return map[string]interface{}{
		"id":    review.GetID(),
		"state": review.GetState(),
		"url":   review.GetHTMLURL(),
	}, nil
}

// --- Issue handlers ---

func (a *Agent) ghIssueList(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	state := p.State
	if state == "" {
		state = "open"
	}
	issues, _, err := client.Issues.ListByRepo(ctx, p.Owner, p.Repo, &gh.IssueListByRepoOptions{
		State:       state,
		ListOptions: gh.ListOptions{PerPage: 30},
	})
	if err != nil {
		return nil, fmt.Errorf("listing issues: %w", err)
	}
	type issueSummary struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		URL    string `json:"url"`
	}
	var result []issueSummary
	for _, issue := range issues {
		if issue.PullRequestLinks != nil {
			continue // skip PRs
		}
		result = append(result, issueSummary{
			Number: issue.GetNumber(),
			Title:  issue.GetTitle(),
			State:  issue.GetState(),
			URL:    issue.GetHTMLURL(),
		})
	}
	return result, nil
}

func (a *Agent) ghIssueGet(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p ghIssueParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.IssueNumber == 0 {
		return nil, fmt.Errorf("owner, repo, and issue_number are required")
	}
	issue, _, err := client.Issues.Get(ctx, p.Owner, p.Repo, p.IssueNumber)
	if err != nil {
		return nil, fmt.Errorf("getting issue: %w", err)
	}
	return issue, nil
}

func (a *Agent) ghIssueCreate(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner  string   `json:"owner"`
		Repo   string   `json:"repo"`
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.Title == "" {
		return nil, fmt.Errorf("owner, repo, and title are required")
	}
	req := &gh.IssueRequest{
		Title: &p.Title,
		Body:  &p.Body,
	}
	if len(p.Labels) > 0 {
		req.Labels = &p.Labels
	}
	issue, _, err := client.Issues.Create(ctx, p.Owner, p.Repo, req)
	if err != nil {
		return nil, fmt.Errorf("creating issue: %w", err)
	}
	return map[string]interface{}{
		"number": issue.GetNumber(),
		"url":    issue.GetHTMLURL(),
		"title":  issue.GetTitle(),
	}, nil
}

func (a *Agent) ghIssueComment(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner       string `json:"owner"`
		Repo        string `json:"repo"`
		IssueNumber int    `json:"issue_number"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.IssueNumber == 0 || p.Body == "" {
		return nil, fmt.Errorf("owner, repo, issue_number, and body are required")
	}
	comment, _, err := client.Issues.CreateComment(ctx, p.Owner, p.Repo, p.IssueNumber, &gh.IssueComment{
		Body: &p.Body,
	})
	if err != nil {
		return nil, fmt.Errorf("commenting on issue: %w", err)
	}
	return map[string]interface{}{
		"id":  comment.GetID(),
		"url": comment.GetHTMLURL(),
	}, nil
}

// --- Repo handlers ---

func (a *Agent) ghRepoGet(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p ghRepoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	repo, _, err := client.Repositories.Get(ctx, p.Owner, p.Repo)
	if err != nil {
		return nil, fmt.Errorf("getting repo: %w", err)
	}
	return map[string]interface{}{
		"name":          repo.GetName(),
		"full_name":     repo.GetFullName(),
		"description":   repo.GetDescription(),
		"default_branch": repo.GetDefaultBranch(),
		"private":       repo.GetPrivate(),
		"url":           repo.GetHTMLURL(),
	}, nil
}

func (a *Agent) ghRepoList(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Org string `json:"org"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Org == "" {
		return nil, fmt.Errorf("org is required")
	}
	repos, _, err := client.Repositories.ListByOrg(ctx, p.Org, &gh.RepositoryListByOrgOptions{
		ListOptions: gh.ListOptions{PerPage: 50},
	})
	if err != nil {
		return nil, fmt.Errorf("listing repos: %w", err)
	}
	type repoSummary struct {
		Name    string `json:"name"`
		Private bool   `json:"private"`
		URL     string `json:"url"`
	}
	var result []repoSummary
	for _, r := range repos {
		result = append(result, repoSummary{
			Name:    r.GetName(),
			Private: r.GetPrivate(),
			URL:     r.GetHTMLURL(),
		})
	}
	return result, nil
}

func (a *Agent) ghRepoCreate(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner       string `json:"owner"` // org name (also accepts "org" for backward compat)
		Org         string `json:"org"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Private     bool   `json:"private"`
		AutoInit    *bool  `json:"auto_init"` // nil = true (default), explicit false = empty repo
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	// Accept both "owner" and "org" — owner takes precedence
	org := p.Owner
	if org == "" {
		org = p.Org
	}
	autoInit := true
	if p.AutoInit != nil {
		autoInit = *p.AutoInit
	}
	repo, _, err := client.Repositories.Create(ctx, org, &gh.Repository{
		Name:        &p.Name,
		Description: &p.Description,
		Private:     &p.Private,
		AutoInit:    &autoInit,
	})
	if err != nil {
		return nil, fmt.Errorf("creating repo: %w", err)
	}
	return map[string]interface{}{
		"name":      repo.GetName(),
		"full_name": repo.GetFullName(),
		"url":       repo.GetHTMLURL(),
		"private":   repo.GetPrivate(),
	}, nil
}

// --- Run handlers ---

func (a *Agent) ghRunList(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p ghRepoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	runs, _, err := client.Actions.ListRepositoryWorkflowRuns(ctx, p.Owner, p.Repo, &gh.ListWorkflowRunsOptions{
		ListOptions: gh.ListOptions{PerPage: 10},
	})
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	type runSummary struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		Branch     string `json:"branch"`
		URL        string `json:"url"`
	}
	var result []runSummary
	for _, r := range runs.WorkflowRuns {
		result = append(result, runSummary{
			ID:         r.GetID(),
			Name:       r.GetName(),
			Status:     r.GetStatus(),
			Conclusion: r.GetConclusion(),
			Branch:     r.GetHeadBranch(),
			URL:        r.GetHTMLURL(),
		})
	}
	return result, nil
}

func (a *Agent) ghRunGet(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		RunID int64  `json:"run_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.RunID == 0 {
		return nil, fmt.Errorf("owner, repo, and run_id are required")
	}
	run, _, err := client.Actions.GetWorkflowRunByID(ctx, p.Owner, p.Repo, p.RunID)
	if err != nil {
		return nil, fmt.Errorf("getting run: %w", err)
	}
	return run, nil
}

// --- helpers ---

func listAllowedOps() string {
	var reads, writes []string
	for op, class := range operationClassification {
		switch class {
		case classRead:
			reads = append(reads, op)
		case classWrite:
			writes = append(writes, op)
		}
	}
	return fmt.Sprintf("read: [%s], write (approval): [%s]", strings.Join(reads, ", "), strings.Join(writes, ", "))
}
