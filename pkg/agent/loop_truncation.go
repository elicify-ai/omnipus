// loop_truncation.go: Detect a truncated model reply and decide whether to continue it

package agent

import (
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// stripOrphanToolCallMarkup removes residual native tool-call markup from
// every user-visible text field of an LLM response, returning the first
// residue found so the caller can log it and decide what the round means.
//
// It is a strip, not a parse: Omnipus accepts tool calls from the structured
// `tool_calls` field and nowhere else, so the residue is discarded rather
// than interpreted. Reconstructing a call from it would mean trusting a
// half-delivered payload — exactly the payload whose other half is missing.
func stripOrphanToolCallMarkup(response *providers.LLMResponse) (providers.OrphanToolMarkup, bool) {
	if response == nil {
		return providers.OrphanToolMarkup{}, false
	}
	var first providers.OrphanToolMarkup
	found := false
	if om, ok := providers.DetectOrphanToolCallMarkup(response.Content); ok {
		response.Content = om.Prose
		first, found = om, true
	}
	if om, ok := providers.DetectOrphanToolCallMarkup(response.ReasoningContent); ok {
		response.ReasoningContent = om.Prose
		if !found {
			first, found = om, true
		}
	}
	return first, found
}

// truncationOutputCapSentence is the advice given to a model whose
// generation was cut off at the output-token limit before it finished:
// make the next attempt smaller. Factored out of orphanToolMarkupRepairMessage
// (ADR-087 D3.6) so both repair paths — the orphan-markup re-prompt and
// toolCallTruncationRepairMessage below — send the model byte-identical
// wording for the same underlying fault.
const truncationOutputCapSentence = " Your previous response was also cut off at the output-token limit before the call was complete. " +
	"Make this call smaller: send shorter arguments, or split the work across several calls."

// orphanToolMarkupRepairMessage builds the corrective turn sent back to a
// model whose tool call arrived as text.
//
// It deliberately does NOT quote the residue back. The residue is the model's
// own malformed output; replaying it is a strong prompt to produce the same
// thing again. The note states the failure, the consequence, and the one
// action that fixes it.
//
// finishReason drives a second sentence when the generation was cut off at
// the output-token cap — the observed trigger for this fault, where the call
// was simply too large to finish. Telling the model to make it smaller is the
// only advice that actually clears that case.
func orphanToolMarkupRepairMessage(finishReason string) providers.Message {
	var b strings.Builder
	b.WriteString("Your previous message did not arrive as a tool call. " +
		"It arrived as plain text containing raw tool-call markup, so no tool ran and nothing was written or changed. " +
		"Re-issue that call now using the tool-calling interface — a structured tool call, not text. " +
		"Do not write tool-call markup, XML tags, or a JSON description of the call into your message body.")
	if isTruncatedFinishReason(finishReason) {
		b.WriteString(truncationOutputCapSentence)
	}
	return providers.Message{Role: "user", Content: b.String()}
}

// isTruncatedFinishReason reports whether a finish_reason means the model ran
// out of output tokens mid-generation. providers/common.normalizeFinishReason
// rewrites OpenAI's "length" to "truncated"; both spellings are accepted here
// because not every provider adapter routes through that normaliser.
func isTruncatedFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "truncated", "length", "max_tokens":
		return true
	}
	return false
}

// toolCallTruncationRepairMessage builds the corrective turn sent back to a
// model whose tool call was cut off at the output-token limit before it
// could be decoded (ADR-087 D3). Reuses truncationOutputCapSentence
// (D3.6) — the same advice orphanToolMarkupRepairMessage gives for the
// sibling fault.
func toolCallTruncationRepairMessage() providers.Message {
	return providers.Message{
		Role: "user",
		Content: "Your tool call was cut off at the output-token limit before it finished, so it could not run and nothing was changed." +
			truncationOutputCapSentence,
	}
}

