// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-058 (tool-denial semantics) — W5 behavioural coverage, Wave 2.
//
// This file drives REAL turns end to end (mustNewAgentLoop + a scripted
// provider + a state-driven approver) to prove the loop.go wiring landed by
// W4 actually produces the payloads/behaviour W1's classifier and ledger
// promise, in the places W1's own unit tests and W4's own reachability
// tests do not already reach:
//
//   - BDD-01 (spec §6): the model-facing JSON a real ask-denied "timeout"
//     call persists into session history is honest — never claims a user
//     denied anything nobody answered.
//   - BDD-06 (spec §6): the aggregate budget bounds a HETEROGENEOUS storm
//     across three distinct tools, not just the homogeneous single-tool
//     storm W4's own tool_denial_quarantine_gate_test.go already proves.
//   - A loop-level (not ledger-level) proof of Binding Rule 4's positive
//     lower bound: a "saturated" denial is never quarantined, so a real
//     turn reaching the approver for it does so on EVERY call, not once.
//     W1's TestBindingRule4_SaturatedDoesNotQuarantineAndPermitsRetry
//     proves this at the bare-ledger level; this is the loop-wiring
//     equivalent. The full "and then it actually executes" proof (AC-06 /
//     BDD-07) needs a real approvalRegistryV2 and is pkg/gateway's
//     (spec §2.5's import-direction constraint: pkg/agent cannot construct
//     the real PolicyApprover that proof needs).
//   - BDD-09 (spec §6): FR-084 is gone BEHAVIOURALLY, not just by grep — the
//     new audit event fires, the old one's slug never appears in a session
//     message, and the six retired identifiers resolve nowhere in pkg/.
//
// NOT duplicated here (see the task brief's "coverage that already exists"):
//   - BDD-02 (the classification table, originally ten rows and now twelve
//     — see tool_denial_test.go's denialTableFixture doc) — pkg/agent/tool_denial_test.go (W1).
//   - BDD-04 (quarantine-gate reachability, turn continues) and the
//     HOMOGENEOUS half of BDD-05 (12 calls to ONE tool, abort at the 10th,
//     not the 11th/12th) — pkg/agent/tool_denial_quarantine_gate_test.go (W4).
//   - BDD-10 (per-turn/per-session counter isolation) —
//     pkg/agent/tool_denial_test.go's TestTurnDenialLedger_PerTurnIsolation
//     and TestTurnDenialLedger_FreshTurnStateIsEmpty (W1). newTurnState gives
//     every turn its own turnDenialLedger value with no shared backing
//     store; a loop-level re-proof of the same fact would be a near-
//     duplicate of an already-adequate ledger-level test, not new coverage.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// namedStubTool is a test-only tools.Tool that records whether Execute was
// called, with a CONFIGURABLE Name() — unlike dangerousStubTool
// (scenario_runturn_test.go), whose name is hardcoded to "dangerous_tool".
// BDD-06/BDD-08 need distinct, literal, production-recognizable tool names
// ("bash", "run_task", "web_fetch") as fixture values (spec §8.1: "Distinct
// fixture values everywhere ... Never 'a'/'a'") rather than one synthetic
// name reused three times.
//
// Registering an instance under the name "bash" deliberately OVERRIDES
// whatever real ExecTool wireExecToolDeps already auto-registered under that
// name for every agent (tools.ToolRegistry.Register permits a same-name
// replacement, logged at WARN as an "expected" collision) — every call in
// this file is denied before Execute is ever reached, so no real shell
// command can run even if a bug in the test slipped a call through; the
// override makes that impossible by construction rather than relying on the
// denial alone.
type namedStubTool struct {
	tools.BaseTool
	name      string
	wasCalled atomic.Bool
}

func (d *namedStubTool) Name() string { return d.name }
func (d *namedStubTool) Description() string {
	return "ADR-058 test stub for " + d.name + " — must never execute"
}
func (d *namedStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *namedStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (d *namedStubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	d.wasCalled.Store(true)
	return &tools.ToolResult{ForLLM: "executed — this should never happen", IsError: false}
}

// setAskPolicyForToolsAllAgents sets ask-policy for SEVERAL tool names at
// once, for every registered agent, in a single StoreToolPolicy call per
// agent. This is NOT the same as calling setAskPolicyForAllAgents (a single
// tool name) once per tool in a loop — StoreToolPolicy REPLACES the agent's
// entire ToolPolicyCfg.Policies map on every call rather than merging into
// it, so three sequential single-tool calls would leave only the LAST
// tool's policy actually set (the first two calls' entries get overwritten
// by the next call's fresh single-entry map) and the other two tools with
// no explicit policy entry at all — which, per this project's no-default-
// policy-fallback rule (CLAUDE.md Constraint #6), fails closed to "deny",
// not "ask". BDD-06 needs all three fixture tools genuinely on "ask"
// simultaneously, so this builds ONE map covering every name up front.
func setAskPolicyForToolsAllAgents(t *testing.T, al *AgentLoop, toolNames []string, policy config.ToolPolicy) {
	t.Helper()
	policies := make(map[string]config.ToolPolicy, len(toolNames))
	for _, name := range toolNames {
		policies[name] = policy
	}
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		agentInst, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: policies})
	}
}

// roundRobinToolCalls builds n parallel tool-call entries cycling through
// names in order (names[0], names[1], ..., names[len-1], names[0], ...) —
// the shape BDD-06 needs to express "one LLM response, several calls
// spread across several distinct tools" in a single batch, which
// tool_denial_quarantine_gate_test.go's distinctToolCalls (single tool
// repeated) cannot express.
func roundRobinToolCalls(names []string, n int) []providers.ToolCall {
	calls := make([]providers.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		name := names[i%len(names)]
		fc := providers.FunctionCall{Name: name, Arguments: "{}"}
		calls = append(calls, providers.ToolCall{
			ID:       fmt.Sprintf("%s-call-%d", name, i),
			Function: &fc,
		})
	}
	return calls
}

