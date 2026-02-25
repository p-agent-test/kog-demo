package cleanup

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
)

// SlackPoster abstracts posting messages to Slack.
type SlackPoster interface {
	PostMessage(channelID string, text string, threadTS string) (string, error)
	PostBlocks(channelID string, threadTS string, fallbackText string, blocks ...slack.Block) (string, error)
	UpdateMessage(channelID string, messageTS string, text string) error
}

// SlackBlockUpdater can update a message with blocks (optional, for richer updates).
type SlackBlockUpdater interface {
	UpdateMessageBlocks(channelID string, messageTS string, blocks []slack.Block) error
}

// SessionDB abstracts access to thread_sessions and session_contexts for cleanup.
type SessionDB interface {
	// GetStaleSessions returns thread sessions with last_message_at older than the cutoff.
	GetStaleSessions(cutoffMs int64) ([]StaleSession, error)
	// DeleteThreadSession removes a thread session.
	DeleteThreadSession(channel, threadTS string) error
	// DeleteSessionContext removes a session context.
	DeleteSessionContext(sessionID string) error
	// TouchThreadSession resets last_message_at to now.
	TouchThreadSession(channel, threadTS string) error
	// TouchSessionContext resets last_used to now.
	TouchSessionContext(sessionID string) error
	// LogAudit writes to audit_log.
	LogAudit(userID, action, resource, result, details string) error
}

// Cleaner manages the session cleanup lifecycle.
type Cleaner struct {
	cfg       CleanupConfig
	store     *CleanupStore
	sessionDB SessionDB
	poster    SlackPoster
	logger    zerolog.Logger
}

// NewCleaner creates a new Cleaner.
func NewCleaner(cfg CleanupConfig, store *CleanupStore, sessionDB SessionDB, poster SlackPoster, logger zerolog.Logger) *Cleaner {
	return &Cleaner{
		cfg:       cfg,
		store:     store,
		sessionDB: sessionDB,
		poster:    poster,
		logger:    logger.With().Str("component", "cleanup").Logger(),
	}
}

// FindStaleSessions queries for sessions with no activity in StaleThresholdDays,
// excluding those already warned or recently kept.
func (c *Cleaner) FindStaleSessions() ([]StaleSession, error) {
	cutoff := time.Now().UnixMilli() - int64(c.cfg.StaleThresholdDays)*24*60*60*1000
	sessions, err := c.sessionDB.GetStaleSessions(cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to get stale sessions: %w", err)
	}

	// Filter out sessions that already have recent warnings
	var result []StaleSession
	for _, s := range sessions {
		has, err := c.store.HasRecentWarning(s.SessionKey, 7*24*time.Hour)
		if err != nil {
			c.logger.Warn().Err(err).Str("session", s.SessionKey).Msg("failed to check recent warning")
			continue
		}
		if !has {
			result = append(result, s)
		}
	}
	return result, nil
}

// WarnStaleSessions finds stale sessions and sends Slack warnings with Keep/Close buttons.
func (c *Cleaner) WarnStaleSessions(ctx context.Context) error {
	sessions, err := c.FindStaleSessions()
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		c.logger.Debug().Msg("no stale sessions found")
		return nil
	}

	c.logger.Info().Int("count", len(sessions)).Msg("found stale sessions")

	for _, s := range sessions {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		lastActivity := time.UnixMilli(s.LastMessageAt)
		blocks := WarningBlocks(s.SessionKey, s.ChannelID, s.ThreadTS, lastActivity, c.cfg.StaleThresholdDays)

		msgTS, err := c.poster.PostBlocks(s.ChannelID, s.ThreadTS, "⏰ Session inaktif — Devam Et / Kapat", blocks...)
		if err != nil {
			c.logger.Error().Err(err).Str("session", s.SessionKey).Msg("failed to post warning")
			continue
		}

		if err := c.store.SaveWarning(s.SessionKey, s.ChannelID, s.ThreadTS, msgTS, c.cfg.WarningTTL); err != nil {
			c.logger.Error().Err(err).Str("session", s.SessionKey).Msg("failed to save warning record")
		}

		c.logger.Info().Str("session", s.SessionKey).Str("channel", s.ChannelID).Msg("stale session warned")
	}

	return nil
}

// ProcessExpiredWarnings auto-closes sessions where 24h passed without response.
func (c *Cleaner) ProcessExpiredWarnings(ctx context.Context) error {
	expired, err := c.store.GetExpiredWarnings()
	if err != nil {
		return err
	}

	if len(expired) == 0 {
		return nil
	}

	c.logger.Info().Int("count", len(expired)).Msg("processing expired warnings")

	for _, rec := range expired {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Close the session
		if err := c.closeSessionInternal(rec.SessionKey, rec.ChannelID, rec.ThreadTS); err != nil {
			c.logger.Error().Err(err).Str("session", rec.SessionKey).Msg("failed to close expired session")
			continue
		}

		// Mark as closed
		if err := c.store.MarkClosed(rec.SessionKey); err != nil {
			c.logger.Error().Err(err).Str("session", rec.SessionKey).Msg("failed to mark closed")
		}

		// Update the warning message
		if rec.MessageTS != "" {
			blocks := ExpiredBlocks()
			if updater, ok := c.poster.(SlackBlockUpdater); ok {
				_ = updater.UpdateMessageBlocks(rec.ChannelID, rec.MessageTS, blocks)
			} else {
				_ = c.poster.UpdateMessage(rec.ChannelID, rec.MessageTS, "⏰ 24 saat içinde yanıt verilmedi — session otomatik kapatıldı")
			}
		}

		c.logger.Info().Str("session", rec.SessionKey).Msg("expired session auto-closed")
	}

	return nil
}

