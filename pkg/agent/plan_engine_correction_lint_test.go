// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_engine_correction_lint_test.go — the ENGINE-side coverage for
// plan-lint on the correction path (H3 review finding).
//
// pkg/plan's lint_correction_test.go says what plan.LintCorrection decides. It
// does not say that anything CALLS it — every test there invokes the pure
// function directly, so deleting the three-line LintCorrection block from
// validateCorrection left the whole suite green and the fix silently absent.
// An unlinted member added to a running plan is precisely the live defect
// (member `epsilon`, is_join=false, converging four predecessors, two of them
// mutually parallel) that approve would have refused, so the wiring is the
// part that matters and it had no test at all.
//
// These tests drive AppendCorrection — the real entrypoint, with its real
// intent-log commit — and fail if that call is removed.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// lintTailMember builds a well-formed tail member: every non-lint
// precondition validateCorrection checks (id, title, criteria) is satisfied,
// so a rejection can only come from plan-lint.
func lintTailMember(id string) task.Task {
	return task.Task{
		ID: id, Title: id, WorkspaceID: "ws", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{planProseCriterion(id + " work is done")},
	}
}

// TestAppendCorrection_RejectsTailMemberThatFailsPlanLint is the H3 wiring
// test: remove the plan.LintCorrection call from validateCorrection and this
// fails.
//
// It replays the live correction. alpha and beta are mutually parallel (no
// ordering between them); the appended member converges both and is not an
// authored join member. Approve refuses exactly this shape, so a correction
// must too.
func TestAppendCorrection_RejectsTailMemberThatFailsPlanLint(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	mustSeedAwaitingCorrection(t, h, "p-lint", doneMember("alpha"), doneMember("beta"))

	epsilon := lintTailMember("epsilon")
	epsilon.IsJoin = false // exactly as the supervisor authored it live.

	_, err := h.pe.AppendCorrection(ctx, "p-lint", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed a single member could assemble both streams",
		TailMembers:         []task.Task{epsilon},
		TailEdges: []IntentEdge{
			{FromTaskID: "alpha", ToTaskID: "epsilon"},
			{FromTaskID: "beta", ToTaskID: "epsilon"},
		},
		Reason: "assemble the two parallel streams",
	})

	if err == nil {
		t.Fatal("AppendCorrection accepted a tail member that converges two parallel predecessors " +
			"with is_join=false — approve refuses this member outright, so the correction path " +
			"must call plan-lint and refuse it too (UAT defect A, correction half)")
	}
	if !strings.Contains(err.Error(), "rejected by plan-lint") {
		t.Fatalf("error = %q, want it to name plan-lint as the rejecting rule — a rejection from "+
			"some other precondition would mean the lint still is not wired", err)
	}
	if !errors.Is(err, plan.ErrValidation) {
		t.Fatalf("error %v must unwrap to plan.ErrValidation so the calling seam maps it to a 400, "+
			"not a 500", err)
	}

	// Nothing may be half-applied: the lint runs before any intent is
	// appended, so the plan must be exactly as the wake found it.
	if _, gerr := h.tasks.Get("epsilon"); !errors.Is(gerr, task.ErrNotFound) {
		t.Fatalf("the rejected tail member was created anyway (get epsilon: %v) — a rejected "+
			"correction must leave no work behind", gerr)
	}
	got, err := h.plans.Get("p-lint")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.PlanPhase != plan.PhaseAwaitingSupervision {
		t.Fatalf("plan_phase = %q, want %q — a rejected correction must not move the plan out of "+
			"the phase where it can still be corrected", got.PlanPhase, plan.PhaseAwaitingSupervision)
	}
}

// TestAppendCorrection_AcceptsACorrectlyAuthoredJoinTailMember is the positive
// control for the test above: the SAME convergence, authored correctly, must
// commit. Without it, the test above would also pass if the engine had simply
// started refusing every correction.
func TestAppendCorrection_AcceptsACorrectlyAuthoredJoinTailMember(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	mustSeedAwaitingCorrection(t, h, "p-lint-ok", doneMember("alpha"), doneMember("beta"))

	epsilon := lintTailMember("epsilon")
	epsilon.IsJoin = true // authored join member, and lintTailMember gives it criteria.

	if _, err := h.pe.AppendCorrection(ctx, "p-lint-ok", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed the two streams needed no assembly step",
		TailMembers:         []task.Task{epsilon},
		TailEdges: []IntentEdge{
			{FromTaskID: "alpha", ToTaskID: "epsilon"},
			{FromTaskID: "beta", ToTaskID: "epsilon"},
		},
		Reason: "assemble the two parallel streams",
	}); err != nil {
		t.Fatalf("a correctly authored join member must be accepted: %v", err)
	}
	if _, err := h.tasks.Get("epsilon"); err != nil {
		t.Fatalf("the accepted tail member was not created: %v", err)
	}
}

// TestAppendCorrection_PreExistingViolationDoesNotBlockTheCorrection is the
// engine-level H2 test: the park promises that a stalled plan can be
// corrected, and a plan carrying a violation it cannot repair must not be
// locked out of that promise.
//
// gamma converges alpha and beta without being a join member — a violation
// that predates this correction, that the correction does not mention, and
// that no correction verb can repair (supersede requires the target be `done`
// and would replace it, not fix it). Linting the whole projected set rejected
// the correction for gamma; every retry failed identically; the supervision
// ladder exhausted and the plan ended failed(supervision_unavailable).
func TestAppendCorrection_PreExistingViolationDoesNotBlockTheCorrection(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	gamma := doneMember("gamma")
	gamma.BlockedBy = []string{"alpha", "beta"}
	gamma.IsJoin = false
	mustSeedAwaitingCorrection(t, h, "p-broken", doneMember("alpha"), doneMember("beta"), gamma)

	// Precondition, asserted rather than assumed: the plan really is carrying
	// a violation, so acceptance below is the diff working and not an empty
	// member set or a mis-built DAG.
	members, err := h.tasks.List(task.Filter{PlanID: "p-broken"})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	seeded, err := h.plans.Get("p-broken")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	base := plan.Lint(seeded, members)
	if base == nil || base.Violations[0].Kind != plan.LintJoinless {
		t.Fatalf("precondition: the seeded plan must already fail plan-lint with a "+
			"join_less_convergence, got %v", base)
	}

	if _, err := h.pe.AppendCorrection(ctx, "p-broken", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed the assembled output was complete",
		TailMembers:         []task.Task{lintTailMember("delta")},
		TailEdges:           []IntentEdge{{FromTaskID: "gamma", ToTaskID: "delta"}},
		Reason:              "add the missing follow-up work",
	}); err != nil {
		t.Fatalf("a correction that introduces no new violation must be accepted on a plan that "+
			"already carries one; rejecting it names a member the correction never mentions and "+
			"locks the plan out of the very mechanism the park exists to offer: %v", err)
	}
	if _, err := h.tasks.Get("delta"); err != nil {
		t.Fatalf("the accepted tail member was not created: %v", err)
	}
}