// truncationContinueMessage is the D6.9 instruction appended after the
// partial answer when a truncated response earns an auto-continue round.
// Never persisted as a user-role session-history message (D6.7) — it lives
// only in the turn-local `messages` slice used to build the next request.
func truncationContinueMessage() providers.Message {
	return providers.Message{
		Role: "user",
		Content: "Your previous message was cut off at the output-token limit. Continue from exactly where it stopped. " +
			"Do not repeat any text you already wrote, and do not restate or summarise it.",
	}
}

// ── ADR-087 D3/D4/D5/D6/D9: the truncation outcome handler ──
//
// runTurn calls the provider from three sites (§2.7: the main retry loop,
// the media-downgrade retry, and the empty-response retry's own attempt).
// D9 requires D3 (error arm) and D4/D6 (success arm) to be evaluated
// identically at all three — implemented here as two shared functions
// (evaluateTruncatedToolCallError, evaluateTruncatedSuccess) that runTurn
// calls at each site; Go's labeled continue/break for turnLoop can only be
// written in runTurn itself, so the functions return a verdict rather than
// controlling the loop directly, and runTurn's three call sites act on it
// identically (§7.9 pins this).
const (
	// truncationReasonMaxOutputTokens is the only Message.truncation_reason
	// value this package writes (ADR-087 D2) — "cancelled" is cancel.go's
	// own, unrelated writer.
	truncationReasonMaxOutputTokens = "max_output_tokens"
	// maxTruncationContinuations bounds ADR-087 D6's auto-continue rounds
	// per turn (D6.3).
	maxTruncationContinuations = 2
	// truncatedToolCallRetryReason labels a D3 tool-call-truncation repair
	// on the event bus, distinct from orphanToolMarkupRetryReason and the
	// plain "empty_response"/"timeout" reasons already in use.
	truncatedToolCallRetryReason = "tool_call_truncated"
	// truncationContinueRetryReason labels a D6 auto-continue round on the
	// event bus.
	truncationContinueRetryReason = "truncation_continue"
)

// evaluateTruncatedToolCallError implements ADR-087 D3: a tool call cut off
// at the output-token limit gets exactly one bounded repair per turnLoop
// round before the turn falls through to the terminal error path.
//
// Returns (repairedMessages, true) when a repair was dispatched — the
// caller must retry the provider call with repairedMessages. Returns
// (nil, false) when the caller's EXISTING error handling (ClassifyError,
// media downgrade, PDF fallback, the generic terminal path) must run
// completely unchanged: the error was not a truncated tool call, nothing
// was streamed's worth of content was already shown, the round already
// spent its one repair, no provider retry remains, or a hard abort is
// already in flight.
func (al *AgentLoop) evaluateTruncatedToolCallError(
	ts *turnState,
	err error,
	callMessages []providers.Message,
	retry, maxRetries int,
	roundRepairUsed *bool,
	llmModel string,
	iteration int,
) ([]providers.Message, bool) {
	if err == nil || !errors.Is(err, common.ErrToolArgumentsUndecodable) {
		return nil, false
	}
	// D3.2: only repair when nothing was streamed yet this attempt — a
	// partially-streamed response has already shown the user real text;
	// repairing here would duplicate it. Same idiom as the timeout-retry
	// guard at :10487.
	if sc, ok := ts.lastStreamer.(interface{ StreamedContentLen() int }); ok && sc.StreamedContentLen() > 0 {
		return nil, false
	}
	// D3.3: a `continue` on the last retry exits the loop without making
	// the promised call — do not count or announce a repair that cannot
	// run.
	if retry >= maxRetries {
		return nil, false
	}
	// D3.4: one repair per turnLoop round; the caller resets
	// roundRepairUsed at the top of every round.
	if roundRepairUsed == nil || *roundRepairUsed {
		return nil, false
	}
	// D3.8: re-check hard-abort explicitly before dispatching the repair
	// call — `continue` skips sleepWithContext, which is where this check
	// normally lands.
	if ts.hardAbortRequested() {
		return nil, false
	}

	*roundRepairUsed = true

	// D3.9: the refused attempt was billed by the provider even though the
	// call was refused locally — debit its usage exactly once, through the
	// SAME function the success arm uses (debitLLMUsage). An earlier cut of
	// this block re-typed the four accounting calls by hand and silently
	// omitted SetLastUsage, so a turn whose last provider call was a refused
	// truncated tool call reported the PREVIOUS call's usage as its last.
	var tae *common.ToolArgumentsError
	if errors.As(err, &tae) {
		al.debitLLMUsage(ts, llmModel, tae.Usage)
	}

	logger.WarnCF("agent", "tool call cut off at the output-token limit — re-prompting the model once",
		map[string]any{
			"agent_id":  ts.agent.ID,
			"iteration": iteration,
			"model":     llmModel,
			"error":     err.Error(),
		})
	al.emitEvent(
		EventKindLLMRetry,
		ts.eventMeta("runTurn", "turn.llm.retry"),
		LLMRetryPayload{
			Attempt:    1,
			MaxRetries: 1,
			Reason:     truncatedToolCallRetryReason,
			Error:      err.Error(),
		},
	)

	// D3.5/D3.6: append to a FRESH copy of callMessages — never mutate the
	// turn's own `messages`, never bare-append (aliasing risk, same idiom
	// as :9867). D3.7: the refused response itself is never appended to
	// history — only this repair note is.
	repaired := append(append([]providers.Message(nil), callMessages...), toolCallTruncationRepairMessage())
	return repaired, true
}

