# Platform Agent — Resilience Plan

## Problem

Agent tamamen in-memory çalışıyor. Restart/crash sonrası:
- `pendingApprovals` map → kayıp (approval butonları çalışmaz)
- `SessionContextStore` → kayıp (response routing bozulur)
- `chatListeners` (WS bridge) → kayıp (streaming response'lar kaybolur)
- Task queue → kayıp (çalışan/bekleyen task'lar yok olur)
- Slack thread context → kayıp (hangi thread'de ne konuşuldu bilinmez)

## Senaryolar

### S1: Agent restart (deploy, crash, OOM kill)
- Çalışan task'lar yarıda kesilir
- Approval bekleyen task'lar kaybolur → kullanıcı butona basar, hiçbir şey olmaz
- WS bridge kopmuş, yeni bağlantı kurar ama eski listener'lar yok

### S2: Thread history kaybı
- Agent restart sonrası Slack'ten mesaj gelir, mevcut bir thread'e
- Agent bu thread'in geçmişini bilmiyor
- Kog-2'ye context'siz mesaj gider, anlamsız cevap döner

### S3: Kog-2 session var ama agent bilmiyor
- Agent restart oldu, Kog-2'deki session hâlâ aktif
- Yeni mesaj gelince agent yeni session açar, eski context kaybolur

### S4: WS bağlantı kopması (network blip)
- WebSocket disconnects mid-stream
- Streaming response yarıda kalır
- Kullanıcı cevap alamaz

---

## Çözüm Planı

### Phase 1: Stateless Recovery (Hızlı, ~1 gün)

#### 1.1 Slack Thread History Read
Yeni task tipi: `slack.read-thread`

```go
// Slack conversations.replies API ile thread geçmişini oku
type SlackReadThreadParams struct {
    Channel  string `json:"channel"`
    ThreadTS string `json:"thread_ts"`
    Limit    int    `json:"limit"` // default 20, max 100
}

type SlackReadThreadResult struct {
    Messages []SlackMessage `json:"messages"`
    HasMore  bool           `json:"has_more"`
}
```

- `classRead` — approval gerekmez
- Bridge startup'ta veya ilk mesajda otomatik çağrılabilir
- Kog-2 thread geçmişini prompt'a inject edebilir

#### 1.2 WS Auto-Reconnect
```go
type WSClient struct {
    // ...
    reconnectBackoff  time.Duration // 1s → 2s → 4s → ... → 30s max
    maxReconnectDelay time.Duration // 30s
    reconnecting      atomic.Bool
}

func (ws *WSClient) readLoop() {
    for {
        _, msg, err := ws.conn.ReadMessage()
        if err != nil {
            ws.scheduleReconnect()
            return
        }
        // ...
    }
}

func (ws *WSClient) scheduleReconnect() {
    if !ws.reconnecting.CompareAndSwap(false, true) {
        return // already reconnecting
    }
    go func() {
        defer ws.reconnecting.Store(false)
        for attempt := 0; ; attempt++ {
            delay := min(ws.reconnectBackoff * (1 << attempt), ws.maxReconnectDelay)
            time.Sleep(delay)
            if err := ws.connect(); err == nil {
                ws.logger.Info().Msg("WS reconnected")
                return
            }
        }
    }()
}
```

#### 1.3 Pending Approval Recovery
Agent restart sonrası Slack'teki approval butonları hâlâ durur. Kullanıcı basar → callback gelir → agent bilmiyor.

**Çözüm:** Approval callback'te `request_id` bilinmiyorsa:
1. Slack thread'den orijinal mesajı oku (button payload'da task bilgisi var)
2. Task'ı yeniden oluştur ve re-queue et
3. Kullanıcıya "⚠️ Agent restart oldu, task'ı tekrar çalıştırıyorum" mesajı at

```go
func (h *ApprovalHandler) handleUnknownApproval(requestID, action string, callback slack.InteractionCallback) {
    // Button value'dan task bilgisini parse et
    // Yeni task oluştur, approval'ı otomatik ver, çalıştır
}
```

---

### Phase 2: Persistent State — SQLite (~2-3 gün)

#### 2.1 Schema

```sql
-- Migration 001: Core tables
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending, running, awaiting_approval, completed, failed, cancelled
    command         TEXT NOT NULL,                     -- e.g. "github.exec"
    params          TEXT NOT NULL,                     -- JSON blob
    caller_id       TEXT NOT NULL DEFAULT '',
    response_channel TEXT,
    response_thread  TEXT,
    result          TEXT,                              -- JSON blob (nullable)
    error           TEXT,                              -- error message (nullable)
    created_at      INTEGER NOT NULL,                  -- unix ms
    updated_at      INTEGER NOT NULL,                  -- unix ms
    completed_at    INTEGER                            -- unix ms (nullable)
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_created ON tasks(created_at);

CREATE TABLE pending_approvals (
    request_id  TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks(id),
    caller_id   TEXT NOT NULL,
    permission  TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    channel_id  TEXT,
    thread_ts   TEXT,
    created_at  INTEGER NOT NULL
);

CREATE TABLE session_contexts (
    session_id  TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    thread_ts   TEXT NOT NULL,
    user_id     TEXT,
    created_at  INTEGER NOT NULL,
    last_used   INTEGER NOT NULL
);

CREATE INDEX idx_session_ctx_channel ON session_contexts(channel, thread_ts);

CREATE TABLE thread_sessions (
    channel         TEXT NOT NULL,
    thread_ts       TEXT NOT NULL,
    session_key     TEXT NOT NULL,     -- "agent:main:slack-C0AGA-1234567.890"
    created_at      INTEGER NOT NULL,
    last_message_at INTEGER NOT NULL,
    PRIMARY KEY (channel, thread_ts)
);

CREATE TABLE dead_letters (
    id              TEXT PRIMARY KEY,
    target_channel  TEXT NOT NULL,
    target_thread   TEXT,
    message         TEXT NOT NULL,
    error           TEXT NOT NULL,
    created_at      INTEGER NOT NULL,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    next_retry_at   INTEGER,          -- unix ms, NULL = give up
    resolved_at     INTEGER           -- unix ms, NULL = unresolved
);

CREATE INDEX idx_dlq_unresolved ON dead_letters(next_retry_at) WHERE resolved_at IS NULL;

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT,
    result      TEXT NOT NULL,        -- completed, denied, error, pending_approval
    details     TEXT,
    created_at  INTEGER NOT NULL
);

CREATE INDEX idx_audit_created ON audit_log(created_at);

-- Metadata table for schema version + retention state
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO meta(key, value) VALUES ('schema_version', '1');
```

#### 2.2 Retention Policy

**Prensip:** Hot data hızlı erişim, cold data temizlenir, ama Slack thread'den her zaman rebuild edilebilir.

| Tablo | Hot | Warm | Cold/Purge | Rebuild? |
|-------|-----|------|------------|----------|
| `tasks` | < 24h: tam | 1-7 gün: result truncate (>10KB → hash ref) | > 7 gün: sil | ✅ Slack history'den |
| `pending_approvals` | < 1h: aktif | > 1h: expired, sil | — | ✅ Button payload'dan |
| `session_contexts` | < 24h | > 24h: sil | — | ✅ Slack message'dan |
| `thread_sessions` | < 7 gün | > 7 gün: sil | — | ✅ Slack thread ilk mesajdan |
| `dead_letters` | unresolved | resolved > 24h: sil | — | Hayır (fire & forget) |
| `audit_log` | < 30 gün | > 30 gün: sil | — | Hayır |

**Retention goroutine:**
```go
func (s *Store) StartRetention(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.cleanupTasks(7 * 24 * time.Hour)
            s.cleanupApprovals(1 * time.Hour)
            s.cleanupSessionContexts(24 * time.Hour)
            s.cleanupThreadSessions(7 * 24 * time.Hour)
            s.cleanupDeadLetters(24 * time.Hour)
            s.cleanupAuditLog(30 * 24 * time.Hour)
            s.vacuum() // SQLite VACUUM (weekly, track in meta)
        }
    }
}
```

#### 2.3 Rebuild from Slack History

Agent restart sonrası thread_sessions tablosu boşsa (veya DB kayıpsa):

```go
// rebuildFromThread reconstructs session state from Slack thread history.
// Called when agent receives a message for an unknown thread.
func (b *Bridge) rebuildFromThread(channel, threadTS string) (*ThreadSession, error) {
    // 1. Slack conversations.replies ile thread'i oku (son 50 mesaj)
    messages, err := b.slack.GetConversationReplies(channel, threadTS, 50)
    if err != nil {
        return nil, err
    }

    // 2. İlk mesajı bul — session'ın orijinal context'i
    // Bot mesajlarından session_key pattern'ı çıkar (varsa)
    sessionKey := b.detectSessionKey(messages)

    // 3. Yoksa yeni session oluştur, thread history'yi context olarak inject et
    if sessionKey == "" {
        sessionKey = b.generateSessionKey(channel, threadTS)
    }

    // 4. Thread'deki insan mesajlarını summary'ye çevir
    summary := b.summarizeThread(messages)

    // 5. Kog-2'ye context inject et:
    //    "[thread_recovery] Bu thread'in önceki context'i:
    //     - User: X söyledi
    //     - Bot: Y cevap verdi
    //     - User: Z istedi"
    b.sendContextRecovery(sessionKey, summary)

    // 6. DB'ye kaydet
    ts := &ThreadSession{
        Channel:    channel,
        ThreadTS:   threadTS,
        SessionKey: sessionKey,
    }
    b.store.SaveThreadSession(ts)
    return ts, nil
}
```

**Rebuild tetikleme noktaları:**
1. **Mesaj geldi + thread bilinmiyor** → otomatik rebuild
2. **Approval callback + approval bilinmiyor** → thread'den rebuild + re-approve
3. **Explicit rebuild** → `POST /api/v1/rebuild-thread` endpoint (debug/admin)
4. **Startup scan** → opsiyonel, aktif thread'leri Slack'ten çek (scope: `channels:history`)

#### 2.4 DB Boyut Kontrolü

```go
// maxDBSize controls the SQLite file size limit.
// If exceeded, aggressive retention kicks in.
const maxDBSize = 50 * 1024 * 1024 // 50MB

func (s *Store) checkDBSize() {
    var size int64
    s.db.QueryRow("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&size)
    if size > maxDBSize {
        s.logger.Warn().Int64("size_mb", size/1024/1024).Msg("DB size exceeded, running aggressive cleanup")
        s.cleanupTasks(24 * time.Hour)      // 7 gün → 1 gün
        s.cleanupAuditLog(7 * 24 * time.Hour) // 30 gün → 7 gün
        s.vacuum()
    }
}
```

#### 2.5 Store Interface

```go
// Store is the persistence layer interface.
// All methods are thread-safe.
type Store interface {
    // Tasks
    SaveTask(task *Task) error
    GetTask(id string) (*Task, error)
    UpdateTaskStatus(id, status string) error
    ListTasks(filter TaskFilter) ([]*Task, error)
    CompleteTask(id string, result json.RawMessage, err error) error

    // Approvals
    SaveApproval(approval *PendingApproval) error
    GetApproval(requestID string) (*PendingApproval, error)
    DeleteApproval(requestID string) error
    ListExpiredApprovals(maxAge time.Duration) ([]*PendingApproval, error)

    // Session Contexts
    SaveSessionContext(ctx *SessionContext) error
    GetSessionContext(sessionID string) (*SessionContext, error)
    GetSessionContextByThread(channel, threadTS string) (*SessionContext, error)

    // Thread Sessions
    SaveThreadSession(ts *ThreadSession) error
    GetThreadSession(channel, threadTS string) (*ThreadSession, error)
    TouchThreadSession(channel, threadTS string) error

    // Dead Letters
    SaveDeadLetter(dl *DeadLetter) error
    ListRetryableDeadLetters(limit int) ([]*DeadLetter, error)
    ResolveDeadLetter(id string) error

    // Audit
    RecordAudit(entry *AuditEntry) error

    // Maintenance
    RunRetention(ctx context.Context) error
    Close() error
}
```

---

### Phase 3: Graceful Degradation (İleri, ~1 gün)

#### 3.1 Circuit Breaker Pattern
GitHub API / Slack API / OpenClaw gateway çökerse → tüm agent çökmemeli.

```go
type CircuitBreaker struct {
    name        string
    failures    atomic.Int32
    threshold   int32         // 5 consecutive failures
    cooldown    time.Duration // 30s
    lastFailure atomic.Int64
    state       atomic.Int32  // closed=0, open=1, half-open=2
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    if cb.state.Load() == 1 { // open
        if time.Since(time.UnixMilli(cb.lastFailure.Load())) < cb.cooldown {
            return fmt.Errorf("circuit %s is open", cb.name)
        }
        cb.state.Store(2) // half-open
    }
    err := fn()
    if err != nil {
        if cb.failures.Add(1) >= cb.threshold {
            cb.state.Store(1) // open
            cb.lastFailure.Store(time.Now().UnixMilli())
        }
        return err
    }
    cb.failures.Store(0)
    cb.state.Store(0) // closed
    return nil
}
```

Her integration için ayrı breaker:
- `github` → GitHub API down ise git operasyonları fail, Slack hâlâ çalışır
- `openclaw` → Gateway down ise bridge fail, approval flow hâlâ çalışır
- `slack` → Slack down ise → DLQ'ya at

#### 3.2 Health Probe Enhancement
```json
{
  "status": "degraded",
  "checks": {
    "github":     {"status": "up", "latency_ms": 120, "orgs": ["p-blackswan", "p-agent-test"]},
    "slack":      {"status": "up", "latency_ms": 45},
    "openclaw_ws": {"status": "down", "error": "connection refused", "fallback": "cli"},
    "sqlite":     {"status": "up", "size_mb": 12, "tasks": 142},
    "task_queue":  {"pending": 2, "running": 1, "awaiting_approval": 0}
  },
  "uptime_seconds": 3600,
  "version": "v0.5.0"
}
```

---

## Startup Recovery Sequence

```
Agent Start
    │
    ├─ 1. Open SQLite (or create)
    │     └─ Run migrations if needed
    │
    ├─ 2. Recovery scan
    │     ├─ tasks WHERE status='running' → SET status='failed', error='agent restart'
    │     ├─ tasks WHERE status='pending' → Re-enqueue
    │     ├─ tasks WHERE status='awaiting_approval' → Reload into pendingApprovals map
    │     └─ Log recovered counts
    │
    ├─ 3. Start integrations
    │     ├─ GitHub MultiClient (lazy, no recovery needed)
    │     ├─ Slack Socket Mode (reconnects automatically)
    │     └─ WS Bridge (auto-reconnect with backoff)
    │
    ├─ 4. Start background goroutines
    │     ├─ Retention cleaner (hourly)
    │     ├─ Dead letter retry (5 min)
    │     └─ Health probe updater (30s)
    │
    └─ 5. Ready — accept messages
```

## Rebuild Decision Tree

```
Message arrives for thread T
    │
    ├─ thread_sessions has T? 
    │     YES → use existing session_key, continue
    │     NO  ↓
    │
    ├─ Is this a new thread (thread_ts == message_ts)?
    │     YES → create new session, save to DB, continue
    │     NO  ↓ (existing thread, agent doesn't know about it)
    │
    ├─ Slack conversations.replies(T, limit=50)
    │     ├─ Extract bot messages → detect session_key pattern
    │     ├─ Has session_key? → reuse it, verify OpenClaw session exists
    │     └─ No session_key? → create new, inject thread summary as context
    │
    └─ Save to thread_sessions, continue with recovered session
```

---

## Öncelik Sırası

| # | İş | Etki | Efor | Bağımlılık |
|---|-----|------|------|------------|
| 1 | WS auto-reconnect | Yüksek — bağlantı kopunca sessizce düzelir | 2-3 saat | Yok |
| 2 | `slack.read-thread` task type | Yüksek — restart sonrası context recovery | 2-3 saat | Yok |
| 3 | Approval unknown handler | Orta — restart sonrası butonlar çalışır | 2-3 saat | Yok |
| 4 | SQLite store + migrations | Yüksek — tüm state persistent olur | 4-6 saat | Yok |
| 5 | Task persistence | Yüksek — restart'ta task recovery | 3-4 saat | #4 |
| 6 | Session context + thread mapping persist | Yüksek — response routing survive restart | 2-3 saat | #4 |
| 7 | Retention goroutine | Orta — DB büyümesini kontrol | 2 saat | #4 |
| 8 | Rebuild from Slack history | Yüksek — cold start recovery | 3-4 saat | #2, #4 |
| 9 | Startup recovery sequence | Yüksek — restart sonrası otomatik düzelme | 2 saat | #4, #5 |
| 10 | Circuit breaker | Orta — partial failure tolerance | 3 saat | Yok |
| 11 | Dead letter queue | Düşük — edge case recovery | 2 saat | #4 |
| 12 | Health probe enhancement | Düşük — observability | 1 saat | Yok |

## Tavsiye

**Aşama 1 (1 gün):** #1 + #2 + #3 — SQLite'sız bile çoğu sorunu çözer
**Aşama 2 (2 gün):** #4 + #5 + #6 + #7 — Persistent state, retention
**Aşama 3 (1 gün):** #8 + #9 — Rebuild + startup recovery
**Aşama 4 (1 gün):** #10 + #11 + #12 — Graceful degradation

Toplam: ~5 gün. Aşama 1-2 en kritik.

## Teknik Notlar

- **SQLite lib:** `modernc.org/sqlite` (pure Go, CGO gereksiz, cross-compile kolay)
- **DB path:** `AGENT_DB_PATH` env var, default `./agent.db`
- **Docker:** `/data/agent.db` (volume mount)
- **WAL mode:** `PRAGMA journal_mode=WAL` — concurrent read + single write, crash-safe
- **Busy timeout:** `PRAGMA busy_timeout=5000` — 5s bekle, lock contention'da
- **Slack scope:** `channels:history` gerekli (`conversations.replies` için)
- **VACUUM:** Haftada 1, `meta` tablosunda track
- **Max DB size:** 50MB soft limit, aggressive cleanup tetikler
- **Migration:** `internal/store/migrations/` altında numbered SQL files
