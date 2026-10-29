package agent

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// attemptTimeoutResolver builds the per-candidate attempt limit for one
// multi-candidate fallback-chain call (issue #1485). The chain asks it once
// per attempt with the candidate being called:
//
//  1. the candidate's explicit request_timeout, when its provider reports one
//     (providers.RequestTimeoutReporter) — the operator's deliberate ceiling;
//  2. otherwise providers.ScaledAttemptTimeout over this request's estimated
//     input tokens (messages plus tool definitions), so a very large prompt
//     is no longer cut at the fixed 120 s on every candidate.
//
// The estimate is computed once per provider round, not per candidate: every
// candidate receives the same request. The streaming silence limit is
// separate and unchanged.
func (rt *agentLoopRunTurn) attemptTimeoutResolver(
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
) providers.AttemptTimeoutFunc {
	estimated := estimateToolDefsTokens(toolDefs)
	for _, m := range messages {
		estimated += estimateMessageTokens(m)
	}
	scaled := providers.ScaledAttemptTimeout(estimated)
	return func(provider, model string) time.Duration {
		if rt.ts != nil && rt.ts.agent != nil {
			rt.ts.agent.mu.RLock()
			p, err := rt.ts.agent.providerForTurnCandidate(providers.FallbackCandidate{Provider: provider, Model: model}, false)
			rt.ts.agent.mu.RUnlock()
			// A routing error is not decided here: the attempt itself
			// reports it (callProviderOnce's closure). The scaled default
			// still bounds the attempt that surfaces it.
			if err == nil {
				if reporter, ok := p.(providers.RequestTimeoutReporter); ok {
					if explicit := reporter.ExplicitRequestTimeout(); explicit > 0 {
						return explicit
					}
				}
			}
		}
		return scaled
	}
}