// truncationSuccessVerdict is evaluateTruncatedSuccess's return value.
type truncationSuccessVerdict struct {
	action       truncationSuccessAction
	finalContent string
	messages     []providers.Message
}

// evaluateTruncatedSuccess implements ADR-087 D4 (one branch, three
// outcomes) and D6 (auto-continue). Called once, ahead of the legacy
// empty-response retry loop, for every one of runTurn's three provider-call
// sites (D9) — main, media-downgrade retry, and the empty-response retry's
// own successful attempt (via its own call site inside that loop).
//
// continuationChain is the caller's turn-scoped bookkeeping (declared once,
// ahead of turnLoop) holding the exact {assistant, user} pair a previous
// round appended to `messages` — mutated here so a later round REPLACES the
// previous round's chain instead of accumulating duplicate copies of the
// answer-so-far (D6.7's "re-appended exactly once").
//
// The response's own CONTENT is read from response.Content deliberately, NOT
// from the caller's `responseContent` local: that local carries the
// ReasoningContent substitution the empty-response path applies, so passing
// it fed a reasoning-only truncated response's chain-of-thought into the
// accumulator, echoed it back to the model under "Continue from exactly
// where it stopped", and persisted it as the answer. A reasoning-only
// truncated response produced no answer text and is therefore D4a.
func (al *AgentLoop) evaluateTruncatedSuccess(
	ts *turnState,
	response *providers.LLMResponse,
	messages []providers.Message,
	providerToolDefs []providers.ToolDefinition,
	gracefulTerminal bool,
	iteration int,
	llmModel string,
	continuationChain *[]providers.Message,
) truncationSuccessVerdict {
	if response == nil || !isTruncatedFinishReason(response.FinishReason) || len(response.ToolCalls) > 0 {
		// D6.10 (part): a round that did not need the truncation branch at
		// all resolves any chain a PRIOR round left pending.
		ts.resolveContinuation()
		return truncationSuccessVerdict{action: truncationActionNone}
	}

	responseContent := response.Content
	accumulated := ts.appendToAccumulator(responseContent)

	if strings.TrimSpace(accumulated) == "" {
		// D4a: nothing was ever produced. No retry, no fallback
		// substitution, no markTurnFailed — the annotation is the whole
		// answer. loop.go's tail (and, for a streamed turn,
		// finalizeStreamer) own writing the zero-content entry this
		// annotates.
		ts.setTruncationReason(truncationReasonMaxOutputTokens)
		ts.resolveContinuation()
		logger.WarnCF("agent", "LLM response truncated at the output-token limit with no content produced",
			map[string]any{"agent_id": ts.agent.ID, "iteration": iteration, "model": llmModel})
		return truncationSuccessVerdict{action: truncationActionEnd, finalContent: ""}
	}

	if responseContent != "" {
		if newMessages, ok := al.truncationContinuationEligible(
			ts, messages, providerToolDefs, gracefulTerminal, iteration, continuationChain, accumulated,
		); ok {
			ts.markContinuationDispatched()
			al.emitEvent(
				EventKindLLMRetry,
				ts.eventMeta("runTurn", "turn.llm.retry"),
				LLMRetryPayload{
					Attempt:    ts.continuationRoundsSnapshot(),
					MaxRetries: maxTruncationContinuations,
					Reason:     truncationContinueRetryReason,
				},
			)
			logger.InfoCF("agent", "LLM response cut off at the output-token limit; auto-continuing",
				map[string]any{
					"agent_id":  ts.agent.ID,
					"iteration": iteration,
					"model":     llmModel,
					"round":     ts.continuationRoundsSnapshot(),
				})
			return truncationSuccessVerdict{action: truncationActionContinue, messages: newMessages}
		}
	}

	// D4b: not eligible for a further continuation (bound reached,
	// iteration capacity, graceful stop already used, or the request
	// cannot fit) — the accumulated answer stands.
	ts.setTruncationReason(truncationReasonMaxOutputTokens)
	ts.resolveContinuation()
	logger.WarnCF("agent", "LLM response remained truncated at the output-token limit; ending the turn with the partial answer",
		map[string]any{"agent_id": ts.agent.ID, "iteration": iteration, "model": llmModel})
	return truncationSuccessVerdict{action: truncationActionEnd, finalContent: accumulated}
}

