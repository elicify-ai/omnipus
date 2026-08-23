// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-058 (tool-denial semantics) — W6/Wave 2: BDD-01 + AC-02, the §1.1
// regression reproduced end to end against the REAL gateway registry.
//
// Traces to: docs/internal/specs/adr-058-tool-denial-semantics-spec.md §6
// BDD-01 (line ~333) and §7's traceability row "— (regression) | §1.1 |
// BDD-01 + BDD-04 | pkg/gateway::tool_denial_timeout_regression_test.go |
// AC-02". Also spec §8.3 item 4's own description of this file: "nobody
// answers, assert the turn ends by itself, the reason names tool + agent +
// timeout, and elapsed < 3 × the window. No manual Stop anywhere in the
// test."
//
// This is the literal UAT reproduction from ADR-058 §1.1: a real,
// `ask`-policy tool whose approval window expires with nobody answering,
// driven through the REAL approvalRegistryV2 + real policyApproverAdapter
// (spec §3.7) — never a stub approver standing in for the timeout (a stub
// can assert the classifier is CALLED with "timeout"; it cannot prove a
// real, unanswered approval window actually produces that reason, which is
// the entire defect this ADR fixes). §1.1's original bug needed a manual
// POST /plans/{id}/stop after 13+ minutes; TestTimeoutBudget_TurnEndsOnItsOwn
// below asserts the opposite outcome with ZERO test-driven
// cancel/resolve/Stop call anywhere in its body.
package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentpkg "github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// timeoutRegressionRunTaskStub is a tool literally named "run_task" — BDD-01's
// own literal fixture ("an agent whose effective policy for 'run_task' is
// 'ask'") — that must never execute across either test in this file: every
// call is denied, either by the real unanswered timeout or by quarantine
// replay.
type timeoutRegressionRunTaskStub struct {
	tools.BaseTool
	executed int
}

func (r *timeoutRegressionRunTaskStub) Name() string { return "run_task" }
func (r *timeoutRegressionRunTaskStub) Description() string {
	return "test stub named run_task — must never execute in this file"
}
func (r *timeoutRegressionRunTaskStub) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (r *timeoutRegressionRunTaskStub) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (r *timeoutRegressionRunTaskStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	r.executed++
	return &tools.ToolResult{ForLLM: "executed — this should never happen", IsError: false}
}

// newTimeoutRegressionHarness builds the shared cfg/AgentLoop/registry
// wiring for both tests below: a real approvalRegistryV2 + real
// policyApproverAdapter + real WSHandler zero value (spec §3.7), agent
// "mia" (Binding Rule 4's denied-agent fixture), tool "run_task" on "ask"
// policy.
func newTimeoutRegressionHarness(t *testing.T, provider *testutil.ScenarioProvider, window time.Duration) (*agentpkg.AgentLoop, *approvalRegistryV2, *timeoutRegressionRunTaskStub) {
	t.Helper()
	tmpHome := t.TempDir()
	const agentID = "mia"
	const toolName = "run_task"

	reg := newApprovalRegistryV2(64, window)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpHome,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: agentID, Home: tmpHome, Model: &config.AgentModelConfig{Primary: "scripted-model"}},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	stub := &timeoutRegressionRunTaskStub{}
	al.RegisterTool(stub)
	for _, id := range al.GetRegistry().ListAgentIDs() {
		if inst, ok := al.GetRegistry().GetAgent(id); ok {
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{toolName: config.ToolPolicyAsk},
			})
		}
	}

	al.SetToolApprover(newPolicyApproverAdapter(reg, &WSHandler{}))
	return al, reg, stub
}

// TestTimeoutDenial_PayloadIsHonest_BDD01 is BDD-01: a single real,
// unanswered approval window expiring naturally denies "run_task" with
// reason "timeout", and the payload the model actually receives —
// inspected from the scripted provider's OWN recorded message history
// (real content, never a call-count spy) — is honest about it: permanent,
// not phrased as a user decision, and names the productive next step.
func TestTimeoutDenial_PayloadIsHonest_BDD01(t *testing.T) {
	const window = 100 * time.Millisecond

	provider := testutil.NewScenario().
		WithToolCall("run_task", `{}`).
		WithText("run_task's approval expired; I will not retry and am reporting this as a blocker.")

	al, _, stub := newTimeoutRegressionHarness(t, provider, window)

	finalContent, err := al.ProcessDirect(context.Background(), "please run run_task", "sess-w6-timeout-bdd01")
	require.NoError(t, err, "a single real timeout denial must not abort the turn — only the aggregate budget does")
	assert.Equal(t, "run_task's approval expired; I will not retry and am reporting this as a blocker.", finalContent)
	assert.Equal(t, 0, stub.executed, "run_task must never execute — the only call was denied")

	// Find the tool-result message the model actually received for the
	// denied call, from the scripted provider's OWN recorded history (the
	// SECOND Chat() call's message list includes the first call's tool
	// result).
	var payload string
	for _, m := range provider.LastMessages() {
		if strings.Contains(m.Content, `"tool":"run_task"`) {
			payload = m.Content
		}
	}
	require.NotEmpty(t, payload, "the model must have received a permission_denied payload naming run_task")

	// The classification-table row for "timeout" is the SAME production
	// function the loop uses (agent.ClassifyDenial) — asserting against its
	// live output, not a hand-typed paraphrase that could silently drift
	// from the real wording.
	cls, known := agentpkg.ClassifyDenial("timeout")
	require.True(t, known)
	require.True(t, cls.Permanent, "timeout is classified PERMANENT (ADR-058 D1 #2)")

	assert.Contains(t, payload, `"error":"permission_denied"`)
	assert.Contains(t, payload, `"reason":"timeout"`)
	assert.Contains(t, payload, `"permanent":true`)
	assert.Contains(t, payload, cls.ModelMessage,
		"the payload's message must be the REAL classified message, not a paraphrase")

	// FR-058-06's core guarantee: on a reason other than "user", the message
	// must never claim a human denied anything.
	assert.NotContains(t, strings.ToLower(payload), "user denied",
		"CRITICAL: a timeout (nobody answered) must never be worded as a user decision — "+
			"this is the exact §1.1 defect (loop.go's old literal 'User denied tool execution.')")

	// The productive next step (D2/D3): tells the agent to stop, not retry.
	lowerMsg := strings.ToLower(cls.ModelMessage)
	assert.True(t, strings.Contains(lowerMsg, "stop") && strings.Contains(lowerMsg, "report"),
		"the message must instruct the agent to stop and report the blocker, matching the "+
			"control-case behaviour from ADR-058 §1.2")
}

