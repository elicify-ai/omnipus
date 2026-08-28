// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-066 D2/D9 — the Agent response's four context-window fields.
//
// `context_window_override` is the persisted per-agent rung-1 value;
// `context_window_effective` / `_source` / `_clamped` are DERIVED on every
// read from the one resolver (agent.ResolveWindow), never stored. Deriving
// them here is what makes the Advanced panel's "Effective window · source"
// row true after a model switch, a catalog refresh or a /settings/context
// write, none of which touch the agent record.
//
// The regression this closes: the wire fields existed and the SPA read them,
// but no response path ever populated them and updateAgent silently dropped
// the override on the way in — a control that reported "Saved" and changed
// nothing (the ADR-037 anti-pattern).

package gateway

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// agentPrimaryModelID returns the model id the agent's turns actually run on:
// its own primary when it pins one, else the global default model. It is the
// model half of the pair ResolveWindow is keyed on, mirroring
// agentPrimaryProviderID's provider half.
func agentPrimaryModelID(cfg *config.Config, ac *config.AgentConfig) string {
	if ac != nil && ac.Model != nil && strings.TrimSpace(ac.Model.Primary) != "" {
		return strings.TrimSpace(ac.Model.Primary)
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Agents.Defaults.DefaultModel.Model)
}

// applyAgentContextWindow populates context_window_override plus the three
// derived read-only fields on a wire Agent.
//
// An exempt subprocess-CLI row reports effective 0 with no source (the
// provider manages its own context). An unknown local window leaves all three
// derived fields absent — the SPA renders the context_window_unknown state
// from `degraded_reason`/the turn refusal, and emitting a fabricated 0 here
// would read as "exempt".
func applyAgentContextWindow(ag *gen.Agent, cfg *config.Config, ac *config.AgentConfig) {
	if ag == nil || ac == nil {
		return
	}
	if ac.ContextWindowOverride != nil && *ac.ContextWindowOverride > 0 {
		v := *ac.ContextWindowOverride
		ag.ContextWindowOverride = &v
	} else {
		ag.ContextWindowOverride = nil
	}

	res := agent.ResolveWindow(cfg, agentPrimaryProviderID(cfg, ac), agentPrimaryModelID(cfg, ac), ac.ID)
	ag.ContextWindowEffective = nil
	ag.ContextWindowSource = nil
	ag.ContextWindowClamped = nil
	switch {
	case res.Exempt:
		zero := 0
		ag.ContextWindowEffective = &zero
	case res.Unknown:
		// Nothing to report: no window, no rung.
	case res.Window > 0:
		window := res.Window
		ag.ContextWindowEffective = &window
		src := gen.AgentContextWindowSource(res.Source)
		if src.Valid() {
			ag.ContextWindowSource = &src
		}
		clamped := res.Clamped
		ag.ContextWindowClamped = &clamped
	}
}
