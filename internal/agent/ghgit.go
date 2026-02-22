package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	gh "github.com/google/go-github/v60/github"
)

// --- Git operation handlers ---
// These use the low-level Git Data API for atomic multi-file commits.
// Flow: get base ref → create blobs → create tree → create commit → update ref

// ghGitCommit creates an atomic commit with multiple file changes.
// Uses the Git Trees API to commit N files in a single commit.
//
// Params:
//   - owner, repo: target repository
//   - branch: target branch (created from base if it doesn't exist)
//   - base: base branch to branch from (default: "main", used only if branch doesn't exist)
//   - message: commit message
//   - files: map of path → content (string content, not base64)
//   - delete: optional list of paths to delete
//
// Returns: commit SHA, commit URL, files changed count
func (a *Agent) ghGitCommit(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner   string            `json:"owner"`
		Repo    string            `json:"repo"`
		Branch  string            `json:"branch"`
		Base    string            `json:"base"`
		Message string            `json:"message"`
		Files   map[string]string `json:"files"`
		Delete  []string          `json:"delete"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	if p.Branch == "" {
		return nil, fmt.Errorf("branch is required")
	}
	if p.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if len(p.Files) == 0 && len(p.Delete) == 0 {
		return nil, fmt.Errorf("at least one file change is required")
	}
	if p.Base == "" {
		p.Base = "main"
	}

	// Step 1: Check if repo is empty (no branches) or get existing branch
	branchSHA, isEmpty, err := a.getBranchOrDetectEmpty(ctx, client, p.Owner, p.Repo, p.Branch, p.Base)
	if err != nil {
		return nil, fmt.Errorf("checking repo state: %w", err)
	}

	// Empty repo: GitHub Git Data API (blobs, trees, commits, refs) ALL return 409.
	// Use Contents API to bootstrap the first file, then continue normally.
	if isEmpty {
		return a.ghGitCommitEmpty(ctx, client, p.Owner, p.Repo, p.Branch, p.Message, p.Files)
	}

	a.logger.Debug().
		Str("branch", p.Branch).
		Str("branchSHA", branchSHA).
		Bool("isEmpty", isEmpty).
		Msg("ghGitCommit: resolved branch state")

	// Step 2: Create blobs for each file and build tree entries
	var treeEntries []*gh.TreeEntry

	for path, content := range p.Files {
		blob, _, err := client.Git.CreateBlob(ctx, p.Owner, p.Repo, &gh.Blob{
			Content:  gh.String(content),
			Encoding: gh.String("utf-8"),
		})
		if err != nil {
			return nil, fmt.Errorf("creating blob for %s: %w", path, err)
		}
		treeEntries = append(treeEntries, &gh.TreeEntry{
			Path: gh.String(path),
			Mode: gh.String("100644"),
			Type: gh.String("blob"),
			SHA:  blob.SHA,
		})
	}

	// Add deletions (tree entry with nil SHA = delete)
	for _, path := range p.Delete {
		treeEntries = append(treeEntries, &gh.TreeEntry{
			Path: gh.String(path),
			Mode: gh.String("100644"),
			Type: gh.String("blob"),
			// SHA intentionally nil — GitHub interprets this as delete
		})
	}

	// Step 3: Create the new tree from base
	baseCommit, _, err := client.Git.GetCommit(ctx, p.Owner, p.Repo, branchSHA)
	if err != nil {
		return nil, fmt.Errorf("getting base commit: %w", err)
	}
	newTree, _, err := client.Git.CreateTree(ctx, p.Owner, p.Repo, baseCommit.GetTree().GetSHA(), treeEntries)
	if err != nil {
		return nil, fmt.Errorf("creating tree: %w", err)
	}

	// Step 4: Create the commit
	commitInput := &gh.Commit{
		Message: gh.String(p.Message),
		Tree:    newTree,
		Parents: []*gh.Commit{{SHA: gh.String(branchSHA)}},
	}
	newCommit, _, err := client.Git.CreateCommit(ctx, p.Owner, p.Repo, commitInput, nil)
	if err != nil {
		return nil, fmt.Errorf("creating commit: %w", err)
	}

	// Step 5: Update the branch ref
	ref := "refs/heads/" + p.Branch
	_, _, err = client.Git.UpdateRef(ctx, p.Owner, p.Repo, &gh.Reference{
		Ref:    gh.String(ref),
		Object: &gh.GitObject{SHA: newCommit.SHA},
	}, false)
	if err != nil {
		return nil, fmt.Errorf("setting ref: %w", err)
	}

	commitURL := fmt.Sprintf("https://github.com/%s/%s/commit/%s", p.Owner, p.Repo, newCommit.GetSHA())

	return map[string]interface{}{
		"sha":           newCommit.GetSHA(),
		"url":           commitURL,
		"message":       p.Message,
		"branch":        p.Branch,
		"files_changed": len(p.Files),
		"files_deleted": len(p.Delete),
	}, nil
}

// ghGitCreateBranch creates a new branch from a base ref.
//
// Params:
//   - owner, repo: target repository
//   - branch: new branch name
//   - base: base branch or SHA (default: "main")
//
// Returns: branch name, head SHA
func (a *Agent) ghGitCreateBranch(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
		Base   string `json:"base"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.Branch == "" {
		return nil, fmt.Errorf("owner, repo, and branch are required")
	}
	if p.Base == "" {
		p.Base = "main"
	}

	// Resolve base to SHA
	baseSHA, err := a.resolveRef(ctx, client, p.Owner, p.Repo, p.Base)
	if err != nil {
		return nil, fmt.Errorf("resolving base ref: %w", err)
	}

	// Create the branch
	ref := "refs/heads/" + p.Branch
	newRef, _, err := client.Git.CreateRef(ctx, p.Owner, p.Repo, &gh.Reference{
		Ref:    gh.String(ref),
		Object: &gh.GitObject{SHA: gh.String(baseSHA)},
	})
	if err != nil {
		return nil, fmt.Errorf("creating branch: %w", err)
	}

	return map[string]interface{}{
		"branch": p.Branch,
		"sha":    newRef.GetObject().GetSHA(),
		"base":   p.Base,
	}, nil
}

