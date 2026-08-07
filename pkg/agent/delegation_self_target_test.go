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
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Regression coverage for the self-delegation authorization bypass and its
// intentionally-asymmetric counterpart. The delegate TOOL spawns a real sub-turn
// instance, so delegate(agent_id=self) IS delegation and is ALWAYS denied
// (buildDelegationDenyCheckerForDelegate). The task tools, in contrast, treat a
// self-target as a no-op reassignment to the task's existing owner and correctly
// ALLOW it (buildDelegationDenyCheckerForTaskReassignment).
//
// This file proves BOTH halves end-to-end, not just at the raw checker level:
//   - delegate self-target DENIED — in the checker, through DelegateTool.Execute
//     (background + await), and with the depth resolver never reached;
//   - task self-target ALLOWED — through the real registerSharedTools construction
//     path (TestTaskCreate_SelfAssignmentAllowedThroughRegisterSharedTools);
//   - the cross-workspace task gate (NewSysagentDelegationDeny) enforces the same
//     asymmetry (TestNewSysagentDelegationDeny_SelfAllowedNonSelfGated).
//
// create_task has a SECOND refusal — an unresolvable calling agent — which is
// not a delegation decision at all and must never be mistaken for one. It is
// pinned separately by TestTaskCreate_NoPrincipalRefusedThroughRegisterSharedTools
// so the trust_set assertions above keep distinguishing the two.
//
// The task-mode-in-isolation "self-assignment IS allowed" property is also pinned by
// TestDelegationDenyChecker_SelfAssignmentSkipsGraph (delegation_enforce_test.go),
// which must stay green.

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
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

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
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

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
		buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground))
	dt.SetDelegationDenyCheckerAwait(
		buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait))
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
		buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground))

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

// TestTaskCreate_SelfAssignmentAllowedThroughRegisterSharedTools proves the OTHER
// half of the asymmetry end-to-end: the task tools MUST still exempt self-assignment.
// It builds the tools through the REAL construction path (NewAgentLoop →
// registerSharedTools), retrieves the agent's actual create_task tool (carrying the
// deny checker registerSharedTools wired — buildDelegationDenyCheckerForTaskReassignment),
// and drives create_task(agent_id=self) end-to-end, asserting success.
//
// This closes the gap pr-test-analyzer proved: swapping the task-tool wiring to the
// delegate variant (exempt=false — the copy-paste mistake this whole fix guards
// against) would deny this self-assignment, and this test would fail. A non-self,
// un-edged target is the control: it is real delegation and must still be denied.
func TestTaskCreate_SelfAssignmentAllowedThroughRegisterSharedTools(t *testing.T) {
	const agentID = "mia"
	// The default workspace graph has NO mia→mia edge (none can exist). The task
	// tools' exempt short-circuit must allow self-assignment WITHOUT consulting it.
	// mia→ray (task) exists only so the control non-self case has a real graph.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"task"}, nil),
	})
	al, _ := wireTestLoopWithGraph(t, agentID)

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil {
		t.Fatalf("agent %q not registered after loop init", agentID)
	}
	tool, ok := inst.Tools.Get("create_task")
	if !ok {
		t.Fatal("create_task tool not registered on agent (task tools require a task store)")
	}

	// Self-assignment: must SUCCEED (not delegation). WithAgentID mirrors real
	// dispatch (the tool registry always injects the calling agent's own ID
	// before Execute) — needed here because criteria authorship (FR-6/D5,
	// review r1 M5) is server-set from ToolAgentID(ctx), never left blank.
	res := tool.Execute(tools.WithAgentID(context.Background(), agentID), map[string]any{
		"title":    "self task",
		"prompt":   "do the thing myself",
		"agent_id": agentID, // SELF
		"criteria": []any{map[string]any{"kind": "prose", "text": "the thing is done"}},
	})
	if res == nil {
		t.Fatal("nil result from create_task")
	}
	if res.IsError {
		t.Fatalf("self-assignment create_task must SUCCEED (self-reassignment is not delegation), "+
			"got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"task_id"`) {
		t.Fatalf("expected a created task_id payload for self-assignment, got: %s", res.ForLLM)
	}

	// Control — non-self, un-edged target (no mia→ava edge): real delegation, DENIED.
	//
	// WithAgentID here for the same reason as the self case above, and it is
	// load-bearing rather than cosmetic: create_task fails CLOSED on an
	// unresolvable caller BEFORE it consults the delegation gate, so driving this
	// control with a bare context.Background() asserts on the empty-principal
	// refusal and never reaches the trust_set path the control exists to pin.
	// Production always supplies a principal — every dispatch path that can reach
	// this tool stamps one (loop.go's turnCtx, the task executor's taskCtx, the
	// judge's callCtx).
	//
	// Criteria are deliberately omitted: authorization is checked before the
	// business-rule validation, so the trust_set denial must still win here.
	denRes := tool.Execute(tools.WithAgentID(context.Background(), agentID), map[string]any{
		"title":    "cross task",
		"prompt":   "hand off to ava",
		"agent_id": "ava",
	})
	if denRes == nil || !denRes.IsError {
		t.Fatalf("non-self un-edged create_task must be DENIED, got: %+v", denRes)
	}
	if !strings.Contains(denRes.ForLLM, `"policy":"trust_set"`) {
		t.Fatalf("expected trust_set denial for un-edged non-self assignment, got: %s", denRes.ForLLM)
	}

	// A denied delegation must persist nothing: exactly the self task survives.
	rows, err := GetTaskStore(al).List(task.Filter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "self task" {
		t.Fatalf("expected exactly the self-assigned task on disk, got %d rows: %+v", len(rows), rows)
	}
}

// TestTaskCreate_NoPrincipalRefusedThroughRegisterSharedTools pins create_task's
// OTHER refusal — an unresolvable calling agent — as a DISTINCT outcome from the
// trust_set denial above, through the same real registerSharedTools wiring.
//
// The two are not interchangeable, and the ordering (principal guard first) is
// load-bearing. The delegation gate's caller identity is baked in at WIRING time
// (registerSharedTools hands buildDelegationDenyCheckerForTaskReassignment the
// agent's own configured id), whereas the FR-037 provenance stamp and the
// criteria authorship are read from the CONTEXT principal. So with no principal
// the gate does not fail — it returns ALLOW, having authorized a caller the write
// path cannot name; the create would only collapse later at Store.CreateByAgent's
// own empty-agent-id rejection, after an authorization decision had been made for
// an agent that does not exist, and after parseCriteriaArgs had stamped a
// criterion author of "".
//
// The target is deliberately "ray", which the seed DOES task-edge from mia: the
// delegation gate would allow it, so the principal guard is the only thing that
// can refuse this call, and deleting the guard flips this test red rather than
// leaving it green on a different denial.
func TestTaskCreate_NoPrincipalRefusedThroughRegisterSharedTools(t *testing.T) {
	const agentID = "mia"
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"task"}, nil), // TRUSTED target — the gate would allow
	})
	al, _ := wireTestLoopWithGraph(t, agentID)

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil {
		t.Fatalf("agent %q not registered after loop init", agentID)
	}
	tool, ok := inst.Tools.Get("create_task")
	if !ok {
		t.Fatal("create_task tool not registered on agent (task tools require a task store)")
	}

	// Both shapes of "no principal": never injected, and injected blank.
	for name, ctx := range map[string]context.Context{
		"absent":     context.Background(),
		"whitespace": tools.WithAgentID(context.Background(), "   "),
	} {
		res := tool.Execute(ctx, map[string]any{
			"title":    "orphan task",
			"prompt":   "work nobody can be credited with",
			"agent_id": "ray", // trusted: the delegation gate is NOT what refuses this
			"criteria": []any{map[string]any{"kind": "prose", "text": "the thing is done"}},
		})
		if res == nil || !res.IsError {
			t.Fatalf("ctx=%s: create_task with an unresolvable caller must be refused, got: %+v", name, res)
		}
		if !strings.Contains(res.ForLLM, "cannot resolve the calling agent") {
			t.Fatalf("ctx=%s: expected the unresolvable-principal refusal, got: %s", name, res.ForLLM)
		}
		// It must not masquerade as a delegation denial: that is a decision ABOUT
		// a named caller, and this call has none to decide about.
		if strings.Contains(res.ForLLM, "delegation_denied") {
			t.Fatalf("ctx=%s: an unresolvable principal must not be reported as a delegation denial: %s",
				name, res.ForLLM)
		}
	}

	rows, err := GetTaskStore(al).List(task.Filter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected zero tasks persisted for an unresolvable principal, got %d: %+v", len(rows), rows)
	}
}

