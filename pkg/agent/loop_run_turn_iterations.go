// loop_run_turn_iterations.go: Run the provider and tool iteration conductor

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// truncationSuccessAction is evaluateTruncatedSuccess's verdict — see its
// doc comment for what each caller must do.
type truncationSuccessAction int

// Orphan tool-call markup repair — see the choke point in runTurn and
// providers.DetectOrphanToolCallMarkup for the failure this handles.
const (
	// maxOrphanToolMarkupRepairs bounds the re-prompts spent on one turn.
	// Two is enough to clear a one-off upstream parse failure (the observed
	// case) without letting a model that cannot produce structured tool calls
	// at all spend the whole iteration budget getting nowhere.
	maxOrphanToolMarkupRepairs = 2
	// orphanToolMarkupRetryReason labels the retry on the event bus so an
	// operator can tell this apart from an empty-response retry.
	orphanToolMarkupRetryReason = "orphan_tool_markup"
	// orphanToolMarkupStage labels the terminal error event/transcript entry.
	orphanToolMarkupStage = "orphan_tool_markup"
)

const (
	// truncationActionNone: the guard did not match (not truncated, or the
	// response carried tool calls) — the caller's existing logic runs
	// completely unchanged.
	truncationActionNone truncationSuccessAction = iota
	// truncationActionContinue: D6 — the caller must set `messages` to
	// verdict.messages and `continue turnLoop`.
	truncationActionContinue
	// truncationActionEnd: D4a or D4b — the caller must set finalContent to
	// verdict.finalContent and `break turnLoop`.
	truncationActionEnd
)

// agentLoopRunTurnConductorRunIterations carries the shared state of runIterations across its stages.
type agentLoopRunTurnConductorRunIterations struct {
	rc                      *agentLoopRunTurnConductor
	orphanToolMarkupRepairs int
	responseContent         string
	ret0                    agentLoopRunTurnConductorFlow
}

// agentLoopRunTurnConductorRunIterationsFlow reports how a block stage of agentLoopRunTurnConductorRunIterations wants the conductor to proceed.
type agentLoopRunTurnConductorRunIterationsFlow int

const (
	agentLoopRunTurnConductorRunIterationsNext agentLoopRunTurnConductorRunIterationsFlow = iota
	agentLoopRunTurnConductorRunIterationsReturn
	agentLoopRunTurnConductorRunIterationsContinue
	agentLoopRunTurnConductorRunIterationsBreak
	agentLoopRunTurnConductorRunIterationsContinueL1
	agentLoopRunTurnConductorRunIterationsBreakL1
)

// agentLoopRunTurnConductorRunIterationsRetryResponse carries the shared state of runIterations across its stages.
type agentLoopRunTurnConductorRunIterationsRetryResponse struct {
	cn *agentLoopRunTurnConductorRunIterations
}

// agentLoopRunTurnConductorRunIterationsRetryResponseFlow reports how a block stage of agentLoopRunTurnConductorRunIterationsRetryResponse wants the conductor to proceed.
type agentLoopRunTurnConductorRunIterationsRetryResponseFlow int

const (
	agentLoopRunTurnConductorRunIterationsRetryResponseNext agentLoopRunTurnConductorRunIterationsRetryResponseFlow = iota
	agentLoopRunTurnConductorRunIterationsRetryResponseReturn
	agentLoopRunTurnConductorRunIterationsRetryResponseContinue
	agentLoopRunTurnConductorRunIterationsRetryResponseBreak
	agentLoopRunTurnConductorRunIterationsRetryResponseContinueL2
	agentLoopRunTurnConductorRunIterationsRetryResponseBreakL2
)

