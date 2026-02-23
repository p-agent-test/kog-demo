package store

import (
	"database/sql"
	"fmt"
	"time"
)

// SessionContext represents a session context
type SessionContext struct {
	SessionID string
	Channel   string
	ThreadTS  string
	UserID    string
	CreatedAt int64
	LastUsed  int64
}

// ThreadSession represents a thread session
type ThreadSession struct {
	Channel       string
	ThreadTS      string
	SessionKey    string
	CreatedAt     int64
	LastMessageAt int64
}

// SaveSessionContext saves a session context
func (s *Store) SaveSessionContext(sc *SessionContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sc.CreatedAt == 0 {
		sc.CreatedAt = time.Now().UnixMilli()
	}
	if sc.LastUsed == 0 {
		sc.LastUsed = time.Now().UnixMilli()
	}

	query := `
	INSERT OR REPLACE INTO session_contexts (
		session_id, channel, thread_ts, user_id, created_at, last_used
	) VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		sc.SessionID, sc.Channel, sc.ThreadTS,
		sql.NullString{String: sc.UserID, Valid: sc.UserID != ""},
		sc.CreatedAt, sc.LastUsed,
	)

	if err != nil {
		return fmt.Errorf("failed to save session context: %w", err)
	}
	return nil
}

// GetSessionContext retrieves a session context by ID
func (s *Store) GetSessionContext(sessionID string) (*SessionContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sc := &SessionContext{}
	var userID sql.NullString

	query := `
	SELECT session_id, channel, thread_ts, user_id, created_at, last_used
	FROM session_contexts WHERE session_id = ?
	`

	err := s.db.QueryRow(query, sessionID).Scan(
		&sc.SessionID, &sc.Channel, &sc.ThreadTS, &userID, &sc.CreatedAt, &sc.LastUsed,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session context: %w", err)
	}

	if userID.Valid {
		sc.UserID = userID.String
	}

	return sc, nil
}

// GetSessionContextByThread retrieves a session context by channel and thread
func (s *Store) GetSessionContextByThread(channel, threadTS string) (*SessionContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sc := &SessionContext{}
	var userID sql.NullString

	query := `
	SELECT session_id, channel, thread_ts, user_id, created_at, last_used
	FROM session_contexts WHERE channel = ? AND thread_ts = ?
	`

	err := s.db.QueryRow(query, channel, threadTS).Scan(
		&sc.SessionID, &sc.Channel, &sc.ThreadTS, &userID, &sc.CreatedAt, &sc.LastUsed,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session context by thread: %w", err)
	}

	if userID.Valid {
		sc.UserID = userID.String
	}

	return sc, nil
}

// TouchSessionContext updates last_used timestamp
func (s *Store) TouchSessionContext(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE session_contexts SET last_used = ? WHERE session_id = ?`
	result, err := s.db.Exec(query, time.Now().UnixMilli(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to touch session context: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	return nil
}

// SaveThreadSession saves a thread session
func (s *Store) SaveThreadSession(ts *ThreadSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ts.CreatedAt == 0 {
		ts.CreatedAt = time.Now().UnixMilli()
	}
	if ts.LastMessageAt == 0 {
		ts.LastMessageAt = time.Now().UnixMilli()
	}

	query := `
	INSERT OR REPLACE INTO thread_sessions (
		channel, thread_ts, session_key, created_at, last_message_at
	) VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		ts.Channel, ts.ThreadTS, ts.SessionKey, ts.CreatedAt, ts.LastMessageAt,
	)

	if err != nil {
		return fmt.Errorf("failed to save thread session: %w", err)
	}
	return nil
}

// GetThreadSession retrieves a thread session
func (s *Store) GetThreadSession(channel, threadTS string) (*ThreadSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ts := &ThreadSession{}

	query := `
	SELECT channel, thread_ts, session_key, created_at, last_message_at
	FROM thread_sessions WHERE channel = ? AND thread_ts = ?
	`

	err := s.db.QueryRow(query, channel, threadTS).Scan(
		&ts.Channel, &ts.ThreadTS, &ts.SessionKey, &ts.CreatedAt, &ts.LastMessageAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get thread session: %w", err)
	}

	return ts, nil
}

// TouchThreadSession updates last_message_at timestamp
func (s *Store) TouchThreadSession(channel, threadTS string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	UPDATE thread_sessions SET last_message_at = ?
	WHERE channel = ? AND thread_ts = ?
	`
	result, err := s.db.Exec(query, time.Now().UnixMilli(), channel, threadTS)
	if err != nil {
		return fmt.Errorf("failed to touch thread session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("thread session not found: %s/%s", channel, threadTS)
	}

	return nil
}
