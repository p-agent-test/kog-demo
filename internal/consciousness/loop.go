// Package consciousness implements Kog's independent thought loop.
// The loop runs as a background goroutine — no external event needed.
// On each cycle it:
//   1. Reads recent memory for context
//   2. Runs all registered monitors
//   3. Asks the LLM "what should I do right now?" given context + observations
//   4. Evaluates the situation through a DecisionPolicy
//   5. Executes or escalates based on the decision
//
// This is what makes Kog autonomous rather than purely reactive.
package consciousness

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/p-blackswan/platform-agent/internal/escalation"
	"github.com/p-blackswan/platform-agent/internal/llm"
	"github.com/p-blackswan/platform-agent/internal/memory"
	"github.com/p-blackswan/platform-agent/internal/monitor"
	"github.com/p-blackswan/platform-agent/internal/policy"
	"github.com/p-blackswan/platform-agent/internal/self"
)

// Action represents something the loop decided to do.
type Action struct {
	// Type is "reflect", "goal", "escalate", "idle".
	Type string `json:"type"`

	// Description is a one-line summary of what the action is.
	Description string `json:"description"`

	// Payload is the action-specific data (e.g. goal text, escalation message).
	Payload string `json:"payload"`

	// DecidedAt is when the action was chosen.
	DecidedAt time.Time `json:"decided_at"`
}

// ActionHandler is called for each action the loop decides to take.
// The loop does not block on the handler — it fires and moves on.
type ActionHandler func(ctx context.Context, a Action)

// LoopConfig configures the consciousness loop.
type LoopConfig struct {
	// CycleInterval is how long to wait between cycles. Default: 5m.
	CycleInterval time.Duration

	// MemorySearchTopK is how many memory entries to retrieve per cycle. Default: 10.
	MemorySearchTopK int

	// MaxCyclesPerHour is a rate limiter (LLM cost guard). Default: 12.
	MaxCyclesPerHour int

	// AgentName is used in prompts and log fields.
	AgentName string
}

// DefaultLoopConfig returns sensible defaults.
func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		CycleInterval:    5 * time.Minute,
		MemorySearchTopK: 10,
		MaxCyclesPerHour: 12,
		AgentName:        "Kog",
	}
}

// Loop is the autonomous thought loop.
type Loop struct {
	cfg       LoopConfig
	provider  llm.LLMProvider
	mem       memory.MemoryStore
	monitors  *monitor.Registry
	policy    policy.DecisionPolicy
	selfModel *self.SelfModel
	notifier  escalation.Notifier
	handler   ActionHandler
	logger    *slog.Logger

	mu           sync.Mutex
	running      bool
	cycles       int
	lastCycle    time.Time
	recentCycles []time.Time
}

// New creates a consciousness Loop.
func New(
	cfg LoopConfig,
	provider llm.LLMProvider,
	mem memory.MemoryStore,
	monitors *monitor.Registry,
	pol policy.DecisionPolicy,
	selfModel *self.SelfModel,
	notifier escalation.Notifier,
	handler ActionHandler,
	logger *slog.Logger,
) *Loop {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.CycleInterval == 0 {
		cfg = DefaultLoopConfig()
	}
	return &Loop{
		cfg:       cfg,
		provider:  provider,
		mem:       mem,
		monitors:  monitors,
		policy:    pol,
		selfModel: selfModel,
		notifier:  notifier,
		handler:   handler,
		logger:    logger,
	}
}

// Start launches the loop in a background goroutine.
// It returns immediately. Cancel ctx to stop the loop.
func (l *Loop) Start(ctx context.Context) error {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return fmt.Errorf("consciousness: loop already running")
	}
	l.running = true
	l.mu.Unlock()

	l.logger.Info("consciousness loop starting",
		"interval", l.cfg.CycleInterval,
		"agent", l.cfg.AgentName)

	go l.run(ctx)
	return nil
}

// IsRunning reports whether the loop is active.
func (l *Loop) IsRunning() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// CycleCount returns the total number of completed cycles.
func (l *Loop) CycleCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cycles
}