// baseLoopDenialTestConfig is the shared cfg/harness shape every test below
// uses — a single, explicitly-registered agent ("mia" — there is no "main"
// sentinel to fall back to anymore) AgentLoop rooted at a private temp
// workspace, with audit logging enabled so the BDD-09 test can read it back.
func baseLoopDenialTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
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
	return cfg, tmpHome
}

// ---------------------------------------------------------------------------
// BDD-01 — timeout no longer claims a user decided (FR-058-05/06 · AC-01/AC-02)
// ---------------------------------------------------------------------------

// TestRunTurn_TimeoutDenial_PersistedPayloadNeverClaimsUserDenied drives a
// REAL ask-denied "timeout" call end to end and inspects the persisted
// session history — not denialPayloadJSON directly (that unit-level proof is
// W1's TestDenialPayloadJSON_ShapeAndFields) — to prove the real dispatch
// loop actually routes the timeout denial through ClassifyDenial/
// denialPayloadJSON rather than the pre-ADR-058 literal
// "User denied tool execution.".
//
// Stub-resistance: an implementation that reverted to the old hardcoded
// literal, or that emitted permanent=false for "timeout", or that emitted a
// message with no actionable next step, would each fail one of the four
// assertions below. A "no error" or "some JSON was written" check alone
// would catch none of them.
func TestRunTurn_TimeoutDenial_PersistedPayloadNeverClaimsUserDenied(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	const toolName = "run_task" // BDD-01's own literal fixture tool name

	// Step 1: one call to the ask-policy tool, denied with reason "timeout".
	// Step 2: recovery text — reached only if the single denial (well under
	// the budget of 10) does not itself abort the turn.
	provider := testutil.NewScenario().
		WithToolCall(toolName, `{}`).
		WithText("understood — I will not retry that tool")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &namedStubTool{name: toolName}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	finalContent, err := al.ProcessDirect(context.Background(), "please run_task for me", "test-session-timeout-honest")
	require.NoError(t, err, "a single denial, far under the aggregate budget of 10, must not abort the turn")
	assert.Equal(t, "understood — I will not retry that tool", finalContent,
		"turn must reach the second scripted response, proving it continued past the denial")
	assert.False(t, stub.wasCalled.Load(), "the tool must never execute — it was denied, not approved")
	assert.Equal(t, 1, approver.callCount())

	// Locate the persisted role="tool" permission_denied message. Same
	// session-key derivation as scenario_runturn_test.go's precedent:
	// ProcessDirect's channel="cli"/chatID="direct" resolves to DMScopeMain,
	// producing session key "agent:mia:main" via BuildAgentMainSessionKey
	// ("mia" is the only agent baseLoopDenialTestConfig registers).
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	const resolvedSessionKey = "agent:mia:main"
	history := defaultAgent.Sessions.GetHistory(resolvedSessionKey)
	require.NotEmpty(t, history)

	var denyContent string
	for _, msg := range history {
		if msg.Role == "tool" && strings.Contains(msg.Content, "permission_denied") {
			denyContent = msg.Content
			break
		}
	}
	require.NotEmpty(t, denyContent, "session history must contain the persisted permission_denied tool message")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(denyContent), &decoded), "persisted payload must be valid JSON")

	// (1) reason is honestly "timeout", not omitted or overwritten.
	assert.Equal(t, "timeout", decoded["reason"])
	// (2) permanent is true for timeout (ADR-058 D1 row 2 / spec §4.1 row 2).
	assert.Equal(t, true, decoded["permanent"])

	msg, _ := decoded["message"].(string)
	require.NotEmpty(t, msg)
	lower := strings.ToLower(msg)
	// (3) BDD-01's core negative assertion — FR-058-06's whole point.
	assert.NotContains(t, lower, "user denied",
		"CRITICAL: a timeout (nobody answered) must never be rendered as a user decision")
	// (4) BDD-01's positive assertion — the message names the productive next
	// step (ADR D2/D3), not just "denied".
	assert.Contains(t, lower, "stop and report the blocker",
		"message must tell the agent what to do instead of retrying")
}

// ---------------------------------------------------------------------------
// BDD-06 — heterogeneous storms are bounded too (FR-058-12 · AC-05)
// ---------------------------------------------------------------------------