// ghGitGetFile reads a file from a repository at a given ref.
//
// Params:
//   - owner, repo: target repository
//   - path: file path
//   - ref: branch/tag/SHA (default: "main")
//
// Returns: content (string), sha, size
func (a *Agent) ghGitGetFile(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Path  string `json:"path"`
		Ref   string `json:"ref"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" || p.Path == "" {
		return nil, fmt.Errorf("owner, repo, and path are required")
	}

	opts := &gh.RepositoryContentGetOptions{}
	if p.Ref != "" {
		opts.Ref = p.Ref
	}

	fileContent, _, _, err := client.Repositories.GetContents(ctx, p.Owner, p.Repo, p.Path, opts)
	if err != nil {
		return nil, fmt.Errorf("getting file: %w", err)
	}
	if fileContent == nil {
		return nil, fmt.Errorf("path is a directory, not a file")
	}

	rawContent, err := fileContent.GetContent()
	if err != nil {
		return nil, fmt.Errorf("decoding file content: %w", err)
	}
	content := []byte(rawContent)

	// Truncate large files
	const maxSize = 100_000
	truncated := false
	if len(content) > maxSize {
		content = content[:maxSize]
		truncated = true
	}

	return map[string]interface{}{
		"path":      p.Path,
		"sha":       fileContent.GetSHA(),
		"size":      fileContent.GetSize(),
		"content":   string(content),
		"truncated": truncated,
		"encoding":  "utf-8",
	}, nil
}

// ghGitListFiles lists files/directories at a given path.
//
// Params:
//   - owner, repo: target repository
//   - path: directory path (default: root "")
//   - ref: branch/tag/SHA (default: "main")
//
// Returns: list of {name, path, type, size, sha}
func (a *Agent) ghGitListFiles(ctx context.Context, client *gh.Client, params json.RawMessage) (interface{}, error) {
	var p struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Path  string `json:"path"`
		Ref   string `json:"ref"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Owner == "" || p.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}

	opts := &gh.RepositoryContentGetOptions{}
	if p.Ref != "" {
		opts.Ref = p.Ref
	}

	_, dirContent, _, err := client.Repositories.GetContents(ctx, p.Owner, p.Repo, p.Path, opts)
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}

	type fileEntry struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"` // "file" or "dir"
		Size int    `json:"size"`
		SHA  string `json:"sha"`
	}

	var entries []fileEntry
	for _, item := range dirContent {
		entries = append(entries, fileEntry{
			Name: item.GetName(),
			Path: item.GetPath(),
			Type: item.GetType(),
			Size: item.GetSize(),
			SHA:  item.GetSHA(),
		})
	}

	return entries, nil
}