// TestTimeoutBudget_TurnEndsOnItsOwn_NoManualStop_AC02 is AC-02: the exact
// §1.1 regression, reproduced against a REAL registry with NO test-driven
// cancel/resolve/Stop call anywhere in this function. One LLM response
// scripts turnDenialBudget (10) parallel calls to the same real,
// permanently-denied ("timeout") tool. Because FR-058-10/11's quarantine
// engages on the FIRST denial, only call 1 pays the real approval window;
// calls 2-10 are instant cache replays — so the turn terminates ON ITS OWN,
// within roughly ONE window, not ten, and with NO human intervention of any
// kind. This is the literal opposite of §1.1's "13+ minutes, needed a
// manual Stop" outcome.
func TestTimeoutBudget_TurnEndsOnItsOwn_NoManualStop_AC02(t *testing.T) {
	// 500ms (not a smaller window) is deliberate slack for the wall-clock
	// assertion below (elapsed < 3*window = 1.5s): the real timer consumes
	// ~1x this window regardless of its size, so a small window leaves
	// little headroom for the rest of the turn (session JSONL writes,
	// scheduler jitter) before hitting the 3x ceiling — and this suite runs
	// under `go test -race`, which commonly inflates wall-clock timing 2-5x
	// versus a non-race build. The ratio asserted (3x) is unchanged; only
	// the base window is widened so that ratio has real margin instead of
	// racing the CI clock.
	const window = 500 * time.Millisecond
	const toolName = "run_task"
	const agentID = "mia"

	provider := testutil.NewScenario().
		WithToolCalls(regressionRepeatedCalls(toolName, 10))
		// Deliberately no further scripted step: the budget must exhaust and
		// the turn must abort inside this ONE batch. If it did not, the loop
		// would ask the LLM again and ScenarioProvider would return
		// ErrNoMoreResponses instead — a visibly different failure.

	al, reg, stub := newTimeoutRegressionHarness(t, provider, window)

	start := time.Now()
	// The ENTIRE test's interaction with the turn is this one blocking call.
	// No goroutine, no reg.resolve, no cancel, no Stop — the turn must end
	// BY ITSELF via the real, unanswered timeout + the aggregate budget.
	finalContent, err := al.ProcessDirect(context.Background(), "please run run_task ten times", "sess-w6-timeout-ac02")
	elapsed := time.Since(start)

	require.Error(t, err, "the turn must terminate on its own once the aggregate denial budget exhausts")
	assert.Empty(t, finalContent)

	// The termination reason names tool + reason + agent — AC-02's explicit
	// requirement, and the literal answer to §1.1's complaint that a Stop-
	// cancelled task's result named neither.
	assert.Contains(t, err.Error(), "tool_denial_budget")
	assert.Contains(t, err.Error(), toolName)
	assert.Contains(t, err.Error(), "timeout")
	assert.Contains(t, err.Error(), agentID)

	// Bounded by a stated multiple of ONE window, never by ten — AC-02's
	// other explicit requirement, and the direct rebuttal of the "100
	// minutes" arithmetic ADR-058 §10.A1 found unsatisfiable in the
	// quarantine-at-ceiling design.
	assert.Less(t, elapsed, 3*window,
		"elapsed must be bounded by ~1 approval window, not 10 — proves quarantine, not repeated real waits, "+
			"is what let the turn end on its own")

	// Real registry state (not a call log): exactly ONE approval entry was
	// EVER created for the whole turn, proving calls 2-10 never opened a
	// round trip either.
	reg.mu.Lock()
	entryCount := len(reg.entries)
	reg.mu.Unlock()
	require.Equal(t, 1, entryCount,
		"the real registry must show exactly ONE entry for the whole 10-call batch — "+
			"more would mean quarantine failed to suppress the round trip; the AC-02 regression "+
			"would then cost multiple real approval windows again, exactly like §1.1")

	assert.Equal(t, 0, stub.executed, "run_task must never execute — every one of the 10 calls was denied")
}

// regressionRepeatedCalls builds n parallel tool-call entries for the same
// tool name with distinct IDs (see tool_denial_quarantine_test.go's
// repeatedToolCalls for the identical shape — kept as a separate,
// file-local helper here rather than shared, since W6's Wave-2 sibling
// files must not introduce cross-file compile-order coupling within the
// same package beyond what each file already needs standalone).
func regressionRepeatedCalls(toolName string, n int) []providers.ToolCall {
	calls := make([]providers.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		fc := providers.FunctionCall{Name: toolName, Arguments: "{}"}
		calls = append(calls, providers.ToolCall{
			ID:       fmt.Sprintf("%s-regress-%d", toolName, i),
			Function: &fc,
		})
	}
	return calls
}
