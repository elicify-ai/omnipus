// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tests for abortTurn's system-initiated (case 2) path — the fix for the
// ORIGINAL motivating bug of the abortTurn 3-wave effort (commit 499b569f).
//
// abortTurn differentiates two cases by the reason string it is called with:
//
//   - Case 1 (reason == hardInterruptAbortReason): user-initiated hard
//     interrupt — clean, nil-error abort. Covered by
//     TestAgentLoop_InterruptHard_RestoresSession (steering_test.go), which
//     pins the nil-error behavior.
//   - Case 2 (any other reason — reached via HookActionHardAbort decisions
//     and, as of ADR-058, the aggregate tool-denial budget): system-initiated
//     abort — BEFORE 499b569f this silently returned turnResult{Aborted}
//     with a NIL error, so the user saw absolutely nothing. 499b569f made
//     abortTurn synthesize a real, non-nil error, emit an EventKindError, and
//     append it to the transcript. Until this file, that branch had ZERO test
//     coverage for its HookActionHardAbort production call-site family —
//     confirmed by grep: the symbol appeared in no _test.go file in this
//     package.
//
// Traces to: pkg/agent/loop.go::AgentLoop.abortTurn (+ its doc comment).
// loop.go/turn.go line numbers churn fast (CLAUDE.md) — cite by symbol, not
// by line range.
//
// ADR-058 note: this file used to also cover the mid-turn TOCTOU policy-deny
// branch's system-initiated abort via FR-084's per-turn counter-and-floor
// mechanism (config field default 2 in that test). ADR-058 deleted that
// mechanism outright (issue #595) rather than repairing its inverted
// sentinel: the one call site left standing after ADR-058's rewiring already
// returns unconditionally on every path, so the counter could never reach a
// floor greater than one. Under the ADR-058 replacement (quarantine-at-first,
// aggregate per-turn denial budget), the first denial to that TOCTOU branch
// now quarantines the tool and the turn CONTINUES rather than aborting — the
// old test's premise (a 2nd denial aborting the turn) no longer holds, so it
// was deleted with the mechanism rather than adapted. Replacement coverage
// for the aggregate-budget abort path lives in
// pkg/agent/loop_tool_denial_test.go and
// pkg/agent/task_executor_tool_denial_test.go.

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hardAbortBeforeLLMHook always returns HookActionHardAbort from BeforeLLM
// with a deliberately non-sentinel Reason, driving abortTurn's case-2
// (system-initiated) branch — the branch that must synthesize a real,
// non-nil, surfaced error rather than the pre-499b569f silent empty return.
type hardAbortBeforeLLMHook struct{}

func (h *hardAbortBeforeLLMHook) BeforeLLM(
	_ context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	return req, HookDecision{
		Action: HookActionHardAbort,
		Reason: "policy violation: disallowed content detected",
	}, nil
}

func (h *hardAbortBeforeLLMHook) AfterLLM(
	_ context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	return resp, HookDecision{Action: HookActionContinue}, nil
}

// TestAgentLoop_AbortTurn_HookHardAbort_SurfacesSystemInitiatedError drives
// one of the four HookActionHardAbort call sites in
// pkg/agent/loop.go::AgentLoop.runTurn (the BeforeLLM decision point — the
// other three are the AfterLLM, before_tool, and after_tool HookActionHardAbort
// cases in the same function) and asserts abortTurn takes case 2: a real,
// non-nil error is returned AND surfaced via an EventKindError event, and the
// turn ends with status Aborted.
//
// BDD: Given a mounted BeforeLLM hook that returns HookActionHardAbort with a
//
//	non-sentinel reason,
//
// When the turn loop reaches its first LLM call,
// Then abortTurn must NOT silently return a nil error (the original bug —
//
//	a test that only checked err == nil would have PASSED against it),
//
// And an EventKindError carrying the hook's stage and reason must be
//
//	emitted,
//
// And the turn must end with TurnEndStatusAborted.
//
// Traces to: pkg/agent/loop.go::AgentLoop.abortTurn, ::AgentLoop.runTurn's
// BeforeLLM HookActionHardAbort call site. loop.go/turn.go line numbers
// churn fast (CLAUDE.md) — cite by symbol, not by line range.
func TestAgentLoop_AbortTurn_HookHardAbort_SurfacesSystemInitiatedError(t *testing.T) {
	provider := &llmHookTestProvider{}
	al, agent, cleanup := newHookTestLoop(t, provider)
	defer cleanup()

	require.NoError(t, al.MountHook(NamedHook("hard-abort-before-llm", &hardAbortBeforeLLMHook{})),
		"MountHook must succeed for a hook implementing LLMInterceptor")

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	resp, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:      "session-hard-abort-hook",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})

	// This is the exact regression the whole abortTurn effort started from:
	// before 499b569f this case returned a NIL error, so the caller (and the
	// user) never learned the turn failed. Assert the opposite, on purpose.
	require.Error(
		t,
		err,
		"a system-initiated hard abort (HookActionHardAbort with a non-sentinel reason) must surface a real, non-nil error — nil here is the ORIGINAL bug this effort fixed",
	)
	assert.Empty(t, resp, "no final response content on a system-initiated abort")
	assert.Contains(t, err.Error(), "before_llm",
		"error must name the stage the abort occurred at, not a generic message")
	assert.Contains(t, err.Error(), "policy violation: disallowed content detected",
		"error must surface the hook's own reason verbatim, not a canned message")

	events := collectEventStream(sub.C)

	errEvt, ok := findEvent(events, EventKindError)
	require.True(
		t,
		ok,
		"expected an EventKindError to be emitted for the system-initiated abort — this is the concrete 'surfaced' signal a user-facing channel renders",
	)
	errPayload, ok := errEvt.Payload.(ErrorPayload)
	require.True(t, ok, "expected ErrorPayload, got %T", errEvt.Payload)
	assert.Equal(t, "before_llm", errPayload.Stage)
	assert.Contains(t, errPayload.Message, "policy violation: disallowed content detected")

	turnEndEvt, ok := findEvent(events, EventKindTurnEnd)
	require.True(t, ok, "expected a turn end event")
	turnEndPayload, ok := turnEndEvt.Payload.(TurnEndPayload)
	require.True(t, ok, "expected TurnEndPayload, got %T", turnEndEvt.Payload)
	assert.Equal(t, TurnEndStatusAborted, turnEndPayload.Status)
}
