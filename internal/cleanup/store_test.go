package cleanup

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	schema := `
	CREATE TABLE session_cleanup (
		id              TEXT PRIMARY KEY,
		session_key     TEXT NOT NULL,
		channel_id      TEXT NOT NULL,
		thread_ts       TEXT NOT NULL,
		status          TEXT NOT NULL DEFAULT 'warned',
		warned_at       INTEGER NOT NULL,
		responded_at    INTEGER,
		expires_at      INTEGER NOT NULL,
		message_ts      TEXT,
		created_at      INTEGER NOT NULL
	);
	CREATE INDEX idx_cleanup_status ON session_cleanup(status);
	CREATE INDEX idx_cleanup_expires ON session_cleanup(expires_at);

	CREATE TABLE thread_sessions (
		channel TEXT NOT NULL,
		thread_ts TEXT NOT NULL,
		session_key TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_message_at INTEGER NOT NULL,
		PRIMARY KEY (channel, thread_ts)
	);

	CREATE TABLE session_contexts (
		session_id TEXT PRIMARY KEY,
		channel TEXT NOT NULL,
		thread_ts TEXT NOT NULL,
		user_id TEXT,
		created_at INTEGER NOT NULL,
		last_used INTEGER NOT NULL
	);

	CREATE TABLE audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		action TEXT NOT NULL,
		resource TEXT,
		result TEXT NOT NULL,
		details TEXT,
		created_at INTEGER NOT NULL
	);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}

func TestSaveAndGetWarning(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)

	err := store.SaveWarning("session-1", "C123", "T456", "msg-ts", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := store.GetWarningBySession("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("expected warning record")
	}
	if rec.SessionKey != "session-1" {
		t.Errorf("got session_key=%s, want session-1", rec.SessionKey)
	}
	if rec.Status != "warned" {
		t.Errorf("got status=%s, want warned", rec.Status)
	}
	if rec.ChannelID != "C123" {
		t.Errorf("got channel=%s, want C123", rec.ChannelID)
	}
}

func TestMarkKept(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)

	_ = store.SaveWarning("session-1", "C123", "T456", "", 24*time.Hour)

	err := store.MarkKept("session-1")
	if err != nil {
		t.Fatal(err)
	}

	// Should no longer be found as active warning
	rec, _ := store.GetWarningBySession("session-1")
	if rec != nil {
		t.Error("expected no active warning after marking kept")
	}
}

func TestMarkClosed(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)

	_ = store.SaveWarning("session-1", "C123", "T456", "", 24*time.Hour)

	err := store.MarkClosed("session-1")
	if err != nil {
		t.Fatal(err)
	}

	rec, _ := store.GetWarningBySession("session-1")
	if rec != nil {
		t.Error("expected no active warning after marking closed")
	}
}

func TestGetExpiredWarnings(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)

	// Save a warning with TTL already expired
	_ = store.SaveWarning("session-1", "C123", "T456", "", 0)

	expired, err := store.GetExpiredWarnings()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if expired[0].SessionKey != "session-1" {
		t.Errorf("got session=%s", expired[0].SessionKey)
	}
}

func TestHasRecentWarning(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)

	_ = store.SaveWarning("session-1", "C123", "T456", "", 24*time.Hour)

	has, err := store.HasRecentWarning("session-1", 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected recent warning to exist")
	}

	has, _ = store.HasRecentWarning("session-2", 7*24*time.Hour)
	if has {
		t.Error("expected no recent warning for session-2")
	}
}

func TestCleanupOldRecords(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)

	// Insert an old closed record directly
	_, _ = db.Exec(`INSERT INTO session_cleanup (id, session_key, channel_id, thread_ts, status, warned_at, expires_at, created_at) VALUES ('old', 'session-old', 'C1', 'T1', 'closed', 0, 0, 0)`)

	err := store.CleanupOldRecords(1)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM session_cleanup WHERE id = 'old'`).Scan(&count)
	if count != 0 {
		t.Error("expected old record to be deleted")
	}
}
