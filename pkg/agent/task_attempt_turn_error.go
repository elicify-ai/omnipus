// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_attempt_turn_error.go decides what a task's goal loop does when a
// worker ATTEMPT ends because the turn returned an error, rather than with a
// claim or a no-signal response.
//
// Before this file, TaskExecutor.finishTaskRun answered every such error the
// same way: failTask, terminal `failed`, no attempt consumed. That is right
// for an error nothing about the next attempt can change (an auth or config
// fault, a user Stop, a context overflow). It is wrong for a fault in the
// MODEL'S OWN OUTPUT. Live UAT (lane L2, scenario A-12, z-ai/glm-5.3): a task's
// second attempt fixed the script the Judge had ruled unmet and self-verified
// it, then emitted a tool call as unparseable text while closing out. The turn
// ended on that malformed output and the task went terminal `failed` at
// attempt 1/20 — nineteen attempts of budget unused, on a fault a fresh
// attempt routinely clears.
//
// The documented rule for an attempt that ends without a judgeable outcome is
// planning-goals-spec.md FR-045 / US-5 AS-4: "the run is treated as an unmet
// claim (attempt consumed, re-dispatch or owner-wake) — NOT terminally failed
// on the spot". No ADR decides that execution errors are terminal regardless
// of cause (ADR-049/052/055/084/086 and the specs were searched; ADR-058 §3.8
// only re-confirms the error-string propagation hop by hop for the
// tool-denial abort, which stays terminal here because it is not a
// malformed-output fault). This file applies FR-045's rule to the one error
// family that is genuinely a model-output fault, and to nothing else.
package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// attemptRecoverableTurnErrorCode reports whether a worker turn's error is a
// malformed-tool-call-output fault that another ATTEMPT can reasonably clear,
// and if so which contract code (CodeToolArgs or CodeToolCallTruncated) names
// it.
//
// Classification is by TYPE, never by message text. Two conditions must both
// hold:
//
//  1. a *common.ToolArgumentsError is in err's chain — the typed refusal the
//     providers raise for an undecodable tool call. A plain error whose text
//     happens to read "invalid tool arguments" does NOT qualify, even though
//     TranslateTurnError's substring fallback would label it CodeToolArgs.
//  2. TranslateTurnError — the one canonical turn-error classifier — maps err
//     to CodeToolArgs or CodeToolCallTruncated. Routing through it keeps this
//     decision from ever disagreeing with what the chat surface reported for
//     the same error, and inherits its precedence: a turn that was cancelled
//     (Stop), timed out, or hit an unrecoverable context classifies as that
//     typed exit first, so a Stop that races a malformed tool call still ends
//     the task instead of re-dispatching it.
//
// isRetryable(CodeToolArgs) is false and is deliberately NOT consulted: it
// answers "will the identical request succeed if resent", which is the wrong
// question here. A new attempt is not the identical request — it is a fresh
// turn carrying a note about what went wrong (malformedToolOutputSteering).
//
// Known gap: the orphan-tool-markup repair ladder's exhaustion exit in
// runTurn (loop.go) returns an UNTYPED error today, so that path does not
// reach this rule until runTurn wraps a *common.ToolArgumentsError into it.
func attemptRecoverableTurnErrorCode(err error) (LLMErrorCode, bool) {
	if err == nil {
		return "", false
	}
	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		return "", false
	}
	switch code := TranslateTurnError(err).Code; code {
	case CodeToolArgs, CodeToolCallTruncated:
		return code, true
	default:
		return "", false
	}
}

