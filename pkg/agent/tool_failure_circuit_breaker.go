// Omnipus — Per-turn tool-call failure circuit breaker
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
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
	// toolFailureCircuitBreakThreshold is the ATTEMPT number at which an
	// identical call is refused outright for the rest of the turn, without
	// being dispatched: after toolFailureCircuitBreakThreshold-1 consecutive
	// identical failures, attempt number toolFailureCircuitBreakThreshold is
	// the first one that never runs (UAT 2026-09-13 D-81 — the earlier
	// reading "trip AFTER the 6th failure" let the 6th identical call execute
	// and only refused the 7th, so the escalation the plan expects at 6 never
	// visibly happened).
	toolFailureCircuitBreakThreshold = 6

	// Oscillation detection (UAT 2026-09-13 D-23). The streak counter above
	// is blind to a loop of SUCCESSFUL, mutually-cancelling calls — the UAT
	// drove create_record_type X / delete_record_type X five times in a row,
	// every call succeeding, and nothing fired. A loop is a repeating cycle
	// of call signatures: the last oscillationWarnCycles full repetitions of
	// a period-2..oscillationMaxPeriod pattern (with at least two distinct
	// calls in it) earns a notice on the result; the call that would extend
	// the pattern to oscillationBreakCycles repetitions is refused without
	// dispatch. A call that breaks the pattern is always allowed — this only
	// ever bites exact repetition.
	oscillationMinPeriod   = 2
	oscillationMaxPeriod   = 4
	oscillationWarnCycles  = 3
	oscillationBreakCycles = 5
	// toolCallHistoryCap bounds the per-turn signature history the detector
	// scans; oscillationMaxPeriod*oscillationBreakCycles is all it needs.
	toolCallHistoryCap = 64
)

// nonSemanticToolArgs lists, per tool, the argument keys that tool declares
// as documentation-only — they change nothing about what is executed, so
// they must not distinguish one retry from the next. Keep this in lockstep
// with the tool's own schema text: bash's `description` is declared
// "(documentation only)" in pkg/tools/shell.go and is never read by the
// executor. Add a key here only when the tool's schema says the same.
var nonSemanticToolArgs = map[string]map[string]bool{
	"bash": {"description": true},
}