// truncationContinuationEligible implements D6's gates 3-6 (D6.2's content
// gate and D4's own "content produced" check are the caller's
// responsibility, above). Returns the candidate `messages` slice — with the
// collapsed continuation chain appended, admission-checked via
// midTurnWindowCheck (D6.6) — and whether a further round may be
// dispatched.
func (al *AgentLoop) truncationContinuationEligible(
	ts *turnState,
	messages []providers.Message,
	providerToolDefs []providers.ToolDefinition,
	gracefulTerminal bool,
	iteration int,
	continuationChain *[]providers.Message,
	accumulated string,
) ([]providers.Message, bool) {
	// D6.3: bounded at 2 continuations per turn.
	if ts.continuationRoundsSnapshot() >= maxTruncationContinuations {
		return nil, false
	}
	// D6.4 (Codex C4): never spend the LAST permitted iteration on a
	// continuation — D4b takes over instead of the toolLimitResponse
	// fallback.
	if ts.agent.MaxIterations > 0 && iteration+1 >= ts.agent.MaxIterations {
		return nil, false
	}
	// D6.5 (Codex C3): cancellation beats recovery — never continue once
	// the turn has already gone through its graceful-stop terminal
	// request.
	if gracefulTerminal {
		return nil, false
	}

	// D6.7's "re-appended exactly once" — by IDENTITY, never by position.
	// This used to slice `messages[:len-2]` on the assumption that the
	// previous round's chain was still the last two entries. It is not: the
	// steering injection at the top of turnLoop and every tool-call round
	// append AFTER the chain, so the blind tail-slice removed whichever two
	// entries happened to be last (a dequeued steering message, or an
	// assistant tool_calls message together with its tool result) while
	// leaving the stale chain in place — the model then continued without
	// the steering instruction or without the tool result, and saw its own
	// partial twice.
	var base []providers.Message
	if continuationChain != nil {
		base = stripContinuationChain(messages, *continuationChain)
	} else {
		base = messages
	}
	chain := continuationChainMessages(accumulated)
	candidate := append(append([]providers.Message(nil), base...), chain...)

	// D6.6 (Codex pass 2, M2): `continue turnLoop` does not re-run the
	// proactive windowTrim — run the existing mid-turn admission check
	// against what the continuation would add. A guard hit here means D4b
	// with the partial (the caller sees ok=false), never typedTurnExit.
	checked, err := al.midTurnWindowCheck(ts, candidate, providerToolDefs)
	if err != nil {
		return nil, false
	}
	if continuationChain != nil {
		*continuationChain = chain
	}
	return checked, true
}

