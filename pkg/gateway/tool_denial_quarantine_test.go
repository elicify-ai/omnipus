// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-058 (tool-denial semantics) — W6/Wave 2: BDD-04 / AC-03, the
// registry-backed proof that quarantine opens NO further approval round-trip.
//
// Traces to: docs/internal/specs/adr-058-tool-denial-semantics-spec.md §6
// BDD-04 (line ~371) and §7's traceability row "FR-058-10/11 | §10.A1 |
// BDD-04 | pkg/gateway::tool_denial_quarantine_test.go | AC-03, AC-02".
//
// Why this belongs in pkg/gateway and not pkg/agent (spec §2.5, the
// import-direction constraint): pkg/gateway/gateway.go imports pkg/agent, so
// pkg/agent cannot import pkg/gateway — the REAL PolicyApprover
// (policyApproverAdapter) and the REAL approvalRegistryV2 are only
// constructible here. pkg/agent/tool_denial_quarantine_gate_test.go (W4)
// already proves the quarantine GATE fires inside runTurn using a
// call-counting stub approver — that is legitimate there because the fact
// under test is the call COUNT (a stub can't be a canned-value spy if the
// call count IS what's being verified), but it cannot assert anything about
// REAL approvalRegistryV2 STATE, because pkg/agent cannot import
// approvalRegistryV2 at all. This file closes that gap: AC-03 explicitly
// requires "assert on pendingCount/registry state, not on a mock's call
// log" — this drives a REAL registry + REAL adapter + REAL WSHandler (per
// spec §3.7's demonstrated construction) through a REAL turn, and asserts on
// the registry's own entries map.
package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// repeatedToolCalls builds n parallel tool-call entries for the same tool
// name with distinct IDs, so one scripted LLM response can invoke the same
// tool several times in a single batch (mirrors
// pkg/agent/tool_denial_quarantine_gate_test.go's distinctToolCalls, which
// this package cannot import — it lives in package agent's own _test.go
// files).
func repeatedToolCalls(toolName string, n int) []providers.ToolCall {
	calls := make([]providers.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		fc := providers.FunctionCall{Name: toolName, Arguments: "{}"}
		calls = append(calls, providers.ToolCall{
			ID:       fmt.Sprintf("%s-%d", toolName, i),
			Function: &fc,
		})
	}
	return calls
}

// quarantineTestBashStub is a tool literally named "bash" (Binding Rule 4's
// fixture: "tools bash (permanent path) and web_fetch (saturated path)") that
// must never execute — every call in this test is either the one real denial
// or a quarantine replay.
type quarantineTestBashStub struct {
	tools.BaseTool
	executed int
}

func (q *quarantineTestBashStub) Name() string { return "bash" }
func (q *quarantineTestBashStub) Description() string {
	return "test stub named bash — must never execute"
}
func (q *quarantineTestBashStub) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (q *quarantineTestBashStub) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (q *quarantineTestBashStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	q.executed++
	return &tools.ToolResult{ForLLM: "executed — this should never happen", IsError: false}
}

