# Projects — Product Requirements Document

**Author:** Platform Engineering  
**Status:** Draft  
**Date:** 2026-02-23  
**Version:** 0.2  

---

## 1. Executive Summary

Projects transforms the platform-agent from a stateless task executor into a **persistent, context-rich project management layer**. Today, every Slack thread is an isolated session that dies when context runs out. Projects gives Anıl the ability to create named, long-lived workstreams — each backed by a dedicated OpenClaw session with accumulated context — and operate them entirely from his phone via Slack.

The 10x insight: this isn't "named sessions." It's an **autonomous project memory** that survives token limits, agent restarts, and days of inactivity. When you say `@kog leader-election`, Kog doesn't just resume a session — it rehydrates full project context (decisions, blockers, progress, repo state) and picks up exactly where you left off.

### Priority Matrix

| Priority | Feature | Phases |
|----------|---------|--------|
| **P1 — MUST HAVE** | **Project Memory** — decisions, blockers, architecture notes survive session resets | Phase 1-2 |
| **P1 — MUST HAVE** | **Short Routing** — `@kog leader-election` (single word) routes to project. No verbose commands. | Phase 1-2 |
| **P1 — MUST HAVE** | **Project Dashboard** — `@kog projects` shows status, last activity, open tasks. Mobile-friendly. | Phase 1-2 |
| **P1 — MUST HAVE** | **Cross-Project Awareness** — Kog can cross-reference between projects (shared context index) | Phase 1-2 |
| P2 — Nice to have | **Project Handoff** — transfer ownership to another person | Phase 3+ |
| P2 — Nice to have | **Auto-pause / Auto-resume** — 7d inactive → pause, mention → auto-resume with context | Phase 3+ |

---

## 2. Problem Statement & User Stories

### Problem

1. **Context evaporates.** Slack threads map 1:1 to OpenClaw sessions. When a session hits token limits or the agent restarts, all accumulated context is lost.
2. **No project continuity.** There's no way to say "continue working on the leader-election refactor" from a new thread days later.
3. **Tasks are orphans.** Tasks submitted via Management API have no parent grouping — you can't see "all work done on project X."
4. **Session routing is channel-based.** The bridge builds session IDs from `slack-{channel}-{thread}`. There's no concept of routing to a *project*.
5. **Mobile-hostile.** Managing multiple workstreams from a phone requires remembering thread links and context.

### User Stories

| ID | As a… | I want to… | So that… |
|----|-------|-----------|----------|
| P-1 | Platform engineer | Create a named project with a description and repo link | I have a persistent workspace for a workstream |
| P-2 | Platform engineer | Continue a project from any Slack thread | I'm not locked to the original thread |
| P-3 | Platform engineer | List all my active projects | I can switch context quickly from my phone |
| P-4 | Platform engineer | See project history (decisions, tasks, progress) | I have full audit trail without searching threads |
| P-5 | Platform engineer | Have Kog remember everything about a project across sessions | Context survives token limits and restarts |
| P-6 | Platform engineer | Associate tasks with projects | I can see all work grouped by project |
| P-7 | Platform engineer | Archive/reactivate projects | Completed work doesn't clutter my active list |
| P-8 | API consumer | Submit tasks scoped to a project via Management API | Programmatic workflows are project-aware |

---

## 3. Architecture Design

### High-Level Flow

```
┌──────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Slack User   │────▶│  Slack Handler    │────▶│  Project Router  │
│  "@kog ..."   │     │  (command parse)  │     │  (new component) │
└──────────────┘     └──────────────────┘     └────────┬────────┘
                                                        │
                                              ┌─────────▼─────────┐
                                              │   Project Store    │
                                              │   (SQLite)         │
                                              │   - projects       │
                                              │   - project_events │
                                              │   - project_memory │
                                              └─────────┬─────────┘
                                                        │
                                              ┌─────────▼─────────┐
                                              │   Session Manager  │
                                              │   (new component)  │
                                              │   - session map    │
                                              │   - context reload │
                                              │   - rotation       │
                                              └─────────┬─────────┘
                                                        │
                                              ┌─────────▼─────────┐
                                              │   Bridge (WS/CLI)  │
                                              │   session routing   │
                                              └─────────┬─────────┘
                                                        │
                                              ┌─────────▼─────────┐
                                              │   OpenClaw Gateway  │
                                              │   (Kog session)     │
                                              └────────────────────┘
```

