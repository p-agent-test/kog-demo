package store

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
	"github.com/rs/zerolog"
)

// Store manages the SQLite database
type Store struct {
	db     *sql.DB
	logger zerolog.Logger
	mu     sync.RWMutex
}

// New opens (or creates) the SQLite database and runs migrations.
func New(dbPath string, logger zerolog.Logger) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &Store{
		db:     db,
		logger: logger,
	}

	// Set PRAGMAs
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	// Run migrations
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	logger.Info().Msg("Store initialized successfully")
	return s, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// DB returns the underlying database connection (for testing)
func (s *Store) DB() *sql.DB {
	return s.db
}
