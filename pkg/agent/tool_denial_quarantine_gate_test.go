// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W4's own proof that the ADR-058 quarantine gate wired into runTurn's
// dispatch loop (pkg/agent/loop.go) is REACHABLE and does what
// FR-058-10/11/12/13 require. This is deliberately narrower than the
// canonical registry-backed proofs (BDD-04/AC-03 in
// pkg/gateway/tool_denial_quarantine_test.go, BDD-05/06 in
// pkg/agent/loop_tool_denial_test.go — both W5/W6, Wave 2): spec §2.5's
// import-direction constraint puts real-approval-registry-state assertions
// in pkg/gateway, and the aggregate-budget behavioural test table is W5's.
//
// What this file proves that nothing else in Wave 1 does: the loop.go
// wiring itself. A stub PolicyApprover that counts its own calls is
// sufficient here because the fact under test IS the call count — whether
// the approver is consulted once or three times for three identical calls
// to an already-quarantined tool — not a canned return value standing in
// for something else (the anti-spy rule targets the latter, not a call
// counter used to prove a short-circuit gate executed).
package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// countingDenyApprover is a test-only PolicyApprover that always denies with
// a fixed reason and counts how many times RequestApproval was actually
// invoked. It is NOT a spy standing in for a canned outcome: the call count
// itself is the thing under test (whether the quarantine gate suppressed a
// second/third round-trip), so counting real invocations is the correct
// instrument here.
type countingDenyApprover struct {
	mu     sync.Mutex
	calls  int
	reason string
}

func (a *countingDenyApprover) RequestApproval(_ context.Context, _ PolicyApprovalReq) (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return false, a.reason
}

func (a *countingDenyApprover) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// setAskPolicyForAllAgents mirrors the pattern already used by
// scenario_runturn_test.go / the (now-deleted) FR-084 test: apply an
// explicit tool policy to every registered agent so the test does not
// depend on which agent ProcessDirect happens to route to.
func setAskPolicyForAllAgents(t *testing.T, al *AgentLoop, toolName string, policy config.ToolPolicy) {
	t.Helper()
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		agentInst, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				toolName: policy,
			},
		})
	}
}

// distinctToolCalls builds n parallel tool-call entries for the same tool
// name with distinct IDs — testutil.WithToolCalls requires this shape to
// script "one LLM response, several calls to the same tool" (BDD-05's
// batch shape), which testutil.WithToolCall (singular) cannot express.
func distinctToolCalls(toolName string, n int) []providers.ToolCall {
	calls := make([]providers.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		fc := providers.FunctionCall{Name: toolName, Arguments: "{}"}
		calls = append(calls, providers.ToolCall{
			ID:       toolName + "-" + string(rune('a'+i)),
			Function: &fc,
		})
	}
	return calls
}

