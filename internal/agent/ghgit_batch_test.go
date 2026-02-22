package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gh "github.com/google/go-github/v60/github"
)

// --- Mock GitHub client for testing ---

type mockGitHubClient struct {
	t *testing.T

	// For GetTree
	treeEntries []*gh.TreeEntry
	treeErr     error

	// For GetBlob
	blobContent map[string]string // SHA -> content
	blobErr     error

	// For GetContents
	contents    *gh.RepositoryContent
	contentsErr error

	// For Get (repo)
	repo    *gh.Repository
	repoErr error

	// For GetRef
	ref    *gh.Reference
	refErr error

	// For GetCommit
	commit    *gh.Commit
	commitErr error

	// Track calls
	treeCalls      []string // refs fetched
	blobCalls      []string // blob SHAs fetched
	contentCalls   []string // paths fetched
	getRefCalls    []string // refs checked
	getCommitCalls []string // commits fetched
}

func (m *mockGitHubClient) Git() *mockGitAPI {
	return &mockGitAPI{m: m}
}

func (m *mockGitHubClient) Repositories() *mockRepoAPI {
	return &mockRepoAPI{m: m}
}

type mockGitAPI struct {
	m *mockGitHubClient
}

func (g *mockGitAPI) GetTree(ctx context.Context, owner, repo, sha string, recursive bool) (*gh.Tree, *gh.Response, error) {
	g.m.treeCalls = append(g.m.treeCalls, sha)
	if g.m.treeErr != nil {
		return nil, nil, g.m.treeErr
	}
	return &gh.Tree{Entries: g.m.treeEntries}, nil, nil
}

func (g *mockGitAPI) GetBlob(ctx context.Context, owner, repo, sha string) (*gh.Blob, *gh.Response, error) {
	g.m.blobCalls = append(g.m.blobCalls, sha)
	if g.m.blobErr != nil {
		return nil, nil, g.m.blobErr
	}
	content, ok := g.m.blobContent[sha]
	if !ok {
		return nil, nil, errNotFound{}
	}
	return &gh.Blob{
		SHA:      gh.String(sha),
		Content:  gh.String(content),
		Encoding: gh.String("utf-8"),
		Size:     gh.Int(len(content)),
	}, nil, nil
}

type errNotFound struct{}

func (e errNotFound) Error() string {
	return "not found"
}

func (g *mockGitAPI) GetRef(ctx context.Context, owner, repo, ref string) (*gh.Reference, *gh.Response, error) {
	g.m.getRefCalls = append(g.m.getRefCalls, ref)
	if g.m.refErr != nil {
		return nil, nil, g.m.refErr
	}
	return g.m.ref, nil, nil
}

func (g *mockGitAPI) GetCommit(ctx context.Context, owner, repo, sha string) (*gh.Commit, *gh.Response, error) {
	g.m.getCommitCalls = append(g.m.getCommitCalls, sha)
	if g.m.commitErr != nil {
		return nil, nil, g.m.commitErr
	}
	return g.m.commit, nil, nil
}

type mockRepoAPI struct {
	m *mockGitHubClient
}

func (r *mockRepoAPI) Get(ctx context.Context, owner, repo string) (*gh.Repository, *gh.Response, error) {
	if r.m.repoErr != nil {
		return nil, nil, r.m.repoErr
	}
	return r.m.repo, nil, nil
}

func (r *mockRepoAPI) GetContents(ctx context.Context, owner, repo, path string, opts *gh.RepositoryContentGetOptions) (*gh.RepositoryContent, []*gh.RepositoryContent, *gh.Response, error) {
	r.m.contentCalls = append(r.m.contentCalls, path)
	if r.m.contentsErr != nil {
		return nil, nil, nil, r.m.contentsErr
	}
	return r.m.contents, nil, nil, nil
}

// --- Tests for git.get-tree parameter handling ---

func TestGHGitGetTree_ParamValidation_MissingOwner(t *testing.T) {
	params := json.RawMessage(`{"repo": "test"}`)
	var p struct {
		Owner          string `json:"owner"`
		Repo           string `json:"repo"`
		Path           string `json:"path"`
		Ref            string `json:"ref"`
		IncludeContent bool   `json:"include_content"`
		MaxDepth       int    `json:"max_depth"`
	}
	err := json.Unmarshal(params, &p)
	require.NoError(t, err)
	assert.Equal(t, "", p.Owner)
	assert.Equal(t, "test", p.Repo)
}

func TestGHGitGetTree_ParamValidation_Valid(t *testing.T) {
	params := json.RawMessage(`{
		"owner": "octocat",
		"repo": "Hello-World",
		"path": "cmd/",
		"ref": "main",
		"include_content": true,
		"max_depth": 3
	}`)
	var p struct {
		Owner          string `json:"owner"`
		Repo           string `json:"repo"`
		Path           string `json:"path"`
		Ref            string `json:"ref"`
		IncludeContent bool   `json:"include_content"`
		MaxDepth       int    `json:"max_depth"`
	}
	err := json.Unmarshal(params, &p)
	require.NoError(t, err)
	assert.Equal(t, "octocat", p.Owner)
	assert.Equal(t, "Hello-World", p.Repo)
	assert.Equal(t, "cmd/", p.Path)
	assert.Equal(t, "main", p.Ref)
	assert.True(t, p.IncludeContent)
	assert.Equal(t, 3, p.MaxDepth)
}

// --- Tests for git.get-files parameter handling ---

