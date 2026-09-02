// Omnipus — Per-turn tool-call failure circuit breaker
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"fmt"
)

// UAT fix (fix/uat-defects-2026-08-22, Defect 1): a live UAT drove run_task
// into a saturated dispatch cap ("global dispatch cap reached (2/2 in
// flight), retry later") and create_task_in_workspace into an unwired plan
// store ("plan store is not configured") — and in both cases the calling
// model retried the EXACT SAME tool call (same tool, same arguments) dozens
// of times in a row (~55 and ~44 respectively) before a human had to step
// in, burning ~20M tokens. Neither failure was actually transient — the
// dispatch cap stayed saturated and the store stayed unwired for the whole
// stretch — but nothing on THIS PROJECT'S OWN dispatch side ever told the
// model that, and nothing capped how many times it could keep paying for a
// full LLM round trip to learn the same thing again.
//
// This file is the fix: a per-turn (turnState-scoped, never persisted)
// streak counter keyed by the exact (tool name, arguments) signature. It
// does NOT try to classify which errors are "retryable" — the two UAT
// failures were of completely different shapes (a capacity message vs a
// config-wiring message), and pattern-matching on wording is exactly the
// kind of thing that silently stops working the next time an error message
// is reworded. Instead it tracks any identical-signature failure streak,
// regardless of what the error says:
//
//   - At toolFailureWarnThreshold consecutive identical failures, the
//     result handed back to the model gets an explicit, unambiguous notice
//     appended: this is not new information, stop retrying blindly.
//   - At toolFailureCircuitBreakThreshold, the signature is marked broken
//     for the rest of the turn: loop.go's dispatch site (mirroring the
//     existing SEC-26 rate-limit denial block immediately above it) skips
//     calling Execute entirely and returns a hard denial instead, so the
//     turn can burn at most a small, fixed number of duplicate calls no
//     matter how many times the model asks.
//
// A success, or any change in the call's own signature, resets the streak
// for that signature back to zero — this only ever fires on genuine,
// exact repetition.
const (
	// toolFailureWarnThreshold is the consecutive-identical-failure count at
	// which the tool result gains an explicit "stop retrying" notice.
	toolFailureWarnThreshold = 3
	// toolFailureCircuitBreakThreshold is the consecutive-identical-failure
	// count at which further identical calls are refused outright for the
	// rest of the turn, without even being dispatched.
	toolFailureCircuitBreakThreshold = 6
)

// toolCallSignature derives a stable per-turn identity for a tool call from
// its name and arguments, so the streak counter tracks "the exact same call"
// rather than "this tool, called with anything". encoding/json.Marshal on a
// map[string]any sorts keys alphabetically (documented Go behavior), so two
// argument maps built from the same content marshal identically regardless
// of iteration order. A marshal failure (unsupported argument value) falls
// back to fmt's %#v, which is not guaranteed key-stable but only degrades
// this to "the streak resets more often than ideal" — never a bug, never a
// panic, never a wrong-tool collision (the tool name is always the prefix).
func toolCallSignature(toolName string, args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return toolName + "\x00" + fmt.Sprintf("%#v", args)
	}
	return toolName + "\x00" + string(b)
}

// recordToolFailure notes one more failure for sig and returns the new
// consecutive-failure count. Call only when the tool result was an error;
// pair every call site with recordToolSuccess on the non-error path so the
// streak actually resets.
func (ts *turnState) recordToolFailure(sig string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.toolFailureStreaks == nil {
		ts.toolFailureStreaks = make(map[string]int)
	}
	ts.toolFailureStreaks[sig]++
	return ts.toolFailureStreaks[sig]
}

// recordToolSuccess clears sig's failure streak — a successful call (or one
// whose signature no longer matches the prior failing call) is proof the
// stuck condition is gone, so the next failure (if any) starts counting
// from scratch rather than inheriting an unrelated streak.
func (ts *turnState) recordToolSuccess(sig string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.toolFailureStreaks != nil {
		delete(ts.toolFailureStreaks, sig)
	}
}

// tripToolCircuitBreaker marks sig as hard-blocked for the remainder of this
// turn, recording reason for the denial message every subsequent identical
// call receives.
func (ts *turnState) tripToolCircuitBreaker(sig, reason string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.toolCircuitBroken == nil {
		ts.toolCircuitBroken = make(map[string]string)
	}
	ts.toolCircuitBroken[sig] = reason
}

// toolCircuitBreakerTripped reports whether sig is currently hard-blocked
// for this turn, and the reason recorded when it tripped.
func (ts *turnState) toolCircuitBreakerTripped(sig string) (reason string, tripped bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	reason, tripped = ts.toolCircuitBroken[sig]
	return reason, tripped
}

// toolFailureWarnNotice is appended to a failing tool result's content once
// its streak reaches toolFailureWarnThreshold — a cheap, always-on
// mitigation independent of whether the harder circuit breaker below ever
// trips: it is meant to give the model a real chance to stop on its own
// before the breaker forces the issue.
func toolFailureWarnNotice(toolName string, streak int) string {
	return fmt.Sprintf(
		"\n\n[SYSTEM NOTICE: this exact %q call (same tool, same arguments) has now failed "+
			"%d times in a row with the same outcome. This is very unlikely to be transient — "+
			"retrying with identical arguments will not change the result. Stop retrying this "+
			"exact call: either address the underlying condition, try a materially different "+
			"approach, or tell the user you are blocked.]",
		toolName, streak,
	)
}

// toolFailureCircuitBreakerReason is the denial reason recorded (and surfaced
// to the model) the moment a signature's streak crosses
// toolFailureCircuitBreakThreshold.
func toolFailureCircuitBreakerReason(toolName string, streak int) string {
	return fmt.Sprintf(
		"this exact %q call failed identically %d times in a row this turn with no change "+
			"in outcome — the dispatch layer is refusing further identical attempts for the "+
			"rest of this turn",
		toolName, streak,
	)
}

// toolCircuitBreakerDenialMessage is the ForLLM-equivalent error text handed
// back for a call that toolCircuitBreakerTripped already refused to
// dispatch.
func toolCircuitBreakerDenialMessage(toolName, reason string) string {
	return fmt.Sprintf(
		"Blocked by dispatch-side circuit breaker: %s. Do not retry these exact arguments again "+
			"this turn — try a different approach or inform the user you are blocked.",
		reason,
	)
}