// runIterations runs provider and tool iterations and incorporates late steering before finalization.
func (rc *agentLoopRunTurnConductor) runIterations() agentLoopRunTurnConductorFlow {
	er := &agentLoopRunTurnConductorRunIterationsRetryResponse{}

	er.cn = &agentLoopRunTurnConductorRunIterations{rc: rc}

	emptyResponseRetries := 0
	const maxEmptyResponseRetries = 1
	// orphanToolMarkupRepairs counts how many times this turn has re-prompted
	// a model that emitted its tool call as unparseable text (see the strip
	// choke point below). Bounded so a model that cannot comply ends the turn
	// with a visible error instead of looping on the user's budget.
	er.cn.orphanToolMarkupRepairs = 0
	// continuationChain (ADR-087 D6.7) holds the exact {assistant, user} pair
	// a previous round appended to `messages` as the D6 continuation chain,
	// so the next round REPLACES it — by identity, via stripContinuationChain
	// — instead of accumulating duplicate copies of the answer-so-far. nil
	// means no chain is currently live in `messages`. Declared before
	// turnLoop so it survives both `continue turnLoop` and `goto turnLoop`.

turnLoop:
	for er.cn.rc.rx.rr.rq.ri.rf.rt.ts.currentIteration() < er.cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.MaxIterations || len(er.cn.rc.rx.rr.rq.ri.pendingMessages) > 0 || func() bool {
		graceful, _ := er.cn.rc.rx.rr.rq.ri.rf.rt.ts.gracefulInterruptRequested()
		return graceful
	}() {
		switch er.cn.prepareIteration() {
		case agentLoopRunTurnConductorRunIterationsReturn:
			return er.cn.ret0
		case agentLoopRunTurnConductorRunIterationsContinueL1:
			continue turnLoop
		case agentLoopRunTurnConductorRunIterationsBreakL1:
			break turnLoop
		}

		if len(er.cn.rc.rx.rr.rq.ri.rf.response.ToolCalls) == 0 || er.cn.rc.rx.rr.rq.ri.gracefulTerminal {
			switch er.cn.handleInitialResponse() {
			case agentLoopRunTurnConductorRunIterationsReturn:
				return er.cn.ret0
			case agentLoopRunTurnConductorRunIterationsContinue:
				continue turnLoop
			case agentLoopRunTurnConductorRunIterationsContinueL1:
				continue turnLoop
			case agentLoopRunTurnConductorRunIterationsBreakL1:
				break turnLoop
			}

			// Empty response recovery (FR-006): if LLM returned empty content with no
			// reasoning and no tool calls, retry once before surfacing a fallback message.
			//
			// H3: perform the retry in an inner loop that calls callLLM directly, so we
			// do NOT increment the outer iteration counter (which would consume the agent's
			// MaxIterations budget for what is purely a provider-level retry).
		agentLoopRunTurnConductorRunIterationsRetryResponseLoop1:
			for strings.TrimSpace(er.cn.responseContent) == "" && emptyResponseRetries < maxEmptyResponseRetries {
				emptyResponseRetries++
				logger.WarnCF("agent", "Empty response from LLM, retrying", map[string]any{
					"agent_id":  er.cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"iteration": er.cn.rc.rx.rr.rq.ri.rf.rt.iteration,
					"attempt":   emptyResponseRetries,
				})
				er.cn.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindLLMRetry,
					er.cn.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
					LLMRetryPayload{
						Attempt:    emptyResponseRetries,
						MaxRetries: maxEmptyResponseRetries,
						Reason:     "empty_response",
					},
				)
				// I1: also emit the dedicated EventKindEmptyResponseRetry for subscribers
				// that specifically track empty-response retry behavior.
				er.cn.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindEmptyResponseRetry,
					er.cn.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.empty_response_retry"),
					EmptyResponseRetryPayload{
						Attempt:    emptyResponseRetries,
						MaxRetries: maxEmptyResponseRetries,
					},
				)
				// Re-call the LLM directly without advancing the outer turn iteration.

				switch er.callAndApplyRetry() {
				case agentLoopRunTurnConductorRunIterationsRetryResponseBreak:
					break agentLoopRunTurnConductorRunIterationsRetryResponseLoop1
				case agentLoopRunTurnConductorRunIterationsRetryResponseContinueL2:
					continue turnLoop
				case agentLoopRunTurnConductorRunIterationsRetryResponseBreakL2:
					break turnLoop
				}
			}
			// If the inner retry loop set an error, surface it via the outer error path.

			switch er.cn.surfaceEmptyRetryOutcome() {
			case agentLoopRunTurnConductorRunIterationsReturn:
				return er.cn.ret0
			}

			// ADR-087 D3/D9: re-test the CURRENT response. This branch was
			// entered on the ORIGINAL response's len(ToolCalls) == 0, but the
			// empty-response retry above may since have replaced `response`
			// with a D3-repaired one that carries a valid, smaller tool call.
			// The condition is byte-identical to this branch's own entry
			// condition, so the graceful-terminal case still finishes here — a
			// winding-down turn must never start executing tools — and
			// everything else falls out of this block into the ordinary
			// tool-dispatch path below.

			switch er.cn.finishTextResponse() {
			case agentLoopRunTurnConductorRunIterationsContinueL1:
				continue turnLoop
			case agentLoopRunTurnConductorRunIterationsBreakL1:
				break turnLoop
			}
		}

		// ADR-087 D6.10 / D4 (last paragraph, "truncated and has complete
		// tool calls"): a round with tool calls never reaches
		// evaluateTruncatedSuccess (its guard requires len(ToolCalls)==0),
		// so this is the one place that handles a D6 chain across a
		// tool-calling round.
		//
		// FIRST, unconditionally, settle whatever the accumulator still holds.
		// Everything this round is about to write — the assistant tool_calls
		// message into session history, and appendIntermediateAssistantTranscript's
		// narration entry into the transcript — lands AFTER any earlier
		// continuation prefix was produced, so the prefix has to reach disk
		// first or the record comes out as [P2][P1+P3] instead of
		// [P1][P2][P3]. flushContinuationAccumulator writes it in order and
		// clears it, which is also what stops it being emitted a second time
		// at turn end.
		//
		// THEN the two truncation cases:
		//   - FinishReason NOT truncated: an ordinary follow-up round
		//     resolves any D6 chain a prior round left pending.
		//   - FinishReason truncated but the response still carried
		//     complete tool calls (parseStreamResponse succeeds once every
		//     collected argument set decodes, regardless of finishReason):
		//     execute the calls once (unchanged below) and carry the
		//     truncation forward as PENDING ONLY. This round's own narration
		//     is deliberately NOT seeded into the accumulator: the tool-call
		//     branch immediately below persists that exact text itself, three
		//     ways (messages, Sessions.AddFullMessage,
		//     appendIntermediateAssistantTranscript), so seeding it here made
		//     every later accumulator reader emit the narration a second time
		//     — and, if a later call errored, made preserveTruncatedAccumulator
		//     append a second identical copy marked truncated. Never
		//     re-executed because of a continuation: nothing here re-dispatches
		//     these tool calls.

		switch er.cn.dispatchToolCalls() {
		case agentLoopRunTurnConductorRunIterationsReturn:
			return er.cn.ret0
		case agentLoopRunTurnConductorRunIterationsContinueL1:
			continue turnLoop
		case agentLoopRunTurnConductorRunIterationsBreakL1:
			break turnLoop
		}
	}

	// ADR-071 §4.3.1(a): advance and sweep this bucket's search-promotion
	// horizon exactly once per REAL conversational turn, not once per
	// turnLoop round-trip. This deliberately sits OUTSIDE (after) the
	// turnLoop for-loop above, unlike the (unrelated) MCP discovery TTL tick
	// it used to sit next to: `iteration`, incremented once per pass through
	// that loop, counts LLM-call rounds within a single turn — a turn that
	// makes several sequential tool calls before its final response can pass
	// through the loop body, and therefore the old in-loop call site, many
	// times before the user ever sees a reply. With
	// searchPromotionHorizonTurns = 5 that could silently expire a
	// ToolSearch promotion mid-turn, even though the field's own doc comment
	// says it counts "across the whole conversation" (turns, not rounds).
	//
	// This site fires once per natural exit of the turnLoop for-loop, which
	// is once per real conversational turn in the overwhelmingly common
	// case. The one nuance: late-arriving steering messages `goto turnLoop`
	// below to continue THIS SAME turn rather than starting a new one — each
	// such continuation is itself a further round of natural back-to-back
	// tool-calling activity on the same turn, so ticking again when it in
	// turn naturally exits is consistent with "count real conversational
	// turns" rather than "count LLM-call rounds," not a double-count of one
	// turn. A turn that instead exits via an early return above (hard abort,
	// delegate park) never reaches this line, so it does not tick at all —
	// deliberate: neither is a completed conversational round from the
	// user's perspective, and a parked turn is expected to resume later
	// rather than count as elapsed time against the horizon.
	er.cn.rc.rx.rr.rq.ri.rf.rt.al.tickSearchPromotionHorizon(er.cn.rc.rx.rr.rq.ri.rf.rt.ts.manifestBucket())

	if steerMsgs, steerCorrelationIDs := er.cn.rc.rx.rr.rq.ri.rf.rt.al.dequeueSteeringMessagesForScope(er.cn.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after turn completion; continuing turn before finalizing",
			map[string]any{
				"agent_id":       er.cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"steering_count": len(steerMsgs),
				"session_key":    er.cn.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey,
			})
		er.cn.rc.rx.rr.rq.ri.pendingMessages = append(er.cn.rc.rx.rr.rq.ri.pendingMessages, steerMsgs...)
		er.cn.rc.rx.rr.rq.ri.pendingSteeringReceipts = append(er.cn.rc.rx.rr.rq.ri.pendingSteeringReceipts, steerCorrelationIDs...)
		er.cn.rc.rx.finalContent = ""
		// I2: guard against bypassing the hard iteration ceiling via goto.
		// If the ceiling is exceeded, fall through to finalization rather than
		// re-entering turnLoop, which would be invalid at this point anyway.
		if er.cn.rc.rx.rr.rq.ri.rf.rt.ts.currentIteration() < 2*er.cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.MaxIterations {
			goto turnLoop
		}
	}
	return agentLoopRunTurnConductorNext
}

