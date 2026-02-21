package supervisor

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestGrantStore() *GrantStore {
	return NewGrantStore(zerolog.Nop())
}

func TestGrantStore_Issue(t *testing.T) {
	gs := newTestGrantStore()

	grant := gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)

	assert.NotEmpty(t, grant.ID)
	assert.Equal(t, PermGithubPRRead, grant.Permission)
	assert.Equal(t, "user1", grant.GrantedTo)
	assert.Equal(t, "policy", grant.GrantedBy)
	assert.Equal(t, "task-1", grant.TaskID)
	assert.False(t, grant.CreatedAt.IsZero())
	assert.True(t, grant.ExpiresAt.After(time.Now()))
	assert.Equal(t, 1, gs.Count())
}

func TestGrantStore_Check_Valid(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermK8sRead, "user1", "policy", "task-1", 5*time.Minute)

	assert.True(t, gs.Check(PermK8sRead, "task-1"))
}

func TestGrantStore_Check_WrongPermission(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermK8sRead, "user1", "policy", "task-1", 5*time.Minute)

	assert.False(t, gs.Check(PermK8sWrite, "task-1"))
}

func TestGrantStore_Check_WrongTask(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermK8sRead, "user1", "policy", "task-1", 5*time.Minute)

	assert.False(t, gs.Check(PermK8sRead, "task-2"))
}

func TestGrantStore_Check_Expired(t *testing.T) {
	gs := newTestGrantStore()

	// Issue with a very short TTL that's already expired
	gs.Issue(PermK8sRead, "user1", "policy", "task-1", -1*time.Second)

	assert.False(t, gs.Check(PermK8sRead, "task-1"))
}

func TestGrantStore_Check_NoGrants(t *testing.T) {
	gs := newTestGrantStore()

	assert.False(t, gs.Check(PermK8sRead, "task-1"))
}

func TestGrantStore_Revoke(t *testing.T) {
	gs := newTestGrantStore()

	grant := gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)
	assert.Equal(t, 1, gs.Count())

	err := gs.Revoke(grant.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, gs.Count())
	assert.False(t, gs.Check(PermGithubPRRead, "task-1"))
}

func TestGrantStore_Revoke_NotFound(t *testing.T) {
	gs := newTestGrantStore()

	err := gs.Revoke("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGrantStore_List(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)
	gs.Issue(PermK8sRead, "user1", "policy", "task-1", 5*time.Minute)
	gs.Issue(PermJiraRead, "user1", "policy", "task-2", 5*time.Minute)

	list := gs.List("task-1")
	assert.Len(t, list, 2)

	list2 := gs.List("task-2")
	assert.Len(t, list2, 1)

	list3 := gs.List("task-3")
	assert.Nil(t, list3)
}

func TestGrantStore_List_ReturnsCopies(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)

	list := gs.List("task-1")
	require.Len(t, list, 1)

	// Modify the returned copy, should not affect the store
	list[0].GrantedTo = "modified"
	original := gs.List("task-1")
	assert.Equal(t, "user1", original[0].GrantedTo)
}

func TestGrantStore_ListAll(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)
	gs.Issue(PermK8sRead, "user2", "admin", "task-2", 10*time.Minute)

	all := gs.ListAll()
	assert.Len(t, all, 2)
}

func TestGrantStore_Cleanup(t *testing.T) {
	gs := newTestGrantStore()

	// Issue one that's already expired
	gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", -1*time.Second)
	// Issue one that's still valid
	gs.Issue(PermK8sRead, "user1", "policy", "task-2", 5*time.Minute)

	assert.Equal(t, 2, gs.Count())

	gs.Cleanup()

	assert.Equal(t, 1, gs.Count())
	assert.False(t, gs.Check(PermGithubPRRead, "task-1"))
	assert.True(t, gs.Check(PermK8sRead, "task-2"))
}

func TestGrantStore_Cleanup_AllExpired(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", -1*time.Second)
	gs.Issue(PermK8sRead, "user1", "policy", "task-2", -1*time.Second)

	gs.Cleanup()

	assert.Equal(t, 0, gs.Count())
}

func TestGrantStore_Cleanup_NoneExpired(t *testing.T) {
	gs := newTestGrantStore()

	gs.Issue(PermGithubPRRead, "user1", "policy", "task-1", 5*time.Minute)

	gs.Cleanup()

	assert.Equal(t, 1, gs.Count())
}

func TestGrantStore_MultipleGrantsSamePerm(t *testing.T) {
	gs := newTestGrantStore()

	// Issue two grants for same permission but different tasks
	gs.Issue(PermK8sRead, "user1", "policy", "task-1", 5*time.Minute)
	gs.Issue(PermK8sRead, "user1", "policy", "task-2", 5*time.Minute)

	assert.True(t, gs.Check(PermK8sRead, "task-1"))
	assert.True(t, gs.Check(PermK8sRead, "task-2"))
	assert.Equal(t, 2, gs.Count())
}
