// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "strings"

// buildSyncDelegateResult produces the persisted session.ToolCall Result map for
// a SYNCHRONOUS "delegate" tool call, mirroring the {"text":…}(+"error":true)
// shape that the deleted spawnSubTurn's async W4 defer (pre-ADR-091,
// pkg/agent/subturn.go) wrote for ASYNC delegation.
//
// Why this exists (W4, sync path): that async defer no-op'd for synchronous
// delegation. It fired before the parent's delegate tool_call record had been
// written — the delegated run could finish before the delegate tool returned to
// the turn loop — so the record lookup found nothing,
// and its "not found, retry" budget was gated on cfg.Async. For the sync path
// the turn loop's own tool_call write (loop.go) is therefore the FINAL
// persisted state; without populating Result there, the record kept a terminal
// status but an EMPTY result, so a reloaded sync delegation showed no trace of
// what the delegate produced — unlike the live WS stream and the async path
// (found live by UAT). pkg/agent/wave4_delegate_result_test.go has the async
// coverage this complements.
//
// ADR-091 fix lane RX-SUBTURN note (comment-only; code unchanged): ADR-091 D4
// deleted the caller-selectable async=false/true choice for `delegate` —
// pkg/tools/delegate.go's Execute no longer implements AsyncExecutor (its own
// doc comment: "ADR-091 made every action return as soon as launch and
// dispatch have returned"), so the isAsync flag this function branches on is
// no longer set the way the "It applies ONLY to synchronous delegation"
// paragraph below originally meant — grep finds no production code left that
// sets Async:true for a delegate ToolResult, so this function's `delegate`
// branch fires on every delegate call today, not a "sync-only" subset.
// Whether that is still correct, or has quietly become dead/wrong logic that
// needs updating, is a code question outside a comment-only lane — flagged
// for the team, not fixed here.
//
// It applies ONLY to synchronous delegation. For ASYNC delegation the tool
// returns an immediate "running in background" ack here (Async:true), and
// the deleted spawnSubTurn's defer owned persisting the real result later; if
// this wrote the ack as Result, a sub-turn that legitimately finished with an
// EMPTY output would keep that stale ack forever — the defer only overwrote
// Result when it had non-empty text or an error — leaving a terminal-status
// delegate whose result wrongly claimed it was still running. So async was
// excluded outright and left to the defer (unchanged from before that fix).
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
