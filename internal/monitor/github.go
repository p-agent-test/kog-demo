// Package monitor — GitHubMonitor watches a repository for notable events:
// new PRs, CI failures, and stale branches.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// GitHubMonitor checks a GitHub repository for:
//   - New open pull requests
//   - Failed CI checks on the default branch
//   - Branches with no commits in a configurable window (stale)
type GitHubMonitor struct {
	id         string
	owner      string
	repo       string
	token      string
	httpClient *http.Client
	logger     *slog.Logger

	// StaleBranchDays: branches not updated in this many days are "stale".
	// Default: 30.
	StaleBranchDays int

	// last seen PR number to avoid repeat observations
	lastSeenPR int
}

// NewGitHubMonitor creates a GitHubMonitor for the given owner/repo.
// token is a GitHub personal access token (or empty for public repos).
func NewGitHubMonitor(id, owner, repo, token string, logger *slog.Logger) *GitHubMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &GitHubMonitor{
		id:              id,
		owner:           owner,
		repo:            repo,
		token:           token,
		httpClient:      &http.Client{Timeout: 15 * time.Second},
		logger:          logger,
		StaleBranchDays: 30,
	}
}

// ID implements Monitor.
func (g *GitHubMonitor) ID() string { return g.id }

// Check implements Monitor. Runs all GitHub checks and returns observations.
func (g *GitHubMonitor) Check(ctx context.Context) ([]Observation, error) {
	var obs []Observation

	prObs, err := g.checkPRs(ctx)
	if err != nil {
		g.logger.Warn("github: PR check failed", "repo", g.repoPath(), "err", err)
	} else {
		obs = append(obs, prObs...)
	}

	branchObs, err := g.checkStaleBranches(ctx)
	if err != nil {
		g.logger.Warn("github: branch check failed", "repo", g.repoPath(), "err", err)
	} else {
		obs = append(obs, branchObs...)
	}

	return obs, nil
}

// checkPRs looks for new open PRs since the last observed PR number.
func (g *GitHubMonitor) checkPRs(ctx context.Context) ([]Observation, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls?state=open&per_page=20&sort=created&direction=desc",
		g.repoPath())

	var prs []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		HTMLURL   string `json:"html_url"`
		User      struct{ Login string `json:"login"` } `json:"user"`
		CreatedAt string `json:"created_at"`
		Draft     bool   `json:"draft"`
	}
	if err := g.get(ctx, url, &prs); err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}

	var obs []Observation
	maxSeen := g.lastSeenPR

	for _, pr := range prs {
		if pr.Number > maxSeen {
			maxSeen = pr.Number
		}
		if pr.Number <= g.lastSeenPR {
			continue
		}
		severity := SeverityInfo
		if !pr.Draft {
			severity = SeverityWarning // non-draft PR needs attention
		}
		obs = append(obs, Observation{
			MonitorID:   g.id,
			SituationID: "github_new_pr",
			Severity:    severity,
			Message:     fmt.Sprintf("New PR #%d by %s: %s", pr.Number, pr.User.Login, pr.Title),
			Details: map[string]string{
				"pr_number": fmt.Sprintf("%d", pr.Number),
				"author":    pr.User.Login,
				"url":       pr.HTMLURL,
				"draft":     fmt.Sprintf("%v", pr.Draft),
			},
			ObservedAt: time.Now().UTC(),
		})
	}
	if maxSeen > g.lastSeenPR {
		g.lastSeenPR = maxSeen
	}
	return obs, nil
}

// checkStaleBranches reports branches with no commits for StaleBranchDays.
func (g *GitHubMonitor) checkStaleBranches(ctx context.Context) ([]Observation, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/branches?per_page=100", g.repoPath())

	var branches []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
			URL string `json:"url"`
		} `json:"commit"`
	}
	if err := g.get(ctx, url, &branches); err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -g.StaleBranchDays)
	var obs []Observation

	for _, b := range branches {
		// Skip main / master / develop — they're expected to be around
		if b.Name == "main" || b.Name == "master" || b.Name == "develop" {
			continue
		}

		// Fetch commit detail to get timestamp
		var commit struct {
			Commit struct {
				Author struct {
					Date string `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		}
		if err := g.get(ctx, b.Commit.URL, &commit); err != nil {
			continue // skip on error
		}
		t, err := time.Parse(time.RFC3339, commit.Commit.Author.Date)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			obs = append(obs, Observation{
				MonitorID:   g.id,
				SituationID: "github_stale_branch",
				Severity:    SeverityInfo,
				Message: fmt.Sprintf("Stale branch %q — last commit %s (%d days ago)",
					b.Name, t.Format("2006-01-02"), int(time.Since(t).Hours()/24)),
				Details: map[string]string{
					"branch":     b.Name,
					"last_commit": t.Format(time.RFC3339),
					"sha":        b.Commit.SHA[:8],
				},
				ObservedAt: time.Now().UTC(),
			})
		}
	}
	return obs, nil
}

// get performs a GitHub API GET request and decodes JSON into dst.
func (g *GitHubMonitor) get(ctx context.Context, url string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	req.Header.Set("User-Agent", "kog-monitor/1.0")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github API %s: status %d: %s", url, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

func (g *GitHubMonitor) repoPath() string {
	return g.owner + "/" + g.repo
}
