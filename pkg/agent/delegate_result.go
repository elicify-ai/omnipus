// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "strings"

// buildSyncDelegateResult produces the persisted session.ToolCall Result map for
// a SYNCHRONOUS "delegate" tool call, mirroring the {"text":…}(+"error":true)
// shape that spawnSubTurn's async W4 defer (subturn.go) writes for ASYNC
// delegation.
//
// Why this exists (W4, sync path): that async defer no-ops for synchronous
// delegation. It fires before the parent's delegate tool_call record has been
// written — spawnSubTurn returns (running its defer) before DelegateTool.
// executeSync returns to the turn loop — so the record-lookup finds nothing,
// and its "not found, retry" budget is gated on cfg.Async. For the sync path
// the turn loop's own tool_call write (loop.go) is therefore the FINAL
// persisted state; without populating Result there, the record kept a terminal
// status but an EMPTY result, so a reloaded sync delegation showed no trace of
// what the delegate produced — unlike the live WS stream and the async path
// (found live by UAT). See spawnSubTurn's W4 defer in pkg/agent/subturn.go for
// the async counterpart and pkg/agent/wave4_delegate_result_test.go for the
// async coverage this complements.
//
// It applies ONLY to synchronous delegation. For ASYNC delegation the tool
// returns an immediate "running in background" ack here (Async:true), and
// spawnSubTurn's defer owns persisting the real result later; if this wrote the
// ack as Result, a sub-turn that legitimately finishes with an EMPTY output
// would keep that stale ack forever — the defer only overwrites Result when it
// has non-empty text or an error — leaving a terminal-status delegate whose
// result wrongly claims it is still running. So async is excluded outright and
// left to the defer (unchanged from before this fix).
//
// Returns nil for any non-"delegate" tool (the caller then leaves Result unset,
// exactly as before this fix — no behavior change for other tools), for async
// delegation, and for an empty, non-error sync delegate result.
func buildSyncDelegateResult(toolName, contentForLLM string, isError, isAsync bool) map[string]any {
	if toolName != "delegate" || isAsync {
		return nil
	}
	var r map[string]any
	if text := strings.TrimSpace(contentForLLM); text != "" {
		r = map[string]any{"text": text}
	} else if isError {
		// Preserve the async defer's behavior: a failed delegate always carries
		// explanatory text alongside the error flag, even when the sub-turn
		// produced no output of its own.
		r = map[string]any{"text": "sub-turn reported an error"}
	}
	if r != nil && isError {
		r["error"] = true
	}
	return r
}
