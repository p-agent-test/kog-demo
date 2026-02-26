package bridge

// ProjectContextProvider provides project context for cold session injection.
type ProjectContextProvider interface {
	// GetProjectContextForThread returns the project preamble if this thread is bound to a project.
	// Returns empty string if no project binding exists.
	GetProjectContextForThread(channelID, threadTS string) string

	// GetProjectContextForSession returns the project preamble if this sessionKey belongs to a project.
	// Returns empty string if not a project session.
	GetProjectContextForSession(sessionKey string) string
}