// callAndApplyRetry calls the provider for an empty-response retry and applies its outcome.
func (er *agentLoopRunTurnConductorRunIterationsRetryResponse) callAndApplyRetry() agentLoopRunTurnConductorRunIterationsRetryResponseFlow {
	retryResp, retryErr := er.cn.rc.rx.rr.rq.ri.rf.callLLM(er.cn.rc.rx.rr.rq.ri.rf.callMessages, er.cn.rc.rx.rr.rq.ri.rf.providerToolDefs)
	if retryErr != nil {
		// ADR-087 D3/D9 (empty-response retry call site): same
		// bounded repair as the other two call sites — issue the
		// repaired call directly rather than looping, since this
		// mini-loop's own iteration budget is about EMPTY
		// content, a different concern from a truncated tool
		// call.
		if repaired, ok := er.cn.rc.rx.rr.rq.ri.rf.rt.al.evaluateTruncatedToolCallError(er.cn.rc.rx.rr.rq.ri.rf.rt.ts, retryErr, er.cn.rc.rx.rr.rq.ri.rf.callMessages, 0, 1, &er.cn.rc.rx.rr.rq.ri.toolCallTruncationRepairUsed, er.cn.rc.rx.rr.rq.ri.rf.rt.llmModel, er.cn.rc.rx.rr.rq.ri.rf.rt.iteration); ok {
			er.cn.rc.rx.rr.rq.ri.rf.callMessages = repaired
			retryResp, retryErr = er.cn.rc.rx.rr.rq.ri.rf.callLLM(er.cn.rc.rx.rr.rq.ri.rf.callMessages, er.cn.rc.rx.rr.rq.ri.rf.providerToolDefs)
		}
		if retryErr != nil {
			// Propagate the error back to the outer error-handling block by
			// overwriting response/err and breaking out of both loops.
			er.cn.rc.rx.rr.rq.ri.rf.response = nil
			er.cn.rc.rx.rr.rq.ri.rf.err = retryErr
			return agentLoopRunTurnConductorRunIterationsRetryResponseBreak
		}
	}
	er.cn.rc.rx.rr.rq.ri.rf.response = retryResp
	er.cn.responseContent = er.cn.rc.rx.rr.rq.ri.rf.response.Content
	if er.cn.responseContent == "" && er.cn.rc.rx.rr.rq.ri.rf.response.ReasoningContent != "" {
		er.cn.responseContent = er.cn.rc.rx.rr.rq.ri.rf.response.ReasoningContent
	}
	// ADR-087 D4/D6/D9: the empty-response retry's own
	// successful attempt goes through the SAME success-arm
	// handler as the other two call sites (§7.9).
	if verdict := er.cn.rc.rx.rr.rq.ri.rf.rt.al.evaluateTruncatedSuccess(er.cn.rc.rx.rr.rq.ri.rf.rt.ts, er.cn.rc.rx.rr.rq.ri.rf.response, er.cn.rc.rx.rr.rq.ri.messages, er.cn.rc.rx.rr.rq.ri.rf.providerToolDefs, er.cn.rc.rx.rr.rq.ri.gracefulTerminal, er.cn.rc.rx.rr.rq.ri.rf.rt.iteration, er.cn.rc.rx.rr.rq.ri.rf.rt.llmModel, &er.cn.rc.rx.rr.continuationChain); verdict.action != truncationActionNone {
		switch verdict.action {
		case truncationActionContinue:
			er.cn.rc.rx.rr.rq.ri.messages = verdict.messages
			return agentLoopRunTurnConductorRunIterationsRetryResponseContinueL2
		case truncationActionEnd:
			er.cn.rc.rx.finalContent = verdict.finalContent
			return agentLoopRunTurnConductorRunIterationsRetryResponseBreakL2
		}
	}
	// ADR-087 D3/D9: a repaired call can come back carrying a
	// (smaller, complete) TOOL CALL — the whole point of the D3
	// repair note is to solicit one. This mini-loop lives inside
	// the direct-answer branch, which was entered because the
	// ORIGINAL response had none, so nothing below inspects
	// response.ToolCalls: the repaired call would be discarded and
	// the turn would end on the defaultResponse fallback with
	// markTurnFailed, as if the model had stayed silent. Stop
	// retrying and let the fall-through below hand it to the
	// normal tool-dispatch path — which is what the main call site
	// would have done with the identical response (D9's "identical
	// at all three sites").
	if len(er.cn.rc.rx.rr.rq.ri.rf.response.ToolCalls) > 0 && !er.cn.rc.rx.rr.rq.ri.gracefulTerminal {
		return agentLoopRunTurnConductorRunIterationsRetryResponseBreak
	}
	return agentLoopRunTurnConductorRunIterationsRetryResponseNext
}