### Component Responsibilities

| Component | Role |
|-----------|------|
| **Project Router** | Parses Slack commands, resolves project slug → session, delegates to bridge |
| **Project Store** | SQLite CRUD for projects, events, memory segments |
| **Session Manager** | Maps projects to OpenClaw sessions, handles rotation on token exhaustion, injects context preamble |
| **Bridge** (modified) | Accepts explicit session IDs from Project Router instead of computing them from channel/thread |

### Session Model: 1:1 with Rotation

Each project has exactly **one active OpenClaw session** at a time. The session key follows the pattern:

```
agent:main:project-{slug}
```

When the session hits token limits (detected via error response from OpenClaw), the Session Manager:

1. Asks Kog to produce a **context summary** of the dying session
2. Stores the summary in `project_memory`
3. Creates a new session: `agent:main:project-{slug}-v{N}`
4. Injects a **context preamble** into the new session (project description + memory segments + last summary)
5. Updates the project's `active_session` pointer

This is the key differentiator from "named sessions" — **projects survive session death**.

---

## 4. Data Model

### New Tables

```sql
-- Migration v2

CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,           -- UUID
    slug        TEXT NOT NULL UNIQUE,       -- URL-safe name: "leader-election"
    name        TEXT NOT NULL,              -- Display name: "Leader Election Refactor"
    description TEXT NOT NULL DEFAULT '',   -- What this project is about
    repo_url    TEXT,                       -- Optional GitHub repo
    status      TEXT NOT NULL DEFAULT 'active',  -- active | paused | archived
    owner_id    TEXT NOT NULL,              -- Slack user ID
    active_session TEXT,                    -- Current OpenClaw session key
    session_version INTEGER NOT NULL DEFAULT 1,  -- Incremented on session rotation
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    archived_at INTEGER
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status);
CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects(owner_id);

CREATE TABLE IF NOT EXISTS project_memory (
    id          TEXT PRIMARY KEY,           -- UUID
    project_id  TEXT NOT NULL REFERENCES projects(id),
    type        TEXT NOT NULL,              -- 'summary' | 'decision' | 'blocker' | 'context_carry'
    content     TEXT NOT NULL,              -- Markdown text
    session_key TEXT,                       -- Which session produced this
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pmem_project ON project_memory(project_id, created_at);

CREATE TABLE IF NOT EXISTS project_events (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id),
    event_type  TEXT NOT NULL,              -- 'created' | 'session_rotated' | 'task_completed' | 'archived' | 'resumed' | 'message'
    actor_id    TEXT NOT NULL,              -- Slack user ID or 'system'
    summary     TEXT NOT NULL DEFAULT '',
    metadata    TEXT,                       -- JSON blob for structured data
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pevt_project ON project_events(project_id, created_at);

-- Add project_id column to existing tasks table
ALTER TABLE tasks ADD COLUMN project_id TEXT REFERENCES projects(id);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
```

### Schema Notes

- **`slug`** is the human-friendly identifier used in Slack commands. Auto-generated from name, must be unique.
- **`project_memory`** is the critical table — this is how context survives session rotation. Types:
  - `context_carry`: Full summary produced when a session is about to die. Injected into the next session.
  - `decision`: Explicit decisions recorded during the project ("we chose etcd over Consul").
  - `blocker`: Known blockers or open questions.
  - `summary`: Periodic progress summaries (can be triggered manually or auto-generated).
- **`project_events`** is the audit trail / activity log.

---

## 5. API Design (Management API)

All endpoints are under `/api/v1/projects`. Auth follows existing Management API patterns (configurable: none / API key).

### Endpoints

#### `POST /api/v1/projects`
Create a new project.

