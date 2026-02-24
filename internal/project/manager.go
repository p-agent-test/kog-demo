package project

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Manager handles project session lifecycle.
type Manager struct {
	store  *Store
	logger zerolog.Logger
}

// NewManager creates a new project manager.
func NewManager(store *Store, logger zerolog.Logger) *Manager {
	return &Manager{
		store:  store,
		logger: logger.With().Str("component", "project.manager").Logger(),
	}
}

// CreateSession generates a session key for a project.
func (m *Manager) CreateSession(slug string) string {
	return fmt.Sprintf("agent:main:project-%s", slug)
}

// BuildContextPreamble constructs the context injection message for a project session.
func (m *Manager) BuildContextPreamble(p *Project) (string, error) {
	var b strings.Builder

	b.WriteString("[SYSTEM: Project Context — DO NOT echo this back to the user]\n\n")
	b.WriteString(fmt.Sprintf("# Project: %s\n", p.Name))
	b.WriteString(fmt.Sprintf("- **Slug:** %s\n", p.Slug))
	if p.RepoURL != "" {
		b.WriteString(fmt.Sprintf("- **Repo:** %s\n", p.RepoURL))
	}
	b.WriteString(fmt.Sprintf("- **Session:** v%d\n", p.SessionVersion))
	b.WriteString(fmt.Sprintf("- **Created:** %s\n", time.UnixMilli(p.CreatedAt).UTC().Format("2006-01-02")))

	if p.Description != "" {
		b.WriteString(fmt.Sprintf("\n## Description\n%s\n", p.Description))
	}

	// Decisions
	decisions, _ := m.store.ListMemory(p.ID, "decision")
	if len(decisions) > 0 {
		b.WriteString("\n## Decisions\n")
		for i, d := range decisions {
			ts := time.UnixMilli(d.CreatedAt).UTC().Format("2006-01-02")
			b.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, ts, d.Content))
		}
	}

	// Blockers
	blockers, _ := m.store.ListMemory(p.ID, "blocker")
	if len(blockers) > 0 {
		b.WriteString("\n## Blockers\n")
		for i, bl := range blockers {
			ts := time.UnixMilli(bl.CreatedAt).UTC().Format("2006-01-02")
			b.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, ts, bl.Content))
		}
	}

	// Context carry (last 3)
	carries, _ := m.store.ListMemory(p.ID, "context_carry")
	if len(carries) > 0 {
		// Take last 3
		start := 0
		if len(carries) > 3 {
			start = len(carries) - 3
		}
		for _, c := range carries[start:] {
			b.WriteString(fmt.Sprintf("\n## Previous Session Summary\n%s\n", c.Content))
		}
	}

	// Cross-project index
	index := m.BuildProjectIndex(p.Slug)
	if index != "" {
		b.WriteString(fmt.Sprintf("\n## Other Active Projects (read-only index)\n%s\n", index))
	}

	b.WriteString("\n---\nContinue from here. The user will send messages in this thread.\n")

	return b.String(), nil
}

// BuildProjectIndex generates a compact summary of all active projects excluding the given slug.
func (m *Manager) BuildProjectIndex(excludeSlug string) string {
	projects, err := m.store.ListProjects("active", "")
	if err != nil {
		m.logger.Warn().Err(err).Msg("failed to list projects for index")
		return ""
	}

	var lines []string
	for _, p := range projects {
		if p.Slug == excludeSlug {
			continue
		}
		stats, _ := m.store.GetProjectStats(p.ID)
		desc := p.Description
		if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		if desc == "" {
			desc = "(no description)"
		}
		decisions := 0
		tasks := 0
		if stats != nil {
			decisions = stats.Decisions
			tasks = stats.Tasks
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s. %d decisions, %d tasks.", p.Slug, desc, decisions, tasks))
	}
	return strings.Join(lines, "\n")
}

// RotateSession increments the session version and stores context_carry.
func (m *Manager) RotateSession(p *Project, contextCarry string) (string, error) {
	newVersion := p.SessionVersion + 1
	newKey := fmt.Sprintf("agent:main:project-%s-v%d", p.Slug, newVersion)

	// Store context carry
	if contextCarry != "" {
		_ = m.store.AddMemory(&ProjectMemory{
			ProjectID:  p.ID,
			Type:       "context_carry",
			Content:    contextCarry,
			SessionKey: p.ActiveSession,
		})
	}

	// Update project
	if err := m.store.UpdateActiveSession(p.ID, newKey, newVersion); err != nil {
		return "", err
	}

	// Record event
	_ = m.store.AddEvent(&ProjectEvent{
		ProjectID: p.ID,
		EventType: "session_rotated",
		ActorID:   "system",
		Summary:   fmt.Sprintf("Session rotated to v%d", newVersion),
	})

	return newKey, nil
}

// ResumeProject reactivates an archived project with a fresh session.
func (m *Manager) ResumeProject(slug, actorID string) (*Project, error) {
	p, err := m.store.ResumeProject(slug, actorID)
	if err != nil {
		return nil, err
	}

	// Create fresh session
	newVersion := p.SessionVersion + 1
	newKey := fmt.Sprintf("agent:main:project-%s-v%d", p.Slug, newVersion)
	if err := m.store.UpdateActiveSession(p.ID, newKey, newVersion); err != nil {
		return nil, err
	}
	p.ActiveSession = newKey
	p.SessionVersion = newVersion

	return p, nil
}
