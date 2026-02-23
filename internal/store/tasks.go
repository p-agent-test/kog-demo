package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Task represents a task in the database
type Task struct {
	ID              string
	Status          string // pending, running, awaiting_approval, completed, failed, cancelled
	Command         string
	Params          string // JSON
	CallerID        string
	ResponseChannel string
	ResponseThread  string
	Result          string // JSON, nullable
	Error           string // nullable
	CreatedAt       int64  // unix ms
	UpdatedAt       int64  // unix ms
	CompletedAt     int64  // unix ms, 0 = not completed
}

// TaskFilter for filtering tasks
type TaskFilter struct {
	Status string
	Limit  int
}

// SaveTask inserts or updates a task
func (s *Store) SaveTask(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().UnixMilli()
	}
	if t.UpdatedAt == 0 {
		t.UpdatedAt = time.Now().UnixMilli()
	}

	query := `
	INSERT OR REPLACE INTO tasks (
		id, status, command, params, caller_id, response_channel,
		response_thread, result, error, created_at, updated_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		t.ID, t.Status, t.Command, t.Params, t.CallerID,
		sql.NullString{String: t.ResponseChannel, Valid: t.ResponseChannel != ""},
		sql.NullString{String: t.ResponseThread, Valid: t.ResponseThread != ""},
		sql.NullString{String: t.Result, Valid: t.Result != ""},
		sql.NullString{String: t.Error, Valid: t.Error != ""},
		t.CreatedAt, t.UpdatedAt,
		sql.NullInt64{Int64: t.CompletedAt, Valid: t.CompletedAt != 0},
	)

	if err != nil {
		return fmt.Errorf("failed to save task: %w", err)
	}
	return nil
}


// GetTask retrieves a task by ID
func (s *Store) GetTask(id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t := &Task{}
	var resChannel, resThread, result, errMsg sql.NullString
	var completedAt sql.NullInt64

	query := `
	SELECT id, status, command, params, caller_id, response_channel,
	       response_thread, result, error, created_at, updated_at, completed_at
	FROM tasks WHERE id = ?
	`

	err := s.db.QueryRow(query, id).Scan(
		&t.ID, &t.Status, &t.Command, &t.Params, &t.CallerID,
		&resChannel, &resThread, &result, &errMsg,
		&t.CreatedAt, &t.UpdatedAt, &completedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	if resChannel.Valid {
		t.ResponseChannel = resChannel.String
	}
	if resThread.Valid {
		t.ResponseThread = resThread.String
	}
	if result.Valid {
		t.Result = result.String
	}
	if errMsg.Valid {
		t.Error = errMsg.String
	}
	if completedAt.Valid {
		t.CompletedAt = completedAt.Int64
	}

	return t, nil
}

// UpdateTaskStatus updates a task's status and updated_at
func (s *Store) UpdateTaskStatus(id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`
	result, err := s.db.Exec(query, status, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("task not found: %s", id)
	}

	return nil
}

// CompleteTask marks a task as completed with result/error
func (s *Store) CompleteTask(id string, result, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	query := `
	UPDATE tasks
	SET status = ?, result = ?, error = ?, completed_at = ?, updated_at = ?
	WHERE id = ?
	`

	resultVal := sql.NullString{String: result, Valid: result != ""}
	errorVal := sql.NullString{String: errMsg, Valid: errMsg != ""}

	res, err := s.db.Exec(query, "completed", resultVal, errorVal, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("task not found: %s", id)
	}

	return nil
}

// ListTasks retrieves tasks matching the filter
func (s *Store) ListTasks(f TaskFilter) ([]*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, status, command, params, caller_id, response_channel,
	       response_thread, result, error, created_at, updated_at, completed_at
	FROM tasks
	`

	args := []interface{}{}
	if f.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, f.Status)
	}

	query += ` ORDER BY created_at DESC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		var resChannel, resThread, result, errMsg sql.NullString
		var completedAt sql.NullInt64

		err := rows.Scan(
			&t.ID, &t.Status, &t.Command, &t.Params, &t.CallerID,
			&resChannel, &resThread, &result, &errMsg,
			&t.CreatedAt, &t.UpdatedAt, &completedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		if resChannel.Valid {
			t.ResponseChannel = resChannel.String
		}
		if resThread.Valid {
			t.ResponseThread = resThread.String
		}
		if result.Valid {
			t.Result = result.String
		}
		if errMsg.Valid {
			t.Error = errMsg.String
		}
		if completedAt.Valid {
			t.CompletedAt = completedAt.Int64
		}

		tasks = append(tasks, t)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	return tasks, nil
}

// FailStuckTasks marks running tasks as failed (startup recovery)
func (s *Store) FailStuckTasks() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	query := `
	UPDATE tasks
	SET status = 'failed', error = 'stuck_on_startup', completed_at = ?, updated_at = ?
	WHERE status = 'running'
	`

	result, err := s.db.Exec(query, now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to fail stuck tasks: %w", err)
	}

	return result.RowsAffected()
}

// RequeuePendingTasks returns all pending tasks for re-enqueue
func (s *Store) RequeuePendingTasks() ([]*Task, error) {
	return s.ListTasks(TaskFilter{Status: "pending"})
}
