package consciousness_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/p-blackswan/platform-agent/internal/consciousness"
	"github.com/p-blackswan/platform-agent/internal/llm"
	"github.com/p-blackswan/platform-agent/internal/monitor"
	"github.com/p-blackswan/platform-agent/internal/policy"
	"github.com/p-blackswan/platform-agent/internal/self"
)

// ---- Mocks ----

type mockLLM struct {
	response string
}

func (m *mockLLM) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{Text: m.response, StopReason: llm.StopReasonEndTurn}, nil
}
func (m *mockLLM) Stream(_ context.Context, _ llm.CompletionRequest, out chan<- llm.Token) error {
	out <- llm.Token{Text: m.response, Done: true}
	return nil
}
func (m *mockLLM) ModelID() string { return "mock" }
func (m *mockLLM) MaxTokens() int  { return 1024 }

// ---- Tests ----

func TestLoop_StartStop(t *testing.T) {
	cfg := consciousness.DefaultLoopConfig()
	cfg.CycleInterval = 50 * time.Millisecond
	cfg.MaxCyclesPerHour = 100

	loop := consciousness.New(
		cfg,
		&mockLLM{response: "IDLE: nothing to do"},
		nil,
		monitor.NewRegistry(nil),
		policy.NewThresholdPolicy(7, 2, nil),
		self.New(nil),
		nil,
		nil,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !loop.IsRunning() {
		t.Error("expected loop to be running after Start")
	}

	// Double-start should fail.
	if err := loop.Start(ctx); err == nil {
		t.Error("expected error on double Start")
	}

	// Wait for context to expire.
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond) // let goroutine settle

	if loop.IsRunning() {
		t.Error("expected loop to stop after context cancellation")
	}
}

func TestLoop_CycleCount(t *testing.T) {
	cfg := consciousness.DefaultLoopConfig()
	cfg.CycleInterval = 30 * time.Millisecond
	cfg.MaxCyclesPerHour = 100

	loop := consciousness.New(
		cfg,
		&mockLLM{response: "REFLECT: everything looks good"},
		nil,
		monitor.NewRegistry(nil),
		policy.NewThresholdPolicy(7, 2, nil),
		self.New(nil),
		nil,
		nil,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	loop.Start(ctx) //nolint:errcheck
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)

	cycles := loop.CycleCount()
	if cycles < 2 {
		t.Errorf("expected at least 2 cycles in 150ms with 30ms interval, got %d", cycles)
	}
	t.Logf("cycles completed: %d", cycles)
}

func TestLoop_ActionHandler(t *testing.T) {
	cfg := consciousness.DefaultLoopConfig()
	cfg.CycleInterval = 200 * time.Millisecond
	cfg.MaxCyclesPerHour = 100

	var handlerCalled int64

	loop := consciousness.New(
		cfg,
		&mockLLM{response: "GOAL: check the build status"},
		nil,
		monitor.NewRegistry(nil),
		policy.NewThresholdPolicy(7, 2, nil),
		self.New(nil),
		nil,
		func(_ context.Context, a consciousness.Action) {
			atomic.AddInt64(&handlerCalled, 1)
			if a.Type != "goal" {
				t.Errorf("expected action type 'goal', got %q", a.Type)
			}
		},
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	loop.Start(ctx) //nolint:errcheck
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt64(&handlerCalled) == 0 {
		t.Error("expected action handler to be called at least once")
	}
}

func TestLoop_RateLimit(t *testing.T) {
	cfg := consciousness.DefaultLoopConfig()
	cfg.CycleInterval = 5 * time.Millisecond
	cfg.MaxCyclesPerHour = 3 // very low limit

	loop := consciousness.New(
		cfg,
		&mockLLM{response: "IDLE: ok"},
		nil,
		monitor.NewRegistry(nil),
		policy.NewThresholdPolicy(7, 2, nil),
		self.New(nil),
		nil,
		nil,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	loop.Start(ctx) //nolint:errcheck
	<-ctx.Done()
	time.Sleep(30 * time.Millisecond)

	cycles := loop.CycleCount()
	// Should be capped at MaxCyclesPerHour=3.
	if cycles > 4 { // small buffer for timing
		t.Errorf("expected rate limiting to cap cycles ~3, got %d", cycles)
	}
}

func TestLoop_Stats(t *testing.T) {
	cfg := consciousness.DefaultLoopConfig()
	cfg.CycleInterval = 500 * time.Millisecond

	loop := consciousness.New(cfg, &mockLLM{response: "IDLE: ok"}, nil, nil, policy.NewThresholdPolicy(7, 2, nil), nil, nil, nil, nil)

	stats := loop.Stats()
	if stats["running"] != false {
		t.Errorf("expected running=false before start, got %v", stats["running"])
	}
	if stats["cycles"] != 0 {
		t.Errorf("expected cycles=0 before start, got %v", stats["cycles"])
	}
}