// TestRunTurn_QuarantineGate_ShortCircuitsAndTurnContinues drives one LLM
// response containing THREE calls to the same permanently-denied tool
// (reason "timeout") and asserts:
//
//  1. The approver is consulted EXACTLY ONCE, not three times — proving the
//     quarantine gate (FR-058-11) actually short-circuits calls 2 and 3
//     rather than merely existing in source with no reachable path (the
//     grill's B2 finding against the original spec).
//  2. The tool itself never executes (permanent denial, not an approval).
//  3. The turn does NOT abort after the first denial — it CONTINUES to a
//     second LLM round-trip and completes normally. This is the specific
//     regression the task brief calls out: if quarantine and termination
//     coincided, the short-circuit gate would be unreachable and this would
//     be the exact bug the grill found, rebuilt.
//  4. All three dispatched calls receive a permission_denied
//     ToolExecSkipped event (none of the three are silently dropped), and
//     the second/third are attributed to the quarantine cache while the
//     first is attributed to the real ask-denied path.
func TestRunTurn_QuarantineGate_ShortCircuitsAndTurnContinues(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const toolName = "dangerous_tool"

	// Step 1: one LLM response containing THREE parallel calls to the same
	// ask-policy tool. Step 2: a plain-text recovery reply — reached only if
	// the turn genuinely continues past the quarantine rather than aborting.
	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 3)).
		WithText("acknowledged — I will not retry that tool")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &dangerousStubTool{}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"please run dangerous_tool three times",
		"test-session-quarantine-gate",
	)

	// (3) The turn must NOT abort: only 3 denials occurred, far under the
	// aggregate budget of 10, and the quarantine gate must not itself be
	// mistaken for a termination point (the exact B2 regression).
	require.NoError(t, err,
		"the turn must continue past the first permanent denial and complete normally — "+
			"an error here means quarantine was mistaken for termination")
	assert.Equal(t, "acknowledged — I will not retry that tool", finalContent,
		"the turn must reach the SECOND scripted LLM response, proving it did not abort after call 1's denial")
	assert.Equal(t, 2, provider.CallCount(),
		"expected exactly 2 LLM round trips: the 3-call batch, then the recovery text")

	// (1) The core quarantine-gate proof: three identical calls, one
	// approval round-trip. A do-nothing/always-real-approval implementation
	// would report 3 here, not 1.
	assert.Equal(t, 1, approver.callCount(),
		"CRITICAL: the approver was consulted more than once for an already-quarantined tool — "+
			"the quarantine gate is not short-circuiting calls 2 and 3 (FR-058-11)")

	// (2) A permanent denial is never an approval; Execute must never run.
	assert.False(t, stub.wasCalled.Load(), "dangerous_tool.Execute must never run — every call was denied")

	// (4) All three calls produced a skip event; the first is NOT attributed
	// to the quarantine cache, the second and third ARE.
	events := collectEventStream(sub.C)
	var skipReasons []string
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok, "expected ToolExecSkippedPayload, got %T", evt.Payload)
		assert.Equal(t, toolName, payload.Tool)
		skipReasons = append(skipReasons, payload.Reason)
	}
	require.Len(t, skipReasons, 3, "expected exactly 3 tool-exec-skipped events, one per dispatched call")
	assert.NotContains(t, skipReasons[0], "quarantined",
		"the FIRST call must go through the real ask-denied path, not the quarantine cache")
	assert.Contains(t, skipReasons[1], "quarantined",
		"the SECOND call must be served from the quarantine cache")
	assert.Contains(t, skipReasons[2], "quarantined",
		"the THIRD call must be served from the quarantine cache")

	// No error/turn-end-aborted event should be present.
	if _, ok := findEvent(events, EventKindError); ok {
		t.Error("no EventKindError expected — the turn must complete without aborting")
	}
}

// TestRunTurn_ToolDenialBudget_AbortsAtTenNotEleven drives one LLM response
// containing TWELVE parallel calls to the same permanently-denied tool and
// asserts the aggregate per-turn budget (turnDenialBudget = 10,
// FR-058-12/13) fires on exactly the 10th denial response — the 1st being a
// real approval round-trip and the 2nd-10th being quarantine replays — and
// that calls 11 and 12 are never dispatched at all.
//
// The config below is built from Go zero values for every ADR-058-related
// field because there IS no such field any more (FR-084's inverted-sentinel
// config knob was deleted, not repaired — issue #595). This is the direct
// stub-resistance guard AC-05 requires: a test that only passes with an
// explicitly-configured budget would be repeating FR-084's exact defect.
func TestRunTurn_ToolDenialBudget_AbortsAtTenNotEleven(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const toolName = "dangerous_tool"
	const totalScripted = 12

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, totalScripted))
	// Deliberately no further scripted step: if the loop dispatched all 12
	// calls and asked the LLM again, ScenarioProvider would return
	// ErrNoMoreResponses — so a clean abort (not that error) is itself proof
	// the budget fired before a second LLM round-trip was ever attempted.

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
		// No Gateway field of any kind is set here — see doc comment above.
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &dangerousStubTool{}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"please run dangerous_tool twelve times",
		"test-session-denial-budget",
	)

	require.Error(t, err, "the turn must abort once the aggregate denial budget is exhausted")
	assert.Empty(t, finalContent)
	assert.Contains(t, err.Error(), "tool_denial_budget",
		"error must name the tool_denial_budget abort stage")
	assert.Contains(t, err.Error(), toolName, "error must name the offending tool")
	assert.Contains(t, err.Error(), "timeout", "error must name the denial reason")
	assert.Contains(t, err.Error(), "10", "error must name the exhausted budget (10)")

	// Exactly one real approval round trip: call 1 only. Calls 2-10 are
	// quarantine replays; calls 11-12 are never dispatched.
	assert.Equal(t, 1, approver.callCount(),
		"only the FIRST of the 12 identical calls should ever reach the approver")
	assert.False(t, stub.wasCalled.Load(), "dangerous_tool.Execute must never run")

	// Exactly 10 tool-exec-skipped events — not 12. Calls 11 and 12 must
	// never be dispatched once the budget is exhausted mid-batch.
	events := collectEventStream(sub.C)
	skipped := 0
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok, "expected ToolExecSkippedPayload, got %T", evt.Payload)
		assert.Equal(t, toolName, payload.Tool)
		skipped++
	}
	assert.Equal(t, 10, skipped,
		"expected exactly 10 tool-exec-skipped events before the budget aborted the turn — "+
			"11 or 12 would mean the budget did not stop mid-batch dispatch")

	errEvt, ok := findEvent(events, EventKindError)
	require.True(t, ok, "expected an EventKindError to be emitted for the budget-exhaustion abort")
	errPayload, ok := errEvt.Payload.(ErrorPayload)
	require.True(t, ok, "expected ErrorPayload, got %T", errEvt.Payload)
	assert.Equal(t, "tool_denial_budget", errPayload.Stage)
}

