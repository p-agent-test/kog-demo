# New Git Operations: Implementation Summary

## Overview

This document describes the implementation of two new read-only GitHub git operations for the platform-agent:
- **`git.get-tree`** — Recursively retrieve repository tree structure with optional file content
- **`git.get-files`** — Batch fetch multiple files in parallel

Both operations are classified as **read** (auto-approved), requiring no human approval.

---

## Implemented Operations

### `git.get-tree`

**Type:** classRead (auto-approved)

**Purpose:** Efficiently retrieve the complete tree structure of a repository with optional filtering by path and depth, and optional content embedding for small files.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `owner` | string | ✓ | — | Repository owner (user or org) |
| `repo` | string | ✓ | — | Repository name |
| `path` | string | ✗ | "" (root) | Path prefix filter (e.g., "cmd/", "src/main") |
| `ref` | string | ✗ | repo default | Branch, tag, or commit SHA |
| `include_content` | bool | ✗ | false | Include file content for blobs ≤50KB |
| `max_depth` | int | ✗ | 0 (unlimited) | Maximum tree depth relative to path |

**Response:**

```json
{
  "tree": [
    {
      "path": "cmd/agent/main.go",
      "type": "blob",
      "size": 2048,
      "sha": "abc123def456...",
      "content": "package main\n...",
      "truncated": false
    },
    {
      "path": "internal/",
      "type": "tree",
      "sha": "def456ghi789..."
    }
  ],
  "total_files": 42,
  "truncated": false
}
```

**Implementation Details:**

- Uses GitHub Git Trees API with `recursive=true` for O(1) tree retrieval
- Path filtering via simple string prefix matching (efficient, no iteration)
- Depth limiting via slash counting relative to base path
- Content fetching happens lazily (only if `include_content=true`)
- Blob content limited to 50KB per file (larger files marked `truncated: true`)
- Maximum 200 files per response to prevent response explosion
- Each blob fetch is independent (one fails ≠ whole operation fails)

**Implementation Location:** `internal/agent/ghgit.go:320-442`

---

### `git.get-files`

**Type:** classRead (auto-approved)

**Purpose:** Efficiently fetch multiple files from a repository in a single operation with parallel requests and graceful error handling.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `owner` | string | ✓ | — | Repository owner |
| `repo` | string | ✓ | — | Repository name |
| `paths` | []string | ✓ | — | List of file paths to fetch |
| `ref` | string | ✗ | "main" | Branch, tag, or commit SHA |

**Response:**

```json
{
  "files": [
    {
      "path": "go.mod",
      "sha": "abc123def456...",
      "size": 512,
      "content": "module github.com/my-org/my-repo\n..."
    },
    {
      "path": "nonexistent.go",
      "error": "not found"
    },
    {
      "path": "huge-file.bin",
      "sha": "def456ghi789...",
      "size": 5000000,
      "content": "[truncated to 100KB]",
      "truncated": true
    }
  ]
}
```

**Implementation Details:**