// prepareIteration prepares one provider iteration and records its usage.
func (cn *agentLoopRunTurnConductorRunIterations) prepareIteration() agentLoopRunTurnConductorRunIterationsFlow {
	switch cn.rc.rx.rr.rq.ri.beginIteration() {
	case agentLoopRunTurnIterationReturn:
		cn.rc.ret0 = cn.rc.rx.rr.rq.ri.ret0
		cn.rc.ret1 = cn.rc.rx.rr.rq.ri.ret1
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	case agentLoopRunTurnIterationBreak:
		return agentLoopRunTurnConductorRunIterationsBreakL1
	case agentLoopRunTurnIterationBreakL1:
		return agentLoopRunTurnConductorRunIterationsBreakL1
	}

	// FR-003, FR-041: Apply per-agent tool policy at LLM-call assembly time.
	// FilterToolsByPolicy enforces global × agent deny>ask>allow resolution and
	// the ScopeCore-on-custom-agent gate before the tool list reaches the LLM.
	// Tools with effective policy "ask" are included — the mid-turn policy snapshot
	// (FR-041) handles human-in-the-loop confirmation; see the ADR-058
	// quarantine gate and recordToolDenial (tool_denial.go).

	switch cn.rc.rx.rr.rq.prepareToolSurface() {
	case agentLoopRunTurnRequestReturn:
		cn.rc.ret0 = cn.rc.rx.rr.rq.ret0
		cn.rc.ret1 = cn.rc.rx.rr.rq.ret1
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	}

	// Transparent repair of orphan tool_use / tool_result pairs in the
	// outbound history. OpenRouter's mid-stream provider rotation can leave
	// the context jsonl desynced with its own transcript; we reconcile from
	// the transcript before every LLM call so Anthropic never sees a broken
	// pair. No-op fast path when there are no orphans (the common case).

	cn.rc.rx.rr.rq.prepareCallMessages()

	// ADR-088 D3 Layer 1 narrowing is active for THIS request exactly
	// when goalForce.layer1 holds and gracefulTerminal hasn't nilled the
	// tool surface. review-round-1 finding #6 (kept under the D3
	// amendment, 2026-09-07): native_search must never ride alongside
	// the narrowed pair — it would silently add a THIRD callable "tool"
	// (the provider's own built-in search) outside {set_goal[,
	// AskUserQuestion]}, undermining the narrowed surface's "exactly the
	// pair" promise even though nothing forces the model to touch it
	// anymore (provider tool-choice forcing is deleted — determinism now
	// comes from the immediate post-turn correction, goal_loop.go, not
	// the request shape). Suppress native search for this one request
	// while narrowing is active; the client-side search_web tool is not
	// offered here either (it's excluded from goalForce.narrowed, same
	// as every other non-goal tool). No tool-choice option is ever set —
	// see evaluateGoalForcing's doc comment for why.

	switch cn.rc.rx.rr.rq.prepareLLMRequest() {
	case agentLoopRunTurnRequestReturn:
		cn.rc.ret0 = cn.rc.rx.rr.rq.ret0
		cn.rc.ret1 = cn.rc.rx.rr.rq.ret1
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	}

	switch cn.rc.rx.rr.callLLMWithRetries() {
	case agentLoopRunTurnResponseReturn:
		cn.rc.ret0 = cn.rc.rx.rr.ret0
		cn.rc.ret1 = cn.rc.rx.rr.ret1
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	}

	switch cn.rc.rx.rr.handleProviderResponse() {
	case agentLoopRunTurnResponseReturn:
		cn.rc.ret0 = cn.rc.rx.rr.ret0
		cn.rc.ret1 = cn.rc.rx.rr.ret1
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	}

	// Record the provider-reported usage on the turn. debitLLMUsage also
	// records ts.lastUsage — the write that used to sit ~90 lines above
	// this block, guarded by its own
	// turnStateFromContext(turnCtx) lookup that resolves to this very same
	// ts (withTurnState(turnCtx, ts) is how turnCtx was built). Two copies
	// of one accounting step is how the ADR-087 D3.9 refused-attempt debit
	// came to omit SetLastUsage; there is now exactly one.
	//
	// ADR-087 D8: the old lastUsage site also called the now-deleted
	// SetLastFinishReason("for SubTurn truncation detection") — that
	// consumer was never built (GetLastFinishReason had zero callers); see
	// §5.1 of the ADR for the recorded gap.
	if cn.rc.rx.rr.rq.ri.rf.response != nil {
		cn.rc.rx.rr.rq.ri.rf.rt.al.debitLLMUsage(cn.rc.rx.rr.rq.ri.rf.rt.ts, cn.rc.rx.rr.rq.ri.rf.rt.llmModel, cn.rc.rx.rr.rq.ri.rf.response.Usage)
	}
	return agentLoopRunTurnConductorRunIterationsNext
}