```json
// Request
{
    "name": "Leader Election Refactor",
    "description": "Migrate from custom leader election to etcd-based leases",
    "repo_url": "https://github.com/p-blackswan/infra-services",
    "owner_id": "U0123ABC"
}

// Response 201
{
    "id": "proj_abc123",
    "slug": "leader-election-refactor",
    "name": "Leader Election Refactor",
    "status": "active",
    "active_session": "agent:main:project-leader-election-refactor",
    "created_at": "2026-02-23T10:00:00Z"
}
```

#### `GET /api/v1/projects`
List projects. Query params: `?status=active&owner_id=U0123ABC&limit=20&offset=0`

#### `GET /api/v1/projects/:slug`
Get project details including recent memory and events.

```json
{
    "id": "proj_abc123",
    "slug": "leader-election-refactor",
    "name": "Leader Election Refactor",
    "status": "active",
    "active_session": "agent:main:project-leader-election-refactor-v3",
    "session_version": 3,
    "recent_memory": [...],
    "recent_events": [...],
    "task_count": 12,
    "created_at": "2026-02-23T10:00:00Z",
    "updated_at": "2026-02-24T15:30:00Z"
}
```

#### `PATCH /api/v1/projects/:slug`
Update project metadata (name, description, repo_url, status).

#### `POST /api/v1/projects/:slug/message`
Send a message to the project's OpenClaw session. This is the primary programmatic interface.

```json
// Request
{
    "message": "What's the status of the etcd integration?",
    "caller_id": "U0123ABC",
    "response_channel": "C0AH79R9X24",
    "response_thread": "1234567890.123456"
}

// Response 202
{
    "task_id": "task_xyz",
    "session": "agent:main:project-leader-election-refactor-v3",
    "status": "pending"
}
```

#### `POST /api/v1/projects/:slug/memory`
Add an explicit memory entry (decision, blocker, note).

```json
{
    "type": "decision",
    "content": "We're going with etcd 3.5 with TLS enabled"
}
```

#### `GET /api/v1/projects/:slug/memory`
Get all memory entries for context review.

#### `GET /api/v1/projects/:slug/events`
Get project event log with pagination.

#### `POST /api/v1/projects/:slug/archive`
Archive a project. Sets status to `archived`, records event.

#### `POST /api/v1/projects/:slug/resume`
Reactivate an archived project. Creates a new session with full context preamble.

#### `DELETE /api/v1/projects/:slug`
Hard delete (admin only). Removes project, memory, events, unlinks tasks.

---

## 6. Slack UX

### Command Design Principles

1. **Short commands** — everything works from a phone keyboard
2. **Slug-based** — `leader-election` not UUIDs
3. **Implicit context** — if you're in a project thread, commands target that project
4. **Turkish-friendly** — support both `proje` and `project`

### Commands

**The golden rule: `@kog <slug>` is all you need.** If `leader-election` is a known project slug, `@kog leader-election` opens/continues it. No `continue`, no `project` prefix. One word from your phone.

| Command | Action |
|---------|--------|
| `@kog leader-election` | **Short route** — continue project (new thread with context) |
| `@kog leader-election what's the etcd status?` | **Short route + message** — route question to project session |
| `@kog projects` or `@kog projeler` | List active projects (dashboard) |
| `@kog new project "Leader Election" --repo github.com/x/y` | Create project |
| `@kog decide leader-election we're using etcd 3.5` | Record a decision |
| `@kog blocker leader-election waiting on SRE for TLS certs` | Record a blocker |
| `@kog archive leader-election` | Archive project |
| `@kog resume leader-election` | Reactivate archived project |
| `@kog handoff leader-election @sre-lead` | Transfer ownership *(P2)* |

#### Short Routing Resolution Order

When `@kog <word> [rest...]` arrives, the router resolves in this order:

1. **Exact slug match** → route to project (`@kog leader-election` → project)
2. **Built-in command** → execute command (`@kog projects`, `@kog new project ...`)
3. **No match** → treat as general message (existing behavior, thread-based session)