// continuationChainMessages builds the collapsed
// {assistant: answer-so-far, user: continue-instruction} pair ADR-087 D6.9
// appends to the turn-local `messages` slice. It is the SINGLE definition of
// that pair: truncationContinuationEligible above and the two history-rebuild
// restoration sites in runTurn (D6.7 — post-timeout-trim and
// post-context-overflow-trim assembly) all call it, so the shape the model
// sees can never drift between the three.
func continuationChainMessages(accumulated string) []providers.Message {
	return []providers.Message{
		{Role: "assistant", Content: accumulated},
		truncationContinueMessage(),
	}
}

// stripContinuationChain removes a previously-appended D6 continuation chain
// from msgs by IDENTITY — the recorded pair's role+content, matched as two
// ADJACENT entries, searched from the end — and returns a fresh slice.
//
// Position is not usable here (see truncationContinuationEligible's comment):
// appends land after the chain, and midTurnWindowCheck may hand back a
// re-sliced `messages`, so neither an index nor a trailing-count survives.
// When the recorded pair is not found (nothing was recorded, or a trim
// already evicted it) msgs is returned unchanged — the caller then appends a
// fresh chain, which is the correct degraded behaviour: at worst the model
// re-reads a partial it already has, never loses a tool result.
func stripContinuationChain(msgs, chain []providers.Message) []providers.Message {
	if len(chain) != 2 || len(msgs) < 2 {
		return msgs
	}
	for i := len(msgs) - 2; i >= 0; i-- {
		if !sameContinuationChainMessage(msgs[i], chain[0]) ||
			!sameContinuationChainMessage(msgs[i+1], chain[1]) {
			continue
		}
		out := make([]providers.Message, 0, len(msgs)-2)
		out = append(out, msgs[:i]...)
		out = append(out, msgs[i+2:]...)
		return out
	}
	return msgs
}

// sameContinuationChainMessage is stripContinuationChain's identity test. A
// chain entry is plain text with no tool calls and no media, so a message
// carrying either is never the chain even if its role and content match.
func sameContinuationChainMessage(a, b providers.Message) bool {
	return a.Role == b.Role &&
		a.Content == b.Content &&
		len(a.ToolCalls) == 0 && len(b.ToolCalls) == 0 &&
		len(a.Media) == 0 && len(b.Media) == 0
}

// flushContinuationAccumulator settles the D6 accumulator into the durable
// record, IN ORDER, and clears it.
//
// The invariant it enforces: the accumulator holds exactly the answer text
// that has NOT yet been persisted anywhere. A tool-calling round persists its
// OWN narration (the assistant tool_calls message into session history, plus
// appendIntermediateAssistantTranscript into the transcript) the moment it
// runs, so any earlier continuation prefix still sitting in the accumulator
// must be written FIRST or the archive ends up out of order: the prefix would
// otherwise only reach disk at turn end, prepended to the final answer, i.e.
// [P2][P1+P3] instead of [P1][P2][P3] — content preserved, order wrong, and
// the SPA's replay merge renders it as "P2\n\nP1P3" while the next turn's
// context sees P1 after P2.
//
// Clearing is the other half: content that has been flushed must never be
// re-emitted by the accumulator's later readers (the D6.10 merge at the
// direct-answer tail, D4b, preserveTruncatedAccumulator, or finalizeStreamer's
// SetContinuationContent probe).
//
// The recorded continuation chain is dropped at the same time: the chain's
// assistant entry is now settled context that the next round's rebuild must
// KEEP rather than strip, and the accumulator it would otherwise be rebuilt
// from no longer contains that text.
//
// Written here rather than as a turnState method because pkg/agent/turn.go is
// owned by a concurrent work item in this wave; the field access is
// mutex-guarded exactly as turn.go's own accessors do it.
func (al *AgentLoop) flushContinuationAccumulator(ts *turnState, continuationChain *[]providers.Message) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	pending := ts.continuationAccum
	ts.continuationAccum = ""
	ts.mu.Unlock()
	if continuationChain != nil {
		*continuationChain = nil
	}
	if pending == "" {
		return
	}
	ts.appendIntermediateAssistantTranscript(pending)
	if !ts.opts.NoHistory && ts.agent != nil && ts.agent.Sessions != nil {
		ts.agent.Sessions.AddMessage(ts.sessionKey, "assistant", pending)
	}
}

