// Omnipus — plan_correct shared-type / shared-cap tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- FR-004: the correction types are ONE type, not two that match ---------

// engineSideConsumer stands in for the engine seam. Its parameters are spelled
// with the pkg/plan types — the same spelling pkg/agent's AppendCorrection now
// carries via its aliases. If tools.CorrectionCaller / tools.CorrectionRequest
// were separate declarations with a matching field set (the state this move
// replaced), Go would refuse to pass a value of one where the other is named:
// two defined types are never assignable to each other, however identical.
func engineSideConsumer(caller plan.CorrectionCaller, req plan.CorrectionRequest) (string, plan.RevisionVerb, int) {
	return caller.AgentID, req.Verb, len(req.TailMembers)
}

// TestCorrectionTypesAreOnePlanType proves the OUTCOME of the FR-004 move: a
// correction value built in pkg/tools is accepted by a pkg/plan-typed
// signature with NO conversion, in both directions, and carries its fields
// through intact.
//
// The no-conversion call is the load-bearing assertion and it is enforced by
// the compiler; the reflect check is the runtime witness that they share one
// identity rather than one shape — reflect.Type is per-declaration, so two
// structurally identical types declared in different packages compare unequal
// here even though every field matches.
func TestCorrectionTypesAreOnePlanType(t *testing.T) {
	t.Parallel()

	caller := CorrectionCaller{AgentID: PlanSupervisorAgentID, SessionID: "session:plan-1"}
	req := CorrectionRequest{
		Verb:                plan.RevisionSupersede,
		FalsifiedAssumption: "assumed the first parser pass covered floats",
		Reason:              "the float fixtures never ran",
		SupersededMemberID:  "m-done",
		TailMembers:         []task.Task{{ID: "m-new", Title: "handle floats"}},
		TailEdges:           []plan.IntentEdge{{FromTaskID: "m-done", ToTaskID: "m-new"}},
	}

	// tools value -> plan-typed parameters, no conversion.
	gotAgent, gotVerb, gotMembers := engineSideConsumer(caller, req)
	if gotAgent != PlanSupervisorAgentID {
		t.Errorf("caller.AgentID crossed the seam as %q, want %q", gotAgent, PlanSupervisorAgentID)
	}
	if gotVerb != plan.RevisionSupersede {
		t.Errorf("req.Verb crossed the seam as %q, want %q", gotVerb, plan.RevisionSupersede)
	}
	if gotMembers != 1 {
		t.Errorf("req.TailMembers crossed the seam with %d entries, want 1", gotMembers)
	}

	// plan value -> tools-typed variables, no conversion.
	var backCaller CorrectionCaller = plan.CorrectionCaller{AgentID: "jim"}
	var backReq CorrectionRequest = plan.CorrectionRequest{Verb: plan.RevisionAbandon}
	if backCaller.AgentID != "jim" || backReq.Verb != plan.RevisionAbandon {
		t.Errorf("round trip lost data: caller=%+v req.Verb=%q", backCaller, backReq.Verb)
	}

	// One identity, not one shape.
	pairs := []struct {
		name       string
		tools, pln reflect.Type
	}{
		{"CorrectionCaller", reflect.TypeOf(CorrectionCaller{}), reflect.TypeOf(plan.CorrectionCaller{})},
		{"CorrectionRequest", reflect.TypeOf(CorrectionRequest{}), reflect.TypeOf(plan.CorrectionRequest{})},
	}
	for _, p := range pairs {
		if p.tools != p.pln {
			t.Errorf("%s is two types, not one: tools=%s (%s) plan=%s (%s)",
				p.name, p.tools, p.tools.PkgPath(), p.pln, p.pln.PkgPath())
		}
		if p.tools.PkgPath() != "github.com/elicify-ai/omnipus/pkg/plan" {
			t.Errorf("%s should be declared in pkg/plan, but reflect reports %q", p.name, p.tools.PkgPath())
		}
	}
}

// --- FR-046 / D-06: the caps come from pkg/plan ---------------------------

