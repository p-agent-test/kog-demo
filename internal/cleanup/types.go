package cleanup

import "time"

// CleanupConfig holds configuration for the session cleanup system.
type CleanupConfig struct {
	StaleThresholdDays int           // default 3
	WarningTTL         time.Duration // default 24h
	CheckInterval      time.Duration // default 1h
}

// DefaultConfig returns sane defaults.
func DefaultConfig() CleanupConfig {
	return CleanupConfig{
		StaleThresholdDays: 3,
		WarningTTL:         24 * time.Hour,
		CheckInterval:      1 * time.Hour,
	}
}

// CleanupRecord represents a row in the session_cleanup table.
type CleanupRecord struct {
	ID          string
	SessionKey  string
	ChannelID   string
	ThreadTS    string
	Status      string // warned | kept | closed
	WarnedAt    int64  // Unix ms
	RespondedAt int64  // Unix ms
	ExpiresAt   int64  // Unix ms
	MessageTS   string
	CreatedAt   int64  // Unix ms
}

// StaleSession represents a session that has been inactive.
type StaleSession struct {
	SessionKey    string
	ChannelID     string
	ThreadTS      string
	LastMessageAt int64 // Unix ms
}
