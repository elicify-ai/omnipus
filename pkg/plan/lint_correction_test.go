// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// lint_correction_test.go — regression coverage for the second half of UAT
// defect A: members added to a RUNNING plan by a PlanSupervisor correction
// were never linted.
//
// Lint had exactly two call sites, both at APPROVE. plan_correct adds
// brand-new members and brand-new dependency edges to an already-approved
// plan, and no lint ran on that path at all — so the write-set-overlap and
// join-point invariants applied only to the members a plan was born with.
// Combined with the empty-plan hole (lint_empty_plan_test.go), approving an
// empty plan and letting the supervisor populate it bypassed plan-lint
// completely, in two steps, with no error anywhere.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// planMember builds a member with an explicit status, mirroring lint_test.go's
// member() helper but letting a test place a member in `done` (the state every
// member is in when a correction is authored, since the plan judge only runs
// on an all-terminal DAG).
func planMember(id string, status task.Status, blockedBy, writeSet []string) task.Task {
	return task.Task{
		ID:        id,
		Title:     "member " + id,
		Status:    status,
		BlockedBy: blockedBy,
		WriteSet:  writeSet,
		Criteria:  mustCriteria(),
	}
}

// liveDAG reproduces the member set of the plan that exposed this defect in
// UAT (plan UAT-T2-PLAN-A13): four members, all done, with gamma and delta
// mutually parallel (gamma depends on alpha, delta depends on beta, and
// nothing orders gamma against delta).
func liveDAG() []task.Task {
	return []task.Task{
		planMember("alpha", task.StatusDone, nil, []string{"out/alpha.md"}),
		planMember("beta", task.StatusDone, nil, []string{"out/beta.md"}),
		planMember("gamma", task.StatusDone, []string{"alpha"}, []string{"out/gamma.md"}),
		planMember("delta", task.StatusDone, []string{"beta"}, []string{"out/delta.md"}),
	}
}

// TestLintCorrection_RejectsJoinlessTailMember is the defect-A (correction
// half) regression test. It replays the exact correction observed live: the
// PlanSupervisor appended a member ("A13 epsilon") depending on all four
// existing members — two of which are mutually parallel — with is_join=false.
//
// Approve would have refused that member outright. The correction path
// accepted it without a murmur, because it ran no lint at all.
//
// SCOPE OF THIS TEST (H3 review finding — the comment here previously claimed
// more than the test does). It exercises the pure function in this package and
// nothing else: it says what LintCorrection decides, not that anything calls
// it. The ENGINE WIRING — that pkg/agent's validateCorrection actually invokes
// LintCorrection and refuses the correction on its verdict — is a separate
// claim and is pinned separately, by
// TestAppendCorrection_RejectsTailMemberThatFailsPlanLint in
// pkg/agent/plan_engine_correction_lint_test.go. Deleting the lint call from
// validateCorrection leaves every test in THIS file green.
func TestLintCorrection_RejectsJoinlessTailMember(t *testing.T) {
	p := &Plan{ID: "plan-a13"}
	members := liveDAG()

	epsilon := planMember("epsilon", task.StatusInbox, nil, nil)
	epsilon.IsJoin = false // exactly as the supervisor authored it live.

	req := CorrectionRequest{
		Verb:        RevisionAppend,
		TailMembers: []task.Task{epsilon},
		TailEdges: []IntentEdge{
			{FromTaskID: "alpha", ToTaskID: "epsilon"},
			{FromTaskID: "beta", ToTaskID: "epsilon"},
			{FromTaskID: "gamma", ToTaskID: "epsilon"},
			{FromTaskID: "delta", ToTaskID: "epsilon"},
		},
	}

	lerr := LintCorrection(p, members, req)
	if lerr == nil {
		t.Fatal("a correction appending a member that converges >=2 parallel predecessors with " +
			"is_join=false must be REJECTED — approve refuses exactly this member, so a correction " +
			"must too (UAT defect A, correction half)")
	}
	if len(lerr.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d: %+v", len(lerr.Violations), lerr.Violations)
	}
	v := lerr.Violations[0]
	if v.Kind != LintJoinless {
		t.Fatalf("violation kind = %q, want %q", v.Kind, LintJoinless)
	}
	if len(v.MemberIDs) != 1 || v.MemberIDs[0] != "epsilon" {
		t.Fatalf("violation must name the offending tail member, got MemberIDs=%v", v.MemberIDs)
	}
}

