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

// runIterations runs provider and tool iterations and incorporates late steering before finalization.
func (rc *agentLoopRunTurnConductor) runIterations() agentLoopRunTurnConductorFlow {
	emptyResponseRetries := 0
	const maxEmptyResponseRetries = 1
	// orphanToolMarkupRepairs counts how many times this turn has re-prompted
	// a model that emitted its tool call as unparseable text (see the strip
	// choke point below). Bounded so a model that cannot comply ends the turn
	// with a visible error instead of looping on the user's budget.
	orphanToolMarkupRepairs := 0
	// continuationChain (ADR-087 D6.7) holds the exact {assistant, user} pair
	// a previous round appended to `messages` as the D6 continuation chain,
	// so the next round REPLACES it — by identity, via stripContinuationChain
	// — instead of accumulating duplicate copies of the answer-so-far. nil
	// means no chain is currently live in `messages`. Declared before
	// turnLoop so it survives both `continue turnLoop` and `goto turnLoop`.

turnLoop:
	for rc.rx.rr.rq.ri.rf.rt.ts.currentIteration() < rc.rx.rr.rq.ri.rf.rt.ts.agent.MaxIterations || len(rc.rx.rr.rq.ri.pendingMessages) > 0 || func() bool {
		graceful, _ := rc.rx.rr.rq.ri.rf.rt.ts.gracefulInterruptRequested()
		return graceful
	}() {

		switch rc.rx.rr.rq.ri.beginIteration() {
		case agentLoopRunTurnIterationReturn:
			rc.ret0 = rc.rx.rr.rq.ri.ret0
			rc.ret1 = rc.rx.rr.rq.ri.ret1
			return agentLoopRunTurnConductorReturn
		case agentLoopRunTurnIterationBreak:
			break turnLoop
		case agentLoopRunTurnIterationBreakL1:
			break turnLoop
		}

		// FR-003, FR-041: Apply per-agent tool policy at LLM-call assembly time.
		// FilterToolsByPolicy enforces global × agent deny>ask>allow resolution and
		// the ScopeCore-on-custom-agent gate before the tool list reaches the LLM.
		// Tools with effective policy "ask" are included — the mid-turn policy snapshot
		// (FR-041) handles human-in-the-loop confirmation; see the ADR-058
		// quarantine gate and recordToolDenial (tool_denial.go).

		switch rc.rx.rr.rq.prepareToolSurface() {
		case agentLoopRunTurnRequestReturn:
			rc.ret0 = rc.rx.rr.rq.ret0
			rc.ret1 = rc.rx.rr.rq.ret1
			return agentLoopRunTurnConductorReturn
		}

		// Transparent repair of orphan tool_use / tool_result pairs in the
		// outbound history. OpenRouter's mid-stream provider rotation can leave
		// the context jsonl desynced with its own transcript; we reconcile from
		// the transcript before every LLM call so Anthropic never sees a broken
		// pair. No-op fast path when there are no orphans (the common case).

		rc.rx.rr.rq.prepareCallMessages()

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

		switch rc.rx.rr.rq.prepareLLMRequest() {
		case agentLoopRunTurnRequestReturn:
			rc.ret0 = rc.rx.rr.rq.ret0
			rc.ret1 = rc.rx.rr.rq.ret1
			return agentLoopRunTurnConductorReturn
		}

		switch rc.rx.rr.callLLMWithRetries() {
		case agentLoopRunTurnResponseReturn:
			rc.ret0 = rc.rx.rr.ret0
			rc.ret1 = rc.rx.rr.ret1
			return agentLoopRunTurnConductorReturn
		}

		switch rc.rx.rr.handleProviderResponse() {
		case agentLoopRunTurnResponseReturn:
			rc.ret0 = rc.rx.rr.ret0
			rc.ret1 = rc.rx.rr.ret1
			return agentLoopRunTurnConductorReturn
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
		if rc.rx.rr.rq.ri.rf.response != nil {
			rc.rx.rr.rq.ri.rf.rt.al.debitLLMUsage(rc.rx.rr.rq.ri.rf.rt.ts, rc.rx.rr.rq.ri.rf.rt.llmModel, rc.rx.rr.rq.ri.rf.response.Usage)
		}

		if len(rc.rx.rr.rq.ri.rf.response.ToolCalls) == 0 || rc.rx.rr.rq.ri.gracefulTerminal {
			responseContent := rc.rx.rr.rq.ri.rf.response.Content
			if responseContent == "" && rc.rx.rr.rq.ri.rf.response.ReasoningContent != "" {
				responseContent = rc.rx.rr.rq.ri.rf.response.ReasoningContent
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
			if rc.rx.rr.hasOrphanMarkup && len(rc.rx.rr.rq.ri.rf.response.ToolCalls) == 0 {
				switch {
				case rc.rx.rr.rq.ri.gracefulTerminal:
					// Fall through: honour the interrupt, do not re-prompt.
				case orphanToolMarkupRepairs < maxOrphanToolMarkupRepairs:
					orphanToolMarkupRepairs++
					logger.WarnCF("agent", "Tool call arrived as unparseable text; re-prompting the model",
						map[string]any{
							"agent_id":      rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
							"iteration":     rc.rx.rr.rq.ri.rf.rt.iteration,
							"model":         rc.rx.rr.rq.ri.rf.rt.llmModel,
							"marker":        rc.rx.rr.orphanMarkup.Marker,
							"finish_reason": rc.rx.rr.rq.ri.rf.response.FinishReason,
							"attempt":       orphanToolMarkupRepairs,
							"max_attempts":  maxOrphanToolMarkupRepairs,
						})
					rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
						EventKindLLMRetry,
						rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
						LLMRetryPayload{
							Attempt:    orphanToolMarkupRepairs,
							MaxRetries: maxOrphanToolMarkupRepairs,
							Reason:     orphanToolMarkupRetryReason,
						},
					)
					rc.rx.rr.rq.ri.messages = append(rc.rx.rr.rq.ri.messages, orphanToolMarkupRepairMessage(rc.rx.rr.rq.ri.rf.response.FinishReason))
					continue
				default:
					// Repair budget spent. Fail LOUDLY — a typed error event
					// for the live client and a typed transcript entry for
					// replay. CodeToolArgs is the contract's existing
					// "tool-call argument format error"; the vocabulary is
					// contract data (contracts/components/schemas/LLMError.yaml),
					// so this path reuses it rather than inventing a code the
					// SPA has no catalogue entry for.
					rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
					llm := LLMError{
						Code:      CodeToolArgs,
						Message:   UserMessageForCode(CodeToolArgs),
						Retryable: isRetryable(CodeToolArgs),
					}
					logger.WarnCF("agent", "Tool call kept arriving as unparseable text; ending turn with an error",
						map[string]any{
							"agent_id":      rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
							"iteration":     rc.rx.rr.rq.ri.rf.rt.iteration,
							"model":         rc.rx.rr.rq.ri.rf.rt.llmModel,
							"marker":        rc.rx.rr.orphanMarkup.Marker,
							"finish_reason": rc.rx.rr.rq.ri.rf.response.FinishReason,
							"attempts":      orphanToolMarkupRepairs,
						})
					rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
						EventKindError,
						rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
						ErrorPayload{
							Stage:     orphanToolMarkupStage,
							Code:      string(llm.Code),
							Message:   llm.Message,
							ChatID:    rc.rx.rr.rq.ri.rf.rt.ts.opts.ChatID,
							SessionID: string(rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
						},
					)
					rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
					// UAT A-12: wrap the TYPED refusal a provider raises for an
					// undecodable tool call. A task attempt's turn error is
					// classified by type only (task_attempt_turn_error.go's
					// attemptRecoverableTurnErrorCode — errors.As for
					// *common.ToolArgumentsError, then TranslateTurnError ->
					// CodeToolArgs, the same code this exit already reports to
					// the client). Untyped, this exhaustion failed the task on
					// the spot instead of consuming one attempt.
					rc.ret0 = turnResult{}
					rc.ret1 = fmt.Errorf(
						"model emitted unparseable tool-call markup (marker %q, finish_reason %q) after %d repair attempts: %w",
						rc.rx.rr.orphanMarkup.Marker, rc.rx.rr.rq.ri.rf.response.FinishReason, orphanToolMarkupRepairs,
						common.NewToolArgumentsError("", common.ErrToolArgumentsUndecodable, false))
					return agentLoopRunTurnConductorReturn
				}
			}

			// FR-7.5/NFR-1: scan the assistant's final answer for references to
			// memories recalled earlier this turn and emit op:cited events.
			if rc.rx.rr.citationTracker != nil {
				rc.rx.rr.citationTracker.EmitCitations(responseContent)
			}
			if steerMsgs := rc.rx.rr.rq.ri.rf.rt.al.dequeueSteeringMessagesForScope(rc.rx.rr.rq.ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
				logger.InfoCF("agent", "Steering arrived after direct LLM response; continuing turn",
					map[string]any{
						"agent_id":       rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
						"iteration":      rc.rx.rr.rq.ri.rf.rt.iteration,
						"steering_count": len(steerMsgs),
					})
				rc.rx.rr.rq.ri.pendingMessages = append(rc.rx.rr.rq.ri.pendingMessages, steerMsgs...)
				continue
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
			if verdict := rc.rx.rr.rq.ri.rf.rt.al.evaluateTruncatedSuccess(rc.rx.rr.rq.ri.rf.rt.ts, rc.rx.rr.rq.ri.rf.response, rc.rx.rr.rq.ri.messages, rc.rx.rr.rq.ri.rf.providerToolDefs, rc.rx.rr.rq.ri.gracefulTerminal, rc.rx.rr.rq.ri.rf.rt.iteration, rc.rx.rr.rq.ri.rf.rt.llmModel, &rc.rx.rr.continuationChain); verdict.action != truncationActionNone {
				switch verdict.action {
				case truncationActionContinue:
					rc.rx.rr.rq.ri.messages = verdict.messages
					continue turnLoop
				case truncationActionEnd:
					rc.rx.finalContent = verdict.finalContent
					break turnLoop
				}
			}
			// Empty response recovery (FR-006): if LLM returned empty content with no
			// reasoning and no tool calls, retry once before surfacing a fallback message.
			//
			// H3: perform the retry in an inner loop that calls callLLM directly, so we
			// do NOT increment the outer iteration counter (which would consume the agent's
			// MaxIterations budget for what is purely a provider-level retry).
			for strings.TrimSpace(responseContent) == "" && emptyResponseRetries < maxEmptyResponseRetries {
				emptyResponseRetries++
				logger.WarnCF("agent", "Empty response from LLM, retrying", map[string]any{
					"agent_id":  rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"iteration": rc.rx.rr.rq.ri.rf.rt.iteration,
					"attempt":   emptyResponseRetries,
				})
				rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindLLMRetry,
					rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
					LLMRetryPayload{
						Attempt:    emptyResponseRetries,
						MaxRetries: maxEmptyResponseRetries,
						Reason:     "empty_response",
					},
				)
				// I1: also emit the dedicated EventKindEmptyResponseRetry for subscribers
				// that specifically track empty-response retry behavior.
				rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindEmptyResponseRetry,
					rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.empty_response_retry"),
					EmptyResponseRetryPayload{
						Attempt:    emptyResponseRetries,
						MaxRetries: maxEmptyResponseRetries,
					},
				)
				// Re-call the LLM directly without advancing the outer turn iteration.
				retryResp, retryErr := rc.rx.rr.rq.ri.rf.callLLM(rc.rx.rr.rq.ri.rf.callMessages, rc.rx.rr.rq.ri.rf.providerToolDefs)
				if retryErr != nil {
					// ADR-087 D3/D9 (empty-response retry call site): same
					// bounded repair as the other two call sites — issue the
					// repaired call directly rather than looping, since this
					// mini-loop's own iteration budget is about EMPTY
					// content, a different concern from a truncated tool
					// call.
					if repaired, ok := rc.rx.rr.rq.ri.rf.rt.al.evaluateTruncatedToolCallError(rc.rx.rr.rq.ri.rf.rt.ts, retryErr, rc.rx.rr.rq.ri.rf.callMessages, 0, 1, &rc.rx.rr.rq.ri.toolCallTruncationRepairUsed, rc.rx.rr.rq.ri.rf.rt.llmModel, rc.rx.rr.rq.ri.rf.rt.iteration); ok {
						rc.rx.rr.rq.ri.rf.callMessages = repaired
						retryResp, retryErr = rc.rx.rr.rq.ri.rf.callLLM(rc.rx.rr.rq.ri.rf.callMessages, rc.rx.rr.rq.ri.rf.providerToolDefs)
					}
					if retryErr != nil {
						// Propagate the error back to the outer error-handling block by
						// overwriting response/err and breaking out of both loops.
						rc.rx.rr.rq.ri.rf.response = nil
						rc.rx.rr.rq.ri.rf.err = retryErr
						break
					}
				}
				rc.rx.rr.rq.ri.rf.response = retryResp
				responseContent = rc.rx.rr.rq.ri.rf.response.Content
				if responseContent == "" && rc.rx.rr.rq.ri.rf.response.ReasoningContent != "" {
					responseContent = rc.rx.rr.rq.ri.rf.response.ReasoningContent
				}
				// ADR-087 D4/D6/D9: the empty-response retry's own
				// successful attempt goes through the SAME success-arm
				// handler as the other two call sites (§7.9).
				if verdict := rc.rx.rr.rq.ri.rf.rt.al.evaluateTruncatedSuccess(rc.rx.rr.rq.ri.rf.rt.ts, rc.rx.rr.rq.ri.rf.response, rc.rx.rr.rq.ri.messages, rc.rx.rr.rq.ri.rf.providerToolDefs, rc.rx.rr.rq.ri.gracefulTerminal, rc.rx.rr.rq.ri.rf.rt.iteration, rc.rx.rr.rq.ri.rf.rt.llmModel, &rc.rx.rr.continuationChain); verdict.action != truncationActionNone {
					switch verdict.action {
					case truncationActionContinue:
						rc.rx.rr.rq.ri.messages = verdict.messages
						continue turnLoop
					case truncationActionEnd:
						rc.rx.finalContent = verdict.finalContent
						break turnLoop
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
				if len(rc.rx.rr.rq.ri.rf.response.ToolCalls) > 0 && !rc.rx.rr.rq.ri.gracefulTerminal {
					break
				}
			}
			// If the inner retry loop set an error, surface it via the outer error path.
			if rc.rx.rr.rq.ri.rf.err != nil {
				// ADR-066 D7: typed, never silent — see typedTurnExit.
				if errors.Is(rc.rx.rr.rq.ri.rf.err, context.Canceled) || errors.Is(rc.rx.rr.rq.ri.rf.err, context.DeadlineExceeded) {
					var res turnResult
					var exitErr error
					res, rc.rx.rr.rq.ri.turnStatus, exitErr = rc.rx.rr.rq.ri.rf.rt.al.typedTurnExit(rc.rx.rr.rq.ri.rf.rt.ts, rc.rx.rr.rq.ri.rf.rt.iteration, rc.rx.rr.rq.ri.rf.rt.llmModel, rc.rx.rr.rq.ri.rf.err)
					rc.ret0 = res
					rc.ret1 = exitErr
					return agentLoopRunTurnConductorReturn
				}
				rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
				// Wave 1 (error-provenance hardening): translate via the
				// shared classifier (CRIT-001). Never surface raw err.Error()
				// to the assistant / bus / transcript. ADR-087 D5/D9: this
				// empty-response retry's own error path is subsumed into the
				// same TranslateTurnError classification the main terminal
				// path uses, so a truncated tool call refused here reports
				// CodeToolCallTruncated identically to every other site.
				pe := errorToProviderError(rc.rx.rr.rq.ri.rf.err)
				llm := TranslateTurnError(rc.rx.rr.rq.ri.rf.err)

				// FR-017a: label an inconclusive residual 4xx after a
				// successful strip-retry. A later distinct classified
				// failure keeps its own code so live and persist agree.
				if outcomeRelabelApplies(llm.Code, rc.rx.rr.rq.ri.rf.rt.ts.outcomeRelabel) {
					llm.Code = rc.rx.rr.rq.ri.rf.rt.ts.outcomeRelabel
					llm.Message = UserMessageForCode(rc.rx.rr.rq.ri.rf.rt.ts.outcomeRelabel)
				}

				rc.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindError,
					rc.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{Stage: "llm_empty_retry", Code: string(llm.Code), Message: llm.Message, ProviderError: pe, ChatID: rc.rx.rr.rq.ri.rf.rt.ts.opts.ChatID, SessionID: string(rc.rx.rr.rq.ri.rf.rt.ts.routingSessionID)},
				)
				// FR-002: persist this provider error to the transcript (write
				// choke point).
				rc.rx.rr.rq.ri.rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
				// ADR-087 D6.8: no further provider call follows this error
				// either — runTurn's deferred preserveTruncatedAccumulator
				// keeps a D6 continuation left unresolved by a prior round.
				rc.ret0 = turnResult{}
				rc.ret1 = fmt.Errorf("LLM call failed during empty-response retry: %w", rc.rx.rr.rq.ri.rf.err)
				return agentLoopRunTurnConductorReturn
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
			if len(rc.rx.rr.rq.ri.rf.response.ToolCalls) == 0 || rc.rx.rr.rq.ri.gracefulTerminal {
				if strings.TrimSpace(responseContent) == "" {
					responseContent = defaultResponse
					rc.rx.rr.rq.ri.rf.rt.ts.markTurnFailed()
					logger.WarnCF("agent", "LLM returned empty response after retry; using fallback message",
						map[string]any{"agent_id": rc.rx.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": rc.rx.rr.rq.ri.rf.rt.iteration})
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
				if rc.rx.rr.rq.ri.rf.rt.ts.hadContinuation() {
					responseContent = rc.rx.rr.rq.ri.rf.rt.ts.appendToAccumulator(responseContent)
					rc.rx.rr.rq.ri.rf.rt.ts.resolveContinuation()
				}
				rc.rx.finalContent = responseContent
				logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
					map[string]any{
						"agent_id":      rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
						"iteration":     rc.rx.rr.rq.ri.rf.rt.iteration,
						"content_chars": len(rc.rx.finalContent),
					})
				break turnLoop
			}
			logger.InfoCF("agent", "empty-response retry returned a repaired tool call; dispatching it",
				map[string]any{
					"agent_id":   rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"iteration":  rc.rx.rr.rq.ri.rf.rt.iteration,
					"tool_calls": len(rc.rx.rr.rq.ri.rf.response.ToolCalls),
				})
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

		rc.rx.rr.recordToolCalls()

		// setGoalSucceededThisRound tracks whether a set_goal call in THIS
		// model response already registered (or updated) the goal record — the
		// gate a few branches down uses to refuse a trailing AskUserQuestion
		// from the same response (UAT B-9 run 4: set_goal plus an invented
		// "Placeholder question - not used" ask in one response both ran; the
		// ask parked a turn whose goal record was already registered, freezing
		// the session for 18 minutes). A successful set_goal only ever happens
		// on a goal turn, so no separate goal-turn predicate is needed.

		switch rc.rx.executeToolCalls() {
		case agentLoopRunTurnToolsReturn:
			rc.ret0 = rc.rx.ret0
			rc.ret1 = rc.rx.ret1
			return agentLoopRunTurnConductorReturn
		}

		switch rc.rx.finishToolIteration() {
		case agentLoopRunTurnToolsBreakL1:
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
	rc.rx.rr.rq.ri.rf.rt.al.tickSearchPromotionHorizon(rc.rx.rr.rq.ri.rf.rt.ts.manifestBucket())

	if steerMsgs := rc.rx.rr.rq.ri.rf.rt.al.dequeueSteeringMessagesForScope(rc.rx.rr.rq.ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after turn completion; continuing turn before finalizing",
			map[string]any{
				"agent_id":       rc.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"steering_count": len(steerMsgs),
				"session_key":    rc.rx.rr.rq.ri.rf.rt.ts.sessionKey,
			})
		rc.rx.rr.rq.ri.pendingMessages = append(rc.rx.rr.rq.ri.pendingMessages, steerMsgs...)
		rc.rx.finalContent = ""
		// I2: guard against bypassing the hard iteration ceiling via goto.
		// If the ceiling is exceeded, fall through to finalization rather than
		// re-entering turnLoop, which would be invalid at this point anyway.
		if rc.rx.rr.rq.ri.rf.rt.ts.currentIteration() < 2*rc.rx.rr.rq.ri.rf.rt.ts.agent.MaxIterations {
			goto turnLoop
		}
	}
	return agentLoopRunTurnConductorNext
}
