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
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Regression coverage for the self-delegation authorization bypass and its
// intentionally-asymmetric counterpart. The delegate tool launches a distinct session
// instance, so delegate(agent_id=self) IS delegation and is ALWAYS denied
// (buildDelegationDenyCheckerForDelegate). The task tools, in contrast, treat a
// self-target as a no-op reassignment to the task's existing owner and correctly
// ALLOW it (buildDelegationDenyCheckerForTaskReassignment).
//
// This file proves BOTH halves end-to-end, not just at the raw checker level:
//   - delegate self-target DENIED — in the checker, through DelegateTool.Execute
//     and through DelegateTool.Execute, with the launcher never reached;
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

// spySessionLauncher records whether Launch was invoked. A denied delegation
// must never reach the session launcher.
type spySessionLauncher struct {
	called bool
}

func (s *spySessionLauncher) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	s.called = true
	return steer.LaunchResult{}, nil
}

func (*spySessionLauncher) Dispatch(context.Context, string, int) (steer.DispatchResult, error) {
	return steer.DispatchResult{}, nil
}

// TestDelegationDenyChecker_SelfTargetDeniedForBackgroundDelegate checks the gate
// in isolation for the delegate tool's background mode: a self-target
// with selfAssignmentExempt=false falls through to the graph lookup and is denied
// with trust_set (no self-edge can exist).
func TestDelegationDenyChecker_SelfTargetDeniedForBackgroundDelegate(t *testing.T) {
	// A real, NON-self edge exists (mia→ray); the self-target must still be denied.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground)

	denial := check(ctxWS(testWS, 0), "mia") // self-target
	if denial == nil {
		t.Fatal("self-targeted background delegate() must be DENIED, got allow (self-delegation bypass)")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial for self-delegation (no self-edge can exist), got: %q (%s)",
			denial.Policy, denial.Reason)
	}
}

// newSelfTargetDelegateTool builds a DelegateTool wired EXACTLY as
// registerSharedTools wires it (the background deny checker uses
// selfAssignmentExempt=false), for caller "mia".
func newSelfTargetDelegateTool() (*tools.DelegateTool, *spySessionLauncher) {
	dt := tools.NewDelegateTool("model", 1000, 0.7)
	spy := &spySessionLauncher{}
	dt.SetSessionLauncher(spy)
	dt.SetDelegationDenyCheckerBackground(
		buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground))
	return dt, spy
}

// TestDelegateTool_SelfTargetDeniedAtExecute drives an actual DelegateTool.Execute
// call with agent_id equal to the caller's own id and asserts it is denied
// end-to-end (not merely that the checker returns non-nil in isolation). The
// launcher must never run.
func TestDelegateTool_SelfTargetDeniedAtExecute(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	dt, spy := newSelfTargetDelegateTool()
	res := dt.Execute(ctxWS(testWS, 0), map[string]any{
		"task":     "attempt self-delegation",
		"agent_id": "mia", // caller's OWN configured id
	})
	if res == nil || !res.IsError {
		t.Fatalf("self-target delegate() must return an error result, got: %+v", res)
	}
	// The structured DelegationFailure payload must carry the trust_set policy.
	if !strings.Contains(res.ForLLM, `"error":"delegation_denied"`) ||
		!strings.Contains(res.ForLLM, `"policy":"trust_set"`) {
		t.Fatalf("expected a trust_set delegation_denied payload, got ForLLM: %q", res.ForLLM)
	}
	if spy.called {
		t.Fatal("launcher MUST NOT run for a denied self-target delegation")
	}
}

// TestDelegateTool_SelfTargetDeniedBeforeLauncher proves that authorization
// runs before the production launch boundary. A denied self-target must not
// create a durable session.
func TestDelegateTool_SelfTargetDeniedBeforeLauncher(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	dt := tools.NewDelegateTool("model", 1000, 0.7)
	spy := &spySessionLauncher{}
	dt.SetSessionLauncher(spy)
	dt.SetDelegationDenyCheckerBackground(
		buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground))

	res := dt.Execute(ctxWS(testWS, 0), map[string]any{
		"task":     "attempt self-delegation",
		"agent_id": "mia",
	})
	if res == nil || !res.IsError {
		t.Fatalf("self-target delegate() must be denied, got: %+v", res)
	}
	if spy.called {
		t.Fatal("launcher must not run for a denied self-target delegation")
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
		// dod is mandatory on every agent-facing task-creation surface
		// (GOAL-FR-021 / operator decision D-C, "criteria + definition-of-done
		// are mandatory at CREATION and EDIT"). This is the only case in this
		// file that must SUCCEED, so it is the only one that has to satisfy the
		// business-rule gates; the denial cases below stop at authorization,
		// which runs first, and deliberately keep omitting them.
		"dod": []any{map[string]any{"kind": "prose", "text": "nothing else broke"}},
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
