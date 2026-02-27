// Package memory provides the MemoryStore interface and SQLite implementation.
// Vector search is planned via sqlite-vec; for now full-text search via SQLite FTS5.
package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// MemoryEntry is a single stored memory.
type MemoryEntry struct {
	ID        string
	AgentID   string
	Content   string
	Tags      []string
	CreatedAt time.Time
}

// MemoryStore is the persistence interface for agent memory.
type MemoryStore interface {
	// Save persists a new memory entry. ID is generated if empty.
	Save(ctx context.Context, entry MemoryEntry) error

	// Search performs a full-text search over content.
	Search(ctx context.Context, query string, topK int) ([]MemoryEntry, error)

	// Get returns a single entry by ID.
	Get(ctx context.Context, id string) (*MemoryEntry, error)

	// Delete removes an entry by ID.
	Delete(ctx context.Context, id string) error

	// Close releases the store's resources.
	Close() error
}

// SQLiteStore implements MemoryStore using modernc SQLite.
type SQLiteStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewSQLiteStore opens (or creates) the SQLite database and applies migrations.
func NewSQLiteStore(dsn string, logger *slog.Logger) (*SQLiteStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// WAL mode for concurrency.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("wal mode: %w", err)
	}

	s := &SQLiteStore{db: db, logger: logger}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_entries (
			id         TEXT PRIMARY KEY,
			agent_id   TEXT NOT NULL,
			content    TEXT NOT NULL,
			tags       TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			id UNINDEXED,
			content,
			tokenize = 'porter ascii'
		);

		CREATE TRIGGER IF NOT EXISTS memory_fts_insert
		AFTER INSERT ON memory_entries BEGIN
			INSERT INTO memory_fts(id, content) VALUES (new.id, new.content);
		END;

		CREATE TRIGGER IF NOT EXISTS memory_fts_delete
		AFTER DELETE ON memory_entries BEGIN
			DELETE FROM memory_fts WHERE id = old.id;
		END;

		CREATE TRIGGER IF NOT EXISTS memory_fts_update
		AFTER UPDATE ON memory_entries BEGIN
			DELETE FROM memory_fts WHERE id = old.id;
			INSERT INTO memory_fts(id, content) VALUES (new.id, new.content);
		END;
	`)
	return err
}

// Save persists a memory entry, generating a UUID if ID is empty.
func (s *SQLiteStore) Save(ctx context.Context, entry MemoryEntry) error {
	if entry.ID == "" {
		entry.ID = newID()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	tags := strings.Join(entry.Tags, ",")

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_entries (id, agent_id, content, tags, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   content    = excluded.content,
		   tags       = excluded.tags,
		   created_at = excluded.created_at`,
		entry.ID, entry.AgentID, entry.Content, tags, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("save memory: %w", err)
	}
	s.logger.Debug("memory saved", "id", entry.ID, "agent", entry.AgentID)
	return nil
}

// Search performs FTS5 search over memory content.
func (s *SQLiteStore) Search(ctx context.Context, query string, topK int) ([]MemoryEntry, error) {
	if topK <= 0 {
		topK = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.agent_id, e.content, e.tags, e.created_at
		FROM memory_entries e
		JOIN memory_fts f ON e.id = f.id
		WHERE memory_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, topK)
	if err != nil {
		// Fall back to LIKE if FTS fails (e.g. special chars in query)
		return s.searchLike(ctx, query, topK)
	}
	defer rows.Close()
	return scanEntries(rows)
}

func (s *SQLiteStore) searchLike(ctx context.Context, query string, topK int) ([]MemoryEntry, error) {
	like := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_id, content, tags, created_at
		FROM memory_entries
		WHERE content LIKE ?
		ORDER BY created_at DESC
		LIMIT ?`, like, topK)
	if err != nil {
		return nil, fmt.Errorf("search memory (like): %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// Get returns a single memory entry by ID.
func (s *SQLiteStore) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, content, tags, created_at FROM memory_entries WHERE id = ?`, id)

	var e MemoryEntry
	var tags string
	if err := row.Scan(&e.ID, &e.AgentID, &e.Content, &tags, &e.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get memory: %w", err)
	}
	if tags != "" {
		e.Tags = strings.Split(tags, ",")
	}
	return &e, nil
}

// Delete removes a memory entry by ID.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory_entries WHERE id = ?`, id)
	return err
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// ---- helpers ----

func scanEntries(rows *sql.Rows) ([]MemoryEntry, error) {
	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var tags string
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Content, &tags, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		if tags != "" {
			e.Tags = strings.Split(tags, ",")
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// newID generates a simple timestamp-based ID. Replace with uuid if preferred.
func newID() string {
	return fmt.Sprintf("mem_%d", time.Now().UnixNano())
}