// handleInitialResponse handles orphan markup, steering, and truncated success.
func (cn *agentLoopRunTurnConductorRunIterations) handleInitialResponse() agentLoopRunTurnConductorRunIterationsFlow {
	cn.responseContent = cn.rc.rx.rr.rq.ri.rf.response.Content
	if cn.responseContent == "" && cn.rc.rx.rr.rq.ri.rf.response.ReasoningContent != "" {
		cn.responseContent = cn.rc.rx.rr.rq.ri.rf.response.ReasoningContent
	}

	// ── Orphan tool-call markup with NO tool call: repair, or fail loudly ──
	//
	// The model tried to call a tool and nothing came back as a
	// structured call, so this round did no work at all. Ending the
	// turn here — which is what happened before this branch existed —
	// presents whatever prose survived the strip as a finished answer
	// and, when the whole response was markup, presents nothing at
	// all: a spinner that resolves into silence, with the goal record
	// left untouched and no error anywhere. That is the defect.
	//
	// Repair first: re-prompt with an explicit instruction to use the
	// tool-calling API, bounded by maxOrphanToolMarkupRepairs so a
	// model that cannot comply does not burn the turn. The repair note
	// is appended to the in-flight request only — never to session
	// history — so a transient protocol fault leaves no residue in the
	// durable archive, and the residue itself is never echoed back
	// (that would invite the model to repeat it verbatim).
	//
	// A graceful interrupt is the one case that does not repair: the
	// user asked the turn to wind down, so the stripped response
	// stands and the empty-response fallback below covers it.
	if cn.rc.rx.rr.hasOrphanMarkup && len(cn.rc.rx.rr.rq.ri.rf.response.ToolCalls) == 0 {
		switch {
		case cn.rc.rx.rr.rq.ri.gracefulTerminal:
			// Fall through: honour the interrupt, do not re-prompt.
		case cn.orphanToolMarkupRepairs < maxOrphanToolMarkupRepairs:
			cn.orphanToolMarkupRepairs++
			logger.WarnCF("agent", "Tool call arrived as unparseable text; re-prompting the model",
				map[string]any{
					"agent_id":      cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"iteration":     cn.rc.rx.rr.rq.ri.rf.rt.iteration,
					"model":         cn.rc.rx.rr.rq.ri.rf.rt.llmModel,
					"marker":        cn.rc.rx.rr.orphanMarkup.Marker,
					"finish_reason": cn.rc.rx.rr.rq.ri.rf.response.FinishReason,
					"attempt":       cn.orphanToolMarkupRepairs,
					"max_attempts":  maxOrphanToolMarkupRepairs,
				})
			cn.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindLLMRetry,
				cn.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
				LLMRetryPayload{
					Attempt:    cn.orphanToolMarkupRepairs,
					MaxRetries: maxOrphanToolMarkupRepairs,
					Reason:     orphanToolMarkupRetryReason,
				},
			)
			cn.rc.rx.rr.rq.ri.messages = append(cn.rc.rx.rr.rq.ri.messages, orphanToolMarkupRepairMessage(cn.rc.rx.rr.rq.ri.rf.response.FinishReason))
			return agentLoopRunTurnConductorRunIterationsContinue
		default:
			// Repair budget spent. Fail LOUDLY — a typed error event
			// for the live client and a typed transcript entry for
			// replay. CodeToolArgs is the contract's existing
			// "tool-call argument format error"; the vocabulary is
			// contract data (contracts/components/schemas/LLMError.yaml),
			// so this path reuses it rather than inventing a code the
			// SPA has no catalogue entry for.
			cn.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
			llm := LLMError{
				Code:      CodeToolArgs,
				Message:   UserMessageForCode(CodeToolArgs),
				Retryable: isRetryable(CodeToolArgs),
			}
			logger.WarnCF("agent", "Tool call kept arriving as unparseable text; ending turn with an error",
				map[string]any{
					"agent_id":      cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"iteration":     cn.rc.rx.rr.rq.ri.rf.rt.iteration,
					"model":         cn.rc.rx.rr.rq.ri.rf.rt.llmModel,
					"marker":        cn.rc.rx.rr.orphanMarkup.Marker,
					"finish_reason": cn.rc.rx.rr.rq.ri.rf.response.FinishReason,
					"attempts":      cn.orphanToolMarkupRepairs,
				})
			cn.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindError,
				cn.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage:     orphanToolMarkupStage,
					Code:      string(llm.Code),
					Message:   llm.Message,
					ChatID:    cn.rc.rx.rr.rq.ri.rf.rt.ts.opts.ChatID,
					SessionID: string(cn.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				},
			)
			cn.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
			// UAT A-12: wrap the TYPED refusal a provider raises for an
			// undecodable tool call. A task attempt's turn error is
			// classified by type only (task_attempt_turn_error.go's
			// attemptRecoverableTurnErrorCode — errors.As for
			// *common.ToolArgumentsError, then TranslateTurnError ->
			// CodeToolArgs, the same code this exit already reports to
			// the client). Untyped, this exhaustion failed the task on
			// the spot instead of consuming one attempt.
			cn.rc.ret0 = turnResult{}
			cn.rc.ret1 = fmt.Errorf(
				"model emitted unparseable tool-call markup (marker %q, finish_reason %q) after %d repair attempts: %w",
				cn.rc.rx.rr.orphanMarkup.Marker, cn.rc.rx.rr.rq.ri.rf.response.FinishReason, cn.orphanToolMarkupRepairs,
				common.NewToolArgumentsError("", common.ErrToolArgumentsUndecodable, false))
			cn.ret0 = agentLoopRunTurnConductorReturn
			return agentLoopRunTurnConductorRunIterationsReturn
		}
	}

	// FR-7.5/NFR-1: scan the assistant's final answer for references to
	// memories recalled earlier this turn and emit op:cited events.
	if cn.rc.rx.rr.citationTracker != nil {
		cn.rc.rx.rr.citationTracker.EmitCitations(cn.responseContent)
	}
	if steerMsgs, steerCorrelationIDs := cn.rc.rx.rr.rq.ri.rf.rt.al.dequeueSteeringMessagesForScope(cn.rc.rx.rr.rq.ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after direct LLM response; continuing turn",
			map[string]any{
				"agent_id":       cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"iteration":      cn.rc.rx.rr.rq.ri.rf.rt.iteration,
				"steering_count": len(steerMsgs),
			})
		cn.rc.rx.rr.rq.ri.pendingMessages = append(cn.rc.rx.rr.rq.ri.pendingMessages, steerMsgs...)
		cn.rc.rx.rr.rq.ri.pendingSteeringReceipts = append(cn.rc.rx.rr.rq.ri.pendingSteeringReceipts, steerCorrelationIDs...)
		return agentLoopRunTurnConductorRunIterationsContinue
	}
	// ADR-087 D4/D6/D9: the one success-arm truncation handler,
	// ahead of the legacy empty-response retry loop below. Guarded
	// internally on isTruncatedFinishReason(FinishReason) &&
	// len(ToolCalls)==0 — when that guard does not match, the
	// verdict is truncationActionNone and every line below runs
	// completely unchanged (§7.10: a normal empty response with a
	// non-truncated finish reason still falls through to the
	// legacy loop). This single insertion covers both the main and
	// media-downgrade-retry call sites, since both `break` into
	// this shared downstream code on success; the empty-response
	// retry's own successful attempt (site 3) reaches the
	// identical branch again below, inside that loop.
	if verdict := cn.rc.rx.rr.rq.ri.rf.rt.al.evaluateTruncatedSuccess(cn.rc.rx.rr.rq.ri.rf.rt.ts, cn.rc.rx.rr.rq.ri.rf.response, cn.rc.rx.rr.rq.ri.messages, cn.rc.rx.rr.rq.ri.rf.providerToolDefs, cn.rc.rx.rr.rq.ri.gracefulTerminal, cn.rc.rx.rr.rq.ri.rf.rt.iteration, cn.rc.rx.rr.rq.ri.rf.rt.llmModel, &cn.rc.rx.rr.continuationChain); verdict.action != truncationActionNone {
		switch verdict.action {
		case truncationActionContinue:
			cn.rc.rx.rr.rq.ri.messages = verdict.messages
			return agentLoopRunTurnConductorRunIterationsContinueL1
		case truncationActionEnd:
			cn.rc.rx.finalContent = verdict.finalContent
			return agentLoopRunTurnConductorRunIterationsBreakL1
		}
	}
	return agentLoopRunTurnConductorRunIterationsNext
}