This means project slugs are **first-class citizens** in the command namespace. Reserved words (`projects`, `new`, `decide`, `blocker`, `archive`, `resume`, `help`, `handoff`) cannot be used as slugs.

### Interaction Flows

#### Creating a Project
```
User:  @kog new project "Leader Election Refactor" --repo github.com/p-blackswan/infra-services
Kog:   ✅ Project created: leader-election-refactor
       📋 Description: (none — tell me about it!)
       🔗 Repo: github.com/p-blackswan/infra-services
       
       Start working: `@kog continue leader-election-refactor`

User:  @kog continue leader-election-refactor
Kog:   🚀 *Leader Election Refactor* — Session started
       [new thread]
       
       I'm ready to work on this project. I have:
       - Repo: github.com/p-blackswan/infra-services
       - No prior context yet
       
       What would you like to work on?
```

#### Continuing a Project (Days Later — Short Route)
```
User:  @kog leader-election
Kog:   🔄 *Leader Election Refactor* — Resuming (Session v3)
       [new thread]
       
       Here's where we left off:
       📌 Decisions: Using etcd 3.5 with TLS
       🚧 Blockers: Waiting on SRE for TLS certs
       📊 Last session: Implemented lease renewal logic, 
          PR #47 opened for review
       
       What's next?
```

#### In-Thread Context (No Slug Needed)
Once a project thread is active, all messages in that thread route to the project session automatically — no need to prefix with the project name.

```
[In leader-election-refactor thread]
User:  Can you check if PR #47 has been reviewed?
Kog:   [checks GitHub] PR #47 has 1 approval from @sre-lead, 
       no requested changes. Ready to merge.
```

#### Project Dashboard (`@kog projects`)

Mobile-first. No tables. Scannable in 3 seconds on a phone screen.

```
User:  @kog projects
Kog:   📂 *3 Active Projects*
       
       🟢 **leader-election** — 2h ago
       ├ 🚧 1 blocker · 📌 3 decisions · 12 tasks
       └ Last: "Implemented lease renewal, PR #47 open"
       
       🟡 **ci-pipeline-v2** — 1d ago
       ├ 📌 2 decisions · 8 tasks
       └ Last: "Migrated to GitHub Actions, testing"
       
       🔵 **monitoring-revamp** — 5d ago
       ├ 📌 1 decision · 3 tasks
       └ Last: "Evaluated Grafana vs Datadog"
       
       `@kog <slug>` to continue
```

Status indicators: 🟢 active today · 🟡 active this week · 🔵 >3 days · ⏸️ paused · 📦 archived

### Thread-Project Binding

When `@kog continue <slug>` creates a new thread, the bridge stores the mapping:

```
thread_sessions: channel + threadTS → project slug
```

All subsequent messages in that thread are routed to the project's OpenClaw session (not a thread-based session). This means:
- The `sessionKey` sent to the bridge is `agent:main:project-{slug}` (or current versioned key)
- Multiple Slack threads can feed into the same project session
- The project accumulates context from all threads

---

## 7. OpenClaw Session Management

### Session Lifecycle

```
                    ┌─────────┐
                    │ Created  │ ◄── new project
                    └────┬────┘
                         │ first message
                    ┌────▼────┐
                    │ Active   │ ◄── normal operation
                    └────┬────┘
                         │ token limit / error
                    ┌────▼──────────┐
                    │ Rotating      │ ── summarize → store memory → new session
                    └────┬──────────┘
                         │
                    ┌────▼────┐
                    │ Active   │ (v+1, with context preamble)
                    └────┬────┘
                         │ archive
                    ┌────▼────┐
                    │ Dormant  │ ── session may be cleaned up by OpenClaw
                    └─────────┘
```

### Context Preamble

When a new session is created (or rotated), the Session Manager injects a **context preamble** as the first message. This is NOT sent by the user — it's a system-level context injection.

