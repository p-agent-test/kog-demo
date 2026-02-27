// Package policy defines the DecisionPolicy interface and default implementations.
// A policy receives a situation (event + context) and returns a decision:
// handle_self, escalate, or ignore.
package policy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Decision is the outcome of a policy evaluation.
type Decision string

const (
	// DecisionHandleSelf means the agent should handle this autonomously.
	DecisionHandleSelf Decision = "handle_self"

	// DecisionEscalate means a human should be notified / asked for guidance.
	DecisionEscalate Decision = "escalate"

	// DecisionIgnore means this situation requires no action.
	DecisionIgnore Decision = "ignore"
)

// Situation is the input to a policy evaluation.
type Situation struct {
	// ID is a stable identifier for this situation type (e.g. "disk_high", "ci_fail").
	ID string

	// Severity is 0–10. 0=info, 5=warning, 8=error, 10=critical.
	Severity int

	// Description is a human-readable summary.
	Description string

	// Context is arbitrary key-value metadata.
	Context map[string]string

	// Timestamp is when the situation was detected.
	Timestamp time.Time
}

// DecisionRecord logs a policy decision for learning.
type DecisionRecord struct {
	Situation  Situation
	Decision   Decision
	Reason     string
	MadeAt     time.Time
	Successful bool // filled in after the fact via Feedback()
}

// DecisionPolicy evaluates a situation and returns a Decision.
type DecisionPolicy interface {
	// Evaluate returns a decision for the given situation.
	Evaluate(ctx context.Context, s Situation) (Decision, string, error)

	// Feedback registers the outcome of a previous decision (for learning).
	// successful=true means the decision was correct / led to a good result.
	Feedback(situationID string, d Decision, successful bool)
}

// ThresholdPolicy is a simple rule-based policy that makes decisions based on
// severity thresholds and situation ID patterns.
// It also tracks outcomes to adjust thresholds over time.
type ThresholdPolicy struct {
	mu     sync.Mutex
	logger *slog.Logger

	// EscalateAbove: situations with Severity >= this value trigger escalation.
	EscalateAbove int

	// IgnoreBelow: situations with Severity < this value are ignored.
	IgnoreBelow int

	// Overrides maps situation IDs to fixed decisions (bypasses thresholds).
	Overrides map[string]Decision

	// history tracks recent decisions for learning.
	history []*DecisionRecord

	// successRate tracks per-situation ID success rates.
	successRate map[string]*rateTracker
}

type rateTracker struct {
	total   int
	success int
}

// NewThresholdPolicy creates a ThresholdPolicy with sensible defaults.
// escalateAbove=7 (severity ≥7 → escalate), ignoreBelow=2 (severity <2 → ignore).
func NewThresholdPolicy(escalateAbove, ignoreBelow int, logger *slog.Logger) *ThresholdPolicy {
	if logger == nil {
		logger = slog.Default()
	}
	return &ThresholdPolicy{
		EscalateAbove: escalateAbove,
		IgnoreBelow:   ignoreBelow,
		Overrides:     make(map[string]Decision),
		successRate:   make(map[string]*rateTracker),
		logger:        logger,
	}
}

// Evaluate returns handle_self / escalate / ignore based on severity thresholds.
func (p *ThresholdPolicy) Evaluate(_ context.Context, s Situation) (Decision, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Fixed override wins.
	if d, ok := p.Overrides[s.ID]; ok {
		reason := fmt.Sprintf("override rule for situation %q", s.ID)
		p.record(s, d, reason)
		p.logger.Info("policy decision (override)",
			"situation", s.ID, "severity", s.Severity, "decision", d)
		return d, reason, nil
	}

	var d Decision
	var reason string

	switch {
	case s.Severity < p.IgnoreBelow:
		d = DecisionIgnore
		reason = fmt.Sprintf("severity %d < ignore_below %d", s.Severity, p.IgnoreBelow)
	case s.Severity >= p.EscalateAbove:
		d = DecisionEscalate
		reason = fmt.Sprintf("severity %d >= escalate_above %d", s.Severity, p.EscalateAbove)
	default:
		d = DecisionHandleSelf
		reason = fmt.Sprintf("severity %d within self-handle range [%d, %d)",
			s.Severity, p.IgnoreBelow, p.EscalateAbove)
	}

	// Adjust based on recent failure rate: if we've been failing > 50%, escalate instead.
	if d == DecisionHandleSelf {
		if rt, ok := p.successRate[s.ID]; ok && rt.total >= 5 {
			rate := float64(rt.success) / float64(rt.total)
			if rate < 0.5 {
				d = DecisionEscalate
				reason = fmt.Sprintf("learning: success rate %.0f%% < 50%% for %q, escalating",
					rate*100, s.ID)
			}
		}
	}

	p.record(s, d, reason)
	p.logger.Info("policy decision",
		"situation", s.ID, "severity", s.Severity, "decision", d, "reason", reason)
	return d, reason, nil
}

// Feedback registers the outcome of a previous decision.
func (p *ThresholdPolicy) Feedback(situationID string, _ Decision, successful bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rt := p.successRate[situationID]
	if rt == nil {
		rt = &rateTracker{}
		p.successRate[situationID] = rt
	}
	rt.total++
	if successful {
		rt.success++
	}
	p.logger.Debug("policy feedback",
		"situation", situationID, "successful", successful,
		"rate", fmt.Sprintf("%d/%d", rt.success, rt.total))
}

// record appends a decision to the history (capped at 500).
func (p *ThresholdPolicy) record(s Situation, d Decision, reason string) {
	rec := &DecisionRecord{
		Situation: s,
		Decision:  d,
		Reason:    reason,
		MadeAt:    time.Now().UTC(),
	}
	p.history = append(p.history, rec)
	if len(p.history) > 500 {
		p.history = p.history[len(p.history)-500:]
	}
}

// History returns a copy of recent decision records.
func (p *ThresholdPolicy) History() []DecisionRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]DecisionRecord, len(p.history))
	for i, r := range p.history {
		out[i] = *r
	}
	return out
}
