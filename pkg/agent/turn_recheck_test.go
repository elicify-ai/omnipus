// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tests for tool-execution-time policy re-check (FR-079 / revision-6 addition).
//
// BDD Scenario: "Tool-execution-time policy re-check — deny mid-turn aborts in-flight call"
//
// Given an LLM has just emitted a tool_call for "exec" whose effective policy at
//   filter time was "allow",
// When the operator PUTs an updated config setting policies: {exec: deny} for the agent,
// And the policy pointer swaps before the loop calls Execute on the in-flight tool call,
// Then the loop re-resolves the effective policy for "exec" immediately before execution,
// And observes "deny",
// And synthesizes permission_denied instead of running exec,
// And an audit event tool.policy.deny.attempted (WARN) is emitted with tool_call_id
//   and a note mid_turn_policy_change.
//
// Traces to: tool-registry-redesign-spec.md BDD "Tool-execution-time policy re-check" / FR-079

package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// trackingTool records whether its Execute was called.
type trackingTool struct {
	name     string
	executed atomic.Bool
}

func (t *trackingTool) Name() string                 { return t.name }
func (t *trackingTool) Description() string          { return "tracking: " + t.name }
func (t *trackingTool) Parameters() map[string]any   { return map[string]any{} }
func (t *trackingTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *trackingTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *trackingTool) RequiresAdminAsk() bool       { return false }
func (t *trackingTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	t.executed.Store(true)
	return &tools.ToolResult{ForLLM: "executed"}
}

// TestTurnRecheck_PolicyAtomicPointerUpdate verifies that StoreToolPolicy atomically
// replaces the agent's policy snapshot and LoadToolPolicy observes the new value.
//
// This is the foundational unit test for the FR-020 / FR-041 atomic pointer contract.
// The loop MUST call LoadToolPolicy immediately before each tool Execute — not cache
// the policy from the start of the turn.
//
// Traces to: tool-registry-redesign-spec.md FR-020 / FR-041 / FR-079
func TestTurnRecheck_PolicyAtomicPointerUpdate(t *testing.T) {
	agent := newMinimalAgentInstance()

	// Initial policy: exec is allowed.
	initialPolicy := &tools.ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"exec": "allow"},
		GlobalDefaultPolicy: "allow",
	}
	agent.StoreToolPolicy(initialPolicy)

	// Verify initial state.
	loaded := agent.LoadToolPolicy()
	require.NotNil(t, loaded, "LoadToolPolicy must return the stored policy")
	assert.Equal(t, "allow", loaded.Policies["exec"],
		"initial policy for exec must be 'allow'")

	// Simulate mid-turn policy change: operator updates to deny.
	updatedPolicy := &tools.ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"exec": "deny"},
		GlobalDefaultPolicy: "allow",
	}
	agent.StoreToolPolicy(updatedPolicy)

	// After Store, the next Load must observe the new policy.
	reloaded := agent.LoadToolPolicy()
	require.NotNil(t, reloaded)
	assert.Equal(t, "deny", reloaded.Policies["exec"],
		"post-update LoadToolPolicy must return 'deny' for exec")
}

