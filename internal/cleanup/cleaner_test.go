package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
)

// mockPoster implements SlackPoster for testing.
type mockPoster struct {
	posted  []postCall
	updated []updateCall
}

type postCall struct {
	channelID, text, threadTS string
}

type updateCall struct {
	channelID, messageTS, text string
}

func (m *mockPoster) PostMessage(channelID, text, threadTS string) (string, error) {
	m.posted = append(m.posted, postCall{channelID, text, threadTS})
	return "msg-ts-1", nil
}

func (m *mockPoster) PostBlocks(channelID, threadTS, fallbackText string, blocks ...slack.Block) (string, error) {
	m.posted = append(m.posted, postCall{channelID, fallbackText, threadTS})
	return "msg-ts-1", nil
}

func (m *mockPoster) UpdateMessage(channelID, messageTS, text string) error {
	m.updated = append(m.updated, updateCall{channelID, messageTS, text})
	return nil
}

// mockSessionDB implements SessionDB for testing.
type mockSessionDB struct {
	staleSessions    []StaleSession
	deletedThreads   []string
	deletedContexts  []string
	touchedThreads   []string
	touchedContexts  []string
}

func (m *mockSessionDB) GetStaleSessions(cutoffMs int64) ([]StaleSession, error) {
	return m.staleSessions, nil
}

func (m *mockSessionDB) DeleteThreadSession(channel, threadTS string) error {
	m.deletedThreads = append(m.deletedThreads, channel+":"+threadTS)
	return nil
}

func (m *mockSessionDB) DeleteSessionContext(sessionID string) error {
	m.deletedContexts = append(m.deletedContexts, sessionID)
	return nil
}

func (m *mockSessionDB) TouchThreadSession(channel, threadTS string) error {
	m.touchedThreads = append(m.touchedThreads, channel+":"+threadTS)
	return nil
}

func (m *mockSessionDB) TouchSessionContext(sessionID string) error {
	m.touchedContexts = append(m.touchedContexts, sessionID)
	return nil
}

func (m *mockSessionDB) LogAudit(userID, action, resource, result, details string) error {
	return nil
}

func TestWarnStaleSessions(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)
	poster := &mockPoster{}
	sessionDB := &mockSessionDB{
		staleSessions: []StaleSession{
			{SessionKey: "s1", ChannelID: "C1", ThreadTS: "T1", LastMessageAt: time.Now().Add(-4 * 24 * time.Hour).UnixMilli()},
		},
	}

	cleaner := NewCleaner(DefaultConfig(), store, sessionDB, poster, zerolog.Nop())

	err := cleaner.WarnStaleSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(poster.posted) != 1 {
		t.Fatalf("expected 1 post, got %d", len(poster.posted))
	}

	// Should have saved a warning
	rec, _ := store.GetWarningBySession("s1")
	if rec == nil {
		t.Error("expected warning record to be saved")
	}
}

func TestWarnSkipsAlreadyWarned(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)
	poster := &mockPoster{}
	sessionDB := &mockSessionDB{
		staleSessions: []StaleSession{
			{SessionKey: "s1", ChannelID: "C1", ThreadTS: "T1", LastMessageAt: time.Now().Add(-4 * 24 * time.Hour).UnixMilli()},
		},
	}

	// Pre-existing warning
	_ = store.SaveWarning("s1", "C1", "T1", "", 24*time.Hour)

	cleaner := NewCleaner(DefaultConfig(), store, sessionDB, poster, zerolog.Nop())
	_ = cleaner.WarnStaleSessions(context.Background())

	if len(poster.posted) != 0 {
		t.Errorf("expected 0 posts (already warned), got %d", len(poster.posted))
	}
}

func TestProcessExpiredWarnings(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)
	poster := &mockPoster{}
	sessionDB := &mockSessionDB{}

	// Save a warning that's already expired
	_ = store.SaveWarning("s1", "C1", "T1", "msg-ts", 0)

	cleaner := NewCleaner(DefaultConfig(), store, sessionDB, poster, zerolog.Nop())
	err := cleaner.ProcessExpiredWarnings(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(sessionDB.deletedThreads) != 1 {
		t.Errorf("expected 1 deleted thread, got %d", len(sessionDB.deletedThreads))
	}
	if len(poster.updated) != 1 {
		t.Errorf("expected 1 update, got %d", len(poster.updated))
	}
}

func TestKeepSession(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)
	poster := &mockPoster{}
	sessionDB := &mockSessionDB{}

	_ = store.SaveWarning("s1", "C1", "T1", "msg-ts", 24*time.Hour)

	cleaner := NewCleaner(DefaultConfig(), store, sessionDB, poster, zerolog.Nop())
	err := cleaner.KeepSession("s1")
	if err != nil {
		t.Fatal(err)
	}

	if len(sessionDB.touchedThreads) != 1 {
		t.Errorf("expected 1 touched thread, got %d", len(sessionDB.touchedThreads))
	}
	if len(poster.updated) != 1 {
		t.Errorf("expected 1 message update, got %d", len(poster.updated))
	}
}

func TestCloseSession(t *testing.T) {
	db := setupTestDB(t)
	store := NewCleanupStore(db)
	poster := &mockPoster{}
	sessionDB := &mockSessionDB{}

	_ = store.SaveWarning("s1", "C1", "T1", "msg-ts", 24*time.Hour)

	cleaner := NewCleaner(DefaultConfig(), store, sessionDB, poster, zerolog.Nop())
	err := cleaner.CloseSession("s1")
	if err != nil {
		t.Fatal(err)
	}

	if len(sessionDB.deletedThreads) != 1 {
		t.Errorf("expected 1 deleted thread, got %d", len(sessionDB.deletedThreads))
	}
	if len(sessionDB.deletedContexts) != 1 {
		t.Errorf("expected 1 deleted context, got %d", len(sessionDB.deletedContexts))
	}
}