// toolCallSignature derives a stable per-turn identity for a tool call from
// its name and arguments, so the streak counter tracks "the exact same call"
// rather than "this tool, called with anything". encoding/json.Marshal on a
// map[string]any sorts keys alphabetically (documented Go behavior), so two
// argument maps built from the same content marshal identically regardless
// of iteration order. A marshal failure (unsupported argument value) falls
// back to fmt's %#v, which is not guaranteed key-stable but only degrades
// this to "the streak resets more often than ideal" — never a bug, never a
// panic, never a wrong-tool collision (the tool name is always the prefix).
//
// Arguments the tool itself declares as having no effect on execution are
// EXCLUDED before hashing (nonSemanticToolArgs). Without that, a model that
// varies only a cosmetic field defeats the breaker completely: on 2026-09-12
// (CI e2e, Conformance_t0_ChatGoalE2E) a model re-issued the byte-identical
// `git commit -m "evidence"` 182 times in 4m22s against a sandbox denial,
// each with a fresh bash `description` ("(third attempt)" … "(one hundred
// eighty-first attempt)") — 182 distinct signatures, max streak 1, neither
// threshold ever fired, and the turn only ended at max_tool_iterations.
func toolCallSignature(toolName string, args map[string]any) string {
	if skip := nonSemanticToolArgs[toolName]; len(skip) > 0 && len(args) > 0 {
		filtered := make(map[string]any, len(args))
		for k, v := range args {
			if !skip[k] {
				filtered[k] = v
			}
		}
		args = filtered
	}
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

// toolCircuitBreakerTripped reports whether sig must be refused without
// dispatch for this turn, and why. Three conditions trip it, checked in
// order:
//
//  1. sig was already marked broken earlier this turn.
//  2. sig has failed identically toolFailureCircuitBreakThreshold-1 times
//     in a row, so THIS attempt is number toolFailureCircuitBreakThreshold
//     (D-81): it is refused and sig is marked broken for the rest of the
//     turn.
//  3. Dispatching sig would extend the turn's call history into
//     oscillationBreakCycles repetitions of a short cycle (D-23) — where a
//     repetition means the same calls returning the SAME results (see
//     detectOscillationAhead). Nothing is marked broken here: the refusal
//     is per-call, so a call that breaks the pattern still runs. (Because
//     a refused call is never appended to the history, repeating the
//     refused call is refused again.)
func (ts *turnState) toolCircuitBreakerTripped(sig string) (reason string, tripped bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if reason, tripped = ts.toolCircuitBroken[sig]; tripped {
		return reason, true
	}
	if streak := ts.toolFailureStreaks[sig]; streak >= toolFailureCircuitBreakThreshold-1 {
		toolName, _, _ := strings.Cut(sig, "\x00")
		reason = toolFailureCircuitBreakerReason(toolName, streak)
		if ts.toolCircuitBroken == nil {
			ts.toolCircuitBroken = make(map[string]string)
		}
		ts.toolCircuitBroken[sig] = reason
		return reason, true
	}
	if period, cycles := detectOscillationAhead(ts.toolCallHistory, sig); cycles >= oscillationBreakCycles {
		toolName, _, _ := strings.Cut(sig, "\x00")
		return toolOscillationBreakerReason(toolName, period, cycles), true
	}
	return "", false
}

// loopHistoryEntry encodes one dispatched call for the oscillation
// detector: the call's signature and a fingerprint of what it returned,
// joined by a separator that cannot occur in either half (the signature is
// name + "\x00" + JSON, the fingerprint is hex).
func loopHistoryEntry(sig, resultKey string) string {
	return sig + "\x01" + resultKey
}

// loopHistorySig recovers the call signature from a history entry.
func loopHistorySig(entry string) string {
	sig, _, _ := strings.Cut(entry, "\x01")
	return sig
}

// toolResultLoopKey reduces a tool result's content to a short, stable
// fingerprint for the history. Two results with the same content fingerprint
// identically; any difference in content is "progress" to the detector.
func toolResultLoopKey(content string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(content))
	return fmt.Sprintf("%016x", h.Sum64())
}

// loopResultUnknown is the fingerprint recorded when a call site does not
// supply the result (recordToolCallForLoopDetection). Every such entry
// fingerprints alike, so a history built without results behaves exactly
// as the original signature-only detector did.
const loopResultUnknown = ""

// recordToolCallForLoopDetection is the result-blind form kept for the
// existing dispatch site in loop.go. It records sig with an unknown result,
// which the detector treats as identical to every other unknown result —
// i.e. the pre-2026-09-14 behaviour, where alternating poll/read calls
// against a progressing background job tripped the breaker (Codex review
// finding #9). The fix is recordToolCallOutcomeForLoopDetection; the
// dispatch site should pass toolResult.ContentForLLM() (captured BEFORE any
// notice is appended to it) so that progress is visible to the detector.
func (ts *turnState) recordToolCallForLoopDetection(sig string) string {
	return ts.recordLoopHistoryEntry(sig, loopResultUnknown)
}

// recordToolCallOutcomeForLoopDetection appends sig and a fingerprint of
// resultContent to this turn's dispatched-call history (success or failure
// alike — a loop of successes is the case this exists for) and returns a
// notice to append to the tool result when the history's tail now forms
// oscillationWarnCycles or more repetitions of a short cycle WITH THE SAME
// RESULTS each time. Call it once per DISPATCHED call, after the result is
// known; never for a call the breaker refused.
//
// Codex review 2026-09-14 finding #9: the detector used to look at call
// signatures alone, so an agent monitoring a background build — bash poll,
// bash read, poll, read … — was refused at the fifth cycle even though every
// read returned new output. A cycle whose results change is not a loop that
// "undoes or repeats the previous one"; it is a job making progress. A cycle
// whose results are byte-identical every time (the D-23 create/delete churn,
// or a poll/read pair that has genuinely stalled) still trips at the
// existing thresholds. No tool or sub-case is special-cased: the evidence
// of progress is in the results, and pattern-matching on tool names is the
// kind of rule that silently stops working when a tool is renamed.
func (ts *turnState) recordToolCallOutcomeForLoopDetection(sig, resultContent string) string {
	return ts.recordLoopHistoryEntry(sig, toolResultLoopKey(resultContent))
}

