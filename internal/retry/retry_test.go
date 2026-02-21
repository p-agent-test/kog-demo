package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	perrors "github.com/p-blackswan/platform-agent/internal/errors"
	"github.com/stretchr/testify/assert"
)

func TestDo_Success(t *testing.T) {
	calls := 0
	err := Do(context.Background(), DefaultConfig(), func(ctx context.Context) error {
		calls++
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestDo_NonRetryableError(t *testing.T) {
	calls := 0
	err := Do(context.Background(), DefaultConfig(), func(ctx context.Context) error {
		calls++
		return perrors.ErrAuthFailure
	})
	assert.ErrorIs(t, err, perrors.ErrAuthFailure)
	assert.Equal(t, 1, calls) // Should not retry
}

func TestDo_RetryableError_EventualSuccess(t *testing.T) {
	calls := 0
	cfg := Config{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, Jitter: false}
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return perrors.ErrTimeout
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestDo_RetryableError_AllFail(t *testing.T) {
	calls := 0
	cfg := Config{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, Jitter: false}
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		calls++
		return perrors.NewAPIError("gh", 429, "rate limit")
	})
	assert.Error(t, err)
	assert.Equal(t, 2, calls)
}

func TestDo_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	cfg := Config{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	err := Do(ctx, cfg, func(ctx context.Context) error {
		calls++
		return perrors.ErrTimeout
	})
	// First call happens, then context is cancelled
	assert.Error(t, err)
}

func TestDo_GenericNonRetryable(t *testing.T) {
	calls := 0
	err := Do(context.Background(), DefaultConfig(), func(ctx context.Context) error {
		calls++
		return errors.New("generic error")
	})
	assert.Error(t, err)
	assert.Equal(t, 1, calls)
}