// TestPlanCorrect_CapsAreThePkgPlanConstants proves the OUTCOME of the cap
// move: the threshold the tool actually enforces IS pkg/plan's constant.
//
// Every size in this test is derived from plan.Max* rather than written as a
// literal, and each case asserts BOTH sides of the boundary — a payload of
// exactly the cap reaches the engine, one byte or one entry more never does.
// A local copy of the number that drifted from pkg/plan's would move one of
// those two boundaries and fail here; asserting only the rejection would not
// catch a cap that had merely been re-typed at the same value.
func TestPlanCorrect_CapsAreThePkgPlanConstants(t *testing.T) {
	t.Parallel()

	// A 4-byte rune, so a title at the BYTE cap stays well under the task
	// store's separate 200-RUNE cap and the byte cap is what decides.
	const wideRune = "\U00010348"
	titleAt := strings.Repeat(wideRune, plan.MaxMemberTitleBytes/len(wideRune))
	if len(titleAt) != plan.MaxMemberTitleBytes {
		t.Fatalf("test setup: title is %d bytes, want exactly %d", len(titleAt), plan.MaxMemberTitleBytes)
	}

	membersAt := make([]any, 0, plan.MaxTailMembers)
	for i := 0; i < plan.MaxTailMembers; i++ {
		m := tailMemberArg(fmt.Sprintf("member %d", i), "c")
		m["ref"] = fmt.Sprintf("r%d", i)
		membersAt = append(membersAt, m)
	}
	membersOver := append(append([]any{}, membersAt...), tailMemberArg("one too many", "c"))

	// An acyclic edge set over the tail refs: every edge runs from a lower
	// index to a higher one, so no ordering of them can close a cycle.
	edgePairs := make([]any, 0, plan.MaxTailEdges+1)
	for i := 0; i < plan.MaxTailMembers && len(edgePairs) <= plan.MaxTailEdges; i++ {
		for j := i + 1; j < plan.MaxTailMembers && len(edgePairs) <= plan.MaxTailEdges; j++ {
			edgePairs = append(edgePairs, map[string]any{
				"from": fmt.Sprintf("r%d", i), "to": fmt.Sprintf("r%d", j),
			})
		}
	}
	if len(edgePairs) < plan.MaxTailEdges+1 {
		t.Fatalf("test setup: only built %d edges, need %d", len(edgePairs), plan.MaxTailEdges+1)
	}
	edgesAt := edgePairs[:plan.MaxTailEdges]
	edgesOver := edgePairs[:plan.MaxTailEdges+1]

	cases := []struct {
		name string
		// at is a payload sitting exactly ON the cap; over exceeds it by one.
		at, over map[string]any
		// wantInError is the number the rejection must name.
		wantInError int
	}{
		{
			name:        "tail_members",
			at:          map[string]any{"tail_members": membersAt},
			over:        map[string]any{"tail_members": membersOver},
			wantInError: plan.MaxTailMembers,
		},
		{
			name:        "tail_edges",
			at:          map[string]any{"tail_members": membersAt, "tail_edges": edgesAt},
			over:        map[string]any{"tail_members": membersAt, "tail_edges": edgesOver},
			wantInError: plan.MaxTailEdges,
		},
		{
			name:        "member title bytes",
			at:          map[string]any{"tail_members": []any{tailMemberArg(titleAt, "c")}},
			over:        map[string]any{"tail_members": []any{tailMemberArg(titleAt+"x", "c")}},
			wantInError: plan.MaxMemberTitleBytes,
		},
		{
			name: "falsified_assumption bytes",
			at: map[string]any{
				"falsified_assumption": strings.Repeat("a", plan.MaxTextBytes),
				"tail_members":         []any{tailMemberArg("t", "c")},
			},
			over: map[string]any{
				"falsified_assumption": strings.Repeat("a", plan.MaxTextBytes+1),
				"tail_members":         []any{tailMemberArg("t", "c")},
			},
			wantInError: plan.MaxTextBytes,
		},
		{
			name: "reason bytes",
			at: map[string]any{
				"reason":       strings.Repeat("a", plan.MaxTextBytes),
				"tail_members": []any{tailMemberArg("t", "c")},
			},
			over: map[string]any{
				"reason":       strings.Repeat("a", plan.MaxTextBytes+1),
				"tail_members": []any{tailMemberArg("t", "c")},
			},
			wantInError: plan.MaxTextBytes,
		},
	}

	base := func(extra map[string]any, planID string) map[string]any {
		args := map[string]any{
			"plan_id":              planID,
			"verb":                 "append",
			"falsified_assumption": "assumed the tail was complete",
		}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	for _, tc := range cases {
		t.Run(tc.name+" exactly at the cap is accepted", func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			spy := &correctionSpy{}
			res := f.tool(spy).Execute(supervisorCtx(), base(tc.at, f.planID))
			if res.IsError {
				t.Fatalf("a payload exactly at plan.Max%s was rejected: %s", tc.name, res.ForLLM)
			}
			if len(spy.calls) != 1 {
				t.Fatalf("payload at the cap reached the engine %d times, want 1", len(spy.calls))
			}
		})
		t.Run(tc.name+" one over the cap is rejected", func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			spy := &correctionSpy{}
			before := f.snapshot(t)
			res := f.tool(spy).Execute(supervisorCtx(), base(tc.over, f.planID))
			if !res.IsError {
				t.Fatalf("a payload one over plan.Max%s was accepted: %s", tc.name, res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("a rejected payload still reached the engine %d times", len(spy.calls))
			}
			if want := fmt.Sprintf("%d", tc.wantInError); !strings.Contains(res.ForLLM, want) {
				t.Errorf("rejection does not name pkg/plan's limit %s: %s", want, res.ForLLM)
			}
			if after := f.snapshot(t); after != before {
				t.Errorf("a rejected correction mutated the plan:\n before=%s\n  after=%s", before, after)
			}
		})
	}
}
