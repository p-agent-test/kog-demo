package cleanup

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CleanupStore handles SQLite operations for session cleanup records.
type CleanupStore struct {
	db *sql.DB
}

// NewCleanupStore creates a new CleanupStore.
func NewCleanupStore(db *sql.DB) *CleanupStore {
	return &CleanupStore{db: db}
}

// SaveWarning creates a new cleanup warning record.
func (s *CleanupStore) SaveWarning(sessionKey, channelID, threadTS, messageTS string, warningTTL time.Duration) error {
	now := time.Now().UnixMilli()
	id := uuid.New().String()

	query := `
	INSERT INTO session_cleanup (id, session_key, channel_id, thread_ts, status, warned_at, expires_at, message_ts, created_at)
	VALUES (?, ?, ?, ?, 'warned', ?, ?, ?, ?)
	`

	expiresAt := now + warningTTL.Milliseconds()

	_, err := s.db.Exec(query, id, sessionKey, channelID, threadTS, now, expiresAt, messageTS, now)
	if err != nil {
		return fmt.Errorf("failed to save warning: %w", err)
	}
	return nil
}

// GetExpiredWarnings returns warnings where status=warned AND expires_at < now.
func (s *CleanupStore) GetExpiredWarnings() ([]CleanupRecord, error) {
	now := time.Now().UnixMilli()

	query := `
	SELECT id, session_key, channel_id, thread_ts, status, warned_at, COALESCE(responded_at, 0), expires_at, COALESCE(message_ts, ''), created_at
	FROM session_cleanup
	WHERE status = 'warned' AND expires_at < ?
	`

	rows, err := s.db.Query(query, now)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired warnings: %w", err)
	}
	defer rows.Close()

	var records []CleanupRecord
	for rows.Next() {
		var r CleanupRecord
		if err := rows.Scan(&r.ID, &r.SessionKey, &r.ChannelID, &r.ThreadTS, &r.Status, &r.WarnedAt, &r.RespondedAt, &r.ExpiresAt, &r.MessageTS, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan cleanup record: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// MarkKept marks a session as kept (user pressed "Devam Et").
func (s *CleanupStore) MarkKept(sessionKey string) error {
	now := time.Now().UnixMilli()

	query := `UPDATE session_cleanup SET status = 'kept', responded_at = ? WHERE session_key = ? AND status = 'warned'`
	result, err := s.db.Exec(query, now, sessionKey)
	if err != nil {
		return fmt.Errorf("failed to mark kept: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("no active warning found for session: %s", sessionKey)
	}
	return nil
}

// MarkClosed marks a session as closed.
func (s *CleanupStore) MarkClosed(sessionKey string) error {
	now := time.Now().UnixMilli()

	query := `UPDATE session_cleanup SET status = 'closed', responded_at = ? WHERE session_key = ? AND status = 'warned'`
	result, err := s.db.Exec(query, now, sessionKey)
	if err != nil {
		return fmt.Errorf("failed to mark closed: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("no active warning found for session: %s", sessionKey)
	}
	return nil
}

// GetWarningBySession retrieves the active warning for a session key.
func (s *CleanupStore) GetWarningBySession(sessionKey string) (*CleanupRecord, error) {
	query := `
	SELECT id, session_key, channel_id, thread_ts, status, warned_at, COALESCE(responded_at, 0), expires_at, COALESCE(message_ts, ''), created_at
	FROM session_cleanup
	WHERE session_key = ? AND status = 'warned'
	ORDER BY warned_at DESC LIMIT 1
	`

	var r CleanupRecord
	err := s.db.QueryRow(query, sessionKey).Scan(&r.ID, &r.SessionKey, &r.ChannelID, &r.ThreadTS, &r.Status, &r.WarnedAt, &r.RespondedAt, &r.ExpiresAt, &r.MessageTS, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get warning: %w", err)
	}
	return &r, nil
}

// HasRecentWarning checks if a session has a warning (warned/kept) within the given duration.
func (s *CleanupStore) HasRecentWarning(sessionKey string, within time.Duration) (bool, error) {
	cutoff := time.Now().UnixMilli() - within.Milliseconds()

	query := `
	SELECT COUNT(*) FROM session_cleanup
	WHERE session_key = ? AND (status = 'warned' OR status = 'kept') AND warned_at > ?
	`

	var count int
	err := s.db.QueryRow(query, sessionKey, cutoff).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check recent warning: %w", err)
	}
	return count > 0, nil
}

// CleanupOldRecords deletes cleanup records older than the given number of days.
func (s *CleanupStore) CleanupOldRecords(olderThanDays int) error {
	cutoff := time.Now().UnixMilli() - int64(olderThanDays)*24*60*60*1000

	_, err := s.db.Exec(`DELETE FROM session_cleanup WHERE created_at < ? AND status != 'warned'`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old records: %w", err)
	}
	return nil
}
