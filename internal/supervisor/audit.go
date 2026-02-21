package supervisor

import (
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/models"
)

// AuditLog records all access events.
type AuditLog struct {
	mu      sync.RWMutex
	entries []models.AuditEntry
	logger  zerolog.Logger
}

// NewAuditLog creates a new audit log.
func NewAuditLog(logger zerolog.Logger) *AuditLog {
	return &AuditLog{
		entries: make([]models.AuditEntry, 0, 1000),
		logger:  logger.With().Str("component", "audit").Logger(),
	}
}

// Record adds a new audit entry.
func (a *AuditLog) Record(entry models.AuditEntry) {
	entry.Timestamp = time.Now()

	a.mu.Lock()
	a.entries = append(a.entries, entry)
	a.mu.Unlock()

	a.logger.Info().
		Str("user_id", entry.UserID).
		Str("action", entry.Action).
		Str("resource", entry.Resource).
		Str("result", entry.Result).
		Msg("audit event")
}

// GetEntries returns audit entries, optionally filtered by user.
func (a *AuditLog) GetEntries(userID string, limit int) []models.AuditEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var result []models.AuditEntry
	for i := len(a.entries) - 1; i >= 0 && len(result) < limit; i-- {
		if userID == "" || a.entries[i].UserID == userID {
			result = append(result, a.entries[i])
		}
	}
	return result
}

// Count returns the total number of audit entries.
func (a *AuditLog) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.entries)
}
