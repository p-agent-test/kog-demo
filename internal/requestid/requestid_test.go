package requestid

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	ctx, id := New(context.Background())
	assert.NotEmpty(t, id)
	assert.Equal(t, id, FromContext(ctx))
}

func TestFromContext_Missing(t *testing.T) {
	id := FromContext(context.Background())
	assert.NotEmpty(t, id) // generates new UUID
}

func TestWithRequestID(t *testing.T) {
	ctx := WithRequestID(context.Background(), "test-123")
	assert.Equal(t, "test-123", FromContext(ctx))
}
