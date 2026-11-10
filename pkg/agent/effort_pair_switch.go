package agent

// effort_pair_switch.go — the chat frame's exact (provider, model) pair
// (effort-levels spec S-15, D5 rows 2, 3 and 8). The gateway forwards a valid
// metadata.provider under the bus metadata key "provider"; a frame without it
// keeps today's bare-name routing (S-75).

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// frameProviderKey is the bus metadata key carrying the chat frame's provider.
const frameProviderKey = "provider"

// frameProvider returns the chat frame's explicit provider ("" = none).
func frameProvider(meta map[string]string) string {
	return strings.TrimSpace(meta[frameProviderKey])
}

// primaryCandidateProvider is the provider of the agent's current primary
// candidate ("" when the primary is unpinned or there is none).
func primaryCandidateProvider(agent *AgentInstance) string {
	agent.mu.RLock()
	defer agent.mu.RUnlock()
	if len(agent.Candidates) == 0 {
		return ""
	}
	return agent.Candidates[0].Provider
}

// pairSwitchNeeded reports whether the frame asks for a primary other than
// the one loaded: another model, or the same model name on another provider.
func pairSwitchNeeded(agent *AgentInstance, provider, model string) bool {
	if model == "" {
		return false
	}
	if model != agent.Model {
		return true
	}
	return provider != "" && provider != primaryCandidateProvider(agent)
}

// switchCandidates builds the candidate chain for a live switch: an explicit
// provider pins the primary to that exact configured pair, exactly as an
// agent's own pinned primary is built at construction; a blank provider keeps
// today's bare-name resolution.
func switchCandidates(cfg *config.Config, provider, model string, agent *AgentInstance) []providers.FallbackCandidate {
	if provider == "" {
		return resolveModelCandidatesForAgent(cfg, cfg.Agents.Defaults.DefaultModel.Provider, model, agent)
	}
	return resolveAgentCandidatesWithPrimaryProvider(cfg, cfg.Agents.Defaults.DefaultModel.Provider, model, provider, agent.FallbackModels, nil)
}

// refuseUnresolvedPair ends the turn before any provider call when the frame's
// explicit (provider, model) pair is not a configured row (D5 row 8): the same
// model_unavailable error event and transcript record as a failed model
// switch, but the turn does not continue on the previous model, because the
// caller named the provider exactly.
func (rp *agentLoopRunTurnPrepare) refuseUnresolvedPair(provider, model string, cause error) (agentLoopRunTurnConductorFlow, bool) {
	rt := rp.rc.rx.rr.rq.ri.rf.rt
	rp.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
	err := fmt.Errorf("provider %q with model %q is not available: %w", provider, model, cause)
	logger.WarnCF("agent", "explicit provider and model pair refused; no provider call made",
		map[string]any{
			"agent_id":   rt.ts.agentID,
			"session_id": rt.ts.sessionKey,
			"provider":   provider,
			"model":      model,
			"error":      cause.Error(),
		})
	llm := LLMError{
		Code:      CodeModelUnavailable,
		Message:   defaultUserMessage(CodeModelUnavailable),
		Retryable: isRetryable(CodeModelUnavailable),
		Detail:    buildDetail(nil, err.Error()),
	}
	rt.al.emitEvent(
		EventKindError,
		rt.ts.eventMeta("runTurn", "turn.error"),
		ErrorPayload{
			Stage:     "model_switch",
			Code:      string(llm.Code),
			Message:   llm.Message,
			ChatID:    rt.ts.chatID,
			SessionID: string(rt.ts.routingSessionID),
		},
	)
	rt.ts.appendClassifiedError(EventKindError.String(), "model_switch", llm)
	rp.rc.ret0 = turnResult{}
	rp.rc.ret1 = err
	return agentLoopRunTurnConductorReturn, true
}
