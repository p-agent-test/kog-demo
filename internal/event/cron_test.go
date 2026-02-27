package event

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCronSource_Name(t *testing.T) {
	s := NewCronSource(nil, nil)
	assert.Equal(t, SourceCron, s.Name())
}

func TestCronSource_Ack(t *testing.T) {
	s := NewCronSource(nil, nil)
	err := s.Ack(context.Background(), "any-id")
	assert.NoError(t, err)
}

func TestCronSource_EmitsTickEvent(t *testing.T) {
	job := CronJob{
		Name:     "test-job",
		Interval: 50 * time.Millisecond,
		Spec:     "* * * * *",
	}
	s := NewCronSource([]CronJob{job}, nil)

	out := make(chan Event, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	require.NoError(t, s.Subscribe(ctx, out))

	select {
	case ev := <-out:
		assert.Equal(t, SourceCron, ev.Source)
		assert.Equal(t, TypeTick, ev.Type)
		assert.Equal(t, "test-job", ev.Metadata["job"])
	case <-ctx.Done():
		t.Fatal("expected at least one tick event")
	}
}

func TestCronSource_StopsOnCtxCancel(t *testing.T) {
	job := CronJob{Name: "fast", Interval: 20 * time.Millisecond}
	s := NewCronSource([]CronJob{job}, nil)

	out := make(chan Event, 100)
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, s.Subscribe(ctx, out))
	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Drain remaining
	count := len(out)
	assert.Greater(t, count, 0, "should have received some events before cancel")
}
