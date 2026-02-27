// Package runtime — SmartRouter routes events to agents based on rules.
// Rules match on event Source, Type, and Metadata fields.
// The first matching rule wins; if no rule matches, the event is broadcast.
package runtime

import (
	"log/slog"
	"strings"

	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/p-blackswan/platform-agent/internal/kogagent"
)

// Rule defines a routing condition and the set of target agent IDs.
type Rule struct {
	// Source matches the event source exactly. Empty = match any.
	Source string `yaml:"source"`

	// Type matches the event type exactly. Empty = match any.
	Type string `yaml:"type"`

	// MetaKey / MetaValue: if both are set, the event metadata must contain
	// MetaKey with a value that starts with MetaValue.
	MetaKey   string `yaml:"meta_key"`
	MetaValue string `yaml:"meta_value"`

	// Agents is the list of agent IDs to route matching events to.
	// Empty means broadcast to all.
	Agents []string `yaml:"agents"`
}

// matches returns true if the rule matches the given event.
func (r Rule) matches(ev event.Event) bool {
	if r.Source != "" && r.Source != ev.Source {
		return false
	}
	if r.Type != "" && r.Type != ev.Type {
		return false
	}
	if r.MetaKey != "" && r.MetaValue != "" {
		val, ok := ev.Metadata[r.MetaKey]
		if !ok || !strings.HasPrefix(val, r.MetaValue) {
			return false
		}
	}
	return true
}

// SmartRouter routes events to agents based on an ordered list of Rules.
// The first matching rule wins; if no rules match, all agents are returned
// (broadcast fallback).
type SmartRouter struct {
	rules  []Rule
	agents map[string]kogagent.Agent
	logger *slog.Logger
}

// NewSmartRouter creates a SmartRouter with the given rules.
// agents is a slice of all available agents; the router looks up by ID.
func NewSmartRouter(rules []Rule, agents []kogagent.Agent, logger *slog.Logger) *SmartRouter {
	if logger == nil {
		logger = slog.Default()
	}
	agentMap := make(map[string]kogagent.Agent, len(agents))
	for _, a := range agents {
		agentMap[a.ID()] = a
	}
	return &SmartRouter{
		rules:  rules,
		agents: agentMap,
		logger: logger,
	}
}

// Route implements runtime.Router.
func (r *SmartRouter) Route(ev event.Event) []kogagent.Agent {
	for _, rule := range r.rules {
		if !rule.matches(ev) {
			continue
		}

		if len(rule.Agents) == 0 {
			// Explicit broadcast.
			return r.allAgents()
		}

		// Look up each agent by ID.
		targets := make([]kogagent.Agent, 0, len(rule.Agents))
		for _, id := range rule.Agents {
			if a, ok := r.agents[id]; ok {
				targets = append(targets, a)
			} else {
				r.logger.Warn("router: unknown agent in rule", "agent_id", id)
			}
		}

		r.logger.Debug("router: matched rule",
			"event_source", ev.Source,
			"event_type", ev.Type,
			"targets", len(targets),
		)
		return targets
	}

	// No rule matched — broadcast.
	r.logger.Debug("router: no rule matched, broadcasting",
		"event_source", ev.Source,
		"event_type", ev.Type,
	)
	return r.allAgents()
}

func (r *SmartRouter) allAgents() []kogagent.Agent {
	agents := make([]kogagent.Agent, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a)
	}
	return agents
}

// AddAgent adds an agent to the router's lookup map.
func (r *SmartRouter) AddAgent(a kogagent.Agent) {
	r.agents[a.ID()] = a
}

// AddRule appends a routing rule at the lowest priority.
func (r *SmartRouter) AddRule(rule Rule) {
	r.rules = append(r.rules, rule)
}

// PrependRule inserts a routing rule at the highest priority.
func (r *SmartRouter) PrependRule(rule Rule) {
	r.rules = append([]Rule{rule}, r.rules...)
}
