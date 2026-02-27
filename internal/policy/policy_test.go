package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/p-blackswan/platform-agent/internal/policy"
)

func makeSit(id string, severity int) policy.Situation {
	return policy.Situation{
		ID:          id,
		Severity:    severity,
		Description: "test situation",
		Timestamp:   time.Now(),
	}
}

func TestThresholdPolicy_Ignore(t *testing.T) {
	p := policy.NewThresholdPolicy(7, 2, nil)
	ctx := context.Background()

	d, reason, err := p.Evaluate(ctx, makeSit("low_disk", 1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != policy.DecisionIgnore {
		t.Errorf("expected ignore, got %s (reason: %s)", d, reason)
	}
}

func TestThresholdPolicy_HandleSelf(t *testing.T) {
	p := policy.NewThresholdPolicy(7, 2, nil)
	ctx := context.Background()

	d, _, err := p.Evaluate(ctx, makeSit("ci_slow", 4))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != policy.DecisionHandleSelf {
		t.Errorf("expected handle_self, got %s", d)
	}
}

func TestThresholdPolicy_Escalate(t *testing.T) {
	p := policy.NewThresholdPolicy(7, 2, nil)
	ctx := context.Background()

	d, _, err := p.Evaluate(ctx, makeSit("prod_down", 9))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != policy.DecisionEscalate {
		t.Errorf("expected escalate, got %s", d)
	}
}

func TestThresholdPolicy_Override(t *testing.T) {
	p := policy.NewThresholdPolicy(7, 2, nil)
	p.Overrides["always_ignore"] = policy.DecisionIgnore
	ctx := context.Background()

	// Even with severity 9, override wins.
	d, reason, err := p.Evaluate(ctx, makeSit("always_ignore", 9))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != policy.DecisionIgnore {
		t.Errorf("expected ignore (override), got %s (reason: %s)", d, reason)
	}
}

func TestThresholdPolicy_LearningEscalate(t *testing.T) {
	p := policy.NewThresholdPolicy(7, 2, nil)
	ctx := context.Background()

	// Prime the failure rate: 6 failures, 0 successes.
	for i := 0; i < 6; i++ {
		p.Feedback("flaky_check", policy.DecisionHandleSelf, false)
	}

	d, reason, err := p.Evaluate(ctx, makeSit("flaky_check", 4))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should escalate due to learning.
	if d != policy.DecisionEscalate {
		t.Errorf("expected escalate (learning), got %s (reason: %s)", d, reason)
	}
}

func TestThresholdPolicy_History(t *testing.T) {
	p := policy.NewThresholdPolicy(7, 2, nil)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		p.Evaluate(ctx, makeSit("test", 4)) //nolint:errcheck
	}
	hist := p.History()
	if len(hist) != 5 {
		t.Errorf("expected 5 history entries, got %d", len(hist))
	}
}
