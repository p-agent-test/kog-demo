package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v60/github"
)

// PRReview contains the result of a PR review.
type PRReview struct {
	Owner    string
	Repo     string
	PRNumber int
	Files    []*github.CommitFile
	Comments []string
}

// ReviewPR reads a PR's diff and generates review data.
func (c *Client) ReviewPR(ctx context.Context, owner, repo string, prNumber int) (*PRReview, error) {
	client, err := c.GetInstallationClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting installation client: %w", err)
	}

	review := &PRReview{
		Owner:    owner,
		Repo:     repo,
		PRNumber: prNumber,
	}

	// Fetch PR files (paginated)
	opts := &github.ListOptions{PerPage: 100}
	for {
		files, resp, err := client.PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("listing PR files: %w", err)
		}
		review.Files = append(review.Files, files...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	c.logger.Info().
		Str("owner", owner).
		Str("repo", repo).
		Int("pr", prNumber).
		Int("files", len(review.Files)).
		Msg("fetched PR files")

	return review, nil
}

// PostReviewComment posts a review comment on a PR.
func (c *Client) PostReviewComment(ctx context.Context, owner, repo string, prNumber int, body string) error {
	client, err := c.GetInstallationClient(ctx)
	if err != nil {
		return fmt.Errorf("getting installation client: %w", err)
	}

	bodyStr := body
	eventStr := "COMMENT"
	review := &github.PullRequestReviewRequest{
		Body:  &bodyStr,
		Event: &eventStr,
	}

	_, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, review)
	if err != nil {
		return fmt.Errorf("creating review: %w", err)
	}

	c.logger.Info().
		Str("owner", owner).
		Str("repo", repo).
		Int("pr", prNumber).
		Msg("posted review comment")

	return nil
}

// ParsePRURL extracts owner, repo, and PR number from a GitHub PR URL.
func ParsePRURL(url string) (owner, repo string, prNumber int, err error) {
	// https://github.com/owner/repo/pull/123
	url = strings.TrimSuffix(url, "/")
	parts := strings.Split(url, "/")
	if len(parts) < 5 {
		return "", "", 0, fmt.Errorf("invalid PR URL: %s", url)
	}

	var num int
	_, err = fmt.Sscanf(parts[len(parts)-1], "%d", &num)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number in URL: %s", url)
	}

	return parts[len(parts)-4], parts[len(parts)-3], num, nil
}
