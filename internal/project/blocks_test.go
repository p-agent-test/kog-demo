package project

import (
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusEmoji(t *testing.T) {
	now := time.Now().UnixMilli()
	tests := []struct {
		name   string
		proj   *Project
		expect string
	}{
		{"archived", &Project{Status: "archived", UpdatedAt: now}, "📦"},
		{"paused", &Project{Status: "paused", UpdatedAt: now}, "⏸️"},
		{"active recent", &Project{Status: "active", UpdatedAt: now}, "🟢"},
		{"active 1d", &Project{Status: "active", UpdatedAt: now - 24*60*60*1000}, "🟡"},
		{"active 5d", &Project{Status: "active", UpdatedAt: now - 5*24*60*60*1000}, "🔵"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, StatusEmoji(tt.proj))
		})
	}
}

func TestDashboardBlocks_Empty(t *testing.T) {
	blocks := DashboardBlocks(nil, nil, nil)
	require.Len(t, blocks, 1)
	section, ok := blocks[0].(*slack.SectionBlock)
	require.True(t, ok)
	assert.Contains(t, section.Text.Text, "No active projects")
}

func TestDashboardBlocks_WithProjects(t *testing.T) {
	now := time.Now().UnixMilli()
	projects := []*Project{
		{ID: "p1", Slug: "alpha", Name: "Alpha Project", Status: "active", UpdatedAt: now},
		{ID: "p2", Slug: "beta", Name: "Beta Project", Status: "active", UpdatedAt: now - 48*60*60*1000},
	}
	statsMap := map[string]*ProjectStats{
		"p1": {Decisions: 3, Blockers: 1, Tasks: 5},
		"p2": {Decisions: 1, Tasks: 2},
	}
	eventsMap := map[string]*ProjectEvent{
		"p1": {Summary: "Implemented feature X"},
	}

	blocks := DashboardBlocks(projects, statsMap, eventsMap)
	require.True(t, len(blocks) >= 4) // header + at least 2 project sections + context
	// First block should be header
	_, isHeader := blocks[0].(*slack.HeaderBlock)
	assert.True(t, isHeader)
}

func TestProjectCreatedBlocks(t *testing.T) {
	p := &Project{Slug: "test-proj", Name: "Test Project", RepoURL: "https://github.com/x"}
	blocks := ProjectCreatedBlocks(p)
	require.True(t, len(blocks) >= 1)
	section, ok := blocks[0].(*slack.SectionBlock)
	require.True(t, ok)
	assert.Contains(t, section.Text.Text, "test-proj")
	assert.Contains(t, section.Text.Text, "Test Project")
}

func TestProjectContinueBlocks(t *testing.T) {
	p := &Project{Slug: "my-proj", Name: "My Project", SessionVersion: 3}
	decisions := []*ProjectMemory{{Content: "Use gRPC"}}
	blockers := []*ProjectMemory{{Content: "CI broken"}}

	blocks := ProjectContinueBlocks(p, decisions, blockers, "Last session did X")
	require.True(t, len(blocks) >= 2)
	_, isHeader := blocks[0].(*slack.HeaderBlock)
	assert.True(t, isHeader)
}

func TestDecisionRecordedBlocks(t *testing.T) {
	blocks := DecisionRecordedBlocks("my-proj", "Use PostgreSQL", 5)
	require.Len(t, blocks, 2)
	section, ok := blocks[0].(*slack.SectionBlock)
	require.True(t, ok)
	assert.Contains(t, section.Text.Text, "Use PostgreSQL")
}

func TestBlockerRecordedBlocks(t *testing.T) {
	blocks := BlockerRecordedBlocks("my-proj", "CI is broken", 3)
	require.Len(t, blocks, 2)
	section, ok := blocks[0].(*slack.SectionBlock)
	require.True(t, ok)
	assert.Contains(t, section.Text.Text, "CI is broken")
}

func TestProjectStatusBlocks(t *testing.T) {
	now := time.Now().UnixMilli()
	p := &Project{
		ID: "p1", Slug: "test", Name: "Test Project", Status: "active",
		OwnerID: "U123", SessionVersion: 2, CreatedAt: now, UpdatedAt: now,
	}
	stats := &ProjectStats{Decisions: 2, Blockers: 1, Tasks: 5, Events: 10}
	decisions := []*ProjectMemory{{Content: "Decision 1", CreatedAt: now}}
	blockers := []*ProjectMemory{{Content: "Blocker 1", CreatedAt: now}}
	events := []*ProjectEvent{{Summary: "Created project", CreatedAt: now}}

	blocks := ProjectStatusBlocks(p, stats, decisions, blockers, events)
	require.True(t, len(blocks) >= 5)
	_, isHeader := blocks[0].(*slack.HeaderBlock)
	assert.True(t, isHeader)
}

func TestTruncateStr(t *testing.T) {
	assert.Equal(t, "hello", truncateStr("hello", 10))
	assert.Equal(t, "hel…", truncateStr("hello world", 4))
	assert.Equal(t, "hello worl…", truncateStr("hello world!", 11))
}

func TestTimeAgo(t *testing.T) {
	now := time.Now().UnixMilli()
	assert.Equal(t, "just now", timeAgo(now))
	assert.Equal(t, "never", timeAgo(0))
	assert.Contains(t, timeAgo(now-3600*1000), "h ago")
}
