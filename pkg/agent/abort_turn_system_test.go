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
//     and recordSyntheticDeny's abort threshold): system-initiated abort —
//     BEFORE 499b569f this silently returned turnResult{Aborted} with a NIL
//     error, so the user saw absolutely nothing. 499b569f made abortTurn
//     synthesize a real, non-nil error, emit an EventKindError, and append it
//     to the transcript. Until this file, that branch had ZERO test coverage
//     for either of its two production call-site families
//     (HookActionHardAbort, recordSyntheticDeny) — confirmed by grep: neither
//     symbol appeared in any _test.go file in this package.
//
// Traces to: pkg/agent/loop.go:7355-7434 (abortTurn + its doc comment).

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// one of the four HookActionHardAbort call sites (pkg/agent/loop.go:5637-5640,
// the BeforeLLM decision point) and asserts abortTurn takes case 2: a real,
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
// Traces to: pkg/agent/loop.go:7355-7434 (abortTurn), :5614-5641 (BeforeLLM
// HookActionHardAbort call site).
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

// TestRunTurn_SyntheticDenyFloor_AbortsWithSurfacedError drives one of the
// four recordSyntheticDeny call sites (pkg/agent/loop.go:6663-6665, the
// mid-turn TOCTOU policy-deny branch) by scripting the LLM to repeatedly call
// a tool the agent's policy denies. With TurnSyntheticErrorFloor=2, the 2nd
// consecutive denial trips FR-084's abort threshold, driving abortTurn's
// case-2 (system-initiated) branch exactly like the hook path above but via
// a completely independent trigger.
//
// BDD: Given an agent whose tool policy denies "dangerous_tool" and a
//
//	synthetic-error floor of 2,
//
// When the LLM calls "dangerous_tool" twice in a row (each denied by the
//
//	mid-turn TOCTOU policy re-check),
//
// Then recordSyntheticDeny reports shouldAbort=true on the 2nd denial,
// And abortTurn synthesizes a real, non-nil error referencing the
//
//	synthetic_error_floor stage,
//
// And the abort is attributed in the audit log to
//
//	audit.EventTurnAbortedSyntheticLoop with the actual denial count.
//
// Traces to: pkg/agent/loop.go:7452-7500 (recordSyntheticDeny), :6637-6667
// (TOCTOU mid-turn-policy-change call site).
func TestRunTurn_SyntheticDenyFloor_AbortsWithSurfacedError(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Two consecutive tool-call steps for the SAME denied tool. If the loop
	// requested a 3rd LLM turn, ScenarioProvider would return
	// ErrNoMoreResponses — so CallCount()==2 below is a hard proof the abort
	// fired exactly at the floor, not before and not after.
	provider := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithToolCall("dangerous_tool", `{}`)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			// Enable audit logging so recordSyntheticDeny's
			// audit.EventTurnAbortedSyntheticLoop entry is written.
			AuditLog: true,
		},
		Gateway: config.GatewayConfig{
			TurnSyntheticErrorFloor: 2,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &dangerousStubTool{}
	al.RegisterTool(stub)
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		agentInst, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"dangerous_tool": "deny",
			},
		})
	}

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"please run dangerous_tool for me",
		"test-session-synthetic-deny",
	)

	// Non-vacuous: if abortTurn's case 2 regressed to a nil error (the
	// original bug this whole effort fixed), this would incorrectly report
	// success instead of the real synthetic-error-floor abort.
	require.Error(t, err,
		"reaching the synthetic-error floor must surface a real, non-nil error — not silently succeed")
	assert.Empty(t, finalContent)
	assert.Contains(t, err.Error(), "synthetic_error_floor",
		"error must name the synthetic_error_floor stage")
	assert.Contains(t, err.Error(), "synthetic_error_loop",
		"error must surface recordSyntheticDeny's own abort reason")
	assert.Contains(t, err.Error(), `"count":2`,
		"error must carry the actual denial count, not a hardcoded/generic message")

	// Differentiation: exactly 2 LLM round trips happened — not 1 (would mean
	// an immediate bail unrelated to the floor) and not 3+ (would mean the
	// floor was never enforced and the scenario ran dry).
	assert.Equal(t, 2, provider.CallCount(),
		"expected exactly 2 LLM round trips before the floor aborted the turn")
	assert.False(t, stub.wasCalled.Load(), "dangerous_tool.Execute must never run — policy denies it")

	events := collectEventStream(sub.C)

	skipped := 0
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok, "expected ToolExecSkippedPayload, got %T", evt.Payload)
		assert.Equal(t, "dangerous_tool", payload.Tool)
		skipped++
	}
	assert.Equal(t, 2, skipped, "expected exactly 2 tool-exec-skipped events, one per denied call")

	errEvt, ok := findEvent(events, EventKindError)
	require.True(t, ok, "expected an EventKindError to be emitted for the synthetic-deny-floor abort")
	errPayload, ok := errEvt.Payload.(ErrorPayload)
	require.True(t, ok, "expected ErrorPayload, got %T", errEvt.Payload)
	assert.Equal(t, "synthetic_error_floor", errPayload.Stage)
	assert.Contains(t, errPayload.Message, "synthetic_error_loop")

	turnEndEvt, ok := findEvent(events, EventKindTurnEnd)
	require.True(t, ok, "expected a turn end event")
	turnEndPayload, ok := turnEndEvt.Payload.(TurnEndPayload)
	require.True(t, ok, "expected TurnEndPayload, got %T", turnEndEvt.Payload)
	assert.Equal(t, TurnEndStatusAborted, turnEndPayload.Status)

	// Audit trail: confirm the abort is attributed to the synthetic-error
	// floor specifically, with the real denial count — not a generic entry.
	auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")
	require.FileExists(t, auditPath, "audit.jsonl must exist — AuditLog=true")
	entries, readErr := readAuditEntries(auditPath)
	require.NoError(t, readErr, "audit.jsonl must be readable and contain valid JSONL")

	var floorEntry map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventTurnAbortedSyntheticLoop {
			floorEntry = e
			break
		}
	}
	require.NotNil(
		t,
		floorEntry,
		"audit log must contain a %q entry; entries: %v",
		audit.EventTurnAbortedSyntheticLoop,
		entries,
	)
	assert.Equal(t, audit.DecisionDeny, floorEntry["decision"])
	details, ok := floorEntry["details"].(map[string]any)
	require.True(t, ok, "expected a details map on the synthetic-loop audit entry")
	assert.Equal(t, float64(2), details["synthetic_error_count"],
		"audit entry must carry the actual denial count that tripped the floor")
}
