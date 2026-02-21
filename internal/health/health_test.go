package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestLivenessHandler(t *testing.T) {
	handler := LivenessHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "ok")
}

func TestChecker_AllHealthy(t *testing.T) {
	c := NewChecker(zerolog.Nop())
	c.Register("db", func(ctx context.Context) Status { return StatusOK })
	c.Register("cache", func(ctx context.Context) Status { return StatusOK })

	assert.True(t, c.IsReady(context.Background()))
}

func TestChecker_OneDown(t *testing.T) {
	c := NewChecker(zerolog.Nop())
	c.Register("db", func(ctx context.Context) Status { return StatusOK })
	c.Register("cache", func(ctx context.Context) Status { return StatusDown })

	assert.False(t, c.IsReady(context.Background()))
}

func TestChecker_Degraded_StillReady(t *testing.T) {
	c := NewChecker(zerolog.Nop())
	c.Register("db", func(ctx context.Context) Status { return StatusDegraded })

	assert.True(t, c.IsReady(context.Background()))
}

func TestChecker_NoChecks(t *testing.T) {
	c := NewChecker(zerolog.Nop())
	assert.True(t, c.IsReady(context.Background()))
}

func TestReadinessHandler_Healthy(t *testing.T) {
	c := NewChecker(zerolog.Nop())
	c.Register("svc", func(ctx context.Context) Status { return StatusOK })

	handler := c.ReadinessHandler()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "ready")
}

func TestReadinessHandler_NotReady(t *testing.T) {
	c := NewChecker(zerolog.Nop())
	c.Register("svc", func(ctx context.Context) Status { return StatusDown })

	handler := c.ReadinessHandler()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Contains(t, rr.Body.String(), "not_ready")
}
