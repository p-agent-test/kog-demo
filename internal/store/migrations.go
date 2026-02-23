package store

import (
	"fmt"
)

func (s *Store) migrate() error {
	return s.migrateV1()
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