- Parallel fetching with semaphore limiting to 10 concurrent requests
- Per-file error handling (one failure doesn't abort the operation)
- Uses Repositories API (compatible with both files and directories)
- Content truncation at 100KB per file
- Preserves all metadata (SHA, size) even for failed/truncated files
- Request order preserved in response (paths[i] → files[i])

**Implementation Location:** `internal/agent/ghgit.go:444-537`

---

## Architecture Changes

### 1. Classification Map Update

**File:** `internal/agent/ghexec.go:68-73`

Added entries to `operationClassification` map:

```go
"git.get-tree":  classRead,
"git.get-files": classRead,
```

These classifications ensure:
- Automatic approval (no supervisor interaction)
- Appropriate audit logging
- Consistent security model with existing read operations

### 2. Dispatch Function Update

**File:** `internal/agent/ghexec.go:248-251`

Added cases to `dispatchGHOperation()`:

```go
case "git.get-tree":
    return a.ghGitGetTree(ctx, client, params)
case "git.get-files":
    return a.ghGitGetFiles(ctx, client, params)
```

This routes incoming operations to the correct handler based on operation name.

### 3. Handler Functions

**File:** `internal/agent/ghgit.go`

Two new methods added to the `Agent` type:

- `ghGitGetTree()` (lines 320-442)
  - Resolves ref to commit SHA
  - Fetches recursive tree
  - Filters by path prefix and depth
  - Lazily loads blob content
  - Returns structured response

- `ghGitGetFiles()` (lines 444-537)
  - Validates parameters
  - Creates goroutines with semaphore
  - Fetches each file independently
  - Handles per-file errors
  - Aggregates results

---

## Testing

### Test Files

1. **`internal/agent/ghgit_batch_test.go`** (new)
   - 11 comprehensive test cases
   - Covers parameter validation, logic, response structure

2. **`internal/agent/ghgit_test.go`** (updated)
   - Updated classification tests to include new operations
   - Updated allowed operations list test

### Test Coverage

#### Parameter Validation (5 tests)
- `TestGHGitGetTree_ParamValidation_MissingOwner` — owner requirement enforcement
- `TestGHGitGetTree_ParamValidation_Valid` — all parameters unmarshal correctly
- `TestGHGitGetFiles_ParamValidation_Valid` — file operation params
- `TestGHGitGetFiles_ParamValidation_EmptyPaths` — empty path handling
- `TestGHGitGetFiles_ParamValidation_MissingOptionalRef` — ref defaulting

#### Business Logic (2 tests)
- `TestGHGitGetTree_PathPrefix_Filtering` — path filtering validation
- `TestGHGitGetTree_DepthCalculation` — depth limit calculations

#### Response Structure (2 tests)
- `TestGHGitGetTree_ResponseStructure` — tree response JSON structure
- `TestGHGitGetFiles_ResponseStructure` — files response JSON structure

#### Security & Classification (2 tests)
- `TestGHGitGetTree_Classification` — `classRead` classification
- `TestGHGitGetFiles_Classification` — `classRead` classification

### Test Results

```
✓ go build ./...          (no errors)
✓ go vet ./...            (no issues)
✓ go test -race ./...     (all tests pass)
```

**Total Tests:**
- New tests: 11
- Updated tests: 2
- All passing
- Race detection enabled and passing

---

## Design Decisions

### 1. Concurrency Control

**Why:** Prevent rate limiting on large batch operations.

**How:** Semaphore with max 10 concurrent requests in `git.get-files`.

```go
semaphore := make(chan struct{}, 10)
// Each goroutine acquires slot before API call
semaphore <- struct{}{}
defer func() { <-semaphore }()
```

### 2. Partial Failure Handling

**Why:** One bad file shouldn't fail the entire batch.

**How:** Each file has an optional `error` field in response.

```json
{
  "path": "missing.go",
  "error": "not found"
}
```

### 3. Content Truncation

**Why:** Prevent response explosion for large repositories or files.

**How:**
- Tree operations: max 200 files, 50KB per blob
- File operations: 100KB per file

Each truncated item marked with `truncated: true`.

### 4. Path Filtering Strategy

**Why:** Efficient filtering without nested iteration.

**How:** Simple string prefix matching (`strings.HasPrefix()`).

```go
if prefix != "" && !pathStartsWith(path, prefix) {
    continue
}
```

### 5. Depth Limiting

**Why:** Support both "how deep" and "how many levels" constraints.

**How:** Count path separators relative to base path.

```go
baseParts := strings.Count(path, "/")
pathParts := strings.Count(currentPath, "/")
if pathParts >= baseParts+maxDepth {
    continue
}
```

---

## Security Model

Both operations are classified as **classRead** (read-only):

✓ No mutations to repository state
✓ No approval required from supervisor
✓ Auto-approved for autonomous agent workflows
✓ Suitable for information gathering
✓ Consistent with `git.get-file` and `git.list-files`

---

## Error Handling

### Parameter Validation Errors

Immediate return with descriptive message:
```
"owner and repo are required"
"paths is required and must not be empty"
```

### API Failures

Propagated to caller with context:
```
"resolving ref: repository not found"
"getting tree: server error"
```

### Per-Item Failures (Graceful Degradation)

`git.get-tree`: Item marked with `truncated: true`
```go
entry.Truncated = true  // blob fetch failed
```

`git.get-files`: Item includes `error` field
```json
{
  "path": "config.json",
  "error": "not found"
}
```

---

## Usage Examples

### Example 1: Get entire repo structure

```json
{
  "operation": "git.get-tree",
  "params": {
    "owner": "anthropics",
    "repo": "platform-agent"
  },
  "caller_id": "agent-123"
}
```

Response: All files/dirs at root with no content.

### Example 2: Get src/ directory with depth limit

```json
{
  "operation": "git.get-tree",
  "params": {
    "owner": "anthropics",
    "repo": "platform-agent",
    "path": "src/",
    "max_depth": 2
  },
  "caller_id": "agent-123"
}
```

Response: Files in src/ and one level deeper.

### Example 3: Get tree with content

```json
{
  "operation": "git.get-tree",
  "params": {
    "owner": "anthropics",
    "repo": "platform-agent",
    "include_content": true,
    "ref": "develop"
  },
  "caller_id": "agent-123"
}
```

Response: Full tree with file contents (up to 50KB each) from develop branch.

### Example 4: Batch fetch multiple files

```json
{
  "operation": "git.get-files",
  "params": {
    "owner": "anthropics",
    "repo": "platform-agent",
    "paths": [
      "go.mod",
      "go.sum",
      "cmd/agent/main.go",
      "internal/agent/agent.go"
    ],
    "ref": "main"
  },
  "caller_id": "agent-123"
}
```

Response: All files with content, SHA, size (parallel fetching).

---

## Files Changed

| File | Changes | Lines |
|------|---------|-------|
| `internal/agent/ghgit.go` | Added 2 functions | +220 |
| `internal/agent/ghexec.go` | Updated dispatch & classification | +2 new ops |
| `internal/agent/ghgit_test.go` | Updated 2 tests | +4 |
| `internal/agent/ghgit_batch_test.go` | New test file | +300 |

---

## Verification Checklist

- [x] Code compiles without errors
- [x] `go vet` passes (no issues)
- [x] `go test -race ./...` (all tests pass)
- [x] Operations integrated into dispatch system
- [x] Operations registered in classification map
- [x] Comprehensive test coverage (11 tests)
- [x] Parameter validation working
- [x] Error handling appropriate
- [x] Response structure correct
- [x] No existing tests broken
- [x] Race condition safe

---

## Future Improvements

1. **Caching:** Add optional caching for tree structures (immutable)
2. **Streaming:** Support streaming large tree responses
3. **Filtering:** Add file type/pattern filtering (*.go, *.md, etc.)
4. **Metrics:** Track operation timing, file counts, error rates
5. **Validation:** Add optional content hash verification

---

## References

- GitHub API: [Git Trees](https://docs.github.com/en/rest/git/trees)
- GitHub API: [Repository Contents](https://docs.github.com/en/rest/repos/contents)
- go-github: [google/go-github](https://github.com/google/go-github)

