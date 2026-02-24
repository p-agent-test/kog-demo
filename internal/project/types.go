package project

// Project represents a named project workspace.
type Project struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	RepoURL        string `json:"repo_url,omitempty"`
	Status         string `json:"status"`          // active | paused | archived
	OwnerID        string `json:"owner_id"`
	ActiveSession  string `json:"active_session"`
	SessionVersion int    `json:"session_version"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	ArchivedAt     int64  `json:"archived_at,omitempty"`
}

// ProjectMemory represents a memory entry for a project.
type ProjectMemory struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	Type       string `json:"type"`    // summary | decision | blocker | context_carry
	Content    string `json:"content"`
	SessionKey string `json:"session_key,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// ProjectEvent represents an event in a project's lifecycle.
type ProjectEvent struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	EventType string `json:"event_type"` // created | session_rotated | task_completed | archived | resumed | message
	ActorID   string `json:"actor_id"`
	Summary   string `json:"summary"`
	Metadata  string `json:"metadata,omitempty"` // JSON
	CreatedAt int64  `json:"created_at"`
}

// ProjectStats holds aggregate statistics for a project.
type ProjectStats struct {
	Decisions int `json:"decisions"`
	Blockers  int `json:"blockers"`
	Summaries int `json:"summaries"`
	Events    int `json:"events"`
	Tasks     int `json:"tasks"`
}

// CreateProjectInput holds the parameters for creating a new project.
type CreateProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoURL     string `json:"repo_url,omitempty"`
	OwnerID     string `json:"owner_id"`
}

// UpdateProjectInput holds the parameters for updating a project.
type UpdateProjectInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	RepoURL     *string `json:"repo_url,omitempty"`
}