// KeepSession handles "Devam Et" button — extends session by resetting activity timestamp.
func (c *Cleaner) KeepSession(sessionKey string) error {
	rec, err := c.store.GetWarningBySession(sessionKey)
	if err != nil {
		return fmt.Errorf("failed to get warning: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("no active warning for session: %s", sessionKey)
	}

	// Mark as kept
	if err := c.store.MarkKept(sessionKey); err != nil {
		return err
	}

	// Reset activity timestamps so the stale clock restarts
	_ = c.sessionDB.TouchThreadSession(rec.ChannelID, rec.ThreadTS)
	_ = c.sessionDB.TouchSessionContext(sessionKey)

	// Update the warning message
	if rec.MessageTS != "" {
		if updater, ok := c.poster.(SlackBlockUpdater); ok {
			_ = updater.UpdateMessageBlocks(rec.ChannelID, rec.MessageTS, KeptBlocks())
		} else {
			_ = c.poster.UpdateMessage(rec.ChannelID, rec.MessageTS, "✅ Session devam edecek (7 gün daha)")
		}
	}

	c.logger.Info().Str("session", sessionKey).Msg("session kept (extended 7 days)")
	return nil
}

// CloseSession handles "Kapat" button — cleans up session data.
func (c *Cleaner) CloseSession(sessionKey string) error {
	rec, err := c.store.GetWarningBySession(sessionKey)
	if err != nil {
		return fmt.Errorf("failed to get warning: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("no active warning for session: %s", sessionKey)
	}

	// Mark as closed
	if err := c.store.MarkClosed(sessionKey); err != nil {
		return err
	}

	// Close the session
	if err := c.closeSessionInternal(sessionKey, rec.ChannelID, rec.ThreadTS); err != nil {
		return err
	}

	// Update the warning message
	if rec.MessageTS != "" {
		if updater, ok := c.poster.(SlackBlockUpdater); ok {
			_ = updater.UpdateMessageBlocks(rec.ChannelID, rec.MessageTS, ClosedBlocks())
		} else {
			_ = c.poster.UpdateMessage(rec.ChannelID, rec.MessageTS, "🗑️ Session kapatıldı")
		}
	}

	c.logger.Info().Str("session", sessionKey).Msg("session closed by user")
	return nil
}

func (c *Cleaner) closeSessionInternal(sessionKey, channelID, threadTS string) error {
	// Delete thread session
	if err := c.sessionDB.DeleteThreadSession(channelID, threadTS); err != nil {
		c.logger.Warn().Err(err).Msg("failed to delete thread session")
	}

	// Delete session context
	if err := c.sessionDB.DeleteSessionContext(sessionKey); err != nil {
		c.logger.Warn().Err(err).Msg("failed to delete session context")
	}

	// Log to audit
	_ = c.sessionDB.LogAudit("system", "session_cleanup", sessionKey, "closed", fmt.Sprintf("channel=%s thread=%s", channelID, threadTS))

	return nil
}

// storeSessionDB adapts *sql.DB to SessionDB interface for use with the main Store.
type storeSessionDB struct {
	db *sql.DB
}

// NewStoreSessionDB creates a SessionDB adapter from a *sql.DB.
func NewStoreSessionDB(db *sql.DB) SessionDB {
	return &storeSessionDB{db: db}
}

func (s *storeSessionDB) GetStaleSessions(cutoffMs int64) ([]StaleSession, error) {
	query := `
	SELECT channel, thread_ts, session_key, last_message_at
	FROM thread_sessions
	WHERE last_message_at < ?
	`

	rows, err := s.db.Query(query, cutoffMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []StaleSession
	for rows.Next() {
		var ss StaleSession
		if err := rows.Scan(&ss.ChannelID, &ss.ThreadTS, &ss.SessionKey, &ss.LastMessageAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, ss)
	}
	return sessions, rows.Err()
}

func (s *storeSessionDB) DeleteThreadSession(channel, threadTS string) error {
	_, err := s.db.Exec(`DELETE FROM thread_sessions WHERE channel = ? AND thread_ts = ?`, channel, threadTS)
	return err
}

func (s *storeSessionDB) DeleteSessionContext(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM session_contexts WHERE session_id = ?`, sessionID)
	return err
}

func (s *storeSessionDB) TouchThreadSession(channel, threadTS string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`UPDATE thread_sessions SET last_message_at = ? WHERE channel = ? AND thread_ts = ?`, now, channel, threadTS)
	return err
}

func (s *storeSessionDB) TouchSessionContext(sessionID string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`UPDATE session_contexts SET last_used = ? WHERE session_id = ?`, now, sessionID)
	return err
}

func (s *storeSessionDB) LogAudit(userID, action, resource, result, details string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`INSERT INTO audit_log (user_id, action, resource, result, details, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, action, resource, result, details, now)
	return err
}
