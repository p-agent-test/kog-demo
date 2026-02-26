package project

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationToMs(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"10m", 600000},
		{"1h", 3600000},
		{"5m", 300000},
		{"30s", 30000},
		{"24h", 86400000},
	}
	for _, tt := range tests {
		ms, err := DurationToMs(tt.input)
		require.NoError(t, err, tt.input)
		assert.Equal(t, tt.want, ms, tt.input)
	}

	_, err := DurationToMs("")
	assert.Error(t, err)

	_, err = DurationToMs("invalid")
	assert.Error(t, err)
}

func TestFormatDurationMs(t *testing.T) {
	assert.Equal(t, "10m", FormatDurationMs(600000))
	assert.Equal(t, "1h", FormatDurationMs(3600000))
	assert.Equal(t, "5m", FormatDurationMs(300000))
	assert.Equal(t, "1h30m", FormatDurationMs(5400000))
}

func TestTimeLeftStr(t *testing.T) {
	assert.Equal(t, "∞", TimeLeftStr(0))
	assert.Equal(t, "expired", TimeLeftStr(time.Now().Add(-1*time.Hour).UnixMilli()))

	future := time.Now().Add(3 * time.Hour).UnixMilli()
	result := TimeLeftStr(future)
	assert.Contains(t, result, "h left")
}

func TestParsePhasesString(t *testing.T) {
	assert.Equal(t, "PRD,Design,Implement", ParsePhasesString("PRD, Design, Implement"))
	assert.Equal(t, "PRD", ParsePhasesString("PRD"))
	assert.Equal(t, "", ParsePhasesString(""))
	assert.Equal(t, "A,B", ParsePhasesString(" A , B , "))
}

func TestDriverStartStop(t *testing.T) {
	d := &Driver{
		tickers: make(map[string]*driveTicker),
		stopCh:  make(chan struct{}),
	}

	// IsActive should be false initially
	assert.False(t, d.IsActive("proj-1"))
	assert.Equal(t, 0, d.ActiveCount())

	// Simulate adding a ticker directly (avoid needing real store/bridge)
	d.mu.Lock()
	ctx, cancel := func() (interface{}, func()) {
		// just need a cancel func
		type noop struct{}
		cancelled := false
		return noop{}, func() { cancelled = true; _ = cancelled }
	}()
	_ = ctx
	ticker := time.NewTicker(time.Hour) // long interval — won't fire
	d.tickers["proj-1"] = &driveTicker{
		projectID:  "proj-1",
		driveTimer: ticker,
		cancel:     cancel,
	}
	d.mu.Unlock()

	assert.True(t, d.IsActive("proj-1"))
	assert.Equal(t, 1, d.ActiveCount())

	d.StopDriving("proj-1")
	assert.False(t, d.IsActive("proj-1"))
	assert.Equal(t, 0, d.ActiveCount())
}
