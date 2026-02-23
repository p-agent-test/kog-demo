package store

import (
	"context"
	"fmt"
	"time"
)

// RunRetention cleans up old data according to retention policies
func (s *Store) RunRetention(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()

	// Completed tasks older than 7 days
	sevenDaysAgo := now - (7 * 24 * 60 * 60 * 1000)
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM tasks WHERE completed_at > 0 AND completed_at < ?",
		sevenDaysAgo,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old tasks: %w", err)
	}

	// Pending approvals older than 1 hour
	oneHourAgo := now - (60 * 60 * 1000)
	_, err = s.db.ExecContext(ctx,
		"DELETE FROM pending_approvals WHERE created_at < ?",
		oneHourAgo,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old approvals: %w", err)
	}

	// Session contexts not used in 24 hours
	oneDayAgo := now - (24 * 60 * 60 * 1000)
	_, err = s.db.ExecContext(ctx,
		"DELETE FROM session_contexts WHERE last_used < ?",
		oneDayAgo,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old session contexts: %w", err)
	}

	// Thread sessions not used in 7 days
	sevenDaysAgoMs := now - (7 * 24 * 60 * 60 * 1000)
	_, err = s.db.ExecContext(ctx,
		"DELETE FROM thread_sessions WHERE last_message_at < ?",
		sevenDaysAgoMs,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old thread sessions: %w", err)
	}

	// Resolved dead letters older than 24 hours
	oneDayAgoMs := now - (24 * 60 * 60 * 1000)
	_, err = s.db.ExecContext(ctx,
		"DELETE FROM dead_letters WHERE resolved_at IS NOT NULL AND resolved_at < ?",
		oneDayAgoMs,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old dead letters: %w", err)
	}

	// Audit logs older than 30 days
	thirtyDaysAgo := now - (30 * 24 * 60 * 60 * 1000)
	_, err = s.db.ExecContext(ctx,
		"DELETE FROM audit_log WHERE created_at < ?",
		thirtyDaysAgo,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old audit logs: %w", err)
	}

	return nil
}

// DBSizeBytes returns the database size in bytes
func (s *Store) DBSizeBytes() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var pageCount int64
	var pageSize int64

	// Get page count
	err := s.db.QueryRow("PRAGMA page_count").Scan(&pageCount)
	if err != nil {
		return 0, fmt.Errorf("failed to get page count: %w", err)
	}

	// Get page size
	err = s.db.QueryRow("PRAGMA page_size").Scan(&pageSize)
	if err != nil {
		return 0, fmt.Errorf("failed to get page size: %w", err)
	}

	return pageCount * pageSize, nil
}
