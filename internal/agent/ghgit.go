package agent

import (
	"context"
	"encoding/json"
	"fmt"

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

	// Step 1: Get or create the branch ref
	branchSHA, err := a.ensureBranch(ctx, client, p.Owner, p.Repo, p.Branch, p.Base)
	if err != nil {
		return nil, fmt.Errorf("ensuring branch: %w", err)
	}

	// Step 2: Get the base tree SHA from the branch tip commit
	baseCommit, _, err := client.Git.GetCommit(ctx, p.Owner, p.Repo, branchSHA)
	if err != nil {
		return nil, fmt.Errorf("getting base commit: %w", err)
	}
	baseTreeSHA := baseCommit.GetTree().GetSHA()

	// Step 3: Create blobs for each file and build tree entries
	var treeEntries []*gh.TreeEntry

	for path, content := range p.Files {
		blob, _, err := client.Git.CreateBlob(ctx, p.Owner, p.Repo, &gh.Blob{
			Content:  gh.String(content),
			Encoding: gh.String("utf-8"),
		})
		if err != nil {
			return nil, fmt.Errorf("creating blob for %s: %w", path, err)
		}

		mode := "100644" // regular file
		treeEntries = append(treeEntries, &gh.TreeEntry{
			Path: gh.String(path),
			Mode: gh.String(mode),
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

	// Step 4: Create the new tree
	newTree, _, err := client.Git.CreateTree(ctx, p.Owner, p.Repo, baseTreeSHA, treeEntries)
	if err != nil {
		return nil, fmt.Errorf("creating tree: %w", err)
	}

	// Step 5: Create the commit
	newCommit, _, err := client.Git.CreateCommit(ctx, p.Owner, p.Repo, &gh.Commit{
		Message: gh.String(p.Message),
		Tree:    newTree,
		Parents: []*gh.Commit{{SHA: gh.String(branchSHA)}},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("creating commit: %w", err)
	}

	// Step 6: Update the branch ref to point to the new commit
	ref := "refs/heads/" + p.Branch
	_, _, err = client.Git.UpdateRef(ctx, p.Owner, p.Repo, &gh.Reference{
		Ref:    gh.String(ref),
		Object: &gh.GitObject{SHA: newCommit.SHA},
	}, false)
	if err != nil {
		return nil, fmt.Errorf("updating ref: %w", err)
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

// ensureBranch gets the branch HEAD SHA, creating it from base if it doesn't exist.
func (a *Agent) ensureBranch(ctx context.Context, client *gh.Client, owner, repo, branch, base string) (string, error) {
	// Try to get existing branch
	ref, resp, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err == nil && ref != nil {
		return ref.GetObject().GetSHA(), nil
	}
	if resp != nil && resp.StatusCode != 404 {
		return "", fmt.Errorf("checking branch: %w", err)
	}

	// Branch doesn't exist — create from base
	baseSHA, err := a.resolveRef(ctx, client, owner, repo, base)
	if err != nil {
		return "", fmt.Errorf("resolving base: %w", err)
	}

	newRef, _, err := client.Git.CreateRef(ctx, owner, repo, &gh.Reference{
		Ref:    gh.String("refs/heads/" + branch),
		Object: &gh.GitObject{SHA: gh.String(baseSHA)},
	})
	if err != nil {
		return "", fmt.Errorf("creating branch from base: %w", err)
	}

	return newRef.GetObject().GetSHA(), nil
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