// TestRunTurn_HeterogeneousDenialStorm_AbortsAtAggregateTenAcrossThreeTools
// is the full-turn counterpart to W1's ledger-level
// TestRecordToolDenial_BudgetFiresAt10NotAt9 (which proves the arithmetic
// with ten SYNTHETIC tool names against a bare turnState) and W4's
// TestRunTurn_ToolDenialBudget_AbortsAtTenNotEleven (which proves the
// aggregate budget through a real turn but with ONE tool repeated twelve
// times). Neither exercises the real dispatch loop with several DISTINCT
// real tool names sharing one aggregate counter — the exact gap ADR-058
// §3.4/§10.A2 was written to close after the UAT observed agents "cycling
// through 2-3 distinct denied tools each".
//
// Batch: 12 calls round-robin across "bash", "run_task", "web_fetch" (BDD-06's
// own literal fixture tools). Calls 1-3 are each tool's FIRST occurrence (one
// real approval round trip per tool, then quarantined). Calls 4-10 are
// quarantine replays (aggregate budget climbs from 4 to 10). The budget
// exhausts exactly on call 10 (bash's fourth appearance, itself a replay);
// calls 11 ("run_task") and 12 ("web_fetch") are never dispatched.
//
// Stub-resistance: a per-(tool,reason) implementation (the mechanism this
// ADR explicitly rejected, spec §3.4/X4) would never exhaust here at all —
// each tool is denied at most 4 times, nowhere near a per-pair ceiling of
// 10 — so this test would hang waiting for a second LLM call the scripted
// provider cannot supply, surfacing as a hard failure rather than a false
// pass. Asserting the approver was consulted exactly 3 times (not 10, not
// 12) additionally excludes an implementation that quarantines nothing and
// just happens to abort on the 10th real call by coincidence.
func TestRunTurn_HeterogeneousDenialStorm_AbortsAtAggregateTenAcrossThreeTools(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	toolNames := []string{"bash", "run_task", "web_fetch"}

	const totalScripted = 12
	provider := testutil.NewScenario().
		WithToolCalls(roundRobinToolCalls(toolNames, totalScripted))
	// Deliberately no further scripted step, mirroring
	// tool_denial_quarantine_gate_test.go's homogeneous-storm test: if the
	// budget failed to fire before a second LLM round trip, ScenarioProvider
	// would return ErrNoMoreResponses instead of a clean abort.

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stubs := make(map[string]*namedStubTool, len(toolNames))
	for _, name := range toolNames {
		stub := &namedStubTool{name: name}
		stubs[name] = stub
		al.RegisterTool(stub)
	}
	setAskPolicyForToolsAllAgents(t, al, toolNames, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(context.Background(), "cycle through three denied tools", "test-session-heterogeneous-storm")

	require.Error(t, err, "the turn must abort once the aggregate budget is exhausted, even across distinct tools")
	assert.Empty(t, finalContent)
	assert.Contains(t, err.Error(), "tool_denial_budget")
	assert.Contains(t, err.Error(), "bash", "the abort reason must name the offending call's tool (bash's 4th occurrence)")
	assert.Contains(t, err.Error(), "timeout")
	assert.Contains(t, err.Error(), "10", "must name the exhausted budget")

	// The core BDD-06 proof: only ONE real approval round trip PER TOOL
	// (three total) — not ten, not one-per-call. A per-(tool,reason) budget
	// would never have aborted at all; an implementation ignoring quarantine
	// entirely would show 10 approver calls here instead of 3.
	assert.Equal(t, 3, approver.callCount(),
		"expected exactly 3 real approval round trips — one per distinct tool — not 10 (would mean no "+
			"quarantine) and not more than 3 (would mean quarantine keyed wrong)")

	for _, name := range toolNames {
		assert.False(t, stubs[name].wasCalled.Load(), "%s.Execute must never run — every call was denied", name)
	}

	events := collectEventStream(sub.C)
	skipped := 0
	quarantinedReplays := 0
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok, "expected ToolExecSkippedPayload, got %T", evt.Payload)
		skipped++
		if strings.Contains(payload.Reason, "quarantined") {
			quarantinedReplays++
		}
	}
	// Exactly 10 skip events (not 12): dispatch stops mid-batch the instant
	// the budget exhausts, matching BDD-06's "aborts on the 10th denial".
	assert.Equal(t, 10, skipped, "expected exactly 10 tool-exec-skipped events across the 3 tools before abort")
	// 3 real denials (one per tool, first occurrence) + 7 quarantine
	// replays = 10. This is the positive proof that quarantine (not just the
	// budget) fired for a heterogeneous mix, not only for the homogeneous
	// case W4's own test already covers.
	assert.Equal(t, 7, quarantinedReplays, "expected exactly 7 of the 10 skips to be quarantine-cache replays")
}

// ---------------------------------------------------------------------------
// Binding Rule 4 positive lower bound, at the LOOP level (spec §3.5 / D1 #3)
// ---------------------------------------------------------------------------

// TestRunTurn_SaturatedDenial_NeverQuarantines_ApproverConsultedEveryTime
// closes the gap between W1's ledger-level
// TestBindingRule4_SaturatedDoesNotQuarantineAndPermitsRetry (a bare
// turnState, no real dispatch loop) and BDD-07/AC-06's full
// registry-backed "and then it actually executes" proof (pkg/gateway,
// needed because spec §2.5's import-direction constraint keeps a real
// approvalRegistryV2 out of pkg/agent). This is the missing middle rung: it
// proves the REAL runTurn dispatch loop's quarantine gate does not engage
// for a retryable reason, by showing the approver is consulted on EVERY one
// of three identical calls to the same tool — not once (which is exactly
// what the permanent-denial tests above and in tool_denial_quarantine_gate_test.go
// show for "timeout"/permanent reasons).
//
// Required per this wave's task brief: "whatever you assert about
// blocking/quarantine, include a case proving the RETRYABLE path (saturated)
// still works — otherwise a 'block everything' implementation passes." Every
// other test in this file (and in tool_denial_quarantine_gate_test.go)
// asserts blocking/quarantine behaviour; this is that required positive
// counterweight at the loop-wiring layer.
//
// Stub-resistance: an implementation that quarantines on ANY permanent-or-not
// denial (the exact defect ADR-058 §10.A1/A5 exists to prevent) would show
// approver.callCount() == 1 here, identical to the permanent-denial tests —
// this test is what tells the two apart.
func TestRunTurn_SaturatedDenial_NeverQuarantines_ApproverConsultedEveryTime(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	const toolName = "web_fetch" // ADR-058 D1 #3's own illustrative saturated-path tool

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 3)).
		WithText("queue drained, moving on")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &namedStubTool{name: toolName}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	// Always denies with reason "saturated" — the one RETRYABLE reason
	// (ADR-058 D1 #3). Reusing countingDenyApprover with a different reason
	// value than the permanent-denial tests above is deliberate: the type
	// itself does not encode permanence, ClassifyDenial does.
	approver := &countingDenyApprover{reason: "saturated"}
	al.SetToolApprover(approver)

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(context.Background(), "please fetch three times", "test-session-saturated-no-quarantine")

	require.NoError(t, err, "3 saturated denials, all retryable and far under budget=10, must not abort the turn")
	assert.Equal(t, "queue drained, moving on", finalContent)

	// The core proof: THREE real approval round trips for the SAME tool —
	// not one. Contrast with TestRunTurn_QuarantineGate_ShortCircuitsAndTurnContinues
	// (tool_denial_quarantine_gate_test.go), whose identical 3-call shape with
	// reason="timeout" (permanent) yields callCount()==1.
	assert.Equal(t, 3, approver.callCount(),
		"CRITICAL: a saturated denial must reach the approver on EVERY call — quarantine must never engage "+
			"for the one retryable reason (Binding Rule 4 / AC-06)")
	assert.False(t, stub.wasCalled.Load(), "web_fetch is denied on all 3 scripted attempts, so it must never execute")

	events := collectEventStream(sub.C)
	skipReasons := 0
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok, "expected ToolExecSkippedPayload, got %T", evt.Payload)
		assert.Equal(t, toolName, payload.Tool)
		assert.NotContains(t, payload.Reason, "quarantined",
			"a saturated denial must never be attributed to the quarantine cache")
		skipReasons++
	}
	assert.Equal(t, 3, skipReasons, "expected exactly 3 tool-exec-skipped events, none of them quarantine replays")
}