// run is the main goroutine. It ticks every CycleInterval.
func (l *Loop) run(ctx context.Context) {
	defer func() {
		l.mu.Lock()
		l.running = false
		l.mu.Unlock()
		l.logger.Info("consciousness loop stopped")
	}()

	ticker := time.NewTicker(l.cfg.CycleInterval)
	defer ticker.Stop()

	// Run one cycle immediately on startup.
	l.cycle(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.cycle(ctx)
		}
	}
}

// cycle is one full iteration of the consciousness loop.
func (l *Loop) cycle(ctx context.Context) {
	if !l.checkRateLimit() {
		l.logger.Debug("consciousness: rate limited, skipping cycle")
		return
	}

	cycleStart := time.Now()
	l.logger.Info("consciousness cycle starting",
		"agent", l.cfg.AgentName,
		"cycle_n", l.CycleCount()+1)

	// 1. Collect recent memories.
	memContext := l.collectMemory(ctx)

	// 2. Run monitors.
	observations := l.runMonitors(ctx)

	// 3. Ask LLM: "what should I do right now?"
	action, err := l.reflect(ctx, memContext, observations)
	if err != nil {
		l.logger.Error("consciousness: reflect failed", "err", err)
		return
	}

	l.logger.Info("consciousness: reflection complete",
		"action_type", action.Type,
		"desc", action.Description)

	// 4. Evaluate each observation through the policy.
	for _, obs := range observations {
		sit := policy.Situation{
			ID:          obs.SituationID,
			Severity:    obs.Severity,
			Description: obs.Message,
			Context:     obs.Details,
			Timestamp:   obs.ObservedAt,
		}
		decision, reason, evalErr := l.policy.Evaluate(ctx, sit)
		if evalErr != nil {
			l.logger.Error("policy evaluate error", "situation", sit.ID, "err", evalErr)
			continue
		}
		l.logger.Info("policy decision",
			"situation", sit.ID, "decision", decision, "reason", reason)

		if decision == policy.DecisionEscalate && l.notifier != nil {
			msg := fmt.Sprintf("[%s] %s — %s", obs.SituationID, obs.Message, reason)
			notifyErr := l.notifier.Notify(ctx, escalation.Escalation{
				Level:   escalation.LevelWarning,
				Title:   fmt.Sprintf("Situation: %s", obs.SituationID),
				Message: msg,
				Source:  "consciousness/" + l.cfg.AgentName,
			})
			if notifyErr != nil {
				l.logger.Error("escalation notify failed", "err", notifyErr)
			}
		}
	}

	// 5. Fire the action handler.
	if l.handler != nil && action.Type != "idle" {
		go l.handler(ctx, *action)
	}

	// 6. Persist cycle summary to memory.
	l.persistCycle(ctx, action, observations, cycleStart)

	// Update counters.
	l.mu.Lock()
	l.cycles++
	l.lastCycle = time.Now().UTC()
	l.recentCycles = append(l.recentCycles, l.lastCycle)
	l.mu.Unlock()

	l.logger.Info("consciousness cycle done",
		"duration", time.Since(cycleStart).Round(time.Millisecond),
		"cycle_n", l.CycleCount())
}

// collectMemory retrieves recent memory entries as formatted context.
func (l *Loop) collectMemory(ctx context.Context) string {
	if l.mem == nil {
		return ""
	}
	entries, err := l.mem.Search(ctx, "recent activity status", l.cfg.MemorySearchTopK)
	if err != nil {
		l.logger.Warn("consciousness: memory search failed", "err", err)
		return ""
	}
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Recent memory context]\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("- [%s] (agent=%s) %s\n",
			e.CreatedAt.Format("2006-01-02 15:04"), e.AgentID, truncate(e.Content, 150)))
	}
	return sb.String()
}

// runMonitors runs all registered monitors and returns observations.
func (l *Loop) runMonitors(ctx context.Context) []monitor.Observation {
	if l.monitors == nil || l.monitors.Count() == 0 {
		return nil
	}
	obs, err := l.monitors.CheckAll(ctx)
	if err != nil {
		l.logger.Warn("consciousness: monitor check errors", "err", err)
	}
	return obs
}

