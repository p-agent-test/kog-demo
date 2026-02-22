package errors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIError_Error(t *testing.T) {
	err := NewAPIError("github", 403, "forbidden")
	assert.Contains(t, err.Error(), "github")
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "forbidden")
}

func TestAPIError_WithWrapped(t *testing.T) {
	inner := errors.New("connection refused")
	err := &APIError{Service: "jira", StatusCode: 500, Message: "fail", Err: inner}
	assert.ErrorIs(t, err, inner)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestIsRetryable(t *testing.T) {
	assert.True(t, IsRetryable(NewAPIError("gh", 429, "rate limit")))
	assert.True(t, IsRetryable(NewAPIError("gh", 502, "bad gateway")))
	assert.True(t, IsRetryable(NewAPIError("gh", 503, "unavailable")))
	assert.True(t, IsRetryable(ErrTimeout))
	assert.True(t, IsRetryable(ErrRateLimit))
	assert.True(t, IsRetryable(ErrUnavailable))

	assert.False(t, IsRetryable(NewAPIError("gh", 401, "unauth")))
	assert.False(t, IsRetryable(NewAPIError("gh", 404, "not found")))
	assert.False(t, IsRetryable(ErrAuthFailure))
	assert.False(t, IsRetryable(ErrDenied))
}

func TestSentinelErrors(t *testing.T) {
	assert.True(t, errors.Is(ErrTimeout, ErrTimeout))
	assert.False(t, errors.Is(ErrTimeout, ErrAuthFailure))
}
