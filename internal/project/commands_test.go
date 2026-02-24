package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCommand_ListProjects(t *testing.T) {
	for _, text := range []string{"projects", "projeler", "Projects"} {
		cmd := ParseCommand(text)
		require.NotNil(t, cmd, text)
		assert.Equal(t, CmdListProjects, cmd.Type)
	}
}

func TestParseCommand_NewProject(t *testing.T) {
	cmd := ParseCommand(`new project "Leader Election" --repo https://github.com/test`)
	require.NotNil(t, cmd)
	assert.Equal(t, CmdNewProject, cmd.Type)
	assert.Equal(t, "Leader Election", cmd.Name)
	assert.Equal(t, "https://github.com/test", cmd.RepoURL)
}

func TestParseCommand_NewProjectNoQuotes(t *testing.T) {
	cmd := ParseCommand(`new project myproject`)
	require.NotNil(t, cmd)
	assert.Equal(t, CmdNewProject, cmd.Type)
	assert.Equal(t, "myproject", cmd.Name)
}

func TestParseCommand_Decide(t *testing.T) {
	cmd := ParseCommand("decide leader-election use etcd 3.5")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdDecide, cmd.Type)
	assert.Equal(t, "leader-election", cmd.Slug)
	assert.Equal(t, "use etcd 3.5", cmd.Message)
}

func TestParseCommand_Blocker(t *testing.T) {
	cmd := ParseCommand("blocker myproj waiting on certs")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdBlocker, cmd.Type)
	assert.Equal(t, "myproj", cmd.Slug)
	assert.Equal(t, "waiting on certs", cmd.Message)
}

func TestParseCommand_Archive(t *testing.T) {
	cmd := ParseCommand("archive leader-election")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdArchive, cmd.Type)
	assert.Equal(t, "leader-election", cmd.Slug)
}

func TestParseCommand_Resume(t *testing.T) {
	cmd := ParseCommand("resume leader-election")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdResume, cmd.Type)
	assert.Equal(t, "leader-election", cmd.Slug)
}

func TestParseCommand_ContinueProject(t *testing.T) {
	cmd := ParseCommand("leader-election")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdContinueProject, cmd.Type)
	assert.Equal(t, "leader-election", cmd.Slug)
}

func TestParseCommand_MessageProject(t *testing.T) {
	cmd := ParseCommand("leader-election what's the status?")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdMessageProject, cmd.Type)
	assert.Equal(t, "leader-election", cmd.Slug)
	assert.Equal(t, "what's the status?", cmd.Message)
}

func TestParseCommand_StripMention(t *testing.T) {
	cmd := ParseCommand("<@U123ABC> projects")
	require.NotNil(t, cmd)
	assert.Equal(t, CmdListProjects, cmd.Type)
}

func TestParseCommand_Empty(t *testing.T) {
	assert.Nil(t, ParseCommand(""))
	assert.Nil(t, ParseCommand("   "))
}

func TestParseCommand_DecideInsufficientArgs(t *testing.T) {
	assert.Nil(t, ParseCommand("decide"))
	assert.Nil(t, ParseCommand("decide slug"))
}
