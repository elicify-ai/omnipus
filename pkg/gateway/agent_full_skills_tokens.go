// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-14 "Page warning" (ruling spec-gaps-1): the Agent
// response's full_skills_tokens, what the agent's next turn would charge for
// its installed Full skills. It is computed by the agent's own ContextBuilder
// with the turn's selection and estimator (pkg/agent/full_skills.go), never by
// a copy of the outcome rules.

import (
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// applyAgentFullSkillsTokens sets full_skills_tokens on a wire Agent. It is
// left absent when the agent has no running instance or its stored skill
// levels cannot be read (the reason is logged).
func (a *restAPI) applyAgentFullSkillsTokens(ag *gen.Agent, agentID string) {
	ag.FullSkillsTokens = nil
	if a.agentLoop == nil {
		return
	}
	registry := a.agentLoop.GetRegistry()
	if registry == nil {
		return
	}
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		return
	}
	tokens, err := inst.ContextBuilder.InstalledFullSkillsTokens()
	if err != nil {
		logsafeWarn("rest: agent Full skills size could not be computed", "agent_id", agentID, "error", err)
		return
	}
	ag.FullSkillsTokens = &tokens
}