// malformedToolOutputSteering is the note the NEXT attempt's prompt carries
// (via consumeAttemptOrExhaust -> writeSteeringPrompt -> buildPrompt's
// "Feedback from attempt N" block) after an attempt ended on malformed
// tool-call output.
//
// It never echoes the residue or the error text back to the model: repeating
// a mangled fragment invites the model to reproduce it verbatim (the same
// reason orphanToolMarkupRepairMessage withholds it). It does tell the worker
// the two facts that change what it should do: nothing was judged, and work
// from the previous attempt may already be on disk.
func malformedToolOutputSteering(code LLMErrorCode) string {
	cause := "its arguments could not be decoded"
	advice := "Make every tool call through the tool-calling interface — never write tool-call markup as text."
	if code == CodeToolCallTruncated {
		cause = "it was cut off at the output-token limit before the call finished"
		advice = "Keep each tool call's arguments small — split a large file write into several smaller calls."
	}
	return "Your previous attempt ended on malformed tool-call output: the model tried to call a tool, but " +
		cause + ", so that call never ran and the attempt ended before anything could be judged. " +
		"Nothing about the work was ruled unmet.\n\n" +
		"Work from the previous attempt may already exist — check the current state before redoing it. " +
		advice + " Then finish with the usual completion signal."
}

// retryAttemptAfterMalformedToolOutput is finishTaskRun's error-branch hook.
// handled is false when turnErr is not a malformed-tool-call-output fault, or
// when the task is not in a state the goal loop may re-dispatch — the caller
// then takes its unchanged terminal execution-error path. handled is true when
// this function has taken ownership of the outcome; redispatchTaskID then
// carries consumeAttemptOrExhaust's own answer (the task id to re-enter, or ""
// when the budget is spent or a concurrent Stop won the CAS).
//
// The attempt IS consumed. A fault that costs nothing would let a model that
// never emits a parseable call loop forever at full LLM spend — the exact
// livelock rejectBareEvidenceClaim's streak bound exists to prevent — so this
// path spends the same AttemptCount/hardCeiling budget every other unmet
// outcome spends, and the task still fails once that budget is gone.
func (te *TaskExecutor) retryAttemptAfterMalformedToolOutput(
	ctx context.Context, t *task.Task, taskSessionID string, turnErr error, run *activeRun,
) (redispatchTaskID string, handled bool) {
	code, ok := attemptRecoverableTurnErrorCode(turnErr)
	if !ok {
		return "", false
	}

	// Decide on the CURRENT record, as every other finishTaskRun outcome does:
	// the worker may have written the task mid-run, and consumeAttemptOrExhaust
	// computes the next AttemptCount from the record it is handed.
	current, gerr := te.store.Get(t.ID)
	if gerr != nil {
		logger.WarnCF("task_executor",
			"goal-loop: attempt ended on malformed tool-call output, but the task could not be re-read "+
				"to consume the attempt — failing the run as an execution error instead",
			map[string]any{"task_id": t.ID, "code": string(code), "error": gerr.Error()})
		return "", false
	}
	if current.Scratchpad {
		// FR-048: Scratchpad tasks are exempt from the goal loop entirely.
		return "", false
	}
	if current.Status != task.StatusInProgress {
		// An explicit update_task terminal write or a concurrent Stop already
		// decided this task; the unchanged execution-error path handles it
		// exactly as before.
		return "", false
	}

	if current.PendingJudgeClaim != "" {
		// A done-claim staged earlier in the errored turn. Leaving it set would
		// make the NEXT attempt's finishTaskRun adjudicate THIS attempt's stale
		// claim text in place of whatever the next attempt reports (the
		// PendingJudgeClaim branch takes priority over the marker there). The
		// next attempt is told to re-check and re-claim.
		cleared := ""
		if _, cerr := te.store.Update(current.ID, task.Patch{PendingJudgeClaim: &cleared}); cerr != nil {
			logger.WarnCF("task_executor",
				"goal-loop: could not clear a pending judge claim staged by an attempt that ended on malformed tool-call output",
				map[string]any{"task_id": current.ID, "error": cerr.Error()})
		} else {
			current.PendingJudgeClaim = ""
		}
	}

	logger.WarnCF("task_executor",
		"goal-loop: attempt ended on malformed tool-call output — consuming one attempt and re-dispatching "+
			"with a note, instead of failing the task",
		map[string]any{
			"task_id":       current.ID,
			"agent_id":      current.AgentID,
			"code":          string(code),
			"attempt":       current.AttemptCount + 1,
			"error":         turnErr.Error(),
			"session_id":    taskSessionID,
			"attempts_used": current.AttemptCount,
		})
	return te.consumeAttemptOrExhaust(ctx, current, taskSessionID, malformedToolOutputSteering(code), nil, run), true
}