// ---------------------------------------------------------------------------
// Post-epic-review fix: policy_denied must NOT quarantine (FR-079 conflict)
// ---------------------------------------------------------------------------

// policyFlipBetweenCallsHook denies the FIRST call to toolName by flipping
// its LIVE policy to "deny" inside BeforeTool (read a few lines later, in
// the SAME dispatch-loop iteration, by the TOCTOU re-check at
// resolveToolPolicyAtExec) and flips it back to "allow" before the SECOND
// call's own TOCTOU check. Both calls share ONE filterTimePolicyMap
// snapshot — computed once per LLM iteration, BEFORE either BeforeTool call
// runs — fixed at "allow" for the whole batch. That fixed snapshot is the
// precondition for resolveToolPolicyAtExec to classify call 1's live "deny"
// as "mid_turn_policy_change" (a real TOCTOU flip) rather than "tool never
// offered at all" (wasInFilterMap == false, which auto-denies unconditionally
// regardless of what BeforeTool does).
type policyFlipBetweenCallsHook struct {
	al       *AgentLoop
	toolName string
	calls    atomic.Int32
}

func (h *policyFlipBetweenCallsHook) BeforeTool(
	_ context.Context, call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	n := h.calls.Add(1)
	policy := config.ToolPolicyAllow
	if n == 1 {
		policy = config.ToolPolicyDeny
	}
	for _, agentID := range h.al.GetRegistry().ListAgentIDs() {
		agentInst, ok := h.al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{h.toolName: policy},
		})
	}
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (h *policyFlipBetweenCallsHook) AfterTool(
	_ context.Context, result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

// TestRunTurn_PolicyDeniedExcludedFromQuarantine_MidBatchAllowFlipExecutes is
// the fix-2 regression. FR-079's TOCTOU re-check exists precisely because
// policy can change mid-turn. Before this fix, site 1 (the TOCTOU
// policy-deny branch) classified its denial permanent=true and quarantined
// the tool on that classification — so a LATER deny->allow flip (an
// operator fixing the policy) would be served the CACHED denial with no
// policy re-resolution at all, while the model was simultaneously told
// "permanent: true, do not retry": false the moment the policy changed back.
//
// Drives ONE LLM response containing TWO parallel calls to the SAME tool:
// call 1 is denied (policy flipped to "deny" by the hook, mid-batch); call 2
// — after the SAME hook flips the policy back to "allow" — must actually
// REACH the approver/execute path, proving policy_denied did not quarantine
// the tool.
//
// Stub-resistance: an implementation that quarantines policy_denied like
// every other permanent reason (i.e. passes cls.Permanent straight through
// to recordToolDenial's quarantine argument instead of a literal false)
// would show stub.wasCalled.Load() == false here — call 2 would be served
// from the quarantine cache instead of reaching TOCTOU's fresh
// resolveToolPolicyAtExec call, and Execute would never run despite the
// live policy genuinely being "allow" by the time call 2 is dispatched.
func TestRunTurn_PolicyDeniedExcludedFromQuarantine_MidBatchAllowFlipExecutes(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	const toolName = "bash" // distinct literal fixture tool name (spec §8.1)

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 2)).
		WithText("both handled")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &namedStubTool{name: toolName}
	al.RegisterTool(stub)
	// Filter-time (and initial live) policy: allow. Both calls share this
	// ONE snapshot, computed once at the top of the single LLM iteration
	// both calls belong to — the hook's mid-batch flips never touch it.
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAllow)

	hook := &policyFlipBetweenCallsHook{al: al, toolName: toolName}
	require.NoError(t, al.MountHook(NamedHook("policy-flip", hook)))

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(
		context.Background(), "run bash twice", "test-session-policy-denied-no-quarantine")
	require.NoError(t, err, "one policy_denied denial, far under budget=10, must not abort the turn")
	assert.Equal(t, "both handled", finalContent,
		"turn must reach the SECOND scripted LLM response, proving it did not abort")

	// The core fix-2 proof: the SECOND call, dispatched after the mid-batch
	// allow flip, actually EXECUTED — proving it was NOT served from a stale
	// quarantine cache populated by call 1's policy_denied.
	assert.True(t, stub.wasCalled.Load(),
		"CRITICAL: call 2 must actually execute after the mid-batch allow flip — "+
			"if policy_denied quarantined the tool, this would be false")

	events := collectEventStream(sub.C)
	var sawPolicyDenied, sawExecStart bool
	for _, evt := range events {
		switch evt.Kind {
		case EventKindToolExecSkipped:
			payload, ok := evt.Payload.(ToolExecSkippedPayload)
			require.True(t, ok)
			if strings.Contains(payload.Reason, "mid-turn policy change") {
				sawPolicyDenied = true
			}
			assert.NotContains(t, payload.Reason, "quarantined",
				"a policy_denied replay must never be attributed to the quarantine cache — "+
					"call 2 must not even reach the skip path, let alone as a replay")
		case EventKindToolExecStart:
			sawExecStart = true
		}
	}
	assert.True(t, sawPolicyDenied, "expected call 1's policy_denied skip event")
	assert.True(t, sawExecStart, "expected call 2's real tool-exec-start event")

	if _, ok := findEvent(events, EventKindError); ok {
		t.Error("no EventKindError expected — the turn must complete without aborting")
	}
}

