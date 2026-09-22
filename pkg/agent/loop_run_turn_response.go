// loop_run_turn_response.go: Call the provider with recovery and process its response

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// agentLoopRunTurnResponseCallLLMWithRetries carries the shared state of callLLMWithRetries across its stages.
type agentLoopRunTurnResponseCallLLMWithRetries struct {
	rr                           *agentLoopRunTurnResponse
	maxRetries                   int
	compactionAttemptedOnTimeout bool
	contextCompressionFailed     bool
	tryPDFTextFallback           func() bool
	synthesizeImageRejection     func(pe *ProviderError, rejectionErr error) bool
	isTimeoutError               bool
	isContextError               bool
	ret0                         agentLoopRunTurnResponseFlow
}

// agentLoopRunTurnResponseCallLLMWithRetriesFlow reports how a block stage of agentLoopRunTurnResponseCallLLMWithRetries wants the conductor to proceed.
type agentLoopRunTurnResponseCallLLMWithRetriesFlow int

const (
	agentLoopRunTurnResponseCallLLMWithRetriesNext agentLoopRunTurnResponseCallLLMWithRetriesFlow = iota
	agentLoopRunTurnResponseCallLLMWithRetriesReturn
	agentLoopRunTurnResponseCallLLMWithRetriesContinue
	agentLoopRunTurnResponseCallLLMWithRetriesBreak
)

// callLLMWithRetries calls the LLM and applies bounded recovery for provider and media failures.
func (rr *agentLoopRunTurnResponse) callLLMWithRetries() agentLoopRunTurnResponseFlow {
	cr := &agentLoopRunTurnResponseCallLLMWithRetries{rr: rr}

	cr.rr.rq.ri.rf.callLLM = func(messagesForCall []providers.Message, toolDefsForCall []providers.ToolDefinition) (*providers.LLMResponse, error) {
		return cr.rr.rq.ri.rf.rt.callProvider(messagesForCall, toolDefsForCall)
	}

	cr.rr.rq.ri.rf.response = nil
	cr.rr.rq.ri.rf.err = nil
	cr.maxRetries = 2
	cr.compactionAttemptedOnTimeout = false
	cr.contextCompressionFailed = false // C3: tracks that compression was tried but returned ok=false

	// tryPDFTextFallback is the provider-agnostic safety net for native-PDF
	// rejections. pdfCapableModel cannot perfectly track OpenRouter's
	// per-route capabilities (e.g. Claude Haiku routed via Amazon Bedrock
	// 400s on PDF input), and each provider phrases the rejection
	// differently, so instead of matching error strings it triggers on the
	// STRUCTURAL signal: a terminal failure on a request that carried a
	// native PDF document block. It downgrades the PDF to extracted text —
	// which every model accepts — and retries once. The turn was going to
	// fail anyway, so this can only improve the outcome. Returns true when
	// the retry succeeded (response/err are updated in place).
	cr.tryPDFTextFallback = func() bool {
		return cr.rr.rq.ri.rf.tryPDFTextFallback()
	}

	// synthesizeImageRejection restores the pre-classifier friendly path for
	// image-only capability/format rejections. Image-only failures are terminal:
	// stripping the image and retrying would silently answer a different prompt.
	// PDF or mixed-media requests continue through TryMediaDowngrade below.
	cr.synthesizeImageRejection = func(pe *ProviderError, rejectionErr error) bool {
		return cr.rr.rq.ri.rf.synthesizeImageRejection(pe, rejectionErr)
	}

agentLoopRunTurnResponseCallLLMWithRetriesLoop1:
	for retry := 0; retry <= cr.maxRetries; retry++ {
		switch cr.handleAttemptFailure(retry) {
		case agentLoopRunTurnResponseCallLLMWithRetriesReturn:
			return cr.ret0
		case agentLoopRunTurnResponseCallLLMWithRetriesContinue:
			continue agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		case agentLoopRunTurnResponseCallLLMWithRetriesBreak:
			break agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		}

		// Use ClassifyError to distinguish turn-level errors from provider errors.
		// Provider-transient errors (429, 5xx, auth) are handled by the FallbackChain;
		// break here and let the error propagate to the caller.
		//
		// C1: pass the provider name (not the model name) as the second argument.
		// The provider name comes from the first active candidate; fall back to the
		// agent's configured provider field when no candidates are resolved.

		switch cr.classifyFailure() {
		case agentLoopRunTurnResponseCallLLMWithRetriesBreak:
			break agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		}

		switch cr.retryTimeout(retry) {
		case agentLoopRunTurnResponseCallLLMWithRetriesReturn:
			return cr.ret0
		case agentLoopRunTurnResponseCallLLMWithRetriesContinue:
			continue agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		case agentLoopRunTurnResponseCallLLMWithRetriesBreak:
			break agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		}

		switch cr.retryContextOverflow(retry) {
		case agentLoopRunTurnResponseCallLLMWithRetriesContinue:
			continue agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		case agentLoopRunTurnResponseCallLLMWithRetriesBreak:
			break agentLoopRunTurnResponseCallLLMWithRetriesLoop1
		}

		break
	}
	return agentLoopRunTurnResponseNext
}