// TestLintCorrection_AcceptsAuthoredJoinTailMember is the positive control for
// the test above: the SAME convergence, authored correctly (is_join=true with
// its own criteria), must be accepted. Without this, the test above would also
// pass if LintCorrection simply rejected every correction.
func TestLintCorrection_AcceptsAuthoredJoinTailMember(t *testing.T) {
	p := &Plan{ID: "plan-a13"}

	epsilon := planMember("epsilon", task.StatusInbox, nil, nil)
	epsilon.IsJoin = true // authored join member, carries criteria via planMember.

	req := CorrectionRequest{
		Verb:        RevisionAppend,
		TailMembers: []task.Task{epsilon},
		TailEdges: []IntentEdge{
			{FromTaskID: "alpha", ToTaskID: "epsilon"},
			{FromTaskID: "beta", ToTaskID: "epsilon"},
			{FromTaskID: "gamma", ToTaskID: "epsilon"},
			{FromTaskID: "delta", ToTaskID: "epsilon"},
		},
	}

	if lerr := LintCorrection(p, liveDAG(), req); lerr != nil {
		t.Fatalf("a correctly authored join member must be accepted, got: %v", lerr)
	}
}

// TestLintCorrection_DoneMemberWriteSetDoesNotBlockAppend guards adjustment 2
// in LintCorrection's doc comment. A member that has already finished cannot
// race anything, so appending new work that touches a path a DONE member wrote
// must NOT be rejected as a parallel write-set conflict.
//
// This is the false-positive the naive "just lint the projected set" fix
// introduces, and it would have made corrections fail on ordinary plans.
func TestLintCorrection_DoneMemberWriteSetDoesNotBlockAppend(t *testing.T) {
	p := &Plan{ID: "plan-1"}
	members := []task.Task{
		planMember("done1", task.StatusDone, nil, []string{"out/shared.md"}),
	}
	tail := planMember("new1", task.StatusInbox, nil, []string{"out/shared.md"})

	req := CorrectionRequest{Verb: RevisionAppend, TailMembers: []task.Task{tail}}
	if lerr := LintCorrection(p, members, req); lerr != nil {
		t.Fatalf("appending a member that writes a path an already-DONE member wrote must be "+
			"accepted (they cannot run concurrently), got: %v", lerr)
	}
}

// TestLintCorrection_RejectsOverlapWithLiveMember is the counterpart: the
// done-member exemption must NOT leak into members that can still run. A tail
// member overlapping a member that is still live (next/in_progress) is a real
// concurrent-clobber risk and must be rejected.
//
// Without this, adjustment 2 could be "fixed" by exempting every existing
// member, silently disabling the overlap check on the correction path.
func TestLintCorrection_RejectsOverlapWithLiveMember(t *testing.T) {
	p := &Plan{ID: "plan-1"}

	for _, status := range []task.Status{task.StatusNext, task.StatusInProgress, task.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			members := []task.Task{
				planMember("live1", status, nil, []string{"out/shared.md"}),
			}
			tail := planMember("new1", task.StatusInbox, nil, []string{"out/shared.md"})

			req := CorrectionRequest{Verb: RevisionAppend, TailMembers: []task.Task{tail}}
			lerr := LintCorrection(p, members, req)
			if lerr == nil {
				t.Fatalf("a tail member overlapping a %s member must be rejected — that member can "+
					"still run, so the two can clobber each other", status)
			}
			if lerr.Violations[0].Kind != LintOverlap {
				t.Fatalf("kind = %q, want %q", lerr.Violations[0].Kind, LintOverlap)
			}
		})
	}
}

// TestLintCorrection_SupersedeReplacementMayReuseWriteSet guards adjustment 1.
// Replacing member X with X' that writes the same file is THE canonical
// supersede pattern; the superseded member must be dropped from the projection
// or the verb rejects itself.
func TestLintCorrection_SupersedeReplacementMayReuseWriteSet(t *testing.T) {
	p := &Plan{ID: "plan-1"}
	members := []task.Task{
		planMember("x", task.StatusDone, nil, []string{"out/x.md"}),
		planMember("other", task.StatusDone, nil, []string{"out/other.md"}),
	}
	replacement := planMember("x-prime", task.StatusInbox, nil, []string{"out/x.md"})

	req := CorrectionRequest{
		Verb:               RevisionSupersede,
		SupersededMemberID: "x",
		TailMembers:        []task.Task{replacement},
	}
	if lerr := LintCorrection(p, members, req); lerr != nil {
		t.Fatalf("a supersede replacement reusing the superseded member's write_set must be "+
			"accepted, got: %v", lerr)
	}
}