// --- helpers ---

// getBranchOrDetectEmpty checks if a branch exists. If not, checks if repo is empty.
// Returns (branchSHA, isEmpty, error). If isEmpty=true, branchSHA is "".
func (a *Agent) getBranchOrDetectEmpty(ctx context.Context, client *gh.Client, owner, repo, branch, base string) (string, bool, error) {
	// Try to get existing branch
	ref, resp, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err == nil && ref != nil {
		return ref.GetObject().GetSHA(), false, nil
	}

	// GitHub returns 409 "Git Repository is empty" for empty repos,
	// and 404 for non-existent branches in non-empty repos.
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}

	// 409 = empty repo (no commits at all)
	if statusCode == 409 {
		return "", true, nil
	}

	// 404 = branch doesn't exist, check if base exists
	if statusCode == 404 {
		baseRef, baseResp, baseErr := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+base)
		if baseErr == nil && baseRef != nil {
			// Base exists, branch doesn't — create branch from base
			baseSHA := baseRef.GetObject().GetSHA()
			newRef, _, createErr := client.Git.CreateRef(ctx, owner, repo, &gh.Reference{
				Ref:    gh.String("refs/heads/" + branch),
				Object: &gh.GitObject{SHA: gh.String(baseSHA)},
			})
			if createErr != nil {
				return "", false, fmt.Errorf("creating branch from base: %w", createErr)
			}
			return newRef.GetObject().GetSHA(), false, nil
		}

		baseStatus := 0
		if baseResp != nil {
			baseStatus = baseResp.StatusCode
		}

		if baseStatus == 404 || baseStatus == 409 {
			// Neither branch nor base exists — repo is empty
			return "", true, nil
		}
		if baseErr != nil {
			return "", false, fmt.Errorf("checking base ref: %w", baseErr)
		}
	}

	if err != nil {
		return "", false, fmt.Errorf("checking branch: %w", err)
	}
	return "", true, nil
}

// ensureBranch gets the branch HEAD SHA, creating it from base if it doesn't exist.
func (a *Agent) ensureBranch(ctx context.Context, client *gh.Client, owner, repo, branch, base string) (string, error) {
	sha, isEmpty, err := a.getBranchOrDetectEmpty(ctx, client, owner, repo, branch, base)
	if err != nil {
		return "", err
	}
	if isEmpty {
		return "", fmt.Errorf("repo is empty (no branches) — use git.commit to create initial commit")
	}
	return sha, nil
}

// resolveRef resolves a branch name or SHA to a commit SHA.
func (a *Agent) resolveRef(ctx context.Context, client *gh.Client, owner, repo, ref string) (string, error) {
	// If it looks like a full SHA, use it directly
	if len(ref) == 40 {
		return ref, nil
	}

	// Try as branch ref
	gitRef, _, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+ref)
	if err != nil {
		// Try as tag
		gitRef, _, err = client.Git.GetRef(ctx, owner, repo, "refs/tags/"+ref)
		if err != nil {
			return "", fmt.Errorf("ref %q not found as branch or tag", ref)
		}
	}

	return gitRef.GetObject().GetSHA(), nil
}