// ---------------------------------------------------------------------------
// Post-epic-review fix: hook-deny branches must route through the ledger
// ---------------------------------------------------------------------------

// alwaysDenyToolHook unconditionally returns HookActionDenyTool from
// BeforeTool and counts its own invocations.
type alwaysDenyToolHook struct {
	reason string
	calls  atomic.Int32
}

func (h *alwaysDenyToolHook) BeforeTool(
	_ context.Context, call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	h.calls.Add(1)
	return call, HookDecision{Action: HookActionDenyTool, Reason: h.reason}, nil
}

func (h *alwaysDenyToolHook) AfterTool(
	_ context.Context, result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

// TestRunTurn_HookDenyTool_BoundedByLedger_QuarantinesAfterFirstDenial is
// fix-3's first half. loop.go's HookActionDenyTool branch used to `continue`
// with no ClassifyDenial, no recordToolDenial and no budget check at all —
// bypassing the ledger entirely, despite tool_denial.go's package doc,
// turnDenialLedger's doc, and audit.EventTurnAbortedToolDenialBudget's doc
// all asserting the aggregate budget covers "every denial response handed
// to the model". A third-party ProcessHook that unconditionally denies a
// tool reproduced the pre-ADR-058 infinite retry exactly: every repeat call
// re-invoked the hook, with no short-circuit and no bound.
//
// Drives THREE parallel calls to the same tool in one LLM response.
//
// Stub-resistance: reverting this branch to its pre-fix bare `continue`
// (no recordToolDenial call at all) would show hook.calls.Load() == 3 — the
// hook re-invoked for every call, since nothing ever quarantines the tool —
// identical to what an unbounded retry loop looks like from the hook's own
// point of view.
func TestRunTurn_HookDenyTool_BoundedByLedger_QuarantinesAfterFirstDenial(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	const toolName = "hook_denied_tool"

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 3)).
		WithText("understood — will not retry")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &namedStubTool{name: toolName}
	al.RegisterTool(stub)

	hook := &alwaysDenyToolHook{reason: "blocked by policy hook"}
	require.NoError(t, al.MountHook(NamedHook("always-deny-tool", hook)))

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	finalContent, err := al.ProcessDirect(
		context.Background(), "please run the tool three times", "test-session-hookdeny-quarantine")
	require.NoError(t, err, "3 hook denials, far under the aggregate budget of 10, must not abort the turn")
	assert.Equal(t, "understood — will not retry", finalContent)

	assert.Equal(t, int32(1), hook.calls.Load(),
		"CRITICAL: the hook must be invoked only ONCE — calls 2 and 3 must be short-circuited "+
			"by the quarantine gate the FIRST hook denial now populates (ADR-058 fix 3)")
	assert.False(t, stub.wasCalled.Load())

	events := collectEventStream(sub.C)
	var skipped, replayed int
	for _, evt := range events {
		if evt.Kind != EventKindToolExecSkipped {
			continue
		}
		payload, ok := evt.Payload.(ToolExecSkippedPayload)
		require.True(t, ok)
		skipped++
		if strings.Contains(payload.Reason, "quarantined") {
			replayed++
		}
	}
	assert.Equal(t, 3, skipped, "all three calls must produce a skip event")
	assert.Equal(t, 2, replayed, "calls 2 and 3 must be attributed to the quarantine cache")
}

// alwaysDenyApprovalHook unconditionally denies from ApproveTool and counts
// its own invocations.
type alwaysDenyApprovalHook struct {
	reason string
	calls  atomic.Int32
}

func (h *alwaysDenyApprovalHook) ApproveTool(
	_ context.Context, _ *ToolApprovalRequest,
) (ApprovalDecision, error) {
	h.calls.Add(1)
	return Deny(h.reason), nil
}

// TestRunTurn_ApprovalHookDeny_BoundedByLedger_QuarantinesAfterFirstDenial is
// fix-3's second half — the twin of the HookActionDenyTool test above for
// the hooks.ApproveTool/!IsApproved branch, which had the identical gap: no
// ClassifyDenial, no recordToolDenial, no budget.
//
// Stub-resistance: reverting this branch to its pre-fix bare `continue`
// would show hook.calls.Load() == 3, not 1.
func TestRunTurn_ApprovalHookDeny_BoundedByLedger_QuarantinesAfterFirstDenial(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	const toolName = "approval_hook_denied_tool"

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 3)).
		WithText("understood — will not retry")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &namedStubTool{name: toolName}
	al.RegisterTool(stub)

	hook := &alwaysDenyApprovalHook{reason: "blocked"}
	require.NoError(t, al.MountHook(NamedHook("always-deny-approval", hook)))

	finalContent, err := al.ProcessDirect(
		context.Background(), "please run the tool three times", "test-session-approvalhookdeny-quarantine")
	require.NoError(t, err, "3 approval-hook denials, far under the aggregate budget of 10, must not abort the turn")
	assert.Equal(t, "understood — will not retry", finalContent)

	assert.Equal(t, int32(1), hook.calls.Load(),
		"CRITICAL: ApproveTool must be consulted only ONCE — calls 2 and 3 must be "+
			"short-circuited by the quarantine gate the FIRST denial now populates (ADR-058 fix 3)")
	assert.False(t, stub.wasCalled.Load())
}

// ---------------------------------------------------------------------------
// Additional fix A: the headless auto-deny reason has its own table row
// ---------------------------------------------------------------------------

