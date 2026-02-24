package project

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/p-blackswan/platform-agent/internal/store"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	logger := zerolog.Nop()
	ds, err := store.New(":memory:", logger)
	require.NoError(t, err)
	t.Cleanup(func() { ds.Close() })
	return NewStore(ds, logger)
}

func TestCreateAndGetProject(t *testing.T) {
	s := setupTestStore(t)

	p, err := s.CreateProject(CreateProjectInput{
		Name:    "Leader Election",
		OwnerID: "U123",
		RepoURL: "https://github.com/test/repo",
	})
	require.NoError(t, err)
	assert.Equal(t, "leader-election", p.Slug)
	assert.Equal(t, "active", p.Status)
	assert.Equal(t, "agent:main:project-leader-election", p.ActiveSession)

	got, err := s.GetProject("leader-election")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, "https://github.com/test/repo", got.RepoURL)

	gotByID, err := s.GetProjectByID(p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.Slug, gotByID.Slug)
}

func TestCreateProject_DuplicateSlug(t *testing.T) {
	s := setupTestStore(t)
	_, err := s.CreateProject(CreateProjectInput{Name: "Test", OwnerID: "U1"})
	require.NoError(t, err)
	_, err = s.CreateProject(CreateProjectInput{Name: "Test", OwnerID: "U1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCreateProject_ReservedSlug(t *testing.T) {
	s := setupTestStore(t)
	_, err := s.CreateProject(CreateProjectInput{Name: "projects", OwnerID: "U1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reserved word")
}

func TestListProjects(t *testing.T) {
	s := setupTestStore(t)
	_, _ = s.CreateProject(CreateProjectInput{Name: "Alpha", OwnerID: "U1"})
	_, _ = s.CreateProject(CreateProjectInput{Name: "Beta", OwnerID: "U2"})

	all, err := s.ListProjects("", "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	byOwner, err := s.ListProjects("", "U1")
	require.NoError(t, err)
	assert.Len(t, byOwner, 1)
}

func TestArchiveAndResume(t *testing.T) {
	s := setupTestStore(t)
	p, _ := s.CreateProject(CreateProjectInput{Name: "Test Proj", OwnerID: "U1"})

	err := s.ArchiveProject(p.Slug, "U1")
	require.NoError(t, err)

	got, _ := s.GetProject(p.Slug)
	assert.Equal(t, "archived", got.Status)

	resumed, err := s.ResumeProject(p.Slug, "U1")
	require.NoError(t, err)
	assert.Equal(t, "active", resumed.Status)
}

func TestDeleteProject(t *testing.T) {
	s := setupTestStore(t)
	p, _ := s.CreateProject(CreateProjectInput{Name: "Delete Me", OwnerID: "U1"})
	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "decision", Content: "test"})
	_ = s.AddEvent(&ProjectEvent{ProjectID: p.ID, EventType: "created", ActorID: "U1"})

	err := s.DeleteProject(p.Slug)
	require.NoError(t, err)

	got, _ := s.GetProject(p.Slug)
	assert.Nil(t, got)
}

func TestMemory(t *testing.T) {
	s := setupTestStore(t)
	p, _ := s.CreateProject(CreateProjectInput{Name: "Mem Test", OwnerID: "U1"})

	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "decision", Content: "use etcd"})
	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "blocker", Content: "waiting on certs"})
	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "decision", Content: "TTL 15s"})

	all, err := s.ListMemory(p.ID, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	decisions, err := s.ListMemory(p.ID, "decision")
	require.NoError(t, err)
	assert.Len(t, decisions, 2)
}

func TestEvents(t *testing.T) {
	s := setupTestStore(t)
	p, _ := s.CreateProject(CreateProjectInput{Name: "Evt Test", OwnerID: "U1"})

	// Creation event already added by CreateProject
	events, err := s.ListEvents(p.ID, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(events), 1)
}

func TestProjectStats(t *testing.T) {
	s := setupTestStore(t)
	p, _ := s.CreateProject(CreateProjectInput{Name: "Stats Test", OwnerID: "U1"})

	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "decision", Content: "d1"})
	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "decision", Content: "d2"})
	_ = s.AddMemory(&ProjectMemory{ProjectID: p.ID, Type: "blocker", Content: "b1"})

	stats, err := s.GetProjectStats(p.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Decisions)
	assert.Equal(t, 1, stats.Blockers)
}

func TestThreadBinding(t *testing.T) {
	s := setupTestStore(t)
	p, _ := s.CreateProject(CreateProjectInput{Name: "Thread Test", OwnerID: "U1"})

	err := s.BindThread("C123", "1234.5678", p.ID, p.ActiveSession)
	require.NoError(t, err)

	found, err := s.GetProjectByThread("C123", "1234.5678")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, p.ID, found.ID)

	// Non-existent thread
	notFound, err := s.GetProjectByThread("C123", "9999.0000")
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestUpdateProject(t *testing.T) {
	s := setupTestStore(t)
	_, _ = s.CreateProject(CreateProjectInput{Name: "Upd Test", OwnerID: "U1"})

	newName := "Updated Name"
	p, err := s.UpdateProject("upd-test", UpdateProjectInput{Name: &newName})
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", p.Name)
}

func TestGetProject_NotFound(t *testing.T) {
	s := setupTestStore(t)
	got, err := s.GetProject("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGenerateSlug(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Leader Election Refactor", "leader-election-refactor"},
		{"CI Pipeline v2", "ci-pipeline-v2"},
		{"Hello!@#World", "helloworld"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, GenerateSlug(tt.name), tt.name)
	}
}
