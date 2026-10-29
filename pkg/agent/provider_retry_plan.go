package agent

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// providerRetryObserver forwards each DECIDED in-place retry of the fallback
// chain (provider-messages C-8, or the issue #1280 retry plan) as one
// provider_retry event built from named fields only (C-2, never an error
// string). The first retry of the #1280 plan also sends the plain
// "Retrying — please wait..." progress notice to a non-web channel, through
// the same BoundaryRetryNotice audience decision as the agent loop's own
// timeout retry (ADR-091 boundary 5: a steered session's notice never
// reaches the user). Since #1280 a connection failure is retried here, in
// the chain, so this is where channel users learn about it.
func (rt *agentLoopRunTurn) providerRetryObserver() providers.RetryObserver {
	return func(info providers.RetryInfo) {
		rt.al.emitEvent(
			EventKindProviderRetry,
			rt.ts.eventMeta("runTurn", "turn.llm.retry"),
			LLMRetryPayload{
				Attempt:     info.Attempt,
				Reason:      info.Reason,
				Provider:    info.Provider,
				Model:       info.Model,
				RetryAt:     info.RetryAt,
				SentAt:      info.SentAt,
				MaxAttempts: info.MaxAttempts,
				SessionID:   string(rt.ts.routingSessionID),
			},
		)
		// Only the #1280 plan sends it: a C-8 rate-limit retry of an earlier
		// candidate never did, and the next candidate answers soon anyway.
		if info.Attempt == 2 && info.MaxAttempts == providers.RetryPlanMaxAttempts {
			rt.publishRetryNotice()
		}
	}
}

// publishRetryNotice sends the channel progress notice for a provider retry
// when the turn's audience is the user (never for an internal channel or a
// steered session — audienceFor also calls the boundary observer first).
func (rt *agentLoopRunTurn) publishRetryNotice() {
	if rt.al == nil || rt.al.bus == nil || rt.ts == nil {
		return // a bare loop fixture with no outbound bus has nobody to tell
	}
	audience := rt.al.audienceFor(rt.turnCtx, steer.BoundaryRetryNotice, rt.ts.transcriptSessionID)
	if constants.IsInternalChannel(rt.ts.channel) || audience != steer.AudienceUser {
		return
	}
	if err := rt.al.bus.PublishOutbound(rt.turnCtx, bus.OutboundMessage{
		Channel:        rt.ts.channel,
		ChatID:         rt.ts.chatID,
		Content:        "Retrying — please wait...",
		ProgressNotice: true,
	}); err != nil {
		logger.WarnCF("agent", "Failed to send retry indicator",
			map[string]any{"channel": rt.ts.channel, "error": err.Error()})
	}
}

// exhaustedRanRetryPlan reports whether a fallback-chain exhaustion already
// includes the issue #1280 retry plan on one of its candidates.
func exhaustedRanRetryPlan(exhausted *providers.FallbackExhaustedError) bool {
	if exhausted == nil {
		return false
	}
	for _, a := range exhausted.Attempts {
		if a.RetryPlan {
			return true
		}
	}
	return false
}

// SetProviderRetryWait replaces how the fallback chain waits between the
// in-place retries of issue #1280's retry plan (2-40 s per step). It is the
// cross-package test seam for harnesses that drive a real AgentLoop through a
// persistent provider failure; production never calls it. A later config
// reload rebuilds the chain with real waits.
func (al *AgentLoop) SetProviderRetryWait(wait func(ctx context.Context, d time.Duration) error) {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.fallback != nil {
		al.fallback = al.fallback.WithRetryWait(wait)
	}
}