// TestRunTurn_HeadlessAutoDeny_UsesDedicatedTableRowNotUnknownFallback is the
// 7th-reviewer spec-conformance finding "Additional fix A": loop.go's
// AutoDenyAsk (FR-009/#264) branch classified its fixed literal reason
// through ClassifyDenial's UNKNOWN-reason fallback — no dedicated
// denialTable row existed for it, so AC-01's "every driven reason must be
// known" guard structurally could not apply, and the rendered message
// stuttered ("the tool call was refused (reason: auto-denied: ...)") with
// no headless-specific guidance. Drives a REAL headless (ProcessScheduled)
// run end to end and asserts both the classifier's own KNOWN status and the
// persisted transcript entry's actual content.
//
// Stub-resistance: an implementation that left the literal unclassified
// (known == false) fails outright; one that added a row but kept generic
// unknown-reason-style wording is caught by the negative Contains assertion
// on the stuttering template and the positive Contains assertion requiring
// "headless" guidance.
func TestRunTurn_HeadlessAutoDeny_UsesDedicatedTableRowNotUnknownFallback(t *testing.T) {
	al, home := schedTestLoop(t)
	const toolName = "dangerous_tool"

	provider := testutil.NewScenario().
		WithToolCall(toolName, `{}`).
		WithText("understood, moving on")
	mia := registerAgent(t, al, home, "mia", provider, false)

	stub := &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{toolName: "ask"},
	})

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	reply, err := al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "please use dangerous_tool", "scheduled", meta.ID,
	)
	require.NoError(t, err)
	assert.Equal(t, "understood, moving on", reply)
	assert.False(t, stub.wasCalled.Load())

	// (1) Unit-level: the literal headless reason is a KNOWN, dedicated row
	// rather than riding ClassifyDenial's unknown-reason fallback.
	cls, known := ClassifyDenial(autoDenyHeadlessReason)
	require.True(t, known,
		"the headless auto-deny literal must be a dedicated denialTable row (Additional Fix A)")
	assert.True(t, cls.Permanent)
	assert.NotContains(t, cls.ModelMessage, "the tool call was refused (reason:",
		"must not ride ClassifyDenial's generic unknown-reason template")
	assert.Contains(t, strings.ToLower(cls.ModelMessage), "headless",
		"message must give headless-specific guidance, not a generic refusal")

	// (2) End-to-end: the transcript entry persisted by the REAL turn
	// carries that same dedicated text, not the generic fallback.
	entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
	require.NoError(t, err)
	var found bool
	for _, e := range entries {
		if e.Type != session.EntryTypeToolCall {
			continue
		}
		for _, tc := range e.ToolCalls {
			if tc.Tool != toolName || tc.Status != toolCallStatusDenied {
				continue
			}
			found = true
			resultText, _ := tc.Result["text"].(string)
			assert.Equal(t, cls.TranscriptText, resultText,
				"persisted transcript text must equal the dedicated row's TranscriptText")
			assert.NotContains(t, resultText, "the tool call was refused (reason:",
				"persisted transcript must not stutter through the unknown-reason fallback template")
			permanent, _ := tc.Result["permanent"].(bool)
			assert.True(t, permanent)
			reason, _ := tc.Result["reason"].(string)
			assert.Equal(t, autoDenyHeadlessReason, reason)
		}
	}
	require.True(t, found, "expected a persisted denied tool_call entry for %s", toolName)
}

// ---------------------------------------------------------------------------
// Fix 4: quarantine replays must persist a transcript record, like the first
// ---------------------------------------------------------------------------

// TestRunTurn_QuarantineReplay_PersistsTranscriptEntryPerCall is the fix-4
// regression. The quarantine gate (loop.go, immediately after
// quarantinedDenialFor) appended a provider message and emitted
// EventKindToolExecSkipped for a replay, but called neither
// recordAskPendingToolCall nor settleAskToolCallTranscript — so replays
// 2..N left NO tool_call transcript entry at all and vanished on reload.
// CLAUDE.md's render-only-hiding allowance for a closed set of infra tool
// calls ("Persistence is unaffected — hidden calls still exist in the
// session transcript") does not cover genuine ABSENCE from persistence,
// which is what this bug produced.
//
// Drives a real ProcessScheduled run (AutoDenyAsk=true) with THREE parallel
// calls to the same ask-policy tool in one LLM response: call 1 denies via
// the headless auto-deny site (which already persisted, via its own
// pre-existing settleAskToolCallTranscript call) and quarantines the tool;
// calls 2 and 3 are quarantine replays — the exact case fix 4 covers.
//
// Stub-resistance: an implementation that only fixed call 1's persistence
// (already working before this fix) would show exactly ONE persisted
// tool_call entry, not three — the positive lower bound here is the COUNT
// itself, not merely "at least one entry exists".
func TestRunTurn_QuarantineReplay_PersistsTranscriptEntryPerCall(t *testing.T) {
	al, home := schedTestLoop(t)
	const toolName = "dangerous_tool"

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, 3)).
		WithText("headless run finished")
	mia := registerAgent(t, al, home, "mia", provider, false)

	stub := &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{toolName: "ask"},
	})

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	reply, err := al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "please run dangerous_tool three times", "scheduled", meta.ID,
	)
	require.NoError(t, err, "3 denials, far under the aggregate budget of 10, must not abort the turn")
	assert.Equal(t, "headless run finished", reply)
	assert.False(t, stub.wasCalled.Load())

	entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
	require.NoError(t, err)

	var toolCallEntries []session.ToolCall
	for _, e := range entries {
		if e.Type != session.EntryTypeToolCall {
			continue
		}
		toolCallEntries = append(toolCallEntries, e.ToolCalls...)
	}
	require.Len(t, toolCallEntries, 3,
		"expected THREE persisted tool_call transcript entries — one per dispatched call, including "+
			"the two quarantine replays that used to leave no record at all")
	for i, tc := range toolCallEntries {
		assert.Equal(t, toolCallStatusDenied, tc.Status, "entry %d must be status=denied", i)
		assert.Equal(t, toolName, tc.Tool, "entry %d must name the correct tool", i)
	}
}

// ---------------------------------------------------------------------------
// BDD-09 — FR-084 is gone, and gone behaviourally (FR-058-14/15 · AC-08)
// ---------------------------------------------------------------------------