// handleAttemptFailure handles repair, media downgrade, and exhausted fallback errors for one attempt.
func (cr *agentLoopRunTurnResponseCallLLMWithRetries) handleAttemptFailure(retry int) agentLoopRunTurnResponseCallLLMWithRetriesFlow {
	cr.rr.rq.ri.rf.response, cr.rr.rq.ri.rf.err = cr.rr.rq.ri.rf.callLLM(cr.rr.rq.ri.rf.callMessages, cr.rr.rq.ri.rf.providerToolDefs)
	if cr.rr.rq.ri.rf.err == nil {
		return agentLoopRunTurnResponseCallLLMWithRetriesBreak
	}
	// ADR-087 D3/D9 (main call site): a tool call cut off at the
	// output-token limit gets one bounded repair before falling
	// through to ClassifyError/the media-downgrade/PDF paths below.
	if repaired, ok := cr.rr.rq.ri.rf.rt.al.evaluateTruncatedToolCallError(cr.rr.rq.ri.rf.rt.ts, cr.rr.rq.ri.rf.err, cr.rr.rq.ri.rf.callMessages, retry, cr.maxRetries, &cr.rr.rq.ri.toolCallTruncationRepairUsed, cr.rr.rq.ri.rf.rt.llmModel, cr.rr.rq.ri.rf.rt.iteration); ok {
		cr.rr.rq.ri.rf.callMessages = repaired
		return agentLoopRunTurnResponseCallLLMWithRetriesContinue
	}
	// Preserve the friendly image-only synthesis before the generic media
	// downgrade path. PDF and mixed-media failures deliberately fall through.
	pe := errorToProviderError(cr.rr.rq.ri.rf.err)
	if cr.synthesizeImageRejection(pe, cr.rr.rq.ri.rf.err) {
		return agentLoopRunTurnResponseCallLLMWithRetriesBreak
	}
	// Wave 1 (ADR-051 RD2): classifier-gated media downgrade-retry.
	// Replaces the prior inline substring "image input" strip path
	// (which only handled vision-capability errors and ran on every
	// retry iteration). The new helper:
	//   1. classifies pe via the shared classifier — only retries
	//      CodeMediaUnsupported (never content-policy/auth/unknown).
	//   2. hoists the per-turn guard onto ts.mediaRetryDone — the
	//      retry cannot fire twice in the same turn.
	//   3. handles both PDF and image media (PDF via
	//      downgradePDFMediaToText; image via stripRejectedImageMedia).
	if downgradeResult := TryMediaDowngrade(cr.rr.rq.ri.rf.rt.ts, cr.rr.rq.ri.rf.callMessages, pe); downgradeResult.Applied {
		// FR-017a (Slice E / Wave 1b): the helper's verdict decides
		// the recorded turn classifier code. The classifier-primary
		// path always reports CodeMediaUnsupported; the
		// outcome-based fallback may report a different code (the
		// original classifier was inconclusive). Read the helper's
		// verdict via the typed result (Wave 1 TD-M8 — the bool
		// return was overloaded and lost the trigger; this commit
		// adds DowngradeTrigger + MediaClass so the warn-log and
		// the FR-017a relabel are both data-derived from the
		// helper, not from a re-classification at the call site).
		// The message fallback is err.Error() (not ""): pe is never
		// actually nil on this path (errorToProviderError only
		// returns nil for a nil err, and this call site is inside
		// the `err != nil` retry branch), but classifyByProviderError
		// falls back to the message when pe is nil — passing the
		// real error text keeps this call correct if that
		// invariant is ever loosened, instead of being a latent
		// no-op that silently classifies "" today.
		helperCode := classifyByProviderError(pe, cr.rr.rq.ri.rf.err.Error())
		logger.WarnCF("agent",
			"provider rejected media input — retrying with downgraded media block",
			map[string]any{
				"agent_id":    cr.rr.rq.ri.rf.rt.ts.agent.ID,
				"model":       cr.rr.rq.ri.rf.rt.llmModel,
				"error":       cr.rr.rq.ri.rf.err.Error(),
				"code":        string(helperCode),
				"trigger":     string(downgradeResult.Trigger),
				"media_class": string(downgradeResult.MediaClass),
			})
		cr.rr.rq.ri.rf.response, cr.rr.rq.ri.rf.err = cr.rr.rq.ri.rf.callLLM(cr.rr.rq.ri.rf.callMessages, cr.rr.rq.ri.rf.providerToolDefs)
		if cr.rr.rq.ri.rf.err == nil {
			// FR-017a success edge (Slice E / Wave 1b): when the
			// outcome-based fallback fired (Trigger ==
			// TriggerOutcomeFallback) AND the retry succeeded,
			// the recorded turn classifier verdict MUST be
			// relabeled to CodeMediaUnsupported — the classifier
			// now LABELS the outcome (per the ADR §4
			// "classify the outcome" contract), not just the
			// trigger. The classifier-primary path's helperCode
			// is already CodeMediaUnsupported so no relabel is
			// needed for that branch.
			if downgradeResult.Trigger == TriggerOutcomeFallback {
				cr.rr.rq.ri.rf.rt.ts.setOutcomeRelabel(CodeMediaUnsupported)
			}
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
		// ADR-087 D3/D9 (media-downgrade retry call site): same
		// bounded repair as the main call site above.
		if repaired, ok := cr.rr.rq.ri.rf.rt.al.evaluateTruncatedToolCallError(cr.rr.rq.ri.rf.rt.ts, cr.rr.rq.ri.rf.err, cr.rr.rq.ri.rf.callMessages, retry, cr.maxRetries, &cr.rr.rq.ri.toolCallTruncationRepairUsed, cr.rr.rq.ri.rf.rt.llmModel, cr.rr.rq.ri.rf.rt.iteration); ok {
			cr.rr.rq.ri.rf.callMessages = repaired
			return agentLoopRunTurnResponseCallLLMWithRetriesContinue
		}
	}
	if cr.rr.rq.ri.rf.rt.ts.hardAbortRequested() && errors.Is(cr.rr.rq.ri.rf.err, context.Canceled) {
		cr.rr.rq.ri.turnStatus = TurnEndStatusAborted
		cr.rr.ret0, cr.rr.ret1 = cr.rr.rq.ri.rf.rt.al.abortTurn(cr.rr.rq.ri.rf.rt.ts, "llm_call", hardInterruptAbortReason)
		cr.ret0 = agentLoopRunTurnResponseReturn
		return agentLoopRunTurnResponseCallLLMWithRetriesReturn
	}

	// I3: if the FallbackChain already exhausted all candidates, don't retry
	// in the outer loop — the chain already tried everything. Break immediately
	// so the error surfaces to the caller without redundant delay.
	//
	// Exception: if every attempt was a transient mid-stream reset (http2
	// body closed, GOAWAY, connection reset, etc.) and no content was
	// streamed to the client yet, the chain can be retried whole — a fresh
	// connection will be attempted for each candidate. This is the primary
	// fix for "0 tokens" turns caused by HTTP/2 pooled-connection drops:
	// the FallbackChain marks candidates in cooldown and returns
	// FallbackExhaustedError even for a single-candidate config, bypassing
	// the normal ClassifyError → isTimeoutError retry path below.
	var exhaustedErr *providers.FallbackExhaustedError
	if errors.As(cr.rr.rq.ri.rf.err, &exhaustedErr) {
		// Check whether every failed attempt was a transient stream reset.
		// Skipped-cooldown entries (Skipped==true) are not counted as
		// streaming failures; we only need all *attempted* calls to have
		// been transient drops.
		allTransient := len(exhaustedErr.Attempts) > 0
		for _, a := range exhaustedErr.Attempts {
			if a.Skipped {
				continue // cooldown skip — not a new attempt, ignore
			}
			if !isTransientStreamError(a.Error) {
				allTransient = false
				break
			}
		}
		if allTransient && retry < cr.maxRetries {
			if cr.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
				return agentLoopRunTurnResponseCallLLMWithRetriesBreak
			}
			// Apply the same "don't retry if partial content was already
			// streamed" guard as the isTimeoutError path to avoid duplicating
			// text in an in-progress SPA bubble.
			if sc, ok := cr.rr.rq.ri.rf.rt.ts.lastStreamer.(interface{ StreamedContentLen() int }); ok && sc.StreamedContentLen() > 0 {
				logger.WarnCF("agent", "Transient stream reset (fallback exhausted) after partial stream; not retrying to avoid duplicated text", map[string]any{
					"agent_id":  cr.rr.rq.ri.rf.rt.ts.agent.ID,
					"iteration": cr.rr.rq.ri.rf.rt.iteration,
					"streamed":  sc.StreamedContentLen(),
					"error":     cr.rr.rq.ri.rf.err.Error(),
				})
				return agentLoopRunTurnResponseCallLLMWithRetriesBreak
			}
			// Backoff: 500ms × 2^retry, capped at 4s (shorter than the
			// timeout-retry backoff — streaming resets are transient and
			// resolve quickly on a fresh connection).
			backoff := 500 * time.Millisecond * (1 << uint(retry))
			if backoff > 4*time.Second {
				backoff = 4 * time.Second
			}
			logger.WarnCF("agent", "Transient streaming reset (fallback exhausted) — retrying LLM call", map[string]any{
				"agent_id": cr.rr.rq.ri.rf.rt.ts.agent.ID,
				"model":    cr.rr.rq.ri.rf.rt.llmModel,
				"retry":    retry,
				"backoff":  backoff.String(),
				"error":    cr.rr.rq.ri.rf.err.Error(),
			})
			cr.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindLLMRetry,
				cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
				LLMRetryPayload{
					Attempt:    retry + 1,
					MaxRetries: cr.maxRetries,
					Reason:     "streaming_reset",
					Error:      cr.rr.rq.ri.rf.err.Error(),
					Backoff:    backoff,
				},
			)
			if sleepErr := sleepWithContext(cr.rr.rq.ri.rf.rt.turnCtx, backoff); sleepErr != nil {
				if cr.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
					cr.rr.rq.ri.turnStatus = TurnEndStatusAborted
					cr.rr.ret0, cr.rr.ret1 = cr.rr.rq.ri.rf.rt.al.abortTurn(cr.rr.rq.ri.rf.rt.ts, "llm_retry_backoff", hardInterruptAbortReason)
					cr.ret0 = agentLoopRunTurnResponseReturn
					return agentLoopRunTurnResponseCallLLMWithRetriesReturn
				}
				cr.rr.rq.ri.rf.err = sleepErr
				return agentLoopRunTurnResponseCallLLMWithRetriesBreak
			}
			return agentLoopRunTurnResponseCallLLMWithRetriesContinue
		}
		// All candidates failed — if the request carried a native PDF
		// block, every provider may have rejected it. Degrade to text
		// and try once more across the chain before surfacing.
		cr.tryPDFTextFallback()
		return agentLoopRunTurnResponseCallLLMWithRetriesBreak
	}
	return agentLoopRunTurnResponseCallLLMWithRetriesNext
}

