package project

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/store"
)

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// reservedSlugs contains words that cannot be used as project slugs.
var reservedSlugs = map[string]bool{
	"projects": true, "projeler": true, "new": true, "decide": true,
	"blocker": true, "archive": true, "resume": true, "help": true, "handoff": true,
	"drive": true, "pause": true, "phase": true,
}

// GenerateSlug converts a name into a URL-safe slug.
func GenerateSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	// collapse multiple hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}

// IsReservedSlug returns true if the slug is a reserved command word.
func IsReservedSlug(slug string) bool {
	return reservedSlugs[slug]
}

// Store handles project-related SQLite operations.
type Store struct {
	ds     *store.Store
	logger zerolog.Logger
}

// NewStore creates a new project store.
func NewStore(ds *store.Store, logger zerolog.Logger) *Store {
	return &Store{
		ds:     ds,
		logger: logger.With().Str("component", "project.store").Logger(),
	}
}

// DB returns the underlying sql.DB for direct use.
func (s *Store) DB() *sql.DB {
	return s.ds.DB()
}

// CreateProject creates a new project.
func (s *Store) CreateProject(input CreateProjectInput) (*Project, error) {
	slug := GenerateSlug(input.Name)
	if slug == "" {
		return nil, fmt.Errorf("invalid project name: generates empty slug")
	}
	if IsReservedSlug(slug) {
		return nil, fmt.Errorf("slug %q is a reserved word", slug)
	}

	now := time.Now().UnixMilli()
	p := &Project{
		ID:             uuid.New().String(),
		Slug:           slug,
		Name:           input.Name,
		Description:    input.Description,
		RepoURL:        input.RepoURL,
		Status:         "active",
		OwnerID:        input.OwnerID,
		ActiveSession:  fmt.Sprintf("agent:main:project-%s", slug),
		SessionVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	query := `
	INSERT INTO projects (id, slug, name, description, repo_url, status, owner_id, active_session, session_version, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.ds.DB().Exec(query,
		p.ID, p.Slug, p.Name, p.Description,
		sql.NullString{String: p.RepoURL, Valid: p.RepoURL != ""},
		p.Status, p.OwnerID, p.ActiveSession, p.SessionVersion, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("project with slug %q already exists", slug)
		}
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	// Record creation event
	_ = s.AddEvent(&ProjectEvent{
		ProjectID: p.ID,
		EventType: "created",
		ActorID:   input.OwnerID,
		Summary:   fmt.Sprintf("Project %q created", p.Name),
	})

	return p, nil
}

// GetProject retrieves a project by slug.
func (s *Store) GetProject(slug string) (*Project, error) {
	return s.scanProject(`SELECT `+projectColumns+` FROM projects WHERE slug = ?`, slug)
}

// GetProjectByID retrieves a project by ID.
func (s *Store) GetProjectByID(id string) (*Project, error) {
	return s.scanProject(`SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
}

// projectColumns is the standard column list for project queries.
const projectColumns = `id, slug, name, description, repo_url, status, owner_id, active_session, session_version, created_at, updated_at, archived_at, auto_drive, drive_interval_ms, report_interval_ms, report_channel_id, report_thread_ts, current_phase, phases, auto_drive_until, phase_models`

func (s *Store) scanProject(query string, args ...interface{}) (*Project, error) {
	p := &Project{}
	var repoURL sql.NullString
	var activeSession sql.NullString
	var archivedAt sql.NullInt64
	var phaseModelsJSON sql.NullString

	err := s.ds.DB().QueryRow(query, args...).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Description, &repoURL,
		&p.Status, &p.OwnerID, &activeSession, &p.SessionVersion,
		&p.CreatedAt, &p.UpdatedAt, &archivedAt,
		&p.AutoDrive, &p.DriveIntervalMs, &p.ReportIntervalMs,
		&p.ReportChannelID, &p.ReportThreadTS, &p.CurrentPhase,
		&p.Phases, &p.AutoDriveUntil, &phaseModelsJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	if repoURL.Valid {
		p.RepoURL = repoURL.String
	}
	if activeSession.Valid {
		p.ActiveSession = activeSession.String
	}
	if archivedAt.Valid {
		p.ArchivedAt = archivedAt.Int64
	}
	if phaseModelsJSON.Valid && phaseModelsJSON.String != "" {
		_ = json.Unmarshal([]byte(phaseModelsJSON.String), &p.PhaseModels)
	}
	return p, nil
}

// ListProjects lists projects with optional filters.
func (s *Store) ListProjects(status, ownerID string) ([]*Project, error) {
	query := `SELECT ` + projectColumns + ` FROM projects WHERE 1=1`
	var args []interface{}

	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if ownerID != "" {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := s.ds.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		var repoURL, activeSession sql.NullString
		var archivedAt sql.NullInt64
		var phaseModelsJSON sql.NullString
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name, &p.Description, &repoURL,
			&p.Status, &p.OwnerID, &activeSession, &p.SessionVersion,
			&p.CreatedAt, &p.UpdatedAt, &archivedAt,
			&p.AutoDrive, &p.DriveIntervalMs, &p.ReportIntervalMs,
			&p.ReportChannelID, &p.ReportThreadTS, &p.CurrentPhase,
			&p.Phases, &p.AutoDriveUntil, &phaseModelsJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		if repoURL.Valid {
			p.RepoURL = repoURL.String
		}
		if activeSession.Valid {
			p.ActiveSession = activeSession.String
		}
		if archivedAt.Valid {
			p.ArchivedAt = archivedAt.Int64
		}
		if phaseModelsJSON.Valid && phaseModelsJSON.String != "" {
			_ = json.Unmarshal([]byte(phaseModelsJSON.String), &p.PhaseModels)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// ListAutoDriveProjects returns all projects with auto_drive enabled.
func (s *Store) ListAutoDriveProjects() ([]*Project, error) {
	query := `SELECT ` + projectColumns + ` FROM projects WHERE auto_drive = 1 AND status = 'active'`
	rows, err := s.ds.DB().Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list auto-drive projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		p := &Project{}
		var repoURL, activeSession sql.NullString
		var archivedAt sql.NullInt64
		var phaseModelsJSON sql.NullString
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name, &p.Description, &repoURL,
			&p.Status, &p.OwnerID, &activeSession, &p.SessionVersion,
			&p.CreatedAt, &p.UpdatedAt, &archivedAt,
			&p.AutoDrive, &p.DriveIntervalMs, &p.ReportIntervalMs,
			&p.ReportChannelID, &p.ReportThreadTS, &p.CurrentPhase,
			&p.Phases, &p.AutoDriveUntil, &phaseModelsJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		if repoURL.Valid {
			p.RepoURL = repoURL.String
		}
		if activeSession.Valid {
			p.ActiveSession = activeSession.String
		}
		if archivedAt.Valid {
			p.ArchivedAt = archivedAt.Int64
		}
		if phaseModelsJSON.Valid && phaseModelsJSON.String != "" {
			_ = json.Unmarshal([]byte(phaseModelsJSON.String), &p.PhaseModels)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// UpdateAutoDrive updates the auto-drive fields for a project.
func (s *Store) UpdateAutoDrive(projectID string, autoDrive bool, driveIntervalMs, reportIntervalMs int64, reportChannelID, reportThreadTS, currentPhase, phases string, autoDriveUntil int64) error {
	now := time.Now().UnixMilli()
	_, err := s.ds.DB().Exec(
		`UPDATE projects SET auto_drive = ?, drive_interval_ms = ?, report_interval_ms = ?, report_channel_id = ?, report_thread_ts = ?, current_phase = ?, phases = ?, auto_drive_until = ?, updated_at = ? WHERE id = ?`,
		autoDrive, driveIntervalMs, reportIntervalMs, reportChannelID, reportThreadTS, currentPhase, phases, autoDriveUntil, now, projectID,
	)
	if err != nil {
		return fmt.Errorf("failed to update auto-drive: %w", err)
	}
	return nil
}

// UpdatePhaseModels sets the model map for phases (phase → model alias).
func (s *Store) UpdatePhaseModels(projectID string, phaseModels map[string]string) error {
	var jsonStr string
	if len(phaseModels) > 0 {
		b, err := json.Marshal(phaseModels)
		if err != nil {
			return fmt.Errorf("failed to marshal phase models: %w", err)
		}
		jsonStr = string(b)
	}
	now := time.Now().UnixMilli()
	_, err := s.ds.DB().Exec(
		`UPDATE projects SET phase_models = ?, updated_at = ? WHERE id = ?`,
		jsonStr, now, projectID,
	)
	if err != nil {
		return fmt.Errorf("failed to update phase models: %w", err)
	}
	return nil
}

// UpdatePhase updates the current phase for a project.
func (s *Store) UpdatePhase(projectID, phase string) error {
	now := time.Now().UnixMilli()
	_, err := s.ds.DB().Exec(
		`UPDATE projects SET current_phase = ?, updated_at = ? WHERE id = ?`,
		phase, now, projectID,
	)
	if err != nil {
		return fmt.Errorf("failed to update phase: %w", err)
	}
	return nil
}

// UpdateProject updates project metadata.
func (s *Store) UpdateProject(slug string, input UpdateProjectInput) (*Project, error) {
	p, err := s.GetProject(slug)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("project not found: %s", slug)
	}

	if input.Name != nil {
		p.Name = *input.Name
	}
	if input.Description != nil {
		p.Description = *input.Description
	}
	if input.RepoURL != nil {
		p.RepoURL = *input.RepoURL
	}
	p.UpdatedAt = time.Now().UnixMilli()

	query := `UPDATE projects SET name = ?, description = ?, repo_url = ?, updated_at = ? WHERE id = ?`
	_, err = s.ds.DB().Exec(query, p.Name, p.Description,
		sql.NullString{String: p.RepoURL, Valid: p.RepoURL != ""},
		p.UpdatedAt, p.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update project: %w", err)
	}
	return p, nil
}

// ArchiveProject archives a project.
func (s *Store) ArchiveProject(slug, actorID string) error {
	now := time.Now().UnixMilli()
	result, err := s.ds.DB().Exec(
		`UPDATE projects SET status = 'archived', archived_at = ?, updated_at = ? WHERE slug = ? AND status != 'archived'`,
		now, now, slug,
	)
	if err != nil {
		return fmt.Errorf("failed to archive project: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("project not found or already archived: %s", slug)
	}

	p, _ := s.GetProject(slug)
	if p != nil {
		_ = s.AddEvent(&ProjectEvent{
			ProjectID: p.ID,
			EventType: "archived",
			ActorID:   actorID,
			Summary:   fmt.Sprintf("Project %q archived", p.Name),
		})
	}
	return nil
}

// ResumeProject reactivates an archived project.
func (s *Store) ResumeProject(slug, actorID string) (*Project, error) {
	now := time.Now().UnixMilli()
	result, err := s.ds.DB().Exec(
		`UPDATE projects SET status = 'active', archived_at = NULL, updated_at = ? WHERE slug = ? AND status = 'archived'`,
		now, slug,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to resume project: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("project not found or not archived: %s", slug)
	}

	p, _ := s.GetProject(slug)
	if p != nil {
		_ = s.AddEvent(&ProjectEvent{
			ProjectID: p.ID,
			EventType: "resumed",
			ActorID:   actorID,
			Summary:   fmt.Sprintf("Project %q resumed", p.Name),
		})
	}
	return p, nil
}

// DeleteProject hard-deletes a project and all related data.
func (s *Store) DeleteProject(slug string) error {
	p, err := s.GetProject(slug)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("project not found: %s", slug)
	}

	tx, err := s.ds.DB().Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Unlink tasks
	if _, err := tx.Exec(`UPDATE tasks SET project_id = NULL WHERE project_id = ?`, p.ID); err != nil {
		return fmt.Errorf("failed to unlink tasks: %w", err)
	}
	// Delete thread bindings
	if _, err := tx.Exec(`DELETE FROM thread_sessions WHERE project_id = ?`, p.ID); err != nil {
		return fmt.Errorf("failed to delete thread bindings: %w", err)
	}
	// Delete memory
	if _, err := tx.Exec(`DELETE FROM project_memory WHERE project_id = ?`, p.ID); err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}
	// Delete events
	if _, err := tx.Exec(`DELETE FROM project_events WHERE project_id = ?`, p.ID); err != nil {
		return fmt.Errorf("failed to delete events: %w", err)
	}
	// Delete project
	if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, p.ID); err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}

	return tx.Commit()
}

// AddMemory adds a memory entry to a project.
func (s *Store) AddMemory(mem *ProjectMemory) error {
	if mem.ID == "" {
		mem.ID = uuid.New().String()
	}
	if mem.CreatedAt == 0 {
		mem.CreatedAt = time.Now().UnixMilli()
	}

	query := `INSERT INTO project_memory (id, project_id, type, content, session_key, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.ds.DB().Exec(query, mem.ID, mem.ProjectID, mem.Type, mem.Content,
		sql.NullString{String: mem.SessionKey, Valid: mem.SessionKey != ""},
		mem.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to add memory: %w", err)
	}

	// Touch project updated_at
	_, _ = s.ds.DB().Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), mem.ProjectID)
	return nil
}

// ListMemory lists memory entries for a project.
func (s *Store) ListMemory(projectID string, memType string) ([]*ProjectMemory, error) {
	query := `SELECT id, project_id, type, content, session_key, created_at FROM project_memory WHERE project_id = ?`
	var args []interface{}
	args = append(args, projectID)
	if memType != "" {
		query += ` AND type = ?`
		args = append(args, memType)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.ds.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memory: %w", err)
	}
	defer rows.Close()

	var mems []*ProjectMemory
	for rows.Next() {
		m := &ProjectMemory{}
		var sessionKey sql.NullString
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Type, &m.Content, &sessionKey, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan memory: %w", err)
		}
		if sessionKey.Valid {
			m.SessionKey = sessionKey.String
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}

// GetProjectStats returns aggregate statistics for a project.
func (s *Store) GetProjectStats(projectID string) (*ProjectStats, error) {
	stats := &ProjectStats{}

	// Count memory by type
	rows, err := s.ds.DB().Query(`SELECT type, COUNT(*) FROM project_memory WHERE project_id = ? GROUP BY type`, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		switch t {
		case "decision":
			stats.Decisions = c
		case "blocker":
			stats.Blockers = c
		case "summary", "context_carry":
			stats.Summaries += c
		}
	}

	// Count events
	_ = s.ds.DB().QueryRow(`SELECT COUNT(*) FROM project_events WHERE project_id = ?`, projectID).Scan(&stats.Events)

	// Count tasks
	_ = s.ds.DB().QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_id = ?`, projectID).Scan(&stats.Tasks)

	return stats, nil
}

// AddEvent adds an event to a project.
func (s *Store) AddEvent(evt *ProjectEvent) error {
	if evt.ID == "" {
		evt.ID = uuid.New().String()
	}
	if evt.CreatedAt == 0 {
		evt.CreatedAt = time.Now().UnixMilli()
	}

	query := `INSERT INTO project_events (id, project_id, event_type, actor_id, summary, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.ds.DB().Exec(query, evt.ID, evt.ProjectID, evt.EventType, evt.ActorID, evt.Summary,
		sql.NullString{String: evt.Metadata, Valid: evt.Metadata != ""},
		evt.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to add event: %w", err)
	}
	return nil
}

// ListEvents lists events for a project.
func (s *Store) ListEvents(projectID string, limit int) ([]*ProjectEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, project_id, event_type, actor_id, summary, metadata, created_at FROM project_events WHERE project_id = ? ORDER BY created_at DESC LIMIT ?`

	rows, err := s.ds.DB().Query(query, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	defer rows.Close()

	var events []*ProjectEvent
	for rows.Next() {
		e := &ProjectEvent{}
		var metadata sql.NullString
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.EventType, &e.ActorID, &e.Summary, &metadata, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		if metadata.Valid {
			e.Metadata = metadata.String
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// BindThread binds a Slack thread to a project.
func (s *Store) BindThread(channel, threadTS, projectID, sessionKey string) error {
	now := time.Now().UnixMilli()
	query := `INSERT OR REPLACE INTO thread_sessions (channel, thread_ts, session_key, project_id, created_at, last_message_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.ds.DB().Exec(query, channel, threadTS, sessionKey, projectID, now, now)
	if err != nil {
		return fmt.Errorf("failed to bind thread: %w", err)
	}
	return nil
}

// GetProjectByThread returns the project bound to a thread, if any.
func (s *Store) GetProjectByThread(channel, threadTS string) (*Project, error) {
	var projectID sql.NullString
	err := s.ds.DB().QueryRow(
		`SELECT project_id FROM thread_sessions WHERE channel = ? AND thread_ts = ? AND project_id IS NOT NULL`,
		channel, threadTS,
	).Scan(&projectID)
	if err == sql.ErrNoRows || !projectID.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project by thread: %w", err)
	}
	return s.GetProjectByID(projectID.String)
}

// TouchProject updates the project's updated_at timestamp to now.
func (s *Store) TouchProject(slug string) error {
	now := time.Now().UnixMilli()
	_, err := s.ds.DB().Exec(`UPDATE projects SET updated_at = ? WHERE slug = ?`, now, slug)
	if err != nil {
		return fmt.Errorf("failed to touch project: %w", err)
	}
	return nil
}

// UpdateActiveSession updates the active session key and version for a project.
func (s *Store) UpdateActiveSession(projectID, sessionKey string, version int) error {
	now := time.Now().UnixMilli()
	_, err := s.ds.DB().Exec(
		`UPDATE projects SET active_session = ?, session_version = ?, updated_at = ? WHERE id = ?`,
		sessionKey, version, now, projectID,
	)
	if err != nil {
		return fmt.Errorf("failed to update active session: %w", err)
	}
	return nil
}