// retiredFR084Identifiers are the six identifiers BDD-09 names plus the one
// extra lowercase/underscore variant the DoD checklist (spec §10) also
// greps for. Declared as plain string literals (not references to the
// removed symbols themselves — a removed symbol cannot even compile) so this
// test compiles regardless of whether the identifiers exist anywhere else in
// the tree.
var retiredFR084Identifiers = []string{
	"recordSyntheticDeny",
	"syntheticErrorFloor",
	"defaultSyntheticErrorFloor",
	"syntheticErrorCount",
	"TurnSyntheticErrorFloor",
	"synthetic_error_floor",
	"EventTurnAbortedSyntheticLoop",
}

// knownFR084HistoricalMentions is a narrow, explicit exception list for
// files that legitimately NAME a retired identifier's string form as
// historical/explanatory prose rather than referencing a surviving symbol —
// mirroring the DoD's own precedent exception for the "User denied" doc
// comment at pkg/audit/events.go:46 (spec §10). Verified in-session:
// pkg/agent/tool_denial.go's doc comment on turnDenialBudget explains WHY
// the new budget is a bare const and not a config field by naming the OLD
// JSON key it deliberately does NOT repeat the mistake of ("FR-084
// (`turn_synthetic_error_floor`) shipped with an 'unset means disabled'
// sentinel..."). The substring "synthetic_error_floor" is a genuine
// substring of "turn_synthetic_error_floor", so a plain Contains scan flags
// it — but it is prose about a deleted config key's name, not a live
// identifier: TestClassifyDenial_TableHasExactlyTwelveRows (tool_denial_test.go
// — renamed post-epic-review when two more rows were added; see its own
// doc) and every other W1 test already prove no such field, const, or
// method compiles into this package. Keyed by path relative to the repo
// root; a file NOT in this map gets zero exceptions.
//
// pkg/config/config_test.go::TestLoadConfig_LegacyTurnSyntheticErrorFloor_Ignored
// is a second, PRE-EXISTING instance of the same pattern this Additional
// Fix C's new positive-lower-bound (scannedFiles/foundPositiveControl,
// below) surfaced as a real, currently-failing assertion: that test is a
// deliberate ADR-058 pinning test proving a legacy
// `"turn_synthetic_error_floor"` JSON key in an on-disk config.json still
// loads without error (spec §10's DoD item, "One boot with a legacy
// ... key present in config.json starts cleanly") — it names both retired
// identifiers as raw JSON/string literal data, never as a live Go symbol.
// It was never added to this map when it was written, so this walk had
// been failing at HEAD before any of the fixes in this file were made; this
// entry closes that gap without touching pkg/config (outside this file's
// ownership) at all — this map is a pkg/agent-owned allowlist regardless of
// which package the exempted file lives in.
var knownFR084HistoricalMentions = map[string][]string{
	filepath.Join("pkg", "agent", "tool_denial.go"): {"synthetic_error_floor"},
	filepath.Join("pkg", "config", "config_test.go"): {
		"TurnSyntheticErrorFloor", "synthetic_error_floor",
	},
}

// TestADR058_FR084Identifiers_ResolveNowhereInSource is BDD-09's static half:
// "the identifiers ... resolve nowhere". It walks pkg/ (where the spec's own
// DoD count places every historical occurrence: 46 in pkg/agent, 8 in
// pkg/audit, 2 in pkg/config — spec §10) and asserts none of the seven
// retired identifiers appears in any .go file — except this test file
// itself, which necessarily names them as data (excluded via runtime.Caller,
// not by string-matching its own path, so a rename of this file cannot
// silently defeat the exclusion) and the single documented historical
// mention above.
//
// Stub-resistance (spec §8.2 AC-08's own warning): "A grep-only assertion
// that a renamed clone of FR-084 would survive" is exactly why this test is
// paired with TestRunTurn_ToolDenialBudgetAbort_AuditsNewEventNotOldOne
// below, which proves the BEHAVIOUR (new audit event, no old event string in
// any session message) rather than relying on identifier absence alone.
//
// Additional Fix C (7th-reviewer spec-conformance finding): the walk below
// asserts violations is empty, but originally never asserted HOW MANY .go
// files it actually scanned — a wrong repoRoot that still passes os.Stat, a
// broken suffix filter, or a WalkDir call that silently skips everything
// would all make this pass VACUOUSLY (Binding Rule 4: every zero-count
// assertion needs a positive lower bound). Two independent guards are added:
// a scanned-file counter asserted against a floor far below this repo's
// real pkg/ file count, and a positive control — turnDenialBudget, a genuinely
// LIVE identifier defined in this same epic's tool_denial.go — which the
// walk must actually FIND at least once, proving the content-scanning logic
// itself works rather than the loop body never running.
func TestADR058_FR084Identifiers_ResolveNowhereInSource(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) must resolve this test file's own path")
	thisFileAbs, err := filepath.Abs(thisFile)
	require.NoError(t, err)

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "pkg/agent is two levels below the repo root")
	pkgDir := filepath.Join(repoRoot, "pkg")
	if _, statErr := os.Stat(pkgDir); statErr != nil {
		t.Fatalf("BLOCKED: expected repo pkg/ directory at %s (resolved from pkg/agent's own location): %v", pkgDir, statErr)
	}

	// Additional Fix C: a control identifier known to be LIVE (defined in
	// tool_denial.go, part of this very epic) — the walk must find it at
	// least once, or the content-scanning logic itself is broken/vacuous.
	const positiveControlIdentifier = "turnDenialBudget"

	var violations []string
	var scannedFiles int
	var foundPositiveControl bool
	walkErr := filepath.WalkDir(pkgDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		absPath, absErr := filepath.Abs(path)
		if absErr != nil {
			return absErr
		}
		if absPath == thisFileAbs {
			return nil // this file legitimately names the identifiers as data
		}
		relPath, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		allowedHere := knownFR084HistoricalMentions[relPath]
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)
		scannedFiles++
		if strings.Contains(content, positiveControlIdentifier) {
			foundPositiveControl = true
		}
		for _, ident := range retiredFR084Identifiers {
			if !strings.Contains(content, ident) {
				continue
			}
			if slices.Contains(allowedHere, ident) {
				continue // documented historical mention — see knownFR084HistoricalMentions
			}
			violations = append(violations, fmt.Sprintf("%s: contains %q", path, ident))
		}
		return nil
	})
	require.NoError(t, walkErr, "walking %s must not fail", pkgDir)

	// Additional Fix C's positive lower bound: this repo's pkg/ tree has
	// (per GitNexus's own index) tens of thousands of symbols across
	// hundreds of .go files — 100 is a floor with generous headroom below
	// the real count, chosen so a genuine future repo restructuring would
	// have to shrink pkg/ by an order of magnitude before this floor itself
	// became the false-failure risk, while still catching a repoRoot
	// miscalculation, a broken suffix filter, or a WalkDir call that never
	// descends into anything.
	const minScannedFiles = 100
	require.GreaterOrEqual(t, scannedFiles, minScannedFiles,
		"the walk scanned only %d .go files under %s — a wrong repoRoot, a broken suffix filter, "+
			"or a WalkDir call that skips everything would ALSO produce an empty violations list, "+
			"which is exactly the vacuous-pass this floor exists to catch", scannedFiles, pkgDir)
	require.True(t, foundPositiveControl,
		"the walk never found the positive-control identifier %q anywhere under %s — the "+
			"content-scanning logic itself is broken (an empty violations list from THIS state "+
			"would be a vacuous pass, not proof FR-084 is gone)", positiveControlIdentifier, pkgDir)

	assert.Empty(t, violations,
		"FR-084 was deleted in full (ADR-058 §10.A3) — these identifiers must resolve nowhere under pkg/:\n%s",
		strings.Join(violations, "\n"))
}