// TestNewSysagentDelegationDeny_SelfAllowedNonSelfGated gives the cross-workspace
// task-tool delegation gate (AgentLoop.NewSysagentDelegationDeny) its first direct
// coverage — pr-test-analyzer confirmed it had ZERO. It backs the sysagent
// create_task_in_workspace / update_task_in_workspace tools, so it must enforce the
// SAME task-mode policy as the plain task tools: self-target and empty-target are
// no-op reassignments (allowed), a trusted non-self target is allowed, an un-edged
// non-self target is denied trust_set.
func TestNewSysagentDelegationDeny_SelfAllowedNonSelfGated(t *testing.T) {
	const callerID = "jim"
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "ava", []string{"task"}, nil), // jim→ava trusted (task)
	})
	al, _ := wireTestLoopWithGraph(t, callerID)
	deny := al.NewSysagentDelegationDeny()

	// Self-target: a no-op task reassignment to the owner — allowed.
	if d := deny(ctxWS(testWS, 0), callerID, callerID); d != nil {
		t.Fatalf("self-target task reassignment must be allowed, got deny: %+v", d)
	}
	// Empty target: no-op assignment — allowed.
	if d := deny(ctxWS(testWS, 0), callerID, ""); d != nil {
		t.Fatalf("empty target must be a no-op (allowed), got deny: %+v", d)
	}
	// Trusted non-self (jim→ava task edge present): allowed.
	if d := deny(ctxWS(testWS, 0), callerID, "ava"); d != nil {
		t.Fatalf("jim→ava is task-edged and must be allowed, got deny: %+v", d)
	}
	// Un-edged non-self (no jim→ray edge): DENIED trust_set.
	d := deny(ctxWS(testWS, 0), callerID, "ray")
	if d == nil {
		t.Fatal("jim→ray (no edge) must be DENIED, got allow")
	}
	if d.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial for un-edged cross-workspace target, got: %q (%s)",
			d.Policy, d.Reason)
	}
}
