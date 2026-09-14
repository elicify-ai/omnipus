// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_attempt_turn_error.go classifies the one error family a worker turn
// can end on that ANOTHER TURN IN THE SAME RUN can reasonably clear: malformed
// tool-call output. Everything else (auth, config, cancellation, context
// overflow) breaks the run.
//
// Under the two-level run model (task_run_loop.go) a malformed-tool-output
// turn spends an INNER goal try and steers the worker in the same session —
// it never consumes a task attempt by itself. Live UAT origin (lane L2,
// scenario A-12, z-ai/glm-5.3): a task's second attempt fixed the script the
// Judge had ruled unmet and self-verified it, then emitted a tool call as
// unparseable text while closing out; the turn ended on that malformed output
// and nineteen attempts of budget went unused on a fault a fresh turn
// routinely clears.
package agent

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// attemptRecoverableTurnErrorCode reports whether a worker turn's error is a
// malformed-tool-call-output fault that another turn can reasonably clear,
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
//     the run instead of steering it.
//
// isRetryable(CodeToolArgs) is false and is deliberately NOT consulted: it
// answers "will the identical request succeed if resent", which is the wrong
// question here. A steered turn is not the identical request — it is a fresh
// turn carrying a note about what went wrong (malformedToolOutputSteering).
//
// The orphan-tool-markup repair ladder's exhaustion exit in runTurn (loop.go)
// also reaches this rule: it wraps a *common.ToolArgumentsError into the error
// it returns (commit f363f564, UAT A-12), so an attempt that runs out of
// markup repairs is consumed and re-dispatched rather than failing the task.
// pkg/agent/orphan_markup_task_attempt_test.go pins that path.
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

// malformedToolOutputSteering is the note the NEXT turn in the run carries
// after a try ended on malformed tool-call output.
//
// It never echoes the residue or the error text back to the model: repeating
// a mangled fragment invites the model to reproduce it verbatim (the same
// reason orphanToolMarkupRepairMessage withholds it). It does tell the worker
// the two facts that change what it should do: nothing was judged, and work
// from the previous turn may already be on disk.
func malformedToolOutputSteering(code LLMErrorCode) string {
	cause := "its arguments could not be decoded"
	advice := "Make every tool call through the tool-calling interface — never write tool-call markup as text."
	if code == CodeToolCallTruncated {
		cause = "it was cut off at the output-token limit before the call finished"
		advice = "Keep each tool call's arguments small — split a large file write into several smaller calls."
	}
	return "Your previous turn ended on malformed tool-call output: the model tried to call a tool, but " +
		cause + ", so that call never ran and the turn ended before anything could be judged. " +
		"Nothing about the work was ruled unmet.\n\n" +
		"Work from the previous turn may already exist — check the current state before redoing it. " +
		advice + " Then finish with a goal_claim call when the work is verified."
}