// TestLintCorrection_DoesNotMutateInputs pins projectCorrectedMembers' purity.
// It edits BlockedBy while applying tail edges, and the slices it is handed are
// the caller's LIVE task-store snapshot and the caller's request payload — a
// stray append into a shared backing array would corrupt plan state as a side
// effect of validating it.
func TestLintCorrection_DoesNotMutateInputs(t *testing.T) {
	p := &Plan{ID: "plan-1"}
	members := []task.Task{
		planMember("a", task.StatusDone, nil, []string{"out/a.md"}),
	}
	// Give the tail member spare capacity in BlockedBy so a careless append
	// would write into this very array rather than reallocating.
	tail := planMember("b", task.StatusInbox, make([]string, 0, 4), nil)

	req := CorrectionRequest{
		Verb:        RevisionAppend,
		TailMembers: []task.Task{tail},
		TailEdges:   []IntentEdge{{FromTaskID: "a", ToTaskID: "b"}},
	}
	if lerr := LintCorrection(p, members, req); lerr != nil {
		t.Fatalf("unexpected violation: %v", lerr)
	}

	if members[0].WriteSet == nil {
		t.Fatal("LintCorrection cleared a DONE member's WriteSet on the caller's own slice; the " +
			"done-member exemption must apply to the projection only")
	}
	if got := len(req.TailMembers[0].BlockedBy); got != 0 {
		t.Fatalf("LintCorrection wrote a tail edge back into the caller's request payload "+
			"(BlockedBy now has %d entries); the projection must be a copy", got)
	}
}

// TestProjectCorrectedMembers_AppliesEdgeDirection pins that an IntentEdge
// {From, To} projects as "To is blocked by From" — the same direction
// buildCorrectionApplyFunc commits it (AddDependency(To, From)).
//
// If the projection reversed the edge, the join check would analyse a DAG the
// commit never builds, and the defect-A test above could pass for the wrong
// reason.
func TestProjectCorrectedMembers_AppliesEdgeDirection(t *testing.T) {
	members := []task.Task{planMember("a", task.StatusDone, nil, nil)}
	tail := planMember("b", task.StatusInbox, nil, nil)

	projected := projectCorrectedMembers(members, CorrectionRequest{
		Verb:        RevisionAppend,
		TailMembers: []task.Task{tail},
		TailEdges:   []IntentEdge{{FromTaskID: "a", ToTaskID: "b"}},
	})

	byID := map[string]task.Task{}
	for _, m := range projected {
		byID[m.ID] = m
	}
	if got := byID["b"].BlockedBy; len(got) != 1 || got[0] != "a" {
		t.Fatalf("edge a->b must make b blocked_by [a], got b.BlockedBy=%v", got)
	}
	if got := byID["a"].BlockedBy; len(got) != 0 {
		t.Fatalf("edge a->b must not touch a's BlockedBy, got %v", got)
	}
}

// --- Pre-existing violations must not lock a plan out of corrections -------
//
// (H2 review finding against the first version of this file's subject, which
// was `return Lint(p, projectCorrectedMembers(members, req))` — a lint of the
// WHOLE projected set, which rejected a correction for violations the plan was
// already carrying and could not repair.)

// brokenDAG is a plan carrying a PRE-EXISTING join-less convergence: gamma
// depends on alpha and beta, which are mutually parallel, and gamma is not an
// authored join member. Approve would have refused this plan; a plan created
// before the lint existed, or populated through the unlinted third writer
// (POST /tasks with a plan_id), carries it anyway.
//
// Every member is `next` rather than `done` so the projection's done-member
// exemption cannot be what makes a case pass.
func brokenDAG() []task.Task {
	alpha := planMember("alpha", task.StatusNext, nil, []string{"out/alpha.md"})
	beta := planMember("beta", task.StatusNext, nil, []string{"out/beta.md"})
	gamma := planMember("gamma", task.StatusNext, []string{"alpha", "beta"}, []string{"out/gamma.md"})
	gamma.IsJoin = false
	return []task.Task{alpha, beta, gamma}
}

