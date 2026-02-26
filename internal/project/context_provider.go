package project

import (
	"regexp"
)

// sessionKeyProjectRe matches session keys like "agent:main:project-{slug}" or "agent:main:project-{slug}-v{N}"
var sessionKeyProjectRe = regexp.MustCompile(`^agent:main:project-([a-z0-9-]+?)(?:-v\d+)?$`)

// ContextProvider implements bridge.ProjectContextProvider.
type ContextProvider struct {
	store   *Store
	manager *Manager
}

// NewContextProvider creates a new ContextProvider.
func NewContextProvider(store *Store, manager *Manager) *ContextProvider {
	return &ContextProvider{store: store, manager: manager}
}

// GetProjectContextForThread returns the project preamble if this thread is bound to a project.
func (cp *ContextProvider) GetProjectContextForThread(channelID, threadTS string) string {
	proj, err := cp.store.GetProjectByThread(channelID, threadTS)
	if err != nil || proj == nil {
		return ""
	}
	preamble, err := cp.manager.BuildContextPreamble(proj)
	if err != nil {
		return ""
	}
	return preamble
}

// GetProjectContextForSession returns the project preamble if this sessionKey belongs to a project.
func (cp *ContextProvider) GetProjectContextForSession(sessionKey string) string {
	slug := extractSlugFromSessionKey(sessionKey)
	if slug == "" {
		return ""
	}
	proj, err := cp.store.GetProject(slug)
	if err != nil || proj == nil {
		return ""
	}
	preamble, _ := cp.manager.BuildContextPreamble(proj)
	return preamble
}

// extractSlugFromSessionKey parses the project slug from a session key.
func extractSlugFromSessionKey(sessionKey string) string {
	m := sessionKeyProjectRe.FindStringSubmatch(sessionKey)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