// recordLoopHistoryEntry is the shared tail of the two recording entry
// points: append, cap, and report the current oscillation length.
func (ts *turnState) recordLoopHistoryEntry(sig, resultKey string) string {
	entry := loopHistoryEntry(sig, resultKey)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.toolCallHistory = append(ts.toolCallHistory, entry)
	if len(ts.toolCallHistory) > toolCallHistoryCap {
		ts.toolCallHistory = ts.toolCallHistory[len(ts.toolCallHistory)-toolCallHistoryCap:]
	}
	period, cycles := detectOscillation(ts.toolCallHistory)
	if cycles < oscillationWarnCycles {
		return ""
	}
	toolName, _, _ := strings.Cut(sig, "\x00")
	return toolOscillationWarnNotice(toolName, period, cycles)
}

// detectOscillationAhead answers "if pendingSig were dispatched now and
// returned the same result it returned one cycle ago, how long would the
// oscillation be?" — the pre-dispatch question toolCircuitBreakerTripped
// asks. The pending call has no result yet, so for each candidate period p
// whose entry p places back carries the same signature, that entry (result
// included) stands in for the pending call and the best count wins. A
// history whose results changed across cycles (a progressing job) never
// reaches oscillationBreakCycles here; one whose results are identical does,
// exactly as the signature-only detector did.
func detectOscillationAhead(history []string, pendingSig string) (period, cycles int) {
	n := len(history)
	for p := oscillationMinPeriod; p <= oscillationMaxPeriod && p <= n; p++ {
		prior := history[n-p]
		if loopHistorySig(prior) != pendingSig {
			continue
		}
		candidate := make([]string, 0, n+1)
		candidate = append(candidate, history...)
		candidate = append(candidate, prior)
		if pp, c := detectOscillation(candidate); c > cycles {
			period, cycles = pp, c
		}
	}
	return period, cycles
}

// detectOscillation finds the longest run of exact repetitions of a short
// cycle at the END of history. For each period p in
// [oscillationMinPeriod, oscillationMaxPeriod] it counts how many complete
// cycles of length p the tail repeats and returns the period with the most
// cycles (ties go to the shorter period). A cycle whose p members are all
// the same signature is not an oscillation — that is the identical-failure
// streak's business — and is reported as 0.
func detectOscillation(history []string) (period, cycles int) {
	n := len(history)
	for p := oscillationMinPeriod; p <= oscillationMaxPeriod; p++ {
		if n < 2*p {
			continue
		}
		distinct := make(map[string]struct{}, p)
		for _, s := range history[n-p:] {
			distinct[s] = struct{}{}
		}
		if len(distinct) < 2 {
			continue
		}
		matched := 0
		for i := n - p - 1; i >= 0 && history[i] == history[i+p]; i-- {
			matched++
		}
		c := (matched + p) / p
		// One occurrence of a cycle is not a repetition: report only from
		// the second full repetition on.
		if c >= 2 && c > cycles {
			period, cycles = p, c
		}
	}
	return period, cycles
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

// toolOscillationWarnNotice is appended to a tool result once the turn's
// call history repeats a short cycle oscillationWarnCycles times.
func toolOscillationWarnNotice(toolName string, period, cycles int) string {
	return fmt.Sprintf(
		"\n\n[SYSTEM NOTICE: the last %d tool calls repeat the same %d-call cycle %d times in a row "+
			"(this %q call closes the latest repetition). Each round undoes or repeats the previous one, "+
			"so the vault is churning and nothing is converging. Stop the loop: decide the end state once, "+
			"apply it once, or tell the user you are stuck.]",
		period*cycles, period, cycles, toolName,
	)
}

// toolOscillationBreakerReason is the refusal reason for the call that would
// extend an oscillation to oscillationBreakCycles repetitions.
func toolOscillationBreakerReason(toolName string, period, cycles int) string {
	return fmt.Sprintf(
		"this %q call would be the %dth repetition of the same %d-call cycle this turn — "+
			"the dispatch layer is refusing to extend the loop",
		toolName, cycles, period,
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
