package supervisor

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// GrantStore manages time-limited permission grants.
type GrantStore struct {
	mu     sync.RWMutex
	grants map[string]*PermissionGrant // grant ID → grant
	logger zerolog.Logger
}

// NewGrantStore creates a new grant store.
func NewGrantStore(logger zerolog.Logger) *GrantStore {
	return &GrantStore{
		grants: make(map[string]*PermissionGrant),
		logger: logger.With().Str("component", "grant_store").Logger(),
	}
}

// Issue creates a new time-limited permission grant.
func (gs *GrantStore) Issue(perm Permission, grantedTo, grantedBy, taskID string, ttl time.Duration) *PermissionGrant {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	now := time.Now()
	grant := &PermissionGrant{
		ID:         uuid.New().String(),
		Permission: perm,
		Level:      PolicyAutoApprove, // grants are always "approved" once issued
		GrantedTo:  grantedTo,
		GrantedBy:  grantedBy,
		TaskID:     taskID,
		ExpiresAt:  now.Add(ttl),
		CreatedAt:  now,
	}

	gs.grants[grant.ID] = grant

	gs.logger.Info().
		Str("grant_id", grant.ID).
		Str("permission", string(perm)).
		Str("granted_to", grantedTo).
		Str("granted_by", grantedBy).
		Str("task_id", taskID).
		Time("expires_at", grant.ExpiresAt).
		Msg("grant issued")

	return grant
}

// Check returns true if there is a valid (non-expired) grant for the given
// permission and task.
func (gs *GrantStore) Check(perm Permission, taskID string) bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	now := time.Now()
	for _, g := range gs.grants {
		if g.Permission == perm && g.TaskID == taskID && now.Before(g.ExpiresAt) {
			return true
		}
	}
	return false
}

// Revoke removes a grant by ID.
func (gs *GrantStore) Revoke(grantID string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if _, ok := gs.grants[grantID]; !ok {
		return fmt.Errorf("grant %s not found", grantID)
	}

	delete(gs.grants, grantID)

	gs.logger.Info().
		Str("grant_id", grantID).
		Msg("grant revoked")

	return nil
}

// List returns all grants for a given task ID. Returns nil if none found.
func (gs *GrantStore) List(taskID string) []*PermissionGrant {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	var result []*PermissionGrant
	for _, g := range gs.grants {
		if g.TaskID == taskID {
			// Return a copy
			cp := *g
			result = append(result, &cp)
		}
	}
	return result
}

// ListAll returns all grants (copies). Used for diagnostics.
func (gs *GrantStore) ListAll() []*PermissionGrant {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	result := make([]*PermissionGrant, 0, len(gs.grants))
	for _, g := range gs.grants {
		cp := *g
		result = append(result, &cp)
	}
	return result
}

// Cleanup removes all expired grants.
func (gs *GrantStore) Cleanup() {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	now := time.Now()
	removed := 0
	for id, g := range gs.grants {
		if now.After(g.ExpiresAt) || now.Equal(g.ExpiresAt) {
			delete(gs.grants, id)
			removed++
		}
	}

	if removed > 0 {
		gs.logger.Info().Int("removed", removed).Msg("expired grants cleaned up")
	}
}

// Count returns the number of active grants.
func (gs *GrantStore) Count() int {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return len(gs.grants)
}