// surfaceEmptyRetryOutcome surfaces the terminal outcome of empty-response recovery.
func (cn *agentLoopRunTurnConductorRunIterations) surfaceEmptyRetryOutcome() agentLoopRunTurnConductorRunIterationsFlow {
	if cn.rc.rx.rr.rq.ri.rf.err != nil {
		// ADR-066 D7: typed, never silent — see typedTurnExit.
		if errors.Is(cn.rc.rx.rr.rq.ri.rf.err, context.Canceled) || errors.Is(cn.rc.rx.rr.rq.ri.rf.err, context.DeadlineExceeded) {
			var res turnResult
			var exitErr error
			res, cn.rc.rx.rr.rq.ri.turnStatus, exitErr = cn.rc.rx.rr.rq.ri.rf.rt.al.typedTurnExit(cn.rc.rx.rr.rq.ri.rf.rt.ts, cn.rc.rx.rr.rq.ri.rf.rt.iteration, cn.rc.rx.rr.rq.ri.rf.rt.llmModel, cn.rc.rx.rr.rq.ri.rf.err)
			cn.rc.ret0 = res
			cn.rc.ret1 = exitErr
			cn.ret0 = agentLoopRunTurnConductorReturn
			return agentLoopRunTurnConductorRunIterationsReturn
		}
		cn.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
		// Wave 1 (error-provenance hardening): translate via the
		// shared classifier (CRIT-001). Never surface raw err.Error()
		// to the assistant / bus / transcript. ADR-087 D5/D9: this
		// empty-response retry's own error path is subsumed into the
		// same TranslateTurnError classification the main terminal
		// path uses, so a truncated tool call refused here reports
		// CodeToolCallTruncated identically to every other site.
		pe := errorToProviderError(cn.rc.rx.rr.rq.ri.rf.err)
		llm := TranslateTurnError(cn.rc.rx.rr.rq.ri.rf.err)

		// FR-017a: label an inconclusive residual 4xx after a
		// successful strip-retry. A later distinct classified
		// failure keeps its own code so live and persist agree.
		if outcomeRelabelApplies(llm.Code, cn.rc.rx.rr.rq.ri.rf.rt.ts.outcomeRelabel) {
			llm.Code = cn.rc.rx.rr.rq.ri.rf.rt.ts.outcomeRelabel
			llm.Message = UserMessageForCode(cn.rc.rx.rr.rq.ri.rf.rt.ts.outcomeRelabel)
		}

		cn.rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindError,
			cn.rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
			ErrorPayload{Stage: "llm_empty_retry", Code: string(llm.Code), Message: llm.Message, ProviderError: pe, ChatID: cn.rc.rx.rr.rq.ri.rf.rt.ts.opts.ChatID, SessionID: string(cn.rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID)},
		)
		// FR-002: persist this provider error to the transcript (write
		// choke point).
		cn.rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
		// ADR-087 D6.8: no further provider call follows this error
		// either — runTurn's deferred preserveTruncatedAccumulator
		// keeps a D6 continuation left unresolved by a prior round.
		cn.rc.ret0 = turnResult{}
		cn.rc.ret1 = fmt.Errorf("LLM call failed during empty-response retry: %w", cn.rc.rx.rr.rq.ri.rf.err)
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	}
	return agentLoopRunTurnConductorRunIterationsNext
}