// TestLintCorrection_PreExistingViolationIsNotAttributedToTheCorrection is the
// H2 regression test.
//
// A plan carrying a pre-existing join-less convergence parks at PhaseStalled —
// a phase chosen precisely because plan_correct is accepted there, so the park
// "unlocks the mechanism the bug had locked out". Linting the whole projected
// set then re-locked it: the correction was rejected for `gamma`, a member it
// never mentioned, and since no correction verb can repair an existing member
// (supersede requires the target be `done`), every retry failed identically
// until the supervision ladder exhausted and the plan ended
// failed(supervision_unavailable).
func TestLintCorrection_PreExistingViolationIsNotAttributedToTheCorrection(t *testing.T) {
	p := &Plan{ID: "plan-broken"}
	members := brokenDAG()

	// Precondition, asserted rather than assumed: the plan really is carrying
	// a violation. Without this the test could pass on a clean DAG and prove
	// nothing at all.
	base := Lint(p, members)
	if base == nil || len(base.Violations) != 1 || base.Violations[0].Kind != LintJoinless {
		t.Fatalf("precondition: this plan must carry exactly one pre-existing join_less_convergence "+
			"violation, got %v", base)
	}
	if got := base.Violations[0].MemberIDs; len(got) != 1 || got[0] != "gamma" {
		t.Fatalf("precondition: the pre-existing violation must name gamma, got %v", got)
	}

	// The fix correction: append delta, which introduces nothing. It writes
	// its own file and converges nothing.
	delta := planMember("delta", task.StatusInbox, nil, []string{"out/delta.md"})
	req := CorrectionRequest{
		Verb:        RevisionAppend,
		TailMembers: []task.Task{delta},
		TailEdges:   []IntentEdge{{FromTaskID: "gamma", ToTaskID: "delta"}},
	}

	if lerr := LintCorrection(p, members, req); lerr != nil {
		t.Fatalf("a correction that introduces NO new violation must be accepted on a plan that "+
			"already carries one — rejecting it names a member the correction never mentions and "+
			"leaves the plan unfixable by the only mechanism that could fix it; got: %v", lerr)
	}
}

// TestLintCorrection_StillRejectsANewViolationOnAnAlreadyBrokenPlan is the
// other half of the H2 rule, and the one that stops it from degenerating into
// "a broken plan accepts anything". The SAME plan, a correction that adds a
// second join-less convergence of its own: still rejected, and blamed on the
// member that actually caused it.
func TestLintCorrection_StillRejectsANewViolationOnAnAlreadyBrokenPlan(t *testing.T) {
	p := &Plan{ID: "plan-broken"}

	epsilon := planMember("epsilon", task.StatusInbox, nil, nil)
	epsilon.IsJoin = false
	req := CorrectionRequest{
		Verb:        RevisionAppend,
		TailMembers: []task.Task{epsilon},
		TailEdges: []IntentEdge{
			{FromTaskID: "alpha", ToTaskID: "epsilon"},
			{FromTaskID: "beta", ToTaskID: "epsilon"},
		},
	}

	lerr := LintCorrection(p, brokenDAG(), req)
	if lerr == nil {
		t.Fatal("a correction that introduces a NEW join_less_convergence must still be rejected, " +
			"even on a plan that already carries one — otherwise one pre-existing violation would " +
			"disable the check for every correction that follows it")
	}
	if len(lerr.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation (the introduced one), got %d: %+v",
			len(lerr.Violations), lerr.Violations)
	}
	v := lerr.Violations[0]
	if v.Kind != LintJoinless {
		t.Fatalf("violation kind = %q, want %q", v.Kind, LintJoinless)
	}
	if len(v.MemberIDs) != 1 || v.MemberIDs[0] != "epsilon" {
		t.Fatalf("the rejection must name the member the CORRECTION added, not the pre-existing "+
			"one; got MemberIDs=%v", v.MemberIDs)
	}
}

