// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Regression coverage for self-delegation and its intentionally-asymmetric
// counterpart under FR-014 (session-core U5a).
//
// FR-014 supersedes the retired ADR-091 rule "delegate(agent_id=self) is ALWAYS
// denied". An ELIGIBLE native MAIN/WORKER caller may now self-delegate with NO
// self-edge — the delegate tool launches a distinct session instance, and that
// is permitted for an eligible caller. The task tools remain as before: a
// self-target is a no-op reassignment to the task's existing owner and is
// ALLOWED without consulting the graph (buildDelegationDenyCheckerForTaskReassignment,
// exempt=true).
//
// This file proves both halves end-to-end through the REAL production wiring:
//   - delegate self-target ALLOWED for an eligible native caller (mia) — via
//     the caller's own registerSharedTools-built delegate tool, with the
//     launcher reached (TestDelegateTool_EligibleSelfTargetLaunchesAtExecute);
//   - a named, un-edged NON-self target is still DENIED before the launcher
//     runs (TestDelegateTool_UnedgedTargetDeniedBeforeLauncher) — the live
//     "authorization precedes launch" property, kept on the case that still
//     refuses;
//   - task self-target ALLOWED — through the real registerSharedTools
//     construction path (TestTaskCreate_SelfAssignmentAllowedThroughRegisterSharedTools).
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
// must never reach the session launcher; an ALLOWED one must reach it exactly
// once and be observable (called). It returns a well-formed result so an
// allowed self-delegation can run to completion through DelegateTool.Execute
// without spawning a real child.
type spySessionLauncher struct {
	called bool
	req    steer.LaunchRequest
}

func (s *spySessionLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	s.called = true
	s.req = req
	return steer.LaunchResult{SessionID: "spy-child-session", Generation: 1}, nil
}

func (*spySessionLauncher) Dispatch(context.Context, string, int) (steer.DispatchResult, error) {
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: 1}, nil
}

// TestDelegationDenyChecker_EligibleSelfTargetAllowedForBackgroundDelegate pins
// FR-014 at the gate's own level (via the production wiring's delegate tool):
// an eligible native caller (mia) self-targeting under the background mode is
// PERMITTED with no self-edge. The retired ADR-091 rule denied this with
// trust_set; FR-014 replaces it.
func TestDelegationDenyChecker_EligibleSelfTargetAllowedForBackgroundDelegate(t *testing.T) {
	dt, spy := u5aDelegateTool(t, "mia", []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	res := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task":     "self helper",
		"agent_id": "mia", // self-target
	})
	if res == nil || res.IsError {
		t.Fatalf("FR-014: an eligible self-targeted background delegate() must be ALLOWED; got: %+v", res)
	}
	if !spy.called {
		t.Fatal("FR-014: the allowed self-delegation must reach the launcher")
	}
}

// TestDelegateTool_EligibleSelfTargetLaunchesAtExecute drives an actual
// DelegateTool.Execute — the caller's OWN production-wired tool — with
// agent_id equal to the caller's own id and asserts FR-014 end-to-end: the
// self-delegation is ALLOWED and reaches the launcher (a helper is started).
func TestDelegateTool_EligibleSelfTargetLaunchesAtExecute(t *testing.T) {
	dt, spy := u5aDelegateTool(t, "mia", []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	res := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task":     "self-delegation",
		"agent_id": "mia", // caller's OWN configured id
	})
	if res == nil || res.IsError {
		t.Fatalf("FR-014: an eligible self-target delegate() must succeed, got: %+v", res)
	}
	if !spy.called {
		t.Fatal("FR-014: the allowed self-delegation must reach the launcher")
	}
}

// TestDelegateTool_UnedgedTargetDeniedBeforeLauncher proves authorization still
// runs before the production launch boundary on the case that still refuses: a
// NAMED, un-edged non-self target is denied and must not create a durable
// session. (The eligible self case now reaches the launcher — pinned above —
// so this test keeps the live "deny precedes launch" property on the refusal
// path.)
func TestDelegateTool_UnedgedTargetDeniedBeforeLauncher(t *testing.T) {
	dt, spy := u5aDelegateTool(t, "mia", []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	res := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task":     "cross delegation",
		"agent_id": "ava", // real agent, no mia→ava edge
	})
	if res == nil || !res.IsError {
		t.Fatalf("an un-edged non-self delegate() must be denied, got: %+v", res)
	}
	if spy.called {
		t.Fatal("launcher must not run for a denied delegation")
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
