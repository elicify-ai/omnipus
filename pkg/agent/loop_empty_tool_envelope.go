package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Issue #1278: a response whose finish_reason says the model stopped to call
// tools, but which carries no tool call, is not a final answer. The preamble it
// does carry ("...let me test:") announced a call that never arrived (a provider
// that dropped it, or died producing it), so ending the turn on it makes the
// agent look like it silently gave up.
const (
	// maxEmptyToolEnvelopeRetries is how many times the same completion is
	// re-requested before the turn fails visibly. One: a repeat is not a blip.
	maxEmptyToolEnvelopeRetries = 1
	// emptyToolEnvelopeRetryReason labels the retry event.
	emptyToolEnvelopeRetryReason = "empty_tool_envelope"
	// emptyToolEnvelopeStage labels the terminal error event.
	emptyToolEnvelopeStage = "empty_tool_envelope"
	// emptyToolEnvelopeText is what the person reads when the retry fails too.
	emptyToolEnvelopeText = "The model said it was about to use a tool, but no tool call arrived, so nothing was run. Please try again."
	// emptyToolEnvelopeDiagnosticChars bounds the assembled content copied into
	// the diagnostic log line.
	emptyToolEnvelopeDiagnosticChars = 2000
)

// finishReasonClaimsToolCalls reports whether a finish_reason says the model
// ended its response to call tools (OpenAI "tool_calls"/"function_call",
// Anthropic "tool_use").
func finishReasonClaimsToolCalls(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "tool_calls", "tool_use", "function_call":
		return true
	}
	return false
}

// handleEmptyToolEnvelope retries a "tool_calls with no calls" response once,
// with a diagnostic log that keeps the assembled envelope, then fails the turn
// with a visible error. Not a punctuation heuristic: the trigger is the
// provider's own finish_reason against the assembled tool-call array.
func (cn *agentLoopRunTurnConductorRunIterations) handleEmptyToolEnvelope() agentLoopRunTurnConductorRunIterationsFlow {
	rf := cn.rc.rx.rr.rq.ri.rf
	resp := rf.response
	if resp == nil || len(resp.ToolCalls) > 0 || !finishReasonClaimsToolCalls(resp.FinishReason) || cn.rc.rx.rr.rq.ri.gracefulTerminal {
		return agentLoopRunTurnConductorRunIterationsNext
	}
	content := resp.Content
	if len(content) > emptyToolEnvelopeDiagnosticChars {
		content = content[len(content)-emptyToolEnvelopeDiagnosticChars:]
	}
	completionTokens := 0
	if resp.Usage != nil {
		completionTokens = resp.Usage.CompletionTokens
	}
	fields := map[string]any{
		"agent_id":          rf.rt.ts.agent.ID,
		"iteration":         rf.rt.iteration,
		"model":             rf.rt.llmModel,
		"finish_reason":     resp.FinishReason,
		"content_chars":     len(resp.Content),
		"content_tail":      content,
		"reasoning_chars":   len(resp.ReasoningContent),
		"completion_tokens": completionTokens,
		"attempt":           cn.emptyToolEnvelopeRetries + 1,
	}
	if cn.emptyToolEnvelopeRetries < maxEmptyToolEnvelopeRetries {
		cn.emptyToolEnvelopeRetries++
		logger.WarnCF("agent", "Provider reported finish_reason tool_calls but no tool call was assembled; retrying the completion", fields)
		rf.rt.al.emitEvent(
			EventKindLLMRetry,
			rf.rt.ts.eventMeta("runTurn", "turn.llm.retry"),
			LLMRetryPayload{Attempt: cn.emptyToolEnvelopeRetries, MaxRetries: maxEmptyToolEnvelopeRetries, Reason: emptyToolEnvelopeRetryReason},
		)
		return agentLoopRunTurnConductorRunIterationsContinue
	}

	logger.ErrorCF("agent", "Provider again reported finish_reason tool_calls with no tool call; ending the turn with an error", fields)
	cn.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
	llm := LLMError{Code: CodeUnknown, Message: emptyToolEnvelopeText, Retryable: true}
	rf.rt.al.emitEvent(
		EventKindError,
		rf.rt.ts.eventMeta("runTurn", "turn.error"),
		ErrorPayload{
			Stage:     emptyToolEnvelopeStage,
			Code:      string(llm.Code),
			Message:   llm.Message,
			ChatID:    rf.rt.ts.opts.ChatID,
			SessionID: string(rf.rt.ts.routingSessionID),
		},
	)
	rf.rt.ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)
	cn.rc.ret0 = turnResult{}
	cn.rc.ret1 = &curatedTurnError{text: emptyToolEnvelopeText}
	cn.ret0 = agentLoopRunTurnConductorReturn
	return agentLoopRunTurnConductorRunIterationsReturn
}