// renamingBeforeToolHook renames every incoming tool call from `from` to
// `to` via HookActionModify, and counts its own invocations. Used by
// TestRunTurn_QuarantineGate_KeyMatchesAcrossRenamingHook to reproduce the
// exact shape of a third-party hook that rewrites a tool name — the case
// that exposed the ADR-058 fix-1 key mismatch (the quarantine gate's lookup
// ran on the PRE-hook name while every recordToolDenial call ran on
// whatever HookActionModify had already reassigned toolName to).
type renamingBeforeToolHook struct {
	t        *testing.T
	from, to string
	calls    atomic.Int32
}

func (h *renamingBeforeToolHook) BeforeTool(
	_ context.Context, call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	h.calls.Add(1)
	// The gate must never itself mutate the incoming name before handing it
	// to this hook — if it had, this assertion would fail on call 2/3 even
	// under the fix, since the gate would then be presenting the RENAMED
	// name here instead of the fresh incoming alias each dispatch resolves.
	require.Equal(h.t, h.from, call.Tool,
		"BeforeTool must see the ORIGINAL incoming tool name on every call it IS invoked for")
	next := call.Clone()
	next.Tool = h.to
	return next, HookDecision{Action: HookActionModify}, nil
}

func (h *renamingBeforeToolHook) AfterTool(
	_ context.Context, result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

// TestRunTurn_QuarantineGate_KeyMatchesAcrossRenamingHook is the ADR-058
// fix-1 regression. loop.go's dispatch loop resolves an incoming tool name,
// then (before this fix) looked it up in the quarantine ledger using THAT
// name but recorded denials under whatever hooks.BeforeTool's
// HookActionModify branch had already reassigned the SAME loop variable to.
// A hook that renames a tool consistently (alias -> real name, every call)
// would therefore store its quarantine entry under the RENAMED name while
// every future lookup for the same incoming alias keys on the ORIGINAL
// name — a latent key-shape mismatch with a lifetime of zero test coverage
// until now: no in-tree hook renames a tool today, so nothing had ever
// exercised it. The fix captures the pre-hook name once (ledgerToolName)
// and uses it consistently for both the gate's lookup and every
// recordToolDenial/recordQuarantineReplay call for that dispatch.
//
// Stub-resistance: reverting the three recordToolDenial call sites' first
// argument from ledgerToolName back to the (post-hook) loop variable
// toolName — the exact shape of the original bug — would make this test's
// core assertions fail: the approver would be consulted THREE times (once
// per call, since the gate would MISS every time) instead of once, and the
// hook itself would be invoked three times instead of once (FR-058-11's
// gate-runs-before-BeforeTool guarantee would be defeated on calls 2 and 3
// precisely because the gate's lookup never finds what call 1 stored).
func TestRunTurn_QuarantineGate_KeyMatchesAcrossRenamingHook(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const incomingToolName = "dangerous_tool_alias"
	const renamedToolName = "dangerous_tool"

	// Three parallel calls to the SAME alias in one LLM response — the hook
	// renames every one of them identically, before any policy/approval
	// logic ever sees the alias.
	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(incomingToolName, 3)).
		WithText("acknowledged — I will not retry that tool")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	// Only the RENAMED name needs a stub tool / explicit policy: the hook
	// rewrites the alias to this name before resolveToolPolicyAtExec or
	// CheckGrantOrRequestApproval ever run, so the alias itself never needs
	// to be registered.
	stub := &dangerousStubTool{}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, renamedToolName, config.ToolPolicyAsk)

	hook := &renamingBeforeToolHook{t: t, from: incomingToolName, to: renamedToolName}
	require.NoError(t, al.MountHook(NamedHook("rename-tool", hook)))

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"please run the tool three times",
		"test-session-quarantine-rename",
	)

	require.NoError(t, err,
		"the turn must continue past the first permanent denial and complete normally")
	assert.Equal(t, "acknowledged — I will not retry that tool", finalContent,
		"the turn must reach the SECOND scripted LLM response, proving it did not abort")

	// The core fix-1 proof, from TWO independent observation points that
	// must agree: the approver is consulted exactly once (not three times),
	// and the renaming hook itself is invoked exactly once (not three
	// times) — the gate must short-circuit calls 2 and 3 BEFORE the hook
	// ever runs for them, which is only possible if the gate's lookup key
	// for calls 2/3 matches what call 1 recorded.
	assert.Equal(t, int32(1), hook.calls.Load(),
		"CRITICAL: hooks.BeforeTool must not be invoked for calls 2/3 — a quarantine-key "+
			"mismatch across the rename would make the gate miss every time and let the "+
			"hook (and a full approval round-trip) re-fire on every call")
	assert.Equal(t, 1, approver.callCount(),
		"CRITICAL: the approver was consulted more than once for an already-quarantined "+
			"(renamed) tool — the quarantine gate is not short-circuiting calls 2 and 3")
	assert.False(t, stub.wasCalled.Load(), "dangerous_tool.Execute must never run — every call was denied")
}

