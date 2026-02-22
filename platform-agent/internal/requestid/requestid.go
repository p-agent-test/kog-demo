// Package requestid provides request ID propagation via context.
package requestid

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey struct{}

// WithRequestID returns a context with the given request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext extracts the request ID from context, or generates a new one.
func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKey{}).(string); ok && id != "" {
		return id
	}
	return uuid.New().String()
}

// New generates a new request ID and returns the enriched context and ID.
func New(ctx context.Context) (context.Context, string) {
	id := uuid.New().String()
	return WithRequestID(ctx, id), id
}
