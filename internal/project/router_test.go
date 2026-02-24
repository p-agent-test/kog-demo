package project

import (
	"context"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/p-blackswan/platform-agent/internal/store"
)

// mockBridge captures calls for testing.
type mockBridge struct {
	mu                sync.Mutex
	messages          []mockMsg
	sessionMessages   []mockSessionMsg
	activeThreads     map[string]bool
}

type mockMsg struct {
	channelID, userID, text, threadTS, messageTS string
}

type mockSessionMsg struct {
	channelID, userID, text, threadTS, messageTS, sessionKey string
}

func newMockBridge() *mockBridge {
	return &mockBridge{activeThreads: make(map[string]bool)}
}

func (m *mockBridge) HandleMessage(_ context.Context, channelID, userID, text, threadTS, messageTS string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, mockMsg{channelID, userID, text, threadTS, messageTS})
}

func (m *mockBridge) HandleMessageWithSession(_ context.Context, channelID, userID, text, threadTS, messageTS, sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionMessages = append(m.sessionMessages, mockSessionMsg{channelID, userID, text, threadTS, messageTS, sessionKey})
}

func (m *mockBridge) IsActiveThread(channelID, threadTS string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeThreads[channelID+":"+threadTS]
}

// mockPoster captures posted messages.
type mockPoster struct {
	mu       sync.Mutex
	messages []postedMsg
	blocks   []postedBlockMsg
}

type postedMsg struct {
	channel, text, thread string
}

type postedBlockMsg struct {
	channel, thread, fallback string
	blockCount               int
}

func (m *mockPoster) PostMessage(channelID, text, threadTS string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, postedMsg{channelID, text, threadTS})
	return "msg-ts", nil
}

func (m *mockPoster) PostBlocks(channelID, threadTS, fallbackText string, blocks ...slack.Block) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks = append(m.blocks, postedBlockMsg{channelID, threadTS, fallbackText, len(blocks)})
	return "msg-ts", nil
}

func setupTestRouter(t *testing.T) (*Router, *mockBridge, *mockPoster, *Store) {
	t.Helper()
	logger := zerolog.Nop()
	ds, err := store.New(":memory:", logger)
	require.NoError(t, err)
	t.Cleanup(func() { ds.Close() })

	s := NewStore(ds, logger)
	mgr := NewManager(s, logger)
	mb := newMockBridge()
	mp := &mockPoster{}
	r := NewRouter(s, mgr, mb, mp, "UBOT", logger)
	return r, mb, mp, s
}

func TestRouter_DefaultFallthrough(t *testing.T) {
	r, mb, _, _ := setupTestRouter(t)
	ctx := context.Background()

	r.HandleMessage(ctx, "C1", "U1", "<@UBOT> hello world", "", "ts1")

	mb.mu.Lock()
	defer mb.mu.Unlock()
	assert.Len(t, mb.messages, 1)
	assert.Equal(t, "<@UBOT> hello world", mb.messages[0].text)
}

func TestRouter_SlugRouting(t *testing.T) {
	r, mb, _, s := setupTestRouter(t)
	ctx := context.Background()

	// Create a project
	_, _ = s.CreateProject(CreateProjectInput{Name: "My Project", OwnerID: "U1"})

	// Message to project slug
	r.HandleMessage(ctx, "C1", "U1", "<@UBOT> my-project what's the status?", "", "ts1")

	mb.mu.Lock()
	defer mb.mu.Unlock()
	assert.Len(t, mb.sessionMessages, 1)
	assert.Equal(t, "agent:main:project-my-project", mb.sessionMessages[0].sessionKey)
	assert.Equal(t, "what's the status?", mb.sessionMessages[0].text)
}

func TestRouter_ListProjects(t *testing.T) {
	r, _, mp, s := setupTestRouter(t)
	ctx := context.Background()

	_, _ = s.CreateProject(CreateProjectInput{Name: "Alpha", OwnerID: "U1"})

	r.HandleMessage(ctx, "C1", "U1", "<@UBOT> projects", "", "ts1")

	mp.mu.Lock()
	defer mp.mu.Unlock()
	require.Len(t, mp.blocks, 1)
	assert.Contains(t, mp.blocks[0].fallback, "1 Active Projects")
	assert.True(t, mp.blocks[0].blockCount > 0)
}

func TestRouter_ThreadBinding(t *testing.T) {
	r, mb, _, s := setupTestRouter(t)
	ctx := context.Background()

	p, _ := s.CreateProject(CreateProjectInput{Name: "Thread Proj", OwnerID: "U1"})
	_ = s.BindThread("C1", "thread1", p.ID, p.ActiveSession)

	r.HandleMessage(ctx, "C1", "U1", "some message in thread", "thread1", "ts2")

	mb.mu.Lock()
	defer mb.mu.Unlock()
	assert.Len(t, mb.sessionMessages, 1)
	assert.Equal(t, p.ActiveSession, mb.sessionMessages[0].sessionKey)
}

func TestRouter_IsActiveThread_ProjectBound(t *testing.T) {
	r, _, _, s := setupTestRouter(t)

	p, _ := s.CreateProject(CreateProjectInput{Name: "Active Thread", OwnerID: "U1"})
	_ = s.BindThread("C1", "t1", p.ID, p.ActiveSession)

	assert.True(t, r.IsActiveThread("C1", "t1"))
	assert.False(t, r.IsActiveThread("C1", "t999"))
}

func TestRouter_NewProject(t *testing.T) {
	r, _, mp, s := setupTestRouter(t)
	ctx := context.Background()

	r.HandleMessage(ctx, "C1", "U1", `<@UBOT> new project "Test Proj" --repo https://github.com/x`, "", "ts1")

	mp.mu.Lock()
	defer mp.mu.Unlock()
	require.Len(t, mp.blocks, 1)
	assert.Contains(t, mp.blocks[0].fallback, "test-proj")

	p, _ := s.GetProject("test-proj")
	assert.NotNil(t, p)
}

func TestRouter_ArchiveAndResume(t *testing.T) {
	r, _, mp, s := setupTestRouter(t)
	ctx := context.Background()

	_, _ = s.CreateProject(CreateProjectInput{Name: "Lifecycle", OwnerID: "U1"})

	r.HandleMessage(ctx, "C1", "U1", "<@UBOT> archive lifecycle", "", "ts1")
	mp.mu.Lock()
	assert.Contains(t, mp.messages[0].text, "archived")
	mp.mu.Unlock()

	r.HandleMessage(ctx, "C1", "U1", "<@UBOT> resume lifecycle", "", "ts2")
	mp.mu.Lock()
	assert.Contains(t, mp.messages[1].text, "resumed")
	mp.mu.Unlock()
}