// finishTextResponse finishes a text response or releases a repaired tool call for dispatch.
func (cn *agentLoopRunTurnConductorRunIterations) finishTextResponse() agentLoopRunTurnConductorRunIterationsFlow {
	if len(cn.rc.rx.rr.rq.ri.rf.response.ToolCalls) == 0 || cn.rc.rx.rr.rq.ri.gracefulTerminal {
		if strings.TrimSpace(cn.responseContent) == "" {
			cn.responseContent = defaultResponse
			cn.rc.rx.rr.rq.ri.rf.rt.ts.markTurnFailed()
			logger.WarnCF("agent", "LLM returned empty response after retry; using fallback message",
				map[string]any{"agent_id": cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": cn.rc.rx.rr.rq.ri.rf.rt.iteration})
		}
		// ADR-087 D6.10: this round did not go through
		// evaluateTruncatedSuccess (it was not itself truncated, or it
		// carried tool calls on an earlier pass through this loop) —
		// but if an EARLIER round in this same turn dispatched a D6
		// continuation, the accumulator holds that earlier content and
		// must be prefixed here, or the prior round's answer is
		// silently dropped and only this round's own text survives.
		// (What the accumulator holds at this point is only what has
		// NOT already been settled into the record by
		// flushContinuationAccumulator — see its doc comment.)
		if cn.rc.rx.rr.rq.ri.rf.rt.ts.hadContinuation() {
			cn.responseContent = cn.rc.rx.rr.rq.ri.rf.rt.ts.appendToAccumulator(cn.responseContent)
			cn.rc.rx.rr.rq.ri.rf.rt.ts.resolveContinuation()
		}
		cn.rc.rx.finalContent = cn.responseContent
		logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
			map[string]any{
				"agent_id":      cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"iteration":     cn.rc.rx.rr.rq.ri.rf.rt.iteration,
				"content_chars": len(cn.rc.rx.finalContent),
			})
		return agentLoopRunTurnConductorRunIterationsBreakL1
	}
	logger.InfoCF("agent", "empty-response retry returned a repaired tool call; dispatching it",
		map[string]any{
			"agent_id":   cn.rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
			"iteration":  cn.rc.rx.rr.rq.ri.rf.rt.iteration,
			"tool_calls": len(cn.rc.rx.rr.rq.ri.rf.response.ToolCalls),
		})
	return agentLoopRunTurnConductorRunIterationsNext
}

// dispatchToolCalls records and dispatches the iteration's tool calls.
func (cn *agentLoopRunTurnConductorRunIterations) dispatchToolCalls() agentLoopRunTurnConductorRunIterationsFlow {
	cn.rc.rx.rr.recordToolCalls()

	// setGoalSucceededThisRound tracks whether a set_goal call in THIS
	// model response already registered (or updated) the goal record — the
	// gate a few branches down uses to refuse a trailing AskUserQuestion
	// from the same response (UAT B-9 run 4: set_goal plus an invented
	// "Placeholder question - not used" ask in one response both ran; the
	// ask parked a turn whose goal record was already registered, freezing
	// the session for 18 minutes). A successful set_goal only ever happens
	// on a goal turn, so no separate goal-turn predicate is needed.

	switch cn.rc.rx.executeToolCalls() {
	case agentLoopRunTurnToolsReturn:
		cn.rc.ret0 = cn.rc.rx.ret0
		cn.rc.ret1 = cn.rc.rx.ret1
		cn.ret0 = agentLoopRunTurnConductorReturn
		return agentLoopRunTurnConductorRunIterationsReturn
	}

	switch cn.rc.rx.finishToolIteration() {
	case agentLoopRunTurnToolsBreakL1:
		return agentLoopRunTurnConductorRunIterationsBreakL1
	}
	return agentLoopRunTurnConductorRunIterationsNext
}
