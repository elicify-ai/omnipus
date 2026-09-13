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
// Fails against the pre-fix engine, where validateCorrection ended at
// validateCorrectionTailEdges and never called into this package.
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
