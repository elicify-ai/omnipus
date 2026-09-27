// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// reasoning_effort.go is C5's effort resolution (spec
// docs/internal/specs/thinking-reasoning-spec.md, Section 1 C5 resolution
// order; D9, D10, T1; pinned by pkg/agent/reasoning_effort_resolve_test.go's
// resolution table and pkg/agent/reasoning_effort_llmopts_test.go's wiring
// tests).
//
// The walk is in the order the spec fixes: WP-H's two higher-precedence
// override sources (the web per-message effort and the non-web
// SessionMeta.ReasoningEffort) slot in AHEAD of this function at the call
// site when they land — its flat signature and this file's semantics do not
// change.
package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// reasoningEffortUnsetToken is the spec's literal unset token: "default"
// means "send nothing" (D9/D24). Matched EXACTLY (lowercase, case-sensitive
// by ruling): any other casing is a plain value (T1 — the resolver selects,
// it never normalizes); e.g. "Default" passes through byte-exactly.
const reasoningEffortUnsetToken = "default"

// resolveReasoningEffort applies C5's resolution order to the four stored
// effort surfaces this walk consults, returning the effort value to place on
// llmOpts["reasoning_effort"] and whether any key may be set at all (D9:
// absence is the send-nothing signal — the caller sets NO key when ok is
// false, never an empty-string placeholder).
//
// Walk (squad-lead ruling 2026-09-28, FLAG 1):
//
//   - Step 3 is TERMINAL and EXCLUSIVE for a serving fallback candidate: any
//     non-empty servingCandidateEffort — including the unset token — resolves
//     to that token alone, so a fallback uses its own stored effort or
//     nothing (D10: never a value chosen for another model). Steps 4/5 run
//     only when servingCandidateEffort is "" (the agent's own primary model
//     is serving).
//   - Step 4: an agent primary INHERITED from the instance default consults
//     defaultModelEffort (config.DefaultModel.ReasoningEffort) first.
//   - Step 5: the agent's own configured effort (the agent primary's stored
//     surface) applies when the earlier steps left nothing.
//
// T1: the function SELECTS, it never validates, normalizes or trims — any
// non-empty, non-sentinel value passes through byte-exactly.
func resolveReasoningEffort(servingCandidateEffort string, primaryInherited bool, defaultModelEffort string, agentEffort string) (value string, ok bool) {
	// Step 3 (exclusive): the serving candidate's own effort, whatever it is.
	if servingCandidateEffort != "" {
		if servingCandidateEffort == reasoningEffortUnsetToken {
			// The unset token on the serving candidate is the fallback's
			// entire resolution: unset means nothing is sent (D9/D10).
			return "", false
		}
		return servingCandidateEffort, true
	}

	// Steps 4/5: the primary's own resolution path.
	if primaryInherited {
		// Step 4: the inherited primary consults the instance default
		// model's stored effort; the unset token there falls through.
		if defaultModelEffort != "" && defaultModelEffort != reasoningEffortUnsetToken {
			return defaultModelEffort, true
		}
	}
	// Step 5: the agent's own configured effort. Empty and the unset token
	// both leave nothing sent (D9).
	if agentEffort != "" && agentEffort != reasoningEffortUnsetToken {
		return agentEffort, true
	}
	return "", false
}

// agentPrimaryIsInherited reports whether agentID's primary is inherited
// from the instance default model — the C5 step (4) gate. It mirrors
// resolveAgentModel's own decision (instance.go): an agent with no own
// Model.Primary rides the default. An agent absent from cfg.Agents.List has
// no own config entry at all, hence no own model — inherited (true), the
// same nil-agentCfg default resolveAgentModel applies.
func agentPrimaryIsInherited(cfg *config.Config, agentID string) bool {
	if cfg == nil {
		return false
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			m := cfg.Agents.List[i].Model
			return m == nil || strings.TrimSpace(m.Primary) == ""
		}
	}
	return true
}

// reasoningEffortForTurn resolves the effort for ONE LLM request: the C5
// walk over the stored surfaces, from the turn's own state. Returns the
// value for llmOpts["reasoning_effort"] and whether the key may be set at
// all (D9 — false means the caller sets no key).
//
// Steps (1)/(2) of the C5 order (the web per-message effort and the
// non-web SessionMeta.ReasoningEffort) are WP-H's: they slot in as a check
// ahead of this call, never as a rewrite of it.
//
// Step (3): when the model about to serve is NOT the agent's primary (the
// routing light tier today), the serving candidate's OWN stored effort —
// its model row's — applies exclusively (D10); the primary's surfaces are
// never consulted for it. servingCandidateEffort is "" on the primary's own
// path, which is where steps (4)/(5) live.
func reasoningEffortForTurn(cfg *config.Config, agent *AgentInstance, servingModel string) (string, bool) {
	agent.mu.RLock()
	primaryModel := agent.Model
	agentEffort := agent.ReasoningEffort
	agent.mu.RUnlock()

	defaultModelEffort := ""
	primaryInherited := true
	servingCandidateEffort := ""
	if cfg != nil {
		defaultModelEffort = cfg.Agents.Defaults.DefaultModel.ReasoningEffort
		primaryInherited = agentPrimaryIsInherited(cfg, agent.ID)
		if servingModel != "" && servingModel != primaryModel {
			if mc, err := cfg.FindModelConfigBySlug(servingModel); err == nil {
				servingCandidateEffort = mc.ReasoningEffort
			}
		}
	}
	return resolveReasoningEffort(servingCandidateEffort, primaryInherited, defaultModelEffort, agentEffort)
}
