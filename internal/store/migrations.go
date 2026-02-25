package store

import (
	"fmt"
)

func (s *Store) migrate() error {
	if err := s.migrateV1(); err != nil {
		return err
	}
	if err := s.migrateV2(); err != nil {
		return err
	}
	return s.migrateV3()
}

func (s *Store) migrateV1() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL DEFAULT 'pending',
		command TEXT NOT NULL,
		params TEXT NOT NULL,
		caller_id TEXT NOT NULL DEFAULT '',
		response_channel TEXT,
		response_thread TEXT,
		result TEXT,
		error TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		completed_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at);

	CREATE TABLE IF NOT EXISTS pending_approvals (
		request_id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		caller_id TEXT NOT NULL,
		permission TEXT NOT NULL,
		action TEXT NOT NULL,
		resource TEXT NOT NULL,
		channel_id TEXT,
		thread_ts TEXT,
		created_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS session_contexts (
		session_id TEXT PRIMARY KEY,
		channel TEXT NOT NULL,
		thread_ts TEXT NOT NULL,
		user_id TEXT,
		created_at INTEGER NOT NULL,
		last_used INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_session_ctx_channel ON session_contexts(channel, thread_ts);

	CREATE TABLE IF NOT EXISTS thread_sessions (
		channel TEXT NOT NULL,
		thread_ts TEXT NOT NULL,
		session_key TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_message_at INTEGER NOT NULL,
		PRIMARY KEY (channel, thread_ts)
	);

	CREATE TABLE IF NOT EXISTS dead_letters (
		id TEXT PRIMARY KEY,
		target_channel TEXT NOT NULL,
		target_thread TEXT,
		message TEXT NOT NULL,
		error TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		retry_count INTEGER NOT NULL DEFAULT 0,
		next_retry_at INTEGER,
		resolved_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_dlq_unresolved ON dead_letters(next_retry_at) WHERE resolved_at IS NULL;

	CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		action TEXT NOT NULL,
		resource TEXT,
		result TEXT NOT NULL,
		details TEXT,
		created_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at);

	CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	INSERT OR REPLACE INTO meta(key, value) VALUES ('schema_version', '1');
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to execute migration v1: %w", err)
	}

	return nil
}

func (s *Store) migrateV2() error {
	// Check current version
	var version string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil || version >= "2" {
		return nil // already at v2+
	}

	schema := `
	CREATE TABLE IF NOT EXISTS projects (
		id              TEXT PRIMARY KEY,
		slug            TEXT NOT NULL UNIQUE,
		name            TEXT NOT NULL,
		description     TEXT NOT NULL DEFAULT '',
		repo_url        TEXT,
		status          TEXT NOT NULL DEFAULT 'active',
		owner_id        TEXT NOT NULL,
		active_session  TEXT,
		session_version INTEGER NOT NULL DEFAULT 1,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		archived_at     INTEGER
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);
	CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status);
	CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects(owner_id);

	CREATE TABLE IF NOT EXISTS project_memory (
		id          TEXT PRIMARY KEY,
		project_id  TEXT NOT NULL REFERENCES projects(id),
		type        TEXT NOT NULL,
		content     TEXT NOT NULL,
		session_key TEXT,
		created_at  INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_pmem_project ON project_memory(project_id, created_at);

	CREATE TABLE IF NOT EXISTS project_events (
		id          TEXT PRIMARY KEY,
		project_id  TEXT NOT NULL REFERENCES projects(id),
		event_type  TEXT NOT NULL,
		actor_id    TEXT NOT NULL,
		summary     TEXT NOT NULL DEFAULT '',
		metadata    TEXT,
		created_at  INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_pevt_project ON project_events(project_id, created_at);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to execute migration v2 (tables): %w", err)
	}

	// ALTER TABLE tasks ADD COLUMN project_id (ignore if already exists)
	_, _ = s.db.Exec(`ALTER TABLE tasks ADD COLUMN project_id TEXT REFERENCES projects(id)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id)`)

	// ALTER TABLE thread_sessions ADD COLUMN project_id (ignore if already exists)
	_, _ = s.db.Exec(`ALTER TABLE thread_sessions ADD COLUMN project_id TEXT REFERENCES projects(id)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_thread_project ON thread_sessions(project_id)`)

	// Update schema version
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('schema_version', '2')`); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	return nil
}

func (s *Store) migrateV3() error {
	var version string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil || version >= "3" {
		return nil
	}

	schema := `
	CREATE TABLE IF NOT EXISTS session_cleanup (
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

	CREATE INDEX IF NOT EXISTS idx_cleanup_status ON session_cleanup(status);
	CREATE INDEX IF NOT EXISTS idx_cleanup_expires ON session_cleanup(expires_at);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to execute migration v3: %w", err)
	}

	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('schema_version', '3')`); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	return nil
}