// reflect asks the LLM "what should I do right now?" and returns an Action.
func (l *Loop) reflect(ctx context.Context, memContext string, obs []monitor.Observation) (*Action, error) {
	prompt := l.buildReflectionPrompt(memContext, obs)

	resp, err := l.provider.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: l.reflectionSystemPrompt(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   256,
		Temperature: 0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("reflect: llm complete: %w", err)
	}

	return l.parseAction(resp.Text), nil
}

// reflectionSystemPrompt returns the system prompt for the reflection step.
func (l *Loop) reflectionSystemPrompt() string {
	caps := ""
	if l.selfModel != nil {
		available := l.selfModel.AvailableCapabilities()
		names := make([]string, 0, len(available))
		for _, c := range available {
			names = append(names, c.ID)
		}
		caps = strings.Join(names, ", ")
	}

	return fmt.Sprintf(`You are %s, an autonomous AI agent. You are doing a self-check.

Your available capabilities: %s

Respond with ONE of these actions in this exact format:
IDLE: <reason — nothing to do>
REFLECT: <insight or note worth remembering>
GOAL: <a specific, actionable goal to pursue right now>
ESCALATE: <message — something a human needs to know>

Be concise. One line. No markdown.`, l.cfg.AgentName, caps)
}

// buildReflectionPrompt assembles the reflection input from memory + observations.
func (l *Loop) buildReflectionPrompt(memContext string, obs []monitor.Observation) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Current time: %s\n", time.Now().UTC().Format(time.RFC3339)))

	if l.selfModel != nil {
		sb.WriteString(fmt.Sprintf("Load: %s\n", l.selfModel.Summary()))
	}

	if memContext != "" {
		sb.WriteString("\n")
		sb.WriteString(memContext)
	}

	if len(obs) > 0 {
		sb.WriteString("\n[Monitor observations]\n")
		for _, o := range obs {
			sb.WriteString(fmt.Sprintf("- [%s severity=%d] %s\n", o.SituationID, o.Severity, o.Message))
		}
	} else {
		sb.WriteString("\n[No monitor observations — all clear]\n")
	}

	sb.WriteString("\nWhat should you do right now?")
	return sb.String()
}

// parseAction parses the LLM's one-line reflection response into an Action.
func (l *Loop) parseAction(text string) *Action {
	text = strings.TrimSpace(text)
	now := time.Now().UTC()

	upper := strings.ToUpper(text)
	for _, prefix := range []string{"IDLE:", "REFLECT:", "GOAL:", "ESCALATE:"} {
		if strings.HasPrefix(upper, prefix) {
			payload := strings.TrimSpace(text[len(prefix):])
			actionType := strings.ToLower(strings.TrimSuffix(prefix, ":"))
			return &Action{
				Type:        actionType,
				Description: payload,
				Payload:     payload,
				DecidedAt:   now,
			}
		}
	}

	// Default: treat unknown response as a reflection.
	return &Action{
		Type:        "reflect",
		Description: text,
		Payload:     text,
		DecidedAt:   now,
	}
}

// persistCycle saves a cycle summary to memory.
func (l *Loop) persistCycle(ctx context.Context, a *Action, obs []monitor.Observation, start time.Time) {
	if l.mem == nil {
		return
	}
	content := fmt.Sprintf("[consciousness cycle] action=%s observations=%d duration=%s payload=%q",
		a.Type, len(obs), time.Since(start).Round(time.Millisecond), truncate(a.Payload, 100))

	_ = l.mem.Save(ctx, memory.MemoryEntry{
		AgentID: l.cfg.AgentName,
		Content: content,
		Tags:    []string{"consciousness", "cycle", a.Type},
	})
}

// checkRateLimit returns true if we're allowed to run a cycle.
func (l *Loop) checkRateLimit() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Hour)

	var fresh []time.Time
	for _, t := range l.recentCycles {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	l.recentCycles = fresh

	if l.cfg.MaxCyclesPerHour > 0 && len(l.recentCycles) >= l.cfg.MaxCyclesPerHour {
		return false
	}
	return true
}

// Stats returns a snapshot of loop statistics.
func (l *Loop) Stats() map[string]interface{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return map[string]interface{}{
		"running":         l.running,
		"cycles":          l.cycles,
		"last_cycle":      l.lastCycle,
		"cycle_interval":  l.cfg.CycleInterval.String(),
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