// TestRunTurn_ToolDenialBudgetAbort_AuditsNewEventNotOldOne is BDD-09's
// behavioural half: a turn producing 10 denials aborts (already proven at
// the event-bus/error-string level by W4's
// TestRunTurn_ToolDenialBudget_AbortsAtTenNotEleven), and ADDITIONALLY —
// the part neither W1 nor W4 asserts —
//  1. the audit log carries the NEW event turn.aborted_tool_denial_budget
//     with the correct tool/reason/budget in Details, and
//  2. no message anywhere in the turn's persisted session history contains
//     the OLD retired event slug "synthetic_error_loop".
//
// Stub-resistance: an implementation that renamed FR-084's abort stage
// string but kept emitting the OLD audit event constant, or that emitted no
// audit event at all, fails assertion (1). An implementation that resurrected
// the old "synthetic_error_loop" wording anywhere in a persisted message
// fails assertion (2).
func TestRunTurn_ToolDenialBudgetAbort_AuditsNewEventNotOldOne(t *testing.T) {
	cfg, tmpHome := baseLoopDenialTestConfig(t)
	const toolName = "budget_probe_tool"
	const totalScripted = 12

	provider := testutil.NewScenario().
		WithToolCalls(distinctToolCalls(toolName, totalScripted))

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &namedStubTool{name: toolName}
	al.RegisterTool(stub)
	setAskPolicyForAllAgents(t, al, toolName, config.ToolPolicyAsk)

	approver := &countingDenyApprover{reason: "timeout"}
	al.SetToolApprover(approver)

	finalContent, err := al.ProcessDirect(context.Background(), "run budget_probe_tool twelve times", "test-session-fr084-gone")
	require.Error(t, err)
	assert.Empty(t, finalContent)
	assert.Contains(t, err.Error(), "tool_denial_budget")
	assert.False(t, stub.wasCalled.Load())

	// (1) The NEW audit event, with the right correlated fields — not a
	// renamed survivor of the old one.
	auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")
	require.FileExists(t, auditPath)
	entries, readErr := readAuditEntries(auditPath)
	require.NoError(t, readErr)
	require.NotEmpty(t, entries)

	var budgetEntry map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventTurnAbortedToolDenialBudget {
			budgetEntry = e
			break
		}
	}
	require.NotNil(t, budgetEntry,
		"audit log must contain an entry with event=%q; got entries: %v", audit.EventTurnAbortedToolDenialBudget, entries)
	assert.Equal(t, toolName, budgetEntry["tool"])

	details, ok := budgetEntry["details"].(map[string]any)
	require.True(t, ok, "expected details to be a JSON object, got %T", budgetEntry["details"])
	assert.Equal(t, "timeout", details["denial_reason"])
	assert.Equal(t, float64(turnDenialBudget), details["denials_used"])
	assert.Equal(t, float64(turnDenialBudget), details["budget"])

	// No audit entry anywhere may carry the OLD retired event slug.
	for _, e := range entries {
		if ev, ok := e["event"].(string); ok {
			assert.NotContains(t, ev, "synthetic_error_loop",
				"an audit entry still names the retired FR-084 event: %v", e)
		}
	}

	// (2) No message the turn produced anywhere names the old retired event
	// slug either — checked on the turn's own returned error text, NOT via
	// Sessions.GetHistory. abortTurn calls ts.restoreSession before
	// returning (turn.go's restoreSession truncates the session archive back
	// to its pre-turn length via RollbackAppended), so for a turn's first
	// message ever — exactly this test's shape — GetHistory legitimately
	// returns empty after an abort: that is documented, intentional
	// rollback behaviour (undoing a turn that never completed), not a
	// defect this test should be asserting against. The turn's returned
	// error (already asserted above to be non-nil and to contain
	// "tool_denial_budget") IS the message a caller/channel actually sees
	// for an aborted turn, so it is the correct surface to check here.
	assert.NotContains(t, err.Error(), "synthetic_error_loop",
		"the turn's own error message still names the retired FR-084 event slug")
}