// classifyFailure classifies the provider failure for retry handling.
func (cr *agentLoopRunTurnResponseCallLLMWithRetries) classifyFailure() agentLoopRunTurnResponseCallLLMWithRetriesFlow {
	activeProviderName := ""
	if len(cr.rr.rq.ri.rf.rt.activeCandidates) > 0 {
		activeProviderName = cr.rr.rq.ri.rf.rt.activeCandidates[0].Provider
	}
	failErr := providers.ClassifyError(cr.rr.rq.ri.rf.err, activeProviderName, cr.rr.rq.ri.rf.rt.llmModel)

	cr.isTimeoutError = false
	cr.isContextError = false
	if failErr != nil {
		cr.isTimeoutError = failErr.Reason == providers.FailoverTimeout
		cr.isContextError = failErr.Reason == providers.FailoverContextOverflow
		// Retriable provider errors (rate limit, auth, overloaded) are handled
		// by the FallbackChain. Don't retry inline — break so the error surfaces.
		if failErr.IsRetriable() && !cr.isTimeoutError {
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
		// Non-retriable, non-timeout, non-context errors: break immediately.
		// First, if the request carried a native PDF block, try the
		// provider-agnostic PDF→text fallback once — a terminal error
		// here is very likely a PDF-input rejection.
		if !cr.isTimeoutError && !cr.isContextError {
			cr.tryPDFTextFallback()
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
	} else {
		// ClassifyError returned nil: the error is not recognizable as a
		// provider-level condition. Before giving up, check whether it is a
		// transient mid-stream reset (e.g. a GOAWAY frame or a network drop
		// that is wrapped by layers ClassifyError does not unwrap). If so,
		// treat it as a timeout-equivalent so the isTimeoutError retry path
		// below fires, rather than breaking immediately with 0 tokens.
		if isTransientStreamError(cr.rr.rq.ri.rf.err) {
			cr.isTimeoutError = true
		} else {
			// Genuinely unknown error. Don't retry.
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
	}
	return agentLoopRunTurnResponseCallLLMWithRetriesNext
}

// retryTimeout performs bounded timeout recovery and backoff.
func (cr *agentLoopRunTurnResponseCallLLMWithRetries) retryTimeout(retry int) agentLoopRunTurnResponseCallLLMWithRetriesFlow {
	if cr.isTimeoutError && retry < cr.maxRetries {
		// FIX 2: re-check hard-abort FIRST. A user cancel mid-stream can
		// surface as a transport-drop string (classified FailoverTimeout)
		// rather than context.Canceled. Without this guard the branch would
		// emit a spurious "Retrying…" message + stray LLMRetry/TurnTimeout
		// events before the canceled turnCtx collapses the backoff. Breaking
		// here lets the canceled turn finalize quietly.
		if cr.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
		// FIX 1: only inline-retry a transport-drop when NO partial content
		// was already streamed to the client for this attempt. If tokens were
		// already streamed, the dropped attempt sent no `done` frame, so the
		// SPA's bubble stays in "streaming" state; re-streaming the full
		// response on retry would concatenate attempt-2 onto attempt-1 and
		// visibly duplicate text. In that case break instead — the turn
		// surfaces the error normally and the SPA finalizes the bubble as
		// interrupted. When there is no active streamer (non-streaming /
		// Chat path) or it streamed nothing yet (drop before the first
		// token — the common, safe case), retry as before.
		if sc, ok := cr.rr.rq.ri.rf.rt.ts.lastStreamer.(interface{ StreamedContentLen() int }); ok && sc.StreamedContentLen() > 0 {
			logger.WarnCF("agent", "Transport drop after partial stream; not inline-retrying to avoid duplicated text", map[string]any{
				"agent_id":  cr.rr.rq.ri.rf.rt.ts.agent.ID,
				"iteration": cr.rr.rq.ri.rf.rt.iteration,
				"streamed":  sc.StreamedContentLen(),
				"error":     cr.rr.rq.ri.rf.err.Error(),
			})
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
		// I1: emit EventKindTurnTimeout when a timeout error is detected.
		cr.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindTurnTimeout,
			cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.timeout"),
			TurnTimeoutPayload{
				TimeoutSeconds: cr.rr.rq.ri.rf.rt.ts.agent.TimeoutSeconds,
				Compacted:      cr.compactionAttemptedOnTimeout,
				Retried:        retry > 0,
			},
		)
		// Timeout recovery: compact context if it's heavily loaded, then retry once.
		//
		// FR-028 / B-38: the check reads the one budget B — the retired
		// summarize_token_percent no longer scales the window here. What
		// it measures is the request the RETRY would assemble: the
		// messages of the failed call plus any recall span that became
		// active during it (the retry re-assembles from the session, so
		// an active span is part of the next request even though it was
		// not part of callMessages). windowTrim counts that span the
		// same way (FR-019 drop-span-first).
		if !cr.compactionAttemptedOnTimeout && !cr.rr.rq.ri.rf.rt.ts.opts.NoHistory && !cr.rr.rq.ri.rf.rt.ts.agent.budgetChecksExempt() {
			// The sent surface, not the whole registry — same helper
			// windowTrim measures with (FR-028; see the pre-turn site).
			toolDefsTokens := cr.rr.rq.ri.rf.rt.al.sentToolSurfaceTokens(cr.rr.rq.ri.rf.rt.ts.agent, cr.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID, cr.rr.rq.ri.rf.rt.ts.sessionKey)
			retryMessages := cr.rr.rq.ri.rf.callMessages
			if span := cr.rr.rq.ri.rf.rt.al.activeRecallSpan(cr.rr.rq.ri.rf.rt.ts.sessionKey); span != nil {
				retryMessages = append(append([]providers.Message(nil), cr.rr.rq.ri.rf.callMessages...), span.Messages()...)
			}
			if isOverContextBudgetTokens(agentContextBudget(cr.rr.rq.ri.rf.rt.ts.agent), retryMessages, toolDefsTokens) {
				cr.compactionAttemptedOnTimeout = true
				// windowTrim has three possible outcomes here:
				//  1. ok=true — a real eviction occurred, either window Turns were
				//     dropped or dropping the active recall span alone (FR-019)
				//     brought the window back under budget — rebuild messages and
				//     retry (this branch).
				//  2. ok=false, NothingToTrim=true — nothing was eligible to evict
				//     (e.g. a fresh turn with no compressible history) — not a
				//     failure; fall through to backoff+retry unchanged.
				//  3. ok=false, NothingToTrim=false — TruncateHistory was attempted
				//     but the window genuinely did not shrink — abandon the retry.
				compression, ok := cr.rr.rq.ri.rf.rt.al.windowTrim(cr.rr.rq.ri.rf.rt.ts.agent, cr.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID, cr.rr.rq.ri.rf.rt.ts.sessionKey)
				if ok {
					cr.rr.rq.ri.rf.rt.al.emitEvent(
						EventKindContextCompress,
						cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.context.compress"),
						ContextCompressPayload{
							Reason:            ContextCompressReasonRetry,
							DroppedMessages:   compression.DroppedMessages,
							RemainingMessages: compression.RemainingMessages,
						},
					)
					// I1: emit EventKindCompactionRetry when compaction is triggered
					// during timeout recovery (separate from the general compress event).
					cr.rr.rq.ri.rf.rt.al.emitEvent(
						EventKindCompactionRetry,
						cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.compaction_retry"),
						CompactionRetryPayload{
							DroppedMessages:   compression.DroppedMessages,
							RemainingMessages: compression.RemainingMessages,
						},
					)
					// Site-3: post-timeout-trim assembly.
					newHistory := cr.rr.rq.ri.rf.rt.ts.agent.Sessions.GetHistory(cr.rr.rq.ri.rf.rt.ts.sessionKey)
					cr.rr.rq.ri.messages = cr.rr.rq.ri.rf.rt.al.assembleMessages(cr.rr.rq.ri.rf.rt.turnCtx, cr.rr.rq.ri.rf.rt.ts, newHistory, "", nil, activeSkillNames(cr.rr.rq.ri.rf.rt.ts.agent, cr.rr.rq.ri.rf.rt.ts.opts))
					cr.rr.continuationChain = nil
					if cr.rr.rq.ri.rf.rt.ts.continuationUnresolved() {
						// ADR-087 D6.7: a history rebuild loses the D6
						// continuation chain (it was never persisted
						// to session history) — re-append it exactly
						// once so the model still sees what it has
						// already written.
						cr.rr.continuationChain = continuationChainMessages(cr.rr.rq.ri.rf.rt.ts.continuationAccumulated())
						cr.rr.rq.ri.messages = append(cr.rr.rq.ri.messages, cr.rr.continuationChain...)
					}
					cr.rr.rq.ri.rf.callMessages = cr.rr.rq.ri.messages
					if cr.rr.rq.ri.gracefulTerminal {
						cr.rr.rq.ri.rf.callMessages = append(append([]providers.Message(nil), cr.rr.rq.ri.messages...), cr.rr.rq.ri.rf.rt.ts.interruptHintMessage())
					}
				} else if compression.NothingToTrim {
					// Nothing eligible to evict (e.g. a fresh turn with a
					// single-message window, no compressible history yet).
					// This is not a compaction failure — the error that got us
					// here was a transient network/streaming reset (isTimeoutError),
					// not a genuine context-overflow rejection from the provider.
					// Abandoning the whole retry here would defeat the timeout-
					// retry path for any turn that happens to sit near the
					// context-budget edge with little/no history. Fall through to
					// the backoff-and-retry below with the existing messages
					// unchanged — the retried call may simply succeed.
					logger.DebugCF("agent", "Window trim skipped during timeout recovery: nothing eligible to evict; proceeding with retry",
						map[string]any{"agent_id": cr.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": cr.rr.rq.ri.rf.rt.iteration})
				} else {
					// Trim was attempted against real compressible history and
					// genuinely failed (e.g. TruncateHistory could not shrink the
					// window). Unlike the isContextError path below, though, the
					// error that got us HERE is a transient transport drop
					// (streaming reset / GOAWAY), and the trim was triggered only
					// by the isOverContextBudget check above — a conservative
					// proactive heuristic (75% of the window), NOT a hard provider
					// context-overflow rejection. So the window is not necessarily
					// over the real limit, and canceling the retry would abandon a
					// call that often just succeeds on a second try (UAT: an 11th
					// tool call tipped the 75% heuristic mid-task and a break here
					// truncated a task that completed fine on retry). Fall through
					// to the backoff-and-retry below rather than returning partial.
					logger.WarnCF("agent", "Window trim failed during timeout recovery; proceeding to retry without compaction",
						map[string]any{"agent_id": cr.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": cr.rr.rq.ri.rf.rt.iteration})
				}
			}
		}

		// Exponential backoff with full jitter (base 2s, max 30s).
		base := 2 * time.Second
		calculated := base * (1 << uint(retry)) // 2^retry * base
		if calculated > 30*time.Second {
			calculated = 30 * time.Second
		}
		jitter := time.Duration(rand.Int64N(int64(calculated) + 1))
		// M3: enforce a minimum backoff floor of 500ms so jitter can never produce
		// a zero or near-zero delay (rand.Int64N(1) == 0 when calculated == 0).
		backoff := jitter
		if backoff < 500*time.Millisecond {
			backoff = 500 * time.Millisecond
		}
		cr.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindLLMRetry,
			cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
			LLMRetryPayload{
				Attempt:    retry + 1,
				MaxRetries: cr.maxRetries,
				Reason:     "timeout",
				Error:      cr.rr.rq.ri.rf.err.Error(),
				Backoff:    backoff,
			},
		)
		// ADR-091 boundary 5 (landing order §6, FR-B-001): a steered
		// session's retry notice is never the user's audience.
		// audienceFor also calls steer.BoundaryObserver.Observe before this
		// decision is acted on (FR-B-014).
		retryAudience := cr.rr.rq.ri.rf.rt.al.audienceFor(cr.rr.rq.ri.rf.rt.turnCtx, steer.BoundaryRetryNotice, cr.rr.rq.ri.rf.rt.ts.transcriptSessionID)
		if retry == 0 && !constants.IsInternalChannel(cr.rr.rq.ri.rf.rt.ts.channel) && retryAudience == steer.AudienceUser {
			if notifyErr := cr.rr.rq.ri.rf.rt.al.bus.PublishOutbound(cr.rr.rq.ri.rf.rt.turnCtx, bus.OutboundMessage{
				Channel: cr.rr.rq.ri.rf.rt.ts.channel,
				ChatID:  cr.rr.rq.ri.rf.rt.ts.chatID,
				Content: "Retrying — please wait...",
			}); notifyErr != nil {
				logger.WarnCF("agent", "Failed to send retry indicator",
					map[string]any{"channel": cr.rr.rq.ri.rf.rt.ts.channel, "error": notifyErr.Error()})
			}
		}
		logger.WarnCF("agent", "Timeout error, retrying after backoff", map[string]any{
			"error":   cr.rr.rq.ri.rf.err.Error(),
			"retry":   retry,
			"backoff": backoff.String(),
		})
		if sleepErr := sleepWithContext(cr.rr.rq.ri.rf.rt.turnCtx, backoff); sleepErr != nil {
			if cr.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
				cr.rr.rq.ri.turnStatus = TurnEndStatusAborted
				cr.rr.ret0, cr.rr.ret1 = cr.rr.rq.ri.rf.rt.al.abortTurn(cr.rr.rq.ri.rf.rt.ts, "llm_timeout_backoff", hardInterruptAbortReason)
				cr.ret0 = agentLoopRunTurnResponseReturn
				return agentLoopRunTurnResponseCallLLMWithRetriesReturn
			}
			cr.rr.rq.ri.rf.err = sleepErr
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
		return agentLoopRunTurnResponseCallLLMWithRetriesContinue
	}
	return agentLoopRunTurnResponseCallLLMWithRetriesNext
}

// retryContextOverflow compacts and retries a context-overflow failure.
func (cr *agentLoopRunTurnResponseCallLLMWithRetries) retryContextOverflow(retry int) agentLoopRunTurnResponseCallLLMWithRetriesFlow {
	if cr.isContextError && retry < cr.maxRetries && !cr.rr.rq.ri.rf.rt.ts.opts.NoHistory {
		// C3: if a previous compression attempt returned ok=false and we're
		// still getting context errors, retrying with identical data won't help.
		// Break to surface the error rather than burning the remaining budget.
		if cr.contextCompressionFailed {
			logger.WarnCF("agent", "Context overflow persists after failed compression; aborting retry",
				map[string]any{"agent_id": cr.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": cr.rr.rq.ri.rf.rt.iteration, "retry": retry})
			return agentLoopRunTurnResponseCallLLMWithRetriesBreak
		}
		cr.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindLLMRetry,
			cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
			LLMRetryPayload{
				Attempt:    retry + 1,
				MaxRetries: cr.maxRetries,
				Reason:     "context_limit",
				Error:      cr.rr.rq.ri.rf.err.Error(),
			},
		)
		logger.WarnCF(
			"agent",
			"Context window error detected, attempting compression",
			map[string]any{
				"error": cr.rr.rq.ri.rf.err.Error(),
				"retry": retry,
			},
		)

		// ADR-091 boundary 5 (landing order §6, FR-B-001): a steered
		// session's retry notice is never the user's audience.
		// audienceFor also calls steer.BoundaryObserver.Observe before this
		// decision is acted on (FR-B-014).
		overflowAudience := cr.rr.rq.ri.rf.rt.al.audienceFor(cr.rr.rq.ri.rf.rt.turnCtx, steer.BoundaryRetryNotice, cr.rr.rq.ri.rf.rt.ts.transcriptSessionID)
		if retry == 0 && !constants.IsInternalChannel(cr.rr.rq.ri.rf.rt.ts.channel) && overflowAudience == steer.AudienceUser {
			if notifyErr := cr.rr.rq.ri.rf.rt.al.bus.PublishOutbound(cr.rr.rq.ri.rf.rt.turnCtx, bus.OutboundMessage{
				Channel: cr.rr.rq.ri.rf.rt.ts.channel,
				ChatID:  cr.rr.rq.ri.rf.rt.ts.chatID,
				Content: "Context window exceeded. Compressing history and retrying...",
			}); notifyErr != nil {
				logger.WarnCF("agent", "Failed to notify user of context compression",
					map[string]any{"channel": cr.rr.rq.ri.rf.rt.ts.channel, "error": notifyErr.Error()})
			}
		}

		// force: the PROVIDER rejected this request with a context
		// error, so our own estimate said it fit and was wrong.
		// Honouring the "already fits" guard here would make the
		// retry byte-identical to the call that just failed.
		if compression, ok := cr.rr.rq.ri.rf.rt.al.windowTrimForce(cr.rr.rq.ri.rf.rt.ts.agent, cr.rr.rq.ri.rf.rt.ts.opts.TranscriptSessionID, cr.rr.rq.ri.rf.rt.ts.sessionKey, true); ok {
			cr.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindContextCompress,
				cr.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.context.compress"),
				ContextCompressPayload{
					Reason:            ContextCompressReasonRetry,
					DroppedMessages:   compression.DroppedMessages,
					RemainingMessages: compression.RemainingMessages,
				},
			)
		} else {
			// C3: windowTrim returned ok=false (nothing to trim). Mark the
			// flag so the NEXT retry attempt will break rather than burning more
			// budget on identical data. We still allow this single retry through
			// because the provider might succeed without context reduction.
			cr.contextCompressionFailed = true
			logger.WarnCF("agent", "Window trim failed during context overflow recovery; will not retry further",
				map[string]any{"agent_id": cr.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": cr.rr.rq.ri.rf.rt.iteration})
		}

		// Site-4: post-context-overflow-trim assembly.
		newHistory := cr.rr.rq.ri.rf.rt.ts.agent.Sessions.GetHistory(cr.rr.rq.ri.rf.rt.ts.sessionKey)
		cr.rr.rq.ri.messages = cr.rr.rq.ri.rf.rt.al.assembleMessages(cr.rr.rq.ri.rf.rt.turnCtx, cr.rr.rq.ri.rf.rt.ts, newHistory, "", nil, activeSkillNames(cr.rr.rq.ri.rf.rt.ts.agent, cr.rr.rq.ri.rf.rt.ts.opts))
		cr.rr.continuationChain = nil
		if cr.rr.rq.ri.rf.rt.ts.continuationUnresolved() {
			// ADR-087 D6.7: same rebuild-restoration as Site-3 above.
			cr.rr.continuationChain = continuationChainMessages(cr.rr.rq.ri.rf.rt.ts.continuationAccumulated())
			cr.rr.rq.ri.messages = append(cr.rr.rq.ri.messages, cr.rr.continuationChain...)
		}
		cr.rr.rq.ri.rf.callMessages = cr.rr.rq.ri.messages
		if cr.rr.rq.ri.gracefulTerminal {
			cr.rr.rq.ri.rf.callMessages = append(append([]providers.Message(nil), cr.rr.rq.ri.messages...), cr.rr.rq.ri.rf.rt.ts.interruptHintMessage())
		}
		return agentLoopRunTurnResponseCallLLMWithRetriesContinue
	}
	return agentLoopRunTurnResponseCallLLMWithRetriesNext
}

// handleProviderResponse handles terminal provider errors and records a successful provider response.
func (rr *agentLoopRunTurnResponse) handleProviderResponse() agentLoopRunTurnResponseFlow {
	if rr.rq.ri.rf.err != nil {
		// C2: check for context cancellation/timeout before reporting a generic
		// "LLM call failed" error — these are user/system actions, not LLM failures.
		// ADR-066 D7: typed, never silent — see typedTurnExit.
		if errors.Is(rr.rq.ri.rf.err, context.Canceled) || errors.Is(rr.rq.ri.rf.err, context.DeadlineExceeded) {
			var res turnResult
			var exitErr error
			res, rr.rq.ri.turnStatus, exitErr = rr.rq.ri.rf.rt.al.typedTurnExit(rr.rq.ri.rf.rt.ts, rr.rq.ri.rf.rt.iteration, rr.rq.ri.rf.rt.llmModel, rr.rq.ri.rf.err)
			rr.ret0 = res
			rr.ret1 = exitErr
			return agentLoopRunTurnResponseReturn
		}
	}
	if rr.rq.ri.rf.err != nil {
		rr.rq.ri.turnStatus = TurnEndStatusError
		// Wave 1 (error-provenance hardening, ADR-051 §RD5 CRIT-001):
		// never emit raw err.Error() to the assistant-facing bus /
		// transcript. Build a *ProviderError from the wrapped chain
		// (best-effort — falls back to substring matching on err.Error()
		// when no FailoverError is in the chain) for the live
		// ErrorPayload, and classify via TranslateTurnError (ADR-087
		// D5): it recognizes a *common.ToolArgumentsError in err's chain
		// (CodeToolCallTruncated vs CodeToolArgs, per whether the
		// refusal carries real truncation evidence) before falling back
		// to the exact same errorToProviderError + TranslateLLMError
		// path this used to call directly — so a 401/413 buried in err
		// still classifies correctly (Codex C6).
		pe := errorToProviderError(rr.rq.ri.rf.err)
		llm := TranslateTurnError(rr.rq.ri.rf.err)

		// FR-017a: label an inconclusive residual 4xx after a
		// successful strip-retry. A later distinct classified
		// failure keeps its own code so live and persist agree.
		if outcomeRelabelApplies(llm.Code, rr.rq.ri.rf.rt.ts.outcomeRelabel) {
			llm.Code = rr.rq.ri.rf.rt.ts.outcomeRelabel
			llm.Message = UserMessageForCode(rr.rq.ri.rf.rt.ts.outcomeRelabel)
		}

		rr.rq.ri.rf.rt.al.emitEvent(
			EventKindError,
			rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{
				Stage: "llm", ChatID: rr.rq.ri.rf.rt.ts.opts.ChatID,
				Code:          string(llm.Code),
				Message:       llm.Message,
				ProviderError: pe,
				SessionID:     string(rr.rq.ri.rf.rt.ts.routingSessionID),
			},
		)
		// FR-002: persist the translated provider error to the transcript
		// (write choke point — ADR-051 §RD5). pe threaded through so the
		// classifier sees status/body, not the stringified err.
		rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
		logger.ErrorCF("agent", "LLM call failed",
			map[string]any{
				"agent_id":  rr.rq.ri.rf.rt.ts.agent.ID,
				"iteration": rr.rq.ri.rf.rt.iteration,
				"model":     rr.rq.ri.rf.rt.llmModel,
				"error":     rr.rq.ri.rf.err.Error(),
				"code":      string(llm.Code),
			})
		// ADR-087 D6.8: exhausted retries make no further provider call —
		// runTurn's deferred preserveTruncatedAccumulator keeps a D6
		// continuation left unresolved by a prior round.
		rr.ret0 = turnResult{}
		rr.ret1 = fmt.Errorf("LLM call failed after retries: %w", rr.rq.ri.rf.err)
		return agentLoopRunTurnResponseReturn
	}

	if rr.rq.ri.rf.rt.al.hooks != nil {
		llmResp, decision := rr.rq.ri.rf.rt.al.hooks.AfterLLM(rr.rq.ri.rf.rt.turnCtx, &LLMHookResponse{
			Meta:     rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.response"),
			Model:    rr.rq.ri.rf.rt.llmModel,
			Response: rr.rq.ri.rf.response,
			Channel:  rr.rq.ri.rf.rt.ts.channel,
			ChatID:   rr.rq.ri.rf.rt.ts.chatID,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if llmResp != nil && llmResp.Response != nil {
				rr.rq.ri.rf.response = llmResp.Response
			}
		case HookActionAbortTurn:
			rr.rq.ri.turnStatus = TurnEndStatusError
			rr.ret0 = turnResult{}
			rr.ret1 = rr.rq.ri.rf.rt.al.hookAbortError(rr.rq.ri.rf.rt.ts, "after_llm", decision)
			return agentLoopRunTurnResponseReturn
		case HookActionHardAbort:
			_ = rr.rq.ri.rf.rt.ts.requestHardAbort()
			rr.rq.ri.turnStatus = TurnEndStatusAborted
			rr.ret0, rr.ret1 = rr.rq.ri.rf.rt.al.abortTurn(rr.rq.ri.rf.rt.ts, "after_llm", decision.Reason)
			return agentLoopRunTurnResponseReturn
		}
	}

	// ── Orphan tool-call markup: the single strip choke point ──
	//
	// Some models emit tool calls as XML-ish markup in the completion
	// TEXT and rely on the hosting provider to parse it back into
	// `tool_calls`. When that upstream parse does not complete, the
	// unconsumed remainder is flushed into the text instead — see
	// providers.DetectOrphanToolCallMarkup for the full dialect and the
	// live evidence. Two things must happen, and they are separate:
	//
	//  1. The residue must never be shown to a user as the assistant's
	//     own words. That is THIS strip, applied once here so every
	//     downstream consumer (citations, the transcript writers, the
	//     assistant history message, the terminal answer) sees text that
	//     has already been cleaned. The live-stream surface is filtered
	//     independently at the ChatStream callback above, because the
	//     gateway streamer persists what it accumulated, not this value.
	//
	//  2. A round that produced NO tool calls has to be repaired or
	//     reported — handled in the no-tool-calls branch below. Silence
	//     is the defect there, not the malformation.
	//
	// ReasoningContent is stripped too: the no-tool-calls branch falls
	// back to it when Content is empty, so leaving it alone would just
	// move the leak. Mutating it here is safe — handleReasoning below
	// receives its own string copy.
	rr.orphanMarkup, rr.hasOrphanMarkup = stripOrphanToolCallMarkup(rr.rq.ri.rf.response)
	if rr.hasOrphanMarkup {
		logger.WarnCF("agent", "LLM emitted unparseable tool-call markup as text; markup suppressed",
			map[string]any{
				"agent_id":      rr.rq.ri.rf.rt.ts.agent.ID,
				"iteration":     rr.rq.ri.rf.rt.iteration,
				"model":         rr.rq.ri.rf.rt.llmModel,
				"marker":        rr.orphanMarkup.Marker,
				"markup_chars":  len(rr.orphanMarkup.Markup),
				"prose_chars":   len(rr.orphanMarkup.Prose),
				"tool_calls":    len(rr.rq.ri.rf.response.ToolCalls),
				"finish_reason": rr.rq.ri.rf.response.FinishReason,
			})
	}

	reasoningContent := rr.rq.ri.rf.response.Reasoning
	if reasoningContent == "" {
		reasoningContent = rr.rq.ri.rf.response.ReasoningContent
	}
	go rr.rq.ri.rf.rt.al.handleReasoning(
		rr.rq.ri.rf.rt.turnCtx,
		reasoningContent,
		rr.rq.ri.rf.rt.ts.channel,
		rr.rq.ri.rf.rt.al.targetReasoningChannelID(rr.rq.ri.rf.rt.ts.channel),
	)
	rr.rq.ri.rf.rt.al.emitEvent(
		EventKindLLMResponse,
		rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.response"),
		LLMResponsePayload{
			ContentLen:   len(rr.rq.ri.rf.response.Content),
			ToolCalls:    len(rr.rq.ri.rf.response.ToolCalls),
			HasReasoning: rr.rq.ri.rf.response.Reasoning != "" || rr.rq.ri.rf.response.ReasoningContent != "",
		},
	)

	llmResponseFields := map[string]any{
		"agent_id":       rr.rq.ri.rf.rt.ts.agent.ID,
		"iteration":      rr.rq.ri.rf.rt.iteration,
		"content_chars":  len(rr.rq.ri.rf.response.Content),
		"tool_calls":     len(rr.rq.ri.rf.response.ToolCalls),
		"reasoning":      rr.rq.ri.rf.response.Reasoning,
		"target_channel": rr.rq.ri.rf.rt.al.targetReasoningChannelID(rr.rq.ri.rf.rt.ts.channel),
		"channel":        rr.rq.ri.rf.rt.ts.channel,
	}
	if rr.rq.ri.rf.response.Usage != nil {
		llmResponseFields["prompt_tokens"] = rr.rq.ri.rf.response.Usage.PromptTokens
		llmResponseFields["completion_tokens"] = rr.rq.ri.rf.response.Usage.CompletionTokens
		llmResponseFields["total_tokens"] = rr.rq.ri.rf.response.Usage.TotalTokens
	}
	logger.DebugCF("agent", "LLM response", llmResponseFields)
	return agentLoopRunTurnResponseNext
}

// recordToolCalls normalizes and records tool calls before execution begins.
func (rr *agentLoopRunTurnResponse) recordToolCalls() {
	rr.rq.ri.rf.rt.al.flushContinuationAccumulator(rr.rq.ri.rf.rt.ts, &rr.continuationChain)
	if isTruncatedFinishReason(rr.rq.ri.rf.response.FinishReason) {
		rr.rq.ri.rf.rt.ts.markContinuationPending()
	} else {
		rr.rq.ri.rf.rt.ts.resolveContinuation()
	}

	rr.normalizedToolCalls = make([]providers.ToolCall, 0, len(rr.rq.ri.rf.response.ToolCalls))
	for _, tc := range rr.rq.ri.rf.response.ToolCalls {
		rr.normalizedToolCalls = append(rr.normalizedToolCalls, providers.NormalizeToolCall(tc))
	}

	toolNames := make([]string, 0, len(rr.normalizedToolCalls))
	for _, tc := range rr.normalizedToolCalls {
		toolNames = append(toolNames, tc.Name)
	}
	logger.InfoCF("agent", "LLM requested tool calls",
		map[string]any{
			"agent_id":  rr.rq.ri.rf.rt.ts.agent.ID,
			"tools":     toolNames,
			"count":     len(rr.normalizedToolCalls),
			"iteration": rr.rq.ri.rf.rt.iteration,
		})

	// FR-7.5/NFR-1: the narration text accompanying this round of tool
	// calls may reference memories recalled in a prior iteration. Scan it
	// for citations before executing the tools.
	if rr.citationTracker != nil {
		rr.citationTracker.EmitCitations(rr.rq.ri.rf.response.Content)
	}

	assistantMsg := providers.Message{
		Role:             "assistant",
		Content:          rr.rq.ri.rf.response.Content,
		ReasoningContent: rr.rq.ri.rf.response.ReasoningContent,
	}
	for _, tc := range rr.normalizedToolCalls {
		argumentsJSON, marshalErr := json.Marshal(tc.Arguments)
		if marshalErr != nil {
			logger.WarnCF("agent", "failed to marshal tool call arguments", map[string]any{"tool": tc.Name, "error": marshalErr.Error()})
			argumentsJSON = []byte("{}")
		}
		// ADR-066 D4×D6 (T066-13): an over-bound arguments string never
		// enters memory. The dispatch below refuses the call from the
		// PARSED args (FR-016 — this elision cannot mask that check),
		// and the refusal result names the real size, so echoing the
		// full blob into the assistant message would plant budget-
		// busting bytes in the archive and every later request that D5
		// can never empty (an assistant message is not a tool result) —
		// DS-3 #2 at the default window would then trip the D6 guard
		// that B-19 forbids. The elided echo is what the archive, the
		// window and every reload all see, so live == reload holds with
		// no projection entry.
		if bound := toolArgumentsBound(rr.rq.ri.cfg); UserMessageChars(string(argumentsJSON)) > bound {
			elided, elideErr := json.Marshal(map[string]any{
				"_omnipus":   "arguments_elided_over_bound",
				"size_chars": UserMessageChars(string(argumentsJSON)),
				"cap_chars":  bound,
			})
			if elideErr == nil {
				argumentsJSON = elided
			} else {
				argumentsJSON = []byte("{}")
			}
		}
		extraContent := tc.ExtraContent
		thoughtSignature := ""
		if tc.Function != nil {
			thoughtSignature = tc.Function.ThoughtSignature
		}
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Name: tc.Name,
			Function: &providers.FunctionCall{
				Name:             tc.Name,
				Arguments:        string(argumentsJSON),
				ThoughtSignature: thoughtSignature,
			},
			ExtraContent:     extraContent,
			ThoughtSignature: thoughtSignature,
		})
	}
	rr.rq.ri.messages = append(rr.rq.ri.messages, assistantMsg)
	if !rr.rq.ri.rf.rt.ts.opts.NoHistory {
		rr.rq.ri.rf.rt.ts.agent.Sessions.AddFullMessage(rr.rq.ri.rf.rt.ts.sessionKey, assistantMsg)
	}

	// Bug #416 fix: persist the narration text the LLM emitted alongside
	// this round's tool calls. Without this, only the FINAL iteration's text
	// reaches the transcript — intermediate "Okay, I've saved X." sentences
	// are shown live via wsStreamer.Update but never written to transcript.jsonl.
	//
	// We write BEFORE the tool_call entries so the transcript order mirrors
	// the live stream: [text segment N] → [tool_call round N] → …
	//
	// Tokens/cost are 0 here — the turn total is attributed to the final
	// assistant entry only (wsStreamer.Finalize or appendAssistantTranscript).
	rr.rq.ri.rf.rt.ts.appendIntermediateAssistantTranscript(rr.rq.ri.rf.response.Content)
	if rr.rq.ri.rf.response.Content != "" {
		// The narration is now in the transcript. If this round's streamer
		// ends up being finalized (the turn exits via max_tool_iterations
		// exhaustion, where the last executed round is a tool-call round),
		// suppress its duplicate transcript write (#416 gate fix). The check
		// mirrors appendIntermediateAssistantTranscript's own content==""
		// early-return so the mark fires only when a write actually happened.
		rr.rq.ri.rf.rt.ts.markLastStreamerTranscriptPersisted()
	}

	rr.rq.ri.rf.rt.ts.setPhase(TurnPhaseTools)
}