```markdown
[SYSTEM: Project Context — DO NOT echo this back to the user]

# Project: Leader Election Refactor
- **Slug:** leader-election-refactor  
- **Repo:** https://github.com/p-blackswan/infra-services
- **Session:** v3 (rotated from v2 due to context limits)
- **Created:** 2026-02-20

## Description
Migrate from custom leader election to etcd-based leases for all 
platform services. Target: Q1 2026.

## Decisions
1. [2026-02-20] Using etcd 3.5 with TLS — chosen over Consul for simplicity
2. [2026-02-21] Lease TTL: 15s with 5s renewal interval
3. [2026-02-22] Graceful failover: 30s grace period before force-acquire

## Blockers
1. [2026-02-21] Waiting on SRE team for TLS certs (assigned to @sre-lead)

## Previous Session Summary (v2)
Implemented lease renewal logic in `pkg/leader/etcd.go`. 
Opened PR #47 with full test coverage. Integration tests passing 
against local etcd cluster. Next: wire into service mesh sidecar.

---
Continue from here. The user will send messages in this thread.
```

### Context Compaction Strategy

Memory segments are bounded to prevent the preamble from growing unbounded:

| Type | Max entries | Strategy when exceeded |
|------|-------------|----------------------|
| `decision` | 20 | Oldest decisions are summarized into a single "early decisions" block |
| `blocker` | 10 | Resolved blockers are auto-removed (Kog marks them resolved) |
| `context_carry` | 3 | Only keep the last 3 session summaries; older ones are merged |
| `summary` | 5 | Rolling window |

**Total preamble budget: ~4000 tokens.** If the preamble exceeds this, the Session Manager compacts by asking Kog (in a throwaway session) to summarize the oldest entries.

### Token Limit Detection

The bridge detects token limit errors from OpenClaw responses:
- Error codes containing `context_length_exceeded` or similar
- HTTP 429 / error responses from the gateway

On detection:
1. Send a final message to the dying session: `"Summarize this entire project session: key decisions, current state, blockers, and next steps. Be comprehensive — this will be the only context for the next session."`
2. Wait for response (with 60s timeout)
3. Store response as `context_carry` memory entry
4. Rotate session

---

## 8. Bridge Integration

### Modified Routing

The bridge currently computes session keys from Slack channel/thread:

```go
sessionKey = fmt.Sprintf("agent:main:slack-%s-%s", channelID, threadTS)
```

With Projects, the **Project Router** (new component) intercepts messages before the bridge and determines routing:

```go
// ProjectRouter sits between Slack handler and Bridge
type ProjectRouter struct {
    store    *store.Store
    bridge   MessageForwarder  // the actual WS/CLI bridge
    sessions *SessionManager
}

func (r *ProjectRouter) HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string) {
    // 1. Check if this thread is bound to a project
    if proj := r.store.GetProjectByThread(channelID, threadTS); proj != nil {
        sessionKey := proj.ActiveSession
        // Route to project session
        r.bridge.HandleMessageWithSession(ctx, channelID, userID, text, threadTS, messageTS, sessionKey)
        return
    }
    
    // 2. Check if this is a project command ("@kog continue leader-election")
    if cmd := parseProjectCommand(text); cmd != nil {
        r.handleProjectCommand(ctx, cmd, channelID, userID, threadTS, messageTS)
        return
    }
    
    // 3. Default: existing behavior (thread-based sessions)
    r.bridge.HandleMessage(ctx, channelID, userID, text, threadTS, messageTS)
}
```

### Bridge Interface Change

The bridge needs a new method that accepts an explicit session key:

```go
// MessageForwarder interface (existing)
type MessageForwarder interface {
    HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string)
}

// Extended interface for project routing
type SessionAwareForwarder interface {
    MessageForwarder
    HandleMessageWithSession(ctx context.Context, channelID, userID, text, threadTS, messageTS, sessionKey string)
}
```

The WSBridge and Bridge both implement `SessionAwareForwarder`. The `HandleMessageWithSession` variant skips session key computation and uses the provided key directly.

### Thread-Project Binding Table

Reuse existing `thread_sessions` table with a new column:

```sql
ALTER TABLE thread_sessions ADD COLUMN project_id TEXT REFERENCES projects(id);
CREATE INDEX IF NOT EXISTS idx_thread_project ON thread_sessions(project_id);
```