// countingBeforeToolHook counts its own BeforeTool invocations and always
// permits the call to continue unchanged. Used to observe hook-invocation
// counts directly, independent of the approver's own call count — see
// TestRunTurn_QuarantineGate_SkipsBeforeToolHookOnReplay's doc for why this
// second, independent observation point is required.
type countingBeforeToolHook struct {
	calls atomic.Int32
}

func (h *countingBeforeToolHook) BeforeTool(
	_ context.Context, call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	h.calls.Add(1)
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (h *countingBeforeToolHook) AfterTool(
	_ context.Context, result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

// TestRunTurn_QuarantineGate_SkipsBeforeToolHookOnReplay closes a gap the
// 7th-reviewer spec-conformance pass found: FR-058-11 requires the
// quarantine gate run BEFORE hooks.BeforeTool, the TOCTOU re-check, and
// CheckGrantOrRequestApproval — placement in loop.go is correct, but
// TestRunTurn_QuarantineGate_ShortCircuitsAndTurnContinues (above) only
// observes ordering through the APPROVER's call count. A refactor that
// moved the gate BELOW hooks.BeforeTool — reintroducing a hook round-trip
// (and, for a stateful hook, real side effects) for every already-
// quarantined call — would pass every other test in this epic unchanged,
// because none of them counts BeforeTool invocations directly. This test
// mounts a hook that counts only its OWN invocations and asserts a
// quarantined replay never reaches it.
//
// Stub-resistance: an implementation that moved the quarantine gate to run
// AFTER hooks.BeforeTool (functionally unchanged for the approver-call-count
// assertion in the sibling test, since BeforeTool here is a no-op Continue)
// would show hook.calls.Load() == 3 here, not 1 — this is the one assertion
// in this file that specifically catches that regression shape.
func TestRunTurn_QuarantineGate_SkipsBeforeToolHookOnReplay(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const toolName = "dangerous_tool"

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 3)).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &dangerousStubTool{}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	hook := &countingBeforeToolHook{}
	require.NoError(t, al.MountHook(NamedHook("count-before-tool", hook)))

	finalContent, err := al.ProcessDirect(
		context.Background(),
		"please run dangerous_tool three times",
		"test-session-quarantine-skips-beforetool",
	)
	require.NoError(t, err)
	assert.Equal(t, "done", finalContent)

	// The core Additional-Fix-B proof: BeforeTool was invoked exactly ONCE
	// — for the first, real denial — never for calls 2/3. A gate that ran
	// AFTER hooks.BeforeTool (the exact regression this guards, invisible to
	// an approver-call-count-only assertion) would show 3 here.
	assert.Equal(t, int32(1), hook.calls.Load(),
		"CRITICAL: hooks.BeforeTool must not be invoked for an already-quarantined "+
			"call — the quarantine gate must run BEFORE it (FR-058-11)")
	assert.Equal(t, 1, approver.callCount())
	assert.False(t, stub.wasCalled.Load())
}