// TestRunTurn_QuarantineAfterFirstPermanentDenial_NoFurtherRegistryRoundTrip
// is BDD-04 / AC-03: a real approvalRegistryV2 + real policyApproverAdapter
// + real turn, where nobody answers the first "bash" request so it denies
// with reason "timeout" (PERMANENT — spec §4.1 row 2). The SAME LLM response
// calls "bash" three times; FR-058-11's quarantine gate must short-circuit
// calls 2 and 3 with NO second/third RequestApproval, which this test proves
// by inspecting the registry's OWN entries map (real state), not a call
// counter standing in for it.
//
// Stub-resistance (spec §8.2 AC-03 row): "A no-op loop that dispatches
// nothing, trivially making 'zero further requestApproval' true" is excluded
// by the POSITIVE lower bound inside the negative criterion — asserting
// exactly 1 entry (not 0), plus that calls 2 and 3 still produced a real
// permission_denied outcome (the turn completed with the recovery text,
// proving the batch of 3 was genuinely dispatched and denied, not silently
// dropped).
func TestRunTurn_QuarantineAfterFirstPermanentDenial_NoFurtherRegistryRoundTrip(t *testing.T) {
	workspaceDir := t.TempDir()

	const agentID = "mia"   // Binding Rule 4 fixture: the denied agent
	const toolName = "bash" // Binding Rule 4 fixture: the permanent-path tool
	// denial reason "timeout" (Binding Rule 4's third fixture) is produced
	// by the real registry timer firing unanswered — asserted below via the
	// resolved entry's state (ApprovalStateDeniedTimeout), not hardcoded.

	// A short REAL timeout so the FIRST call's registry entry genuinely
	// expires unanswered within the test's runtime (spec §3.7's demonstrated
	// construction uses 1s; 500ms keeps this test reasonably fast while still
	// being a real timer, not a synthetic reason string).
	//
	// 500ms (not the original 150ms) is deliberate slack for the wall-clock
	// assertion below (elapsed < 3*approvalWindow = 1.5s): the real timer
	// consumes ~1x this window regardless of its size, so a small window
	// leaves little headroom for everything else the turn does (session
	// JSONL writes, scheduler jitter) before hitting the 3x ceiling — and
	// this suite runs under `go test -race`, which commonly inflates
	// wall-clock timing 2-5x versus a non-race build. The ratio asserted
	// (3x) is unchanged; only the base window is widened so that ratio has
	// real margin instead of racing the CI clock.
	const approvalWindow = 500 * time.Millisecond
	reg := newApprovalRegistryV2(64, approvalWindow)

	// One LLM response with THREE parallel calls to the same permanently
	// denied tool (BDD-04: "the agent calls that tool a second and third
	// time in the same turn"), then a recovery text response — reached only
	// if the turn genuinely continues past the quarantined calls rather than
	// aborting (three denials is far under the aggregate budget of 10).
	provider := testutil.NewScenario().
		WithToolCalls(repeatedToolCalls(toolName, 3)).
		WithText("acknowledged — will not retry bash")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: agentID, Home: workspaceDir, Model: &config.AgentModelConfig{Primary: "scripted-model"}},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &quarantineTestBashStub{}
	al.RegisterTool(stub)
	for _, id := range al.GetRegistry().ListAgentIDs() {
		if inst, ok := al.GetRegistry().GetAgent(id); ok {
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{toolName: config.ToolPolicyAsk},
			})
		}
	}

	al.SetToolApprover(newPolicyApproverAdapter(reg, &WSHandler{}))

	start := time.Now()
	finalContent, err := al.ProcessDirect(context.Background(), "please run bash three times", "sess-w6-quarantine")
	elapsed := time.Since(start)

	// The turn must NOT abort — 3 denials is far under the budget of 10, and
	// quarantine must never be mistaken for termination.
	require.NoError(t, err, "the turn must complete normally after 3 denials of one tool")
	assert.Equal(t, "acknowledged — will not retry bash", finalContent,
		"the turn must reach the SECOND scripted LLM response — proof calls 2 and 3 were "+
			"genuinely denied and returned to the model, not silently dropped")

	// AC-03's core assertion: REAL registry state, not a call log. Exactly
	// ONE approval entry was EVER created in this registry for the whole
	// turn — this registry is fresh and used by nothing else in the test, so
	// its total entry count IS the total RequestApproval-accepted count.
	reg.mu.Lock()
	entryCount := len(reg.entries)
	var sawTool string
	for _, e := range reg.entries {
		sawTool = e.ToolName
	}
	reg.mu.Unlock()
	require.Equal(t, 1, entryCount,
		"CRITICAL: the registry must contain exactly ONE approval entry for the whole turn — "+
			"more than 1 means the quarantine gate let a second/third RequestApproval reach the "+
			"registry; 0 would mean the batch was never really dispatched (the AC-03 stub-resistance "+
			"guard)")
	assert.Equal(t, toolName, sawTool)

	// The one entry that exists resolved to the real timeout path, not some
	// other reason — ties this registry-state assertion back to the
	// specific denial the scenario drove.
	reg.mu.Lock()
	for _, e := range reg.entries {
		assert.Equal(t, ApprovalStateDeniedTimeout, e.state,
			"the sole registry entry must have resolved via the real timeout path")
	}
	reg.mu.Unlock()

	// Tool never executed — a permanent denial is never treated as approval.
	assert.Equal(t, 0, stub.executed, "bash must never execute — every one of the 3 calls was denied")

	// BDD-04's wall-clock bound. The property under test is "ONE real approval
	// round-trip, not three": genuine quarantine waits on the real timer only
	// for call 1, and replays calls 2/3 from cache instantly.
	//
	// The bound must account for FIXED TURN OVERHEAD, which the original 3x
	// bound implicitly assumed was zero. Total elapsed is
	// (approval waits) + O, where O is agent-loop/provider-stub/teardown cost
	// unrelated to approvals. Measured O here is ~1.0-1.1s, so:
	//
	//   quarantining (1 wait):     500ms + O  ≈ 1.6s
	//   non-quarantining (3 waits): 1500ms + O ≈ 2.6s
	//
	// A 3x bound (1.5s) therefore sits BELOW the passing case and failed
	// deterministically — it was measuring O, not the property. 4x (2.0s) sits
	// cleanly between the two and still fails a non-quarantining implementation
	// by a ~600ms margin. The registry-entry assertion above is the primary,
	// timing-independent proof of the same property; this is the corroborating
	// bound, so it should be robust rather than maximally tight.
	assert.Less(t, elapsed, 4*approvalWindow,
		"elapsed wall-clock must reflect ONE approval round-trip plus fixed turn "+
			"overhead, not three round-trips — a non-quarantining implementation "+
			"would spend 3x the approval window waiting")
}