When `@kog continue <slug>` creates a new Slack thread, the router:
1. Posts the initial message (project status summary)
2. Records the thread → project binding in `thread_sessions`
3. All subsequent messages in that thread route to the project's session

---

## 9. Cross-Project Awareness (P1)

A critical differentiator: Kog should be able to **cross-reference between projects**. When working on `leader-election`, Kog should know that `monitoring-revamp` exists and may have relevant decisions.

### Design: Project Index

A lightweight **project index** is injected into every project session's context preamble. This is NOT the full memory of other projects — it's a compact index (~500 tokens) that lets Kog know what exists:

```markdown
## Other Active Projects (read-only index)
- **ci-pipeline-v2**: GitHub Actions migration. 2 decisions, 8 tasks.
- **monitoring-revamp**: Evaluating Grafana vs Datadog. 1 decision, 3 tasks.

To reference another project's details, ask: "What did we decide about X in <project>?"
```

### Cross-Reference Flow

When Kog needs details from another project:

1. Kog recognizes the cross-reference need from the index
2. The agent (via tool/function call) queries `GET /api/v1/projects/:slug/memory` for the target project
3. Relevant memory entries are injected into the current conversation
4. Decision is optionally recorded as a cross-reference event in both projects

### Implementation

```go
// ProjectIndex generates a compact summary of all active projects (excluding current)
func (m *SessionManager) BuildProjectIndex(excludeSlug string) string {
    projects := m.store.ListActiveProjects()
    var lines []string
    for _, p := range projects {
        if p.Slug == excludeSlug { continue }
        stats := m.store.GetProjectStats(p.ID)
        lines = append(lines, fmt.Sprintf("- **%s**: %s. %d decisions, %d tasks.",
            p.Slug, truncate(p.Description, 60), stats.Decisions, stats.Tasks))
    }
    return strings.Join(lines, "\n")
}
```

The project index is included in every context preamble (Section 7) and refreshed on each session rotation.

### `project_cross_refs` Table (Optional, Phase 2+)

```sql
CREATE TABLE IF NOT EXISTS project_cross_refs (
    id          TEXT PRIMARY KEY,
    source_project_id TEXT NOT NULL REFERENCES projects(id),
    target_project_id TEXT NOT NULL REFERENCES projects(id),
    context     TEXT NOT NULL,  -- Why this cross-reference was made
    created_at  INTEGER NOT NULL
);
```

---

## 10. Task-Project Association

### Existing Task Flow (Unchanged)

Tasks submitted without a `project_id` work exactly as before. This is backward compatible.

### Project-Scoped Tasks

```json
// POST /api/v1/tasks
{
    "type": "execute",
    "params": {"command": "check PR status"},
    "project_id": "proj_abc123"
}
```

When a task has `project_id`:
1. Task engine sets the session key to the project's `active_session`
2. Task result is recorded as a `project_event` (type: `task_completed`)
3. Task appears in project history

### Implicit Association

When a message routes through a project session (via Slack thread binding), any tasks spawned from that execution are automatically associated with the project. The `TaskIDContextKey` in context is extended:

```go
type ProjectContextKey string
const ProjectIDContextKey ProjectContextKey = "project_id"
```

---

## 11. Edge Cases & Error Handling

