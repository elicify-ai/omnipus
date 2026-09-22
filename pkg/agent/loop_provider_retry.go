// loop_provider_retry.go: bounded provider-throttle recovery for delegated turns.

package agent

import (
	"math/rand/v2"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const delegatedRateLimitMaxRetries = 2

// delegatedRateLimitBackoff uses equal jitter: half of the exponential delay
// is guaranteed, while the other half is randomized so a batch of delegated
// children does not retry a provider throttle in lockstep. The two retries use
// 500ms..1s and 1s..2s respectively.
func delegatedRateLimitBackoff(retry int) time.Duration {
	base := time.Second * (1 << uint(retry))
	if base > 4*time.Second {
		base = 4 * time.Second
	}
	half := base / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// callProvider gives a delegated turn with a single active provider the same
// recovery opportunity that a sibling with fallback candidates already gets.
// Root turns retain their established policy, and fallback-candidate selection
// remains owned by callProviderOnce/FallbackChain.
func (rt *agentLoopRunTurn) callProvider(
	messagesForCall []providers.Message,
	toolDefsForCall []providers.ToolDefinition,
) (*providers.LLMResponse, error) {
	for retry := 0; ; retry++ {
		rt.providerCallStreamedBytes.Store(0)
		response, err := rt.callProviderOnce(messagesForCall, toolDefsForCall)
		if err == nil || !rt.shouldRetryDelegatedRateLimit(err) || retry >= delegatedRateLimitMaxRetries {
			return response, err
		}

		// A retry after streamed text would duplicate visible and persisted
		// content. Provider 429s normally arrive before content, but keep the
		// same defensive boundary as transport retries.
		attemptStreamed := rt.providerCallStreamedBytes.Load()
		if attemptStreamed > 0 {
			logger.WarnCF("agent", "Provider rate limit after partial stream; not retrying to avoid duplicated text", map[string]any{
				"agent_id":         rt.ts.agent.ID,
				"turn_id":          rt.ts.turnID,
				"session_id":       rt.ts.transcriptSessionID,
				"model":            rt.llmModel,
				"attempt_streamed": attemptStreamed,
				"error":            err.Error(),
			})
			return response, err
		}

		backoff := delegatedRateLimitBackoff(retry)
		logger.WarnCF("agent", "Delegated provider rate limit — retrying after backoff", map[string]any{
			"agent_id":   rt.ts.agent.ID,
			"turn_id":    rt.ts.turnID,
			"session_id": rt.ts.transcriptSessionID,
			"model":      rt.llmModel,
			"retry":      retry + 1,
			"max":        delegatedRateLimitMaxRetries,
			"backoff":    backoff.String(),
			"error":      err.Error(),
		})
		rt.al.emitEvent(
			EventKindLLMRetry,
			rt.ts.eventMeta("runTurn", "turn.llm.retry"),
			LLMRetryPayload{
				Attempt:    retry + 1,
				MaxRetries: delegatedRateLimitMaxRetries,
				Reason:     "rate_limit",
				Error:      err.Error(),
				Backoff:    backoff,
			},
		)
		sleep := sleepWithContext
		if rt.al.delegatedRateLimitSleep != nil {
			sleep = rt.al.delegatedRateLimitSleep
		}
		if sleepErr := sleep(rt.turnCtx, backoff); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

func (rt *agentLoopRunTurn) shouldRetryDelegatedRateLimit(err error) bool {
	if rt == nil || rt.ts == nil || rt.ts.parentTurnState == nil || len(rt.activeCandidates) != 1 {
		return false
	}
	providerName := rt.activeCandidates[0].Provider
	failErr := providers.ClassifyError(err, providerName, rt.llmModel)
	return failErr != nil && failErr.Reason == providers.FailoverRateLimit
}
