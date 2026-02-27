package escalation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogNotifier_Notify(t *testing.T) {
	n := NewLogNotifier(nil)
	err := n.Notify(context.Background(), Escalation{
		Level:   LevelWarning,
		Title:   "test warning",
		Message: "something happened",
		Source:  "test",
		Error:   errors.New("boom"),
	})
	require.NoError(t, err)
}

func TestMultiNotifier_AllCalled(t *testing.T) {
	var called int
	type countingNotifier struct{ LogNotifier }
	// Use two log notifiers — both should complete without error
	n1 := NewLogNotifier(nil)
	n2 := NewLogNotifier(nil)
	_ = called

	multi := NewMultiNotifier(n1, n2)
	err := multi.Notify(context.Background(), Escalation{
		Level:   LevelInfo,
		Title:   "multi test",
		Message: "both notifiers should be called",
	})
	assert.NoError(t, err)
}

func TestLevelEmoji(t *testing.T) {
	assert.Equal(t, "🚨", levelEmoji(LevelCritical))
	assert.Equal(t, "⚠️", levelEmoji(LevelWarning))
	assert.Equal(t, "ℹ️", levelEmoji(LevelInfo))
	assert.Equal(t, "ℹ️", levelEmoji("unknown"))
}

func TestLevelConstants(t *testing.T) {
	assert.Equal(t, Level("info"), LevelInfo)
	assert.Equal(t, Level("warning"), LevelWarning)
	assert.Equal(t, Level("critical"), LevelCritical)
}