// preserveTruncatedAccumulator implements ADR-087 D6.8: every terminal exit
// that can fire while a D6 continuation is unresolved must keep what was
// already written, annotated truncated/max_output_tokens, instead of
// silently discarding it behind its own real error.
//
// It is runTurn's ONE choke point, invoked from a single `defer` registered
// immediately after `defer ts.finalizeStreamer(ctx)` so LIFO runs it just
// BEFORE the streamer is finalised (which is what picks up the finalContent/
// truncationReason set here on a streamed turn). It was previously
// hand-wired into five specific exits, which left every OTHER bare
// `return turnResult{}` inside turnLoop able to fire with a continuation
// pending and preserve nothing: the four process-hook aborts (before_llm /
// after_llm / before_tool / after_tool), the orphan-markup repair-budget
// exhaustion, the tool-dedup denial, the delegate park — and, past the loop,
// the session-save failure. A hook aborting at the top of a continuation
// round persisted the partial as a COMPLETE, un-annotated answer on webchat,
// and dropped it outright on every non-streamed surface (heartbeat, cron,
// delegated sub-turns) where the chain had never reached history at all
// (D6.7) — exactly what D6.8 forbids.
//
// ts.continuationUnresolved() is the "already settled" guard that makes the
// defer safe: resolveContinuation below clears it, so a turn that settled
// its chain during the loop (the overwhelming majority) no-ops here, and no
// exit can preserve twice.
//
// For a streamed turn the write happens later, inside finalizeStreamer's
// deferred call (which reads ts.finalContent/ts.truncationReason set here);
// for a non-streamed turn this writes the transcript entry directly, since
// no later choke point exists for that path. Either way, D6.8's "makes no
// provider call" half of the contract is satisfied by construction: this
// function never calls the provider.
func (al *AgentLoop) preserveTruncatedAccumulator(ts *turnState) {
	if ts == nil || !ts.continuationUnresolved() {
		return
	}
	accumulated := ts.continuationAccumulated()
	ts.SetFinalContent(accumulated)
	ts.setTruncationReason(truncationReasonMaxOutputTokens)
	ts.resolveContinuation()

	ts.mu.RLock()
	hasStreamer := ts.lastStreamer != nil
	ts.mu.RUnlock()
	if hasStreamer {
		// finalizeStreamer's deferred call (registered once, early in
		// runTurn) fires after this function returns and picks up the
		// ts.finalContent / ts.truncationReason just set above.
		return
	}
	// ADR-087 D4a/D4b: stamp Truncated/TruncationReason in the SAME write as
	// the content — see appendAssistantTranscriptTruncated's doc comment.
	// This replaces the former append-then-MarkLastEntryTruncated two-step,
	// which reopened and rewrote the whole transcript.jsonl just to stamp
	// two fields on the entry constructed one call earlier.
	ts.appendAssistantTranscriptTruncated(accumulated, truncationReasonMaxOutputTokens)
}