// TestLintCorrection_CannotLaunderANewOverlapBehindAPreExistingOne is the
// anti-gaming test for the write-set half. A plan already has a parallel
// overlap between alpha and beta on the same path; a correction adds a third
// member writing that same path. Each new colliding PAIR is its own violation
// with its own fingerprint, so none of them can hide behind the existing one.
func TestLintCorrection_CannotLaunderANewOverlapBehindAPreExistingOne(t *testing.T) {
	p := &Plan{ID: "plan-overlap"}
	members := []task.Task{
		planMember("alpha", task.StatusNext, nil, []string{"out/shared.md"}),
		planMember("beta", task.StatusNext, nil, []string{"out/shared.md"}),
	}
	if base := Lint(p, members); base == nil || base.Violations[0].Kind != LintOverlap {
		t.Fatalf("precondition: this plan must already carry a write_set_overlap violation, got %v", base)
	}

	tail := planMember("gamma", task.StatusInbox, nil, []string{"out/shared.md"})
	req := CorrectionRequest{Verb: RevisionAppend, TailMembers: []task.Task{tail}}

	lerr := LintCorrection(p, members, req)
	if lerr == nil {
		t.Fatal("a tail member writing a path two existing parallel members already collide on must " +
			"be rejected — the pre-existing collision excuses alpha and beta, never the new member")
	}
	for _, v := range lerr.Violations {
		if len(v.MemberIDs) == 2 && v.MemberIDs[0] == "alpha" && v.MemberIDs[1] == "beta" {
			t.Fatalf("the pre-existing alpha/beta overlap must NOT be reported as this correction's "+
				"fault; got violations %+v", lerr.Violations)
		}
		found := false
		for _, id := range v.MemberIDs {
			if id == "gamma" {
				found = true
			}
		}
		if !found {
			t.Fatalf("every reported violation must name the added member gamma; got %+v", v)
		}
	}
	if len(lerr.Violations) != 2 {
		t.Fatalf("expected both new pairs (alpha,gamma) and (beta,gamma) to be reported, got %d: %+v",
			len(lerr.Violations), lerr.Violations)
	}
}

// TestLintCorrection_SupersedeThatUnordersTwoMembersIsRejected is the anti-
// gaming test for the one correction effect that can REMOVE ordering rather
// than add it.
//
// Edges are only ever added by a correction, so an existing pair can lose its
// parallelism but never gain it — with exactly one exception: dropping the
// superseded member orphans the edges that ran THROUGH it. Here alpha and beta
// both write out/x.md and are ordered only via the done member `mid`. Nothing
// is wrong with the plan as it stands. Superseding `mid` un-orders them, and
// that overlap is the correction's doing — so the baseline must keep `mid`,
// or the projection would excuse the violation it just created.
func TestLintCorrection_SupersedeThatUnordersTwoMembersIsRejected(t *testing.T) {
	p := &Plan{ID: "plan-supersede"}
	members := []task.Task{
		planMember("alpha", task.StatusNext, nil, []string{"out/x.md"}),
		planMember("mid", task.StatusDone, []string{"alpha"}, nil),
		planMember("beta", task.StatusNext, []string{"mid"}, []string{"out/x.md"}),
	}
	if base := Lint(p, members); base != nil {
		t.Fatalf("precondition: the plan as it stands must be CLEAN (alpha and beta are ordered "+
			"through mid), got %v", base)
	}

	replacement := planMember("mid-prime", task.StatusInbox, nil, nil)
	req := CorrectionRequest{
		Verb:               RevisionSupersede,
		SupersededMemberID: "mid",
		TailMembers:        []task.Task{replacement},
	}

	lerr := LintCorrection(p, members, req)
	if lerr == nil {
		t.Fatal("superseding the member that ORDERED two overlapping write_sets leaves them parallel " +
			"and colliding; that violation is introduced by the correction and must be rejected")
	}
	if lerr.Violations[0].Kind != LintOverlap {
		t.Fatalf("kind = %q, want %q", lerr.Violations[0].Kind, LintOverlap)
	}
}

// TestLintCorrection_EmptyResultIsRejectedEvenOnAnAlreadyEmptyPlan pins the
// one violation the pre-existing-violation diff never forgives. The empty-plan
// check is an ARITY PRECONDITION on the RESULT, not a pairwise invariant, and
// "the plan was already empty" is no reason to accept a correction that leaves
// it empty — which would be the one shape of laundering the diff could
// otherwise permit.
func TestLintCorrection_EmptyResultIsRejectedEvenOnAnAlreadyEmptyPlan(t *testing.T) {
	p := &Plan{ID: "plan-empty"}

	lerr := LintCorrection(p, nil, CorrectionRequest{Verb: RevisionAppend})
	if lerr == nil {
		t.Fatal("a correction that leaves a plan with zero members must be rejected — the arity " +
			"precondition is never suppressed as pre-existing")
	}
	if lerr.Violations[0].Kind != LintEmptyPlan {
		t.Fatalf("kind = %q, want %q", lerr.Violations[0].Kind, LintEmptyPlan)
	}
}