// ghGitCommitEmpty handles initial commits on empty repositories.
// GitHub's Git Data API (blobs, trees, commits, refs) all return 409 for empty repos.
// Strategy: Use Contents API to create the first file (which initializes the repo),
// then use Git Data API for remaining files if any.
func (a *Agent) ghGitCommitEmpty(ctx context.Context, client *gh.Client, owner, repo, branch, message string, files map[string]string) (interface{}, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("at least one file is required for initial commit on empty repo")
	}

	// Sort file paths for deterministic ordering
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// Create the first file via Contents API — this bootstraps the repo
	firstPath := paths[0]
	firstContent := files[firstPath]
	commitMsg := message
	if len(files) > 1 {
		commitMsg = message + fmt.Sprintf(" (1/%d)", len(files))
	}

	createResp, _, err := client.Repositories.CreateFile(ctx, owner, repo, firstPath, &gh.RepositoryContentFileOptions{
		Message: gh.String(commitMsg),
		Content: []byte(firstContent),
		Branch:  gh.String(branch),
	})
	if err != nil {
		return nil, fmt.Errorf("creating initial file %s: %w", firstPath, err)
	}

	lastSHA := createResp.Commit.GetSHA()

	// If there are more files, use Git Data API now that repo is initialized
	if len(files) > 1 {
		// Get the branch ref — may need retry due to GitHub eventual consistency
		var branchSHA string
		for attempt := 0; attempt < 5; attempt++ {
			branchRef, _, refErr := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
			if refErr == nil && branchRef != nil {
				branchSHA = branchRef.GetObject().GetSHA()
				break
			}
			if attempt < 4 {
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			} else {
				return nil, fmt.Errorf("branch %s not available after init (retried %d times): %w", branch, attempt+1, refErr)
			}
		}

		// Create blobs + tree for remaining files
		var treeEntries []*gh.TreeEntry
		for _, p := range paths[1:] {
			blob, _, err := client.Git.CreateBlob(ctx, owner, repo, &gh.Blob{
				Content:  gh.String(files[p]),
				Encoding: gh.String("utf-8"),
			})
			if err != nil {
				return nil, fmt.Errorf("creating blob for %s: %w", p, err)
			}
			treeEntries = append(treeEntries, &gh.TreeEntry{
				Path: gh.String(p),
				Mode: gh.String("100644"),
				Type: gh.String("blob"),
				SHA:  blob.SHA,
			})
		}

		baseCommit, _, err := client.Git.GetCommit(ctx, owner, repo, branchSHA)
		if err != nil {
			return nil, fmt.Errorf("getting base commit: %w", err)
		}

		newTree, _, err := client.Git.CreateTree(ctx, owner, repo, baseCommit.GetTree().GetSHA(), treeEntries)
		if err != nil {
			return nil, fmt.Errorf("creating tree for remaining files: %w", err)
		}

		newCommit, _, err := client.Git.CreateCommit(ctx, owner, repo, &gh.Commit{
			Message: gh.String(message),
			Tree:    newTree,
			Parents: []*gh.Commit{{SHA: gh.String(branchSHA)}},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("creating commit for remaining files: %w", err)
		}

		_, _, err = client.Git.UpdateRef(ctx, owner, repo, &gh.Reference{
			Ref:    gh.String("refs/heads/" + branch),
			Object: &gh.GitObject{SHA: newCommit.SHA},
		}, false)
		if err != nil {
			return nil, fmt.Errorf("updating ref: %w", err)
		}
		lastSHA = newCommit.GetSHA()
	}

	commitURL := fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, lastSHA)
	return map[string]interface{}{
		"sha":           lastSHA,
		"url":           commitURL,
		"message":       message,
		"branch":        branch,
		"files_changed": len(files),
		"files_deleted": 0,
		"initial":       true,
	}, nil
}
