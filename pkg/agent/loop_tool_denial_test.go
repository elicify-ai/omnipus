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
//   - BDD-02 (the ten-row classification table) — pkg/agent/tool_denial_test.go (W1).
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
// uses — a single-agent ("main", the registry default for a Defaults-only
// cfg with no Agents.List) AgentLoop rooted at a private temp workspace,
// with audit logging enabled so the BDD-09 test can read it back.
func baseLoopDenialTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
	// producing session key "agent:main:main" via BuildAgentMainSessionKey.
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	const resolvedSessionKey = "agent:main:main"
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
// identifier: TestClassifyDenial_TableHasExactlyTenRows and every other W1
// test already prove no such field, const, or method compiles into this
// package. Keyed by path relative to the repo root; a file NOT in this map
// gets zero exceptions.
var knownFR084HistoricalMentions = map[string][]string{
	filepath.Join("pkg", "agent", "tool_denial.go"): {"synthetic_error_floor"},
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

	var violations []string
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