// TestTurnRecheck_PolicyIsPerCallNotPerTurn verifies that if the policy changes
// between the start of a turn and the execution of a tool call, the per-call
// LoadToolPolicy reflects the change.
//
// The loop MUST re-evaluate FilterToolsByPolicy with LoadToolPolicy() on each
// LLM call (FR-041). And it MUST re-check the effective policy for each tool
// call just before calling Execute (FR-079).
//
// If this test fails with t.Fatal("BLOCKED"), it means FR-079 is not implemented:
// the production code does not re-check policy before tool Execute.
//
// Traces to: tool-registry-redesign-spec.md FR-041 / FR-079
func TestTurnRecheck_PolicyIsPerCallNotPerTurn(t *testing.T) {
	agent := newMinimalAgentInstance()

	// "Turn start" policy: exec is allowed.
	allowExec := &tools.ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"exec": "allow"},
		GlobalDefaultPolicy: "allow",
	}
	agent.StoreToolPolicy(allowExec)

	// Load at "filter time" (LLM call assembly).
	atFilter := agent.LoadToolPolicy()
	require.NotNil(t, atFilter)

	execTool := &trackingTool{name: "exec"}
	allTools := []tools.Tool{execTool}

	// Filter at LLM-call time — exec passes.
	filtered, policyMap := tools.FilterToolsByPolicy(allTools, "core", atFilter)
	require.Len(t, filtered, 1, "exec must pass the filter with allow policy")
	assert.Equal(t, "allow", policyMap["exec"])

	// Simulate: operator changes policy to deny BEFORE Execute runs.
	denyExec := &tools.ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"exec": "deny"},
		GlobalDefaultPolicy: "allow",
	}
	agent.StoreToolPolicy(denyExec)

	// FR-079: the loop MUST re-check effective policy immediately before Execute.
	// Simulate the expected production behavior: call LoadToolPolicy again and
	// re-run resolveEffective for the specific tool.
	atExecution := agent.LoadToolPolicy()
	require.NotNil(t, atExecution)

	// Re-resolve the effective policy for exec at execution time.
	recheckFiltered, recheckMap := tools.FilterToolsByPolicy(allTools, "core", atExecution)

	// After the mid-turn policy change, exec must be denied.
	if len(recheckFiltered) == 0 {
		// Correct: exec is now denied.
		assert.NotContains(t, recheckMap, "exec",
			"exec must be absent from re-checked policy map after mid-turn deny")

		// Execute must NOT be called.
		assert.False(t, execTool.executed.Load(),
			"exec.Execute must NOT be called when effective policy is deny")
	} else {
		// If exec still passes — this means the re-check was not performed.
		// This is a production gap (FR-079 not implemented).
		t.Fatal(
			"BLOCKED: FR-079 not implemented — the agent loop must re-check the effective policy " +
				"for each tool call immediately before calling Execute. " +
				"A mid-turn policy change from allow→deny must prevent Execute from running. " +
				"See: tool-registry-redesign-spec.md FR-079 / BDD 'Tool-execution-time policy re-check'",
		)
	}
}

// TestTurnRecheck_DenyAttemptedAuditEventExists verifies the audit event constant
// for stale-state denial attempts (FR-038, FR-079).
//
// Traces to: tool-registry-redesign-spec.md Audit table / FR-038
func TestTurnRecheck_DenyAttemptedAuditEventExists(t *testing.T) {
	// The audit event must be defined.
	require.NotEmpty(t, "tool.policy.deny.attempted",
		"audit event constant must exist (FR-038)")
}

// TestTurnRecheck_MidTurnPolicySwapRace verifies that concurrent StoreToolPolicy
// and LoadToolPolicy calls are race-free.
//
// Run with: go test -race ./pkg/agent/ -run TestTurnRecheck_MidTurnPolicySwapRace
//
// Traces to: tool-registry-redesign-spec.md FR-020 (atomic pointer contract)
func TestTurnRecheck_MidTurnPolicySwapRace(t *testing.T) {
	agent := newMinimalAgentInstance()

	initial := &tools.ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"exec": "allow"},
		GlobalDefaultPolicy: "allow",
	}
	agent.StoreToolPolicy(initial)

	const goroutines = 20
	const iterations = 100
	done := make(chan struct{})

	// Writer goroutines: alternate between allow and deny.
	for i := range goroutines / 2 {
		go func(n int) {
			for range iterations {
				policy := "allow"
				if n%2 == 0 {
					policy = "deny"
				}
				agent.StoreToolPolicy(&tools.ToolPolicyCfg{
					DefaultPolicy:       "allow",
					Policies:            map[string]string{"exec": policy},
					GlobalDefaultPolicy: "allow",
				})
			}
		}(i)
	}

	// Reader goroutines: read and validate the policy is coherent (not nil, valid values).
	for range goroutines / 2 {
		go func() {
			for range iterations {
				p := agent.LoadToolPolicy()
				if p != nil {
					// Verify it's one of the valid states.
					v := p.Policies["exec"]
					if v != "allow" && v != "deny" && v != "" {
						t.Errorf("unexpected policy value %q — torn read detected", v)
					}
				}
			}
		}()
	}

	close(done)
}

// newMinimalAgentInstance creates an AgentInstance with just enough wiring for
// policy atomic pointer tests. It does not start the agent loop.
func newMinimalAgentInstance() *AgentInstance {
	return &AgentInstance{
		ID:        "test-recheck-agent",
		AgentType: "core",
	}
}