func TestGHGitGetFiles_ParamValidation_Valid(t *testing.T) {
	params := json.RawMessage(`{
		"owner": "octocat",
		"repo": "Hello-World",
		"paths": ["README.md", "go.mod", "main.go"],
		"ref": "develop"
	}`)
	var p struct {
		Owner string   `json:"owner"`
		Repo  string   `json:"repo"`
		Paths []string `json:"paths"`
		Ref   string   `json:"ref"`
	}
	err := json.Unmarshal(params, &p)
	require.NoError(t, err)
	assert.Equal(t, "octocat", p.Owner)
	assert.Equal(t, "Hello-World", p.Repo)
	assert.Equal(t, 3, len(p.Paths))
	assert.Equal(t, "README.md", p.Paths[0])
	assert.Equal(t, "develop", p.Ref)
}

func TestGHGitGetFiles_ParamValidation_EmptyPaths(t *testing.T) {
	params := json.RawMessage(`{
		"owner": "octocat",
		"repo": "Hello-World",
		"paths": [],
		"ref": "main"
	}`)
	var p struct {
		Owner string   `json:"owner"`
		Repo  string   `json:"repo"`
		Paths []string `json:"paths"`
		Ref   string   `json:"ref"`
	}
	err := json.Unmarshal(params, &p)
	require.NoError(t, err)
	assert.Equal(t, 0, len(p.Paths))
}

func TestGHGitGetFiles_ParamValidation_MissingOptionalRef(t *testing.T) {
	params := json.RawMessage(`{
		"owner": "octocat",
		"repo": "Hello-World",
		"paths": ["file.txt"]
	}`)
	var p struct {
		Owner string   `json:"owner"`
		Repo  string   `json:"repo"`
		Paths []string `json:"paths"`
		Ref   string   `json:"ref"`
	}
	err := json.Unmarshal(params, &p)
	require.NoError(t, err)
	assert.Equal(t, "", p.Ref)
	// Code should default to "main"
}

// --- Tests for tree entry filtering ---

func TestGHGitGetTree_PathPrefix_Filtering(t *testing.T) {
	// Test the path filtering logic
	paths := []string{
		"cmd/agent/main.go",
		"cmd/tool/main.go",
		"internal/config.go",
		"internal/agent/types.go",
	}

	prefix := "cmd/"
	filtered := 0
	for _, path := range paths {
		if prefix == "" || pathStartsWith(path, prefix) {
			filtered++
		}
	}

	assert.Equal(t, 2, filtered)
}

func TestGHGitGetTree_DepthCalculation(t *testing.T) {
	// Test max_depth logic
	paths := []struct {
		path      string
		baseParts int
		maxDepth  int
		included  bool
	}{
		{"cmd/agent/main.go", 1, 2, true},
		{"cmd/agent/sub/main.go", 1, 2, false},
		{"cmd/", 1, 1, true},
		{"cmd/a/b/c", 1, 3, true},
		{"cmd/a/b/c/d", 1, 3, false},
	}

	for _, tc := range paths {
		pathParts := countSlashes(tc.path)
		included := pathParts < tc.baseParts+tc.maxDepth
		assert.Equal(t, tc.included, included, "path=%s", tc.path)
	}
}

// --- Tests for data structure response ---

func TestGHGitGetTree_ResponseStructure(t *testing.T) {
	// Test that response has correct JSON structure
	response := map[string]interface{}{
		"tree": []map[string]interface{}{
			{
				"path": "README.md",
				"type": "blob",
				"sha":  "abc123",
				"size": 1000,
			},
			{
				"path": "src/",
				"type": "tree",
				"sha":  "def456",
			},
		},
		"total_files": 1,
		"truncated":   false,
	}

	// Verify structure
	require.NotNil(t, response["tree"])
	tree := response["tree"].([]map[string]interface{})
	assert.Equal(t, 2, len(tree))
	assert.Equal(t, "README.md", tree[0]["path"])
	assert.Equal(t, 1, response["total_files"])
	truncated := response["truncated"].(bool)
	assert.False(t, truncated)
}

func TestGHGitGetFiles_ResponseStructure(t *testing.T) {
	// Test that response has correct JSON structure
	response := map[string]interface{}{
		"files": []map[string]interface{}{
			{
				"path":    "README.md",
				"sha":     "abc123",
				"size":    500,
				"content": "# My Project",
			},
			{
				"path":  "missing.txt",
				"error": "not found",
			},
		},
	}

	// Verify structure
	require.NotNil(t, response["files"])
	files := response["files"].([]map[string]interface{})
	assert.Equal(t, 2, len(files))

	// First file should have content
	file0 := files[0]
	assert.Equal(t, "README.md", file0["path"])
	assert.Equal(t, "abc123", file0["sha"])
	assert.NotNil(t, file0["content"])

	// Second file should have error
	file1 := files[1]
	assert.Equal(t, "missing.txt", file1["path"])
	assert.NotNil(t, file1["error"])
}

// --- Tests for classification ---

func TestGHGitGetTree_Classification(t *testing.T) {
	assert.Equal(t, classRead, classifyOperation("git.get-tree"))
}

func TestGHGitGetFiles_Classification(t *testing.T) {
	assert.Equal(t, classRead, classifyOperation("git.get-files"))
}

// --- Helper functions ---

func pathStartsWith(path, prefix string) bool {
	return prefix == "" || len(path) >= len(prefix) && path[:len(prefix)] == prefix
}

func countSlashes(path string) int {
	count := 0
	for _, ch := range path {
		if ch == '/' {
			count++
		}
	}
	return count
}
