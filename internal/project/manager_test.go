package project

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/p-blackswan/platform-agent/internal/store"
)

func setupTestManager(t *testing.T) (*Manager, *Store) {
	t.Helper()
	logger := zerolog.Nop()
	ds, err := store.New(":memory:", logger)
	require.NoError(t, err)
	t.Cleanup(func() { ds.Close() })
	s := NewStore(ds, logger)
	m := NewManager(s, logger)
	return m, s
}

func TestCreateSession(t *testing.T) {
	m, _ := setupTestManager(t)
	assert.Equal(t, "agent:main:project-test-slug", m.CreateSession("test-slug"))
}

func TestBuildContextPreamble(t *testing.T) {
	m, s := setupTestManager(t)

	p, _ := s.CreateProject(CreateProjectInput{
		Name:        "Test Project",
		Description: "A test project",
		RepoURL:     "https://github.com/test/repo",
		OwnerID:     "U1",
	})

	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "decision", Content: "use Go"})
	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "blocker", Content: "waiting on API"})

	preamble, err := m.BuildContextPreamble(p)
	require.NoError(t, err)

	assert.Contains(t, preamble, "Test Project")
	assert.Contains(t, preamble, "test-project")
	assert.Contains(t, preamble, "github.com/test/repo")
	assert.Contains(t, preamble, "use Go")
	assert.Contains(t, preamble, "waiting on API")
	assert.Contains(t, preamble, "Decisions")
	assert.Contains(t, preamble, "Blockers")
}

func TestBuildProjectIndex(t *testing.T) {
	m, s := setupTestManager(t)

	_, _ = s.CreateProject(CreateProjectInput{Name: "Alpha", OwnerID: "U1", Description: "First project"})
	_, _ = s.CreateProject(CreateProjectInput{Name: "Beta", OwnerID: "U1", Description: "Second project"})

	idx := m.BuildProjectIndex("alpha")
	assert.Contains(t, idx, "beta")
	assert.NotContains(t, idx, "**alpha**")
}

func TestRotateSession(t *testing.T) {
	m, s := setupTestManager(t)

	p, _ := s.CreateProject(CreateProjectInput{Name: "Rotate Test", OwnerID: "U1"})
	assert.Equal(t, 1, p.SessionVersion)

	newKey, err := m.RotateSession(p, "summary of session v1")
	require.NoError(t, err)
	assert.Equal(t, "agent:main:project-rotate-test-v2", newKey)

	// Check context carry was stored
	mems, _ := s.ListMemory(p.ID, "context_carry")
	assert.Len(t, mems, 1)
	assert.Equal(t, "summary of session v1", mems[0].Content)

	// Check project was updated
	updated, _ := s.GetProject(p.Slug)
	assert.Equal(t, 2, updated.SessionVersion)
	assert.Equal(t, newKey, updated.ActiveSession)
}

func TestResumeProject(t *testing.T) {
	m, s := setupTestManager(t)

	p, _ := s.CreateProject(CreateProjectInput{Name: "Resume Test", OwnerID: "U1"})
	_ = s.ArchiveProject(p.Slug, "U1")

	resumed, err := m.ResumeProject(p.Slug, "U1")
	require.NoError(t, err)
	assert.Equal(t, "active", resumed.Status)
	assert.Equal(t, 2, resumed.SessionVersion)
	assert.True(t, strings.Contains(resumed.ActiveSession, "v2"))
}