| Scenario | Handling |
|----------|---------|
| **Duplicate slug** | Return 409. Suggest appending number: `leader-election-2` |
| **Project not found** | Slack: "Project `foo` not found. Run `@kog projects` to see active projects." |
| **Session rotation fails** (Kog can't summarize) | Store whatever partial response we got. Create new session with raw project metadata only. Log warning. |
| **Multiple users in same project** | Supported. Messages are serialized through the single project session. Bridge semaphore prevents races. |
| **Concurrent messages to same project** | Queue at bridge level (existing semaphore). Project sessions are single-writer. |
| **Archived project receives message** | "Project `foo` is archived. Run `@kog resume foo` to reactivate." |
| **OpenClaw gateway down** | Existing error handling applies. Project state is preserved in SQLite. |
| **Agent restart** | Projects and memory survive (SQLite). Active sessions may need re-establishment on first message (OpenClaw sessions are server-side persistent). |
| **Slug collision with command** | Reserved words: `new`, `list`, `projects`, `help`. Validation rejects these as slugs. |
| **Very old project resumed** | Full context preamble injected. If preamble exceeds budget, compact first. |
| **User deletes Slack thread** | Project is unaffected. User can `@kog <slug>` in a new thread. |
| **Auto-paused project mentioned** | `@kog <slug>` auto-resumes with full context. No manual `resume` needed. *(P2)* |
| **Handoff to non-existent user** | Validate Slack user ID. Return error if invalid. *(P2)* |
| **Cross-project reference to archived project** | Kog can query it explicitly but it's not in the index. Response notes it's archived. |

---

## 12. Security Model

### Access Control

**Phase 1 (MVP):** All authenticated users can access all projects. The `owner_id` is informational.

**Phase 2:** Role-based access:
- **Owner:** Full control (CRUD, archive, delete)
- **Member:** Can send messages, view history
- **Viewer:** Read-only access to events and memory

### Isolation

- Each project has its own OpenClaw session — no cross-project context leakage
- Project memory is stored per-project in SQLite with foreign key constraints
- Management API respects existing auth config (none / API key / future OIDC)

### Audit

All project operations are recorded in:
1. `project_events` table (project-specific audit)
2. `audit_log` table (system-wide audit, existing)

---

## 13. Migration Plan

### From Current System

No breaking changes. The migration is additive:

1. **Schema migration v2** adds new tables and the `project_id` column to `tasks`
2. **Existing threads** continue to work as before (thread-based sessions)
3. **Project Router** wraps the existing bridge — non-project messages pass through unchanged
4. **Management API** adds new `/projects` endpoints alongside existing `/tasks`

### Data Migration

None required. Existing tasks remain un-associated (`project_id = NULL`). Users can retroactively link tasks if needed (Phase 2).

---

## 14. Implementation Phases

### ── P1: MUST HAVE (Phases 1-2) ──

### Phase 1: Core — Storage, Memory & Short Routing (2 weeks)

**Goal:** Projects exist, have persistent memory, and `@kog <slug>` works.

- [ ] SQLite migration v2 (new tables: `projects`, `project_memory`, `project_events`)
- [ ] `internal/project/store.go` — CRUD for projects, memory, events
- [ ] `internal/project/manager.go` — Session Manager (create session, context preamble injection)
- [ ] `internal/project/router.go` — **Short routing**: `@kog <slug>` resolves to project
- [ ] `internal/project/commands.go` — Command parser with slug-first resolution order
- [ ] Modify WSBridge/Bridge: `HandleMessageWithSession` for explicit session routing
- [ ] Thread-project binding in `thread_sessions` (add `project_id` column)
- [ ] Wire Project Router into `main.go` (between Slack handler and bridge)
- [ ] Management API endpoints: create, list, get, archive, resume, delete, message, memory
- [ ] `@kog decide <slug> ...` and `@kog blocker <slug> ...` memory commands
- [ ] Unit + integration tests

**Deliverable:** `@kog leader-election` opens a project thread. Decisions and blockers persist. `@kog leader-election what's the status?` routes to the project session.

### Phase 2: Dashboard, Cross-Project & Session Survival (2 weeks)

**Goal:** Mobile dashboard, cross-project awareness, context survives session rotation.

- [ ] **Project Dashboard**: `@kog projects` with status indicators (🟢🟡🔵), last activity, task counts, last summary line
- [ ] **Cross-project index**: compact index injected into every preamble (~500 tokens)
- [ ] Cross-reference query flow (Kog asks for another project's memory via API)
- [ ] **Session rotation**: token limit detection → summarize → store `context_carry` → new session with preamble
- [ ] Context compaction logic (bounded memory segments, ~4000 token preamble budget)
- [ ] Task-project association (`project_id` on tasks, implicit via context propagation)
- [ ] Project event recording for task completions
- [ ] `@kog <slug>` detailed status view (decisions, blockers, recent activity)

**Deliverable:** Full P1 feature set. Mobile-first dashboard. Projects survive indefinitely. Cross-project references work.

### ── P2: NICE TO HAVE (Phase 3+) ──

### Phase 3: Handoff, Auto-lifecycle & Polish (1-2 weeks)

- [ ] **Project Handoff**: `@kog handoff <slug> @user` — transfers ownership, notifies new owner, records event
- [ ] **Auto-pause**: Cron job checks projects inactive >7 days → status `paused`, event recorded
- [ ] **Auto-resume**: If `@kog <slug>` targets a paused project → auto-resume with full context preamble, no manual `resume` needed
- [ ] `project_cross_refs` table for persistent cross-reference tracking
- [ ] Project templates (clone structure from existing project)
- [ ] Editable/deletable memory entries via Slack (`@kog forget <slug> decision 3`)
- [ ] Documentation, runbook, operational playbook

**Deliverable:** Lifecycle automation, team collaboration features.

---

## 15. Open Questions

| # | Question | Proposed Answer | Status |
|---|----------|----------------|--------|
| 1 | Should projects support multiple simultaneous sessions (parallelism)? | No. 1:1 keeps it simple. Parallelism via tasks within a session. | **Decided** |
| 2 | Should `@kog <slug>` reuse existing Slack threads or always create new ones? | Always new thread. Old threads become read-only history. | **Decided** |
| 3 | How do we handle the very first context preamble before any memory exists? | Inject project metadata only (name, description, repo). Kog figures out the rest. | **Decided** |
| 4 | Should there be a project-level `.md` file in the workspace? | Yes — Phase 2+. `workspace/projects/{slug}/README.md` as persistent memory Kog can read/write. | Open |
| 5 | Max projects per user? | No hard limit. Soft limit of 20 active projects (UI warning). | Open |
| 6 | Should the context preamble be a system message or user message? | System-level injection (first message in session with `[SYSTEM:]` prefix). OpenClaw treats it as context. | **Decided** |
| 7 | How does `@kog <slug> <question>` differ from bare `@kog <slug>`? | `@kog <slug>` alone opens an interactive thread. `@kog <slug> <msg>` routes the message to the project and replies inline (no new thread). | **Decided** |
| 8 | Should we support project templates? | Phase 3+. P2. | Deferred |
| 9 | What happens if two users message the same project simultaneously? | Both threads feed into the same session. Messages are serialized. Responses go to the thread that sent them. | **Decided** |
| 10 | Should project memory be editable/deletable by users? | Phase 3 via Slack (`@kog forget <slug> decision 3`). API in Phase 2. | Open |
| 11 | Auto-pause threshold — 7 days or configurable? | Default 7d, configurable via `PROJECT_AUTO_PAUSE_DAYS` env var. 0 = disabled. | Open |
| 12 | Handoff: does the new owner get a DM notification? | Yes. Kog DMs the new owner with project summary and a `@kog <slug>` prompt to continue. | **Decided** |
| 13 | Cross-project index: should it include archived projects? | No. Only active + paused. Archived projects can be queried explicitly. | **Decided** |

---

## Appendix A: New Go Packages

```
internal/project/
├── store.go        // SQLite operations for projects, memory, events
├── manager.go      // Session lifecycle, rotation, preamble generation
├── router.go       // Slack message routing (project vs thread-based)
├── commands.go     // Slack command parsing
└── types.go        // Project, Memory, Event structs
```

## Appendix B: Config Additions

```env
# Project feature flag (default: true once deployed)
PROJECTS_ENABLED=true

# Context preamble token budget
PROJECT_CONTEXT_BUDGET=4000

# Max memory entries before compaction
PROJECT_MAX_DECISIONS=20
PROJECT_MAX_BLOCKERS=10
PROJECT_MAX_SUMMARIES=5

# Auto-pause after N days of inactivity (0 = disabled) [P2]
PROJECT_AUTO_PAUSE_DAYS=7
```
