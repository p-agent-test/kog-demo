package store

import (
	"database/sql"
	"fmt"
	"time"
)

// DeadLetter represents a dead letter (failed message)
type DeadLetter struct {
	ID            string
	TargetChannel string
	TargetThread  string
	Message       string
	Error         string
	CreatedAt     int64
	RetryCount    int
	NextRetryAt   int64 // 0 = give up
	ResolvedAt    int64 // 0 = unresolved
}

// SaveDeadLetter saves a dead letter
func (s *Store) SaveDeadLetter(dl *DeadLetter) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dl.CreatedAt == 0 {
		dl.CreatedAt = time.Now().UnixMilli()
	}

	query := `
	INSERT OR REPLACE INTO dead_letters (
		id, target_channel, target_thread, message, error,
		created_at, retry_count, next_retry_at, resolved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	targetThread := sql.NullString{String: dl.TargetThread, Valid: dl.TargetThread != ""}
	nextRetry := sql.NullInt64{Int64: dl.NextRetryAt, Valid: dl.NextRetryAt != 0}
	resolved := sql.NullInt64{Int64: dl.ResolvedAt, Valid: dl.ResolvedAt != 0}

	_, err := s.db.Exec(query,
		dl.ID, dl.TargetChannel, targetThread, dl.Message, dl.Error,
		dl.CreatedAt, dl.RetryCount, nextRetry, resolved,
	)

	if err != nil {
		return fmt.Errorf("failed to save dead letter: %w", err)
	}
	return nil
}

// ListRetryable returns unresolved dead letters ready for retry
func (s *Store) ListRetryable(limit int) ([]*DeadLetter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UnixMilli()
	query := `
	SELECT id, target_channel, target_thread, message, error,
	       created_at, retry_count, next_retry_at, resolved_at
	FROM dead_letters
	WHERE next_retry_at <= ? AND resolved_at IS NULL
	ORDER BY next_retry_at ASC
	`

	args := []interface{}{now}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list retryable dead letters: %w", err)
	}
	defer rows.Close()

	var dls []*DeadLetter
	for rows.Next() {
		dl := &DeadLetter{}
		var targetThread sql.NullString
		var nextRetry, resolved sql.NullInt64

		err := rows.Scan(
			&dl.ID, &dl.TargetChannel, &targetThread, &dl.Message, &dl.Error,
			&dl.CreatedAt, &dl.RetryCount, &nextRetry, &resolved,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan dead letter: %w", err)
		}

		if targetThread.Valid {
			dl.TargetThread = targetThread.String
		}
		if nextRetry.Valid {
			dl.NextRetryAt = nextRetry.Int64
		}
		if resolved.Valid {
			dl.ResolvedAt = resolved.Int64
		}

		dls = append(dls, dl)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dead letters: %w", err)
	}

	return dls, nil
}

// IncrementRetry increments retry count and sets next retry time
func (s *Store) IncrementRetry(id string, nextRetryAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	UPDATE dead_letters
	SET retry_count = retry_count + 1, next_retry_at = ?
	WHERE id = ?
	`

	result, err := s.db.Exec(query, nextRetryAt, id)
	if err != nil {
		return fmt.Errorf("failed to increment retry: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("dead letter not found: %s", id)
	}

	return nil
}

// ResolveDeadLetter marks a dead letter as resolved
func (s *Store) ResolveDeadLetter(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE dead_letters SET resolved_at = ? WHERE id = ?`
	result, err := s.db.Exec(query, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("failed to resolve dead letter: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("dead letter not found: %s", id)
	}

	return nil
}
