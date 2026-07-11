// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Regression coverage for the self-delegation authorization bypass: the
// self-assignment carve-out in buildDelegationDenyChecker is correct ONLY for the
// task tools (reassigning a task to its existing owner is not delegation). The
// delegate TOOL spawns a real sub-turn instance, so delegate(agent_id=self) IS
// delegation and MUST be graph-gated — and since workspace.DelegationEdge.Validate
// forbids any self-edge, that gate permanently DENIES it (trust_set). These tests
// pin selfAssignmentExempt=false (the real delegate-tool wiring) and prove the
// denial both in the checker in isolation and end-to-end through DelegateTool.Execute.
//
// The complementary "task-mode self-assignment IS allowed" property is pinned by
// TestDelegationDenyChecker_SelfAssignmentSkipsGraph (delegation_enforce_test.go),
// which must stay green — the two carve-out halves are intentionally asymmetric.

// spyDelegateSpawner records whether SpawnSubTurn was invoked. A DENIED delegation
// must never reach the spawner.
type spyDelegateSpawner struct {
	called bool
}

func (s *spyDelegateSpawner) SpawnSubTurn(ctx context.Context, cfg tools.SubTurnConfig) (*tools.ToolResult, error) {
	s.called = true
	return tools.NewToolResult("spawned (should not happen for a denied delegation)"), nil
}

// TestDelegationDenyChecker_SelfTargetDeniedForBackgroundDelegate checks the gate
// in isolation for the delegate tool's background (async=true) mode: a self-target
// with selfAssignmentExempt=false falls through to the graph lookup and is denied
// with trust_set (no self-edge can exist).
func TestDelegationDenyChecker_SelfTargetDeniedForBackgroundDelegate(t *testing.T) {
	// A real, NON-self edge exists (mia→ray); the self-target must still be denied.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})
	check := buildDelegationDenyChecker("mia", config.AgentDefaults{}, config.DelegationModeBackground, false)

	denial := check(ctxWS(testWS, 0), "mia") // self-target
	if denial == nil {
		t.Fatal("self-targeted background delegate() must be DENIED, got allow (self-delegation bypass)")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial for self-delegation (no self-edge can exist), got: %q (%s)",
			denial.Policy, denial.Reason)
	}
}

// TestDelegationDenyChecker_SelfTargetDeniedForAwaitDelegate is the await
// (async=false) counterpart of the background test above.
func TestDelegationDenyChecker_SelfTargetDeniedForAwaitDelegate(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"await"}, nil),
	})
	check := buildDelegationDenyChecker("mia", config.AgentDefaults{}, config.DelegationModeAwait, false)

	denial := check(ctxWS(testWS, 0), "mia") // self-target
	if denial == nil {
		t.Fatal("self-targeted await delegate() must be DENIED, got allow (self-delegation bypass)")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial for self-delegation, got: %q (%s)",
			denial.Policy, denial.Reason)
	}
}

// newSelfTargetDelegateTool builds a DelegateTool wired EXACTLY as
// registerSharedTools wires it (background + await deny checkers with
// selfAssignmentExempt=false, plus the depth resolver), for caller "mia".
func newSelfTargetDelegateTool() (*tools.DelegateTool, *spyDelegateSpawner) {
	dt := tools.NewDelegateTool("model", 1000, 0.7)
	spy := &spyDelegateSpawner{}
	dt.SetSpawner(spy)
	dt.SetDelegationDenyCheckerBackground(
		buildDelegationDenyChecker("mia", config.AgentDefaults{}, config.DelegationModeBackground, false))
	dt.SetDelegationDenyCheckerAwait(
		buildDelegationDenyChecker("mia", config.AgentDefaults{}, config.DelegationModeAwait, false))
	dt.SetDelegationDepthResolver(buildDelegationDepthResolver("mia", config.AgentDefaults{}))
	return dt, spy
}

// TestDelegateTool_SelfTargetDeniedAtExecute drives an actual DelegateTool.Execute
// call with agent_id equal to the caller's own id and asserts it is denied
// end-to-end (not merely that the checker returns non-nil in isolation), for both
// the background and await modes. The spawner must never run.
func TestDelegateTool_SelfTargetDeniedAtExecute(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background", "await"}, nil),
	})

	for _, async := range []bool{true, false} {
		dt, spy := newSelfTargetDelegateTool()
		res := dt.Execute(ctxWS(testWS, 0), map[string]any{
			"task":     "attempt self-delegation",
			"agent_id": "mia", // caller's OWN configured id
			"async":    async,
		})
		if res == nil || !res.IsError {
			t.Fatalf("async=%v: self-target delegate() must return an error result, got: %+v", async, res)
		}
		// The structured DelegationFailure payload must carry the trust_set policy.
		if !strings.Contains(res.ForLLM, `"error":"delegation_denied"`) ||
			!strings.Contains(res.ForLLM, `"policy":"trust_set"`) {
			t.Fatalf("async=%v: expected a trust_set delegation_denied payload, got ForLLM: %q", async, res.ForLLM)
		}
		if spy.called {
			t.Fatalf("async=%v: spawner MUST NOT run for a denied self-target delegation", async)
		}
	}
}

// TestDelegateTool_SelfTargetDeniedBeforeDepthResolver proves the CHECK ORDERING
// that makes the depth resolver's own self-target branch (delegation_depth.go)
// unreachable dead code: the deny checker runs FIRST in DelegateTool.executeRun and
// returns on denial BEFORE the depth resolver is consulted. A self-targeted
// delegate() is therefore denied without the depth resolver ever running. If a
// future change reorders the checks (depth resolver before deny checker), the spy
// resolver would be invoked and this test fails — the guard the delegation_depth.go
// comment references.
func TestDelegateTool_SelfTargetDeniedBeforeDepthResolver(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	dt := tools.NewDelegateTool("model", 1000, 0.7)
	spy := &spyDelegateSpawner{}
	dt.SetSpawner(spy)
	dt.SetDelegationDenyCheckerBackground(
		buildDelegationDenyChecker("mia", config.AgentDefaults{}, config.DelegationModeBackground, false))

	depthResolverCalled := false
	realResolver := buildDelegationDepthResolver("mia", config.AgentDefaults{})
	dt.SetDelegationDepthResolver(func(ctx context.Context, target string) *int {
		depthResolverCalled = true
		return realResolver(ctx, target)
	})

	res := dt.Execute(ctxWS(testWS, 0), map[string]any{
		"task":     "attempt self-delegation",
		"agent_id": "mia",
		"async":    true,
	})
	if res == nil || !res.IsError {
		t.Fatalf("self-target delegate() must be denied, got: %+v", res)
	}
	if depthResolverCalled {
		t.Fatal("depth resolver was reached for a DENIED self-delegation — the deny checker must run FIRST " +
			"(check-ordering regression); the resolver's self-branch must stay unreachable dead code")
	}
	if spy.called {
		t.Fatal("spawner must not run for a denied self-target delegation")
	}
}
