package project

// SessionResolver implements mgmt.ProjectSessionResolver using project store.
type SessionResolver struct {
	store *Store
}

// NewSessionResolver creates a new resolver backed by the project store.
func NewSessionResolver(s *Store) *SessionResolver {
	return &SessionResolver{store: s}
}

// GetSessionKeyByThread returns the active session key for a project bound to this thread.
func (r *SessionResolver) GetSessionKeyByThread(channel, threadTS string) string {
	proj, err := r.store.GetProjectByThread(channel, threadTS)
	if err != nil || proj == nil {
		return ""
	}
	return proj.ActiveSession
}
