// Package errors provides structured error types for the platform agent.
package errors

import (
	"errors"
	"fmt"
)

// Sentinel errors for common failure modes.
var (
	ErrTimeout       = errors.New("operation timed out")
	ErrAuthFailure   = errors.New("authentication failed")
	ErrRateLimit     = errors.New("rate limit exceeded")
	ErrNotFound      = errors.New("resource not found")
	ErrDenied        = errors.New("access denied")
	ErrApprovalNeeded = errors.New("approval required")
	ErrInvalidInput  = errors.New("invalid input")
	ErrUnavailable   = errors.New("service unavailable")
)

// APIError represents an error from an external API call.
type APIError struct {
	Service    string
	StatusCode int
	Message    string
	Err        error
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s API error (status %d): %s: %v", e.Service, e.StatusCode, e.Message, e.Err)
	}
	return fmt.Sprintf("%s API error (status %d): %s", e.Service, e.StatusCode, e.Message)
}

func (e *APIError) Unwrap() error { return e.Err }

// NewAPIError creates a new API error.
func NewAPIError(service string, statusCode int, message string) *APIError {
	return &APIError{Service: service, StatusCode: statusCode, Message: message}
}

// IsRetryable returns true if the error is likely transient and worth retrying.
func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
	}
	return errors.Is(err, ErrTimeout) || errors.Is(err, ErrRateLimit) || errors.Is(err, ErrUnavailable)
}
