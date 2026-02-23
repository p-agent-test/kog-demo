package store

import (
	"database/sql"
	"fmt"
	"time"
)

// PendingApproval represents a pending approval request
type PendingApproval struct {
	RequestID string
	TaskID    string
	CallerID  string
	Permission string
	Action    string
	Resource  string
	ChannelID string
	ThreadTS  string
	CreatedAt int64
}

// SaveApproval saves a pending approval
func (s *Store) SaveApproval(a *PendingApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().UnixMilli()
	}

	query := `
	INSERT OR REPLACE INTO pending_approvals (
		request_id, task_id, caller_id, permission, action, resource,
		channel_id, thread_ts, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		a.RequestID, a.TaskID, a.CallerID, a.Permission, a.Action, a.Resource,
		sql.NullString{String: a.ChannelID, Valid: a.ChannelID != ""},
		sql.NullString{String: a.ThreadTS, Valid: a.ThreadTS != ""},
		a.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to save approval: %w", err)
	}
	return nil
}

// GetApproval retrieves a pending approval by request ID
func (s *Store) GetApproval(requestID string) (*PendingApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	a := &PendingApproval{}
	var channelID, threadTS sql.NullString

	query := `
	SELECT request_id, task_id, caller_id, permission, action, resource,
	       channel_id, thread_ts, created_at
	FROM pending_approvals WHERE request_id = ?
	`

	err := s.db.QueryRow(query, requestID).Scan(
		&a.RequestID, &a.TaskID, &a.CallerID, &a.Permission, &a.Action, &a.Resource,
		&channelID, &threadTS, &a.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get approval: %w", err)
	}

	if channelID.Valid {
		a.ChannelID = channelID.String
	}
	if threadTS.Valid {
		a.ThreadTS = threadTS.String
	}

	return a, nil
}

// DeleteApproval deletes a pending approval
func (s *Store) DeleteApproval(requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `DELETE FROM pending_approvals WHERE request_id = ?`
	_, err := s.db.Exec(query, requestID)

	if err != nil {
		return fmt.Errorf("failed to delete approval: %w", err)
	}
	return nil
}

// ListExpiredApprovals returns approvals older than maxAgeMs
func (s *Store) ListExpiredApprovals(maxAgeMs int64) ([]*PendingApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().UnixMilli() - maxAgeMs
	query := `
	SELECT request_id, task_id, caller_id, permission, action, resource,
	       channel_id, thread_ts, created_at
	FROM pending_approvals WHERE created_at < ?
	`

	rows, err := s.db.Query(query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to list expired approvals: %w", err)
	}
	defer rows.Close()

	var approvals []*PendingApproval
	for rows.Next() {
		a := &PendingApproval{}
		var channelID, threadTS sql.NullString

		err := rows.Scan(
			&a.RequestID, &a.TaskID, &a.CallerID, &a.Permission, &a.Action, &a.Resource,
			&channelID, &threadTS, &a.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan approval: %w", err)
		}

		if channelID.Valid {
			a.ChannelID = channelID.String
		}
		if threadTS.Valid {
			a.ThreadTS = threadTS.String
		}

		approvals = append(approvals, a)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating approvals: %w", err)
	}

	return approvals, nil
}
