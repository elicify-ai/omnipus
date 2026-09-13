// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// mkCriterion builds a minimal, valid-shaped AcceptanceCriterion for these
// tests — id and status are what every test varies; the rest is fixed
// boilerplate so each test case stays readable.
func mkCriterion(id string, status task.CriterionStatus) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		ID:       id,
		Kind:     task.KindProse,
		Judgment: task.JudgmentBoolean,
		Text:     "criterion " + id,
		Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		Status:   status,
	}
}

func mkVerdict(entries ...task.CriterionVerdict) *task.JudgeVerdict {
	return &task.JudgeVerdict{
		ID:           "v1",
		Scope:        task.VerdictScopeTask,
		Round:        1,
		PerCriterion: entries,
	}
}

// --- GOAL-FR-036: met -> CritMet, unmet -> CritUnmet -----------------------

func TestProjectVerdictOntoCriteria_MetAndUnmet(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		mkCriterion("c1", task.CritPending),
		mkCriterion("c2", task.CritPending),
	}
	verdict := mkVerdict(
		task.CriterionVerdict{CriterionID: "c1", Met: true, Reason: "looks good"},
		task.CriterionVerdict{CriterionID: "c2", Met: false, Reason: "missing X"},
	)

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if len(out) != 2 {
		t.Fatalf("expected 2 criteria back, got %d", len(out))
	}
	if out[0].Status != task.CritMet {
		t.Errorf("c1: want status %q, got %q", task.CritMet, out[0].Status)
	}
	if out[1].Status != task.CritUnmet {
		t.Errorf("c2: want status %q, got %q", task.CritUnmet, out[1].Status)
	}
	if stats.Applied != 2 {
		t.Errorf("want Applied=2, got %d", stats.Applied)
	}
	if stats.EphemeralNoOps != 0 || stats.UnresolvedNoOps != 0 || stats.Unchanged != 0 || stats.DuplicateIDs != 0 {
		t.Errorf("want every other counter at 0, got %+v", stats)
	}
	// Input slice must never be mutated in place (pure function contract).
	if criteria[0].Status != task.CritPending || criteria[1].Status != task.CritPending {
		t.Errorf("input slice was mutated in place: %+v", criteria)
	}
}

// --- GOAL-FR-038: a criterion with no machine-checkable form (prose, no
// Check/Behavior) is fully eligible for `met` on the Judge's Met bool alone.

func TestProjectVerdictOntoCriteria_SubjectiveCriterionReachesMet(t *testing.T) {
	subjective := task.AcceptanceCriterion{
		ID:       "subj-1",
		Kind:     task.KindProse,
		Judgment: task.JudgmentBoolean,
		Text:     "the summary reads clearly to a non-engineer",
		Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		Status:   task.CritPending,
		// Check and Behavior are both nil — no machine-checkable form at all.
	}
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: "subj-1", Met: true, Reason: "reads clearly"})

	out, stats := projectVerdictOntoCriteria([]task.AcceptanceCriterion{subjective}, verdict)

	if out[0].Status != task.CritMet {
		t.Fatalf("a subjective criterion with Met=true must reach CritMet; got %q", out[0].Status)
	}
	if stats.Applied != 1 {
		t.Errorf("want Applied=1, got %d", stats.Applied)
	}
}

// --- GOAL-FR-031: an ephemeral criterion id (soft-tier-implicit,
// goal-condition) is a logged, counted no-op — never a store write, because
// there is no persisted record to write back to.

func TestProjectVerdictOntoCriteria_SoftTierIsLoggedNoOp(t *testing.T) {
	// The real, persisted criteria list is empty (mirrors the task path
	// when usedSoftTier is true: t.Criteria is empty, the ephemeral
	// SoftTierCriterion is what was actually judged).
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: softTierCriterionID, Met: true, Reason: "trusted"})

	out, stats := projectVerdictOntoCriteria(nil, verdict)

	if len(out) != 0 {
		t.Fatalf("want an empty (unchanged) output, got %+v", out)
	}
	if stats.EphemeralNoOps != 1 {
		t.Errorf("want EphemeralNoOps=1, got %+v", stats)
	}
	if stats.Applied != 0 || stats.UnresolvedNoOps != 0 {
		t.Errorf("a soft-tier verdict entry must never be counted as Applied or Unresolved: %+v", stats)
	}
}

func TestProjectVerdictOntoCriteria_GoalConditionFallbackIsLoggedNoOp(t *testing.T) {
	// goal_compile.go's compiledGoalCriteriaFor pre-Phase-2 fallback: a bare
	// condition synthesizes ID "goal-condition", never persisted onto the
	// goal record's real Criteria/DoD lists.
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: "goal-condition", Met: true, Reason: "condition satisfied"})

	out, stats := projectVerdictOntoCriteria(nil, verdict)

	if len(out) != 0 {
		t.Fatalf("want an empty (unchanged) output, got %+v", out)
	}
	if stats.EphemeralNoOps != 1 {
		t.Errorf("want EphemeralNoOps=1 for the goal-condition fallback id, got %+v", stats)
	}
}

// A floor-DoD item (goal-dod-floor-*) is NOT ephemeral — it IS persisted, so
// its verdict must project normally, not as a no-op.

func TestProjectVerdictOntoCriteria_FloorDoDItemProjectsNormally(t *testing.T) {
	floor := mkCriterion("goal-dod-floor-no-secrets", task.CritPending)
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: "goal-dod-floor-no-secrets", Met: false, Reason: "leaked a key"})

	out, stats := projectVerdictOntoCriteria([]task.AcceptanceCriterion{floor}, verdict)

	if out[0].Status != task.CritUnmet {
		t.Fatalf("a floor-DoD item must project normally (not as an ephemeral no-op); got status %q", out[0].Status)
	}
	if stats.Applied != 1 || stats.EphemeralNoOps != 0 {
		t.Errorf("want Applied=1, EphemeralNoOps=0, got %+v", stats)
	}
}

// --- C-52 rule 2: a verdict entry matching nothing in the input list is a
// counted, warn-logged no-op — never a silent skip.

func TestProjectVerdictOntoCriteria_UnresolvedEntryIsCountedNotDropped(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("c1", task.CritPending)}
	verdict := mkVerdict(
		task.CriterionVerdict{CriterionID: "c1", Met: true},
		task.CriterionVerdict{CriterionID: "does-not-exist", Met: true},
	)

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if out[0].Status != task.CritMet {
		t.Fatalf("c1 should still have been resolved despite the stray entry; got %q", out[0].Status)
	}
	if stats.UnresolvedNoOps != 1 {
		t.Errorf("want UnresolvedNoOps=1 for the stray id, got %+v", stats)
	}
	if stats.Applied != 1 {
		t.Errorf("want Applied=1 (only c1), got %d", stats.Applied)
	}
}

// --- C-52 rule 3 / NFR-2: a criterion the verdict says nothing about keeps
// its EXISTING status — never reset to pending, never defaulted to met.

func TestProjectVerdictOntoCriteria_AbsentFromVerdictKeepsExistingStatus(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		mkCriterion("c1", task.CritMet),     // already met from a PRIOR adjudication
		mkCriterion("c2", task.CritPending), // never judged yet
	}
	// This verdict only resolves c2 — c1 is not mentioned at all.
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: "c2", Met: false})

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if out[0].Status != task.CritMet {
		t.Fatalf("c1 (absent from the verdict) must keep its existing status met; got %q", out[0].Status)
	}
	if out[1].Status != task.CritUnmet {
		t.Fatalf("c2 (present in the verdict) must be projected; got %q", out[1].Status)
	}
	if stats.Unchanged != 1 {
		t.Errorf("want Unchanged=1 for c1, got %+v", stats)
	}
}

// --- C-52 rule 4: each call is a full overwrite of the statuses it DOES
// resolve — a previously-met criterion that the LATEST verdict now says is
// unmet must flip to unmet (last verdict wins), never stay stuck at met.

func TestProjectVerdictOntoCriteria_FullOverwriteLastVerdictWins(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("c1", task.CritMet)}
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: "c1", Met: false, Reason: "regressed"})

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if out[0].Status != task.CritUnmet {
		t.Fatalf("a fresh verdict must overwrite a stale met status; got %q", out[0].Status)
	}
	if stats.Applied != 1 {
		t.Errorf("want Applied=1, got %d", stats.Applied)
	}
}

// --- OQ-15: a duplicate criterion id WITHIN one verdict's per_criterion
// list — no rejection policy exists, so the LAST entry by index wins, and
// the collision is counted.

func TestProjectVerdictOntoCriteria_DuplicateVerdictEntry_LastByIndexWins(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("c1", task.CritPending)}
	verdict := mkVerdict(
		task.CriterionVerdict{CriterionID: "c1", Met: true, Reason: "first pass"},
		task.CriterionVerdict{CriterionID: "c1", Met: false, Reason: "the judge changed its mind"},
	)

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if out[0].Status != task.CritUnmet {
		t.Fatalf("the LAST entry by index (Met=false) must win; got %q", out[0].Status)
	}
	if stats.DuplicateIDs != 1 {
		t.Errorf("want DuplicateIDs=1, got %+v", stats)
	}
}

// --- OQ-15: a duplicate criterion id in the INPUT list itself — every
// matching entry is projected identically, and the collision is counted.

func TestProjectVerdictOntoCriteria_DuplicateInputID(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		mkCriterion("dup", task.CritPending),
		mkCriterion("dup", task.CritPending),
	}
	verdict := mkVerdict(task.CriterionVerdict{CriterionID: "dup", Met: true})

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if out[0].Status != task.CritMet || out[1].Status != task.CritMet {
		t.Fatalf("every entry sharing the duplicate id must be projected identically, got %+v", out)
	}
	if stats.DuplicateIDs != 1 {
		t.Errorf("want DuplicateIDs=1 for the input-side collision, got %+v", stats)
	}
	// Applied counts entries actually written — both duplicates were written.
	if stats.Applied != 2 {
		t.Errorf("want Applied=2 (both duplicate entries written), got %d", stats.Applied)
	}
}

// --- NFR-2: absence of a verdict is never synthesized into a status
// change. A nil verdict is a pure, zero-stats no-op.

func TestProjectVerdictOntoCriteria_NilVerdictIsANoOp(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("c1", task.CritPending)}

	out, stats := projectVerdictOntoCriteria(criteria, nil)

	if out[0].Status != task.CritPending {
		t.Fatalf("a nil verdict must never change status; got %q", out[0].Status)
	}
	if stats != (ProjectionStats{}) {
		t.Errorf("want zero stats for a nil verdict, got %+v", stats)
	}
}

// A verdict with a non-nil pointer but an empty PerCriterion slice is the
// same "nothing to project" case as nil — must not panic and must not
// change anything.

func TestProjectVerdictOntoCriteria_EmptyPerCriterionIsANoOp(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("c1", task.CritPending)}
	verdict := &task.JudgeVerdict{ID: "v1", Scope: task.VerdictScopeTask, PerCriterion: nil}

	out, stats := projectVerdictOntoCriteria(criteria, verdict)

	if out[0].Status != task.CritPending {
		t.Fatalf("an empty per_criterion list must never change status; got %q", out[0].Status)
	}
	if stats != (ProjectionStats{}) {
		t.Errorf("want zero stats, got %+v", stats)
	}
}

// --- GOAL-FR-041: the goal write path de-unions the Judge's flattened
// criteria ∪ dod verdict back onto its two originating lists by id.

func TestProjectGoalVerdict_DeUnionsCriteriaAndDoD(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("crit-1", task.CritPending)}
	dod := []task.AcceptanceCriterion{
		mkCriterion("dod-1", task.CritPending),
		mkCriterion("goal-dod-floor-no-secrets", task.CritPending),
	}
	verdict := mkVerdict(
		task.CriterionVerdict{CriterionID: "crit-1", Met: true},
		task.CriterionVerdict{CriterionID: "dod-1", Met: false},
		task.CriterionVerdict{CriterionID: "goal-dod-floor-no-secrets", Met: true},
	)

	updatedCriteria, updatedDoD, stats := projectGoalVerdict(criteria, dod, verdict)

	if len(updatedCriteria) != 1 || updatedCriteria[0].Status != task.CritMet {
		t.Fatalf("crit-1 must land on the CRITERIA list as met; got %+v", updatedCriteria)
	}
	if len(updatedDoD) != 2 {
		t.Fatalf("want 2 dod entries back, got %d", len(updatedDoD))
	}
	if updatedDoD[0].Status != task.CritUnmet {
		t.Errorf("dod-1 must land on the DOD list as unmet; got %q", updatedDoD[0].Status)
	}
	if updatedDoD[1].Status != task.CritMet {
		t.Errorf("the floor DoD item must project normally; got %q", updatedDoD[1].Status)
	}
	if stats.Applied != 3 {
		t.Errorf("want Applied=3 across both lists, got %+v", stats)
	}
	// De-union correctness itself: neither call may have reported the
	// OTHER list's ids as unresolved (that would mean the split leaked).
	if stats.UnresolvedNoOps != 0 {
		t.Errorf("de-union leaked — an id meant for the other list was reported unresolved: %+v", stats)
	}
}

// The ephemeral pre-Phase-2 fallback id must still resolve as a no-op even
// through the goal path's de-union split, not as an "unresolved" entry.

func TestProjectGoalVerdict_EphemeralGoalConditionStillNoOps(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("crit-1", task.CritPending)}
	dod := []task.AcceptanceCriterion{mkCriterion("dod-1", task.CritPending)}
	verdict := mkVerdict(
		task.CriterionVerdict{CriterionID: "crit-1", Met: true},
		task.CriterionVerdict{CriterionID: "goal-condition", Met: true},
	)

	_, _, stats := projectGoalVerdict(criteria, dod, verdict)

	if stats.EphemeralNoOps != 1 {
		t.Errorf("want EphemeralNoOps=1 for the goal-condition id, got %+v", stats)
	}
	if stats.UnresolvedNoOps != 0 {
		t.Errorf("the ephemeral id must never be double-counted as unresolved: %+v", stats)
	}
}

func TestProjectGoalVerdict_NilVerdictLeavesBothListsUnchanged(t *testing.T) {
	criteria := []task.AcceptanceCriterion{mkCriterion("crit-1", task.CritMet)}
	dod := []task.AcceptanceCriterion{mkCriterion("dod-1", task.CritUnmet)}

	updatedCriteria, updatedDoD, stats := projectGoalVerdict(criteria, dod, nil)

	if updatedCriteria[0].Status != task.CritMet || updatedDoD[0].Status != task.CritUnmet {
		t.Fatalf("a nil verdict must leave both lists exactly as they were: criteria=%+v dod=%+v", updatedCriteria, updatedDoD)
	}
	if stats != (ProjectionStats{}) {
		t.Errorf("want zero stats for a nil verdict, got %+v", stats)
	}
}

// --- GOAL-FR-040: the SAME projection function is what all three write
// paths (goal, task, plan) call — proven here by exercising it against a
// representative shape from each scope in one test.

func TestProjectionRunsOnAllThreeScopes(t *testing.T) {
	// Task scope: task_executor.go::adjudicateClaim projects t.Criteria.
	taskCriteria := []task.AcceptanceCriterion{mkCriterion("task-c1", task.CritPending)}
	taskVerdict := mkVerdict(task.CriterionVerdict{CriterionID: "task-c1", Met: true})
	taskOut, taskStats := projectVerdictOntoCriteria(taskCriteria, taskVerdict)
	if taskOut[0].Status != task.CritMet || taskStats.Applied != 1 {
		t.Errorf("task scope: want c1 met, Applied=1; got status=%q stats=%+v", taskOut[0].Status, taskStats)
	}

	// Plan scope: plan_engine.go::applyJudgeRoundOutcome projects plan.DoD
	// (a single list, no de-union — mirrors the task-scope call exactly).
	planDoD := []task.AcceptanceCriterion{mkCriterion("plan-dod-1", task.CritPending)}
	planVerdict := mkVerdict(task.CriterionVerdict{CriterionID: "plan-dod-1", Met: false})
	planOut, planStats := projectVerdictOntoCriteria(planDoD, planVerdict)
	if planOut[0].Status != task.CritUnmet || planStats.Applied != 1 {
		t.Errorf("plan scope: want dod-1 unmet, Applied=1; got status=%q stats=%+v", planOut[0].Status, planStats)
	}

	// Goal scope: goal_triggers.go::runGoalAdjudication de-unions criteria
	// and dod via projectGoalVerdict.
	goalCriteria := []task.AcceptanceCriterion{mkCriterion("goal-c1", task.CritPending)}
	goalDoD := []task.AcceptanceCriterion{mkCriterion("goal-dod-1", task.CritPending)}
	goalVerdict := mkVerdict(
		task.CriterionVerdict{CriterionID: "goal-c1", Met: true},
		task.CriterionVerdict{CriterionID: "goal-dod-1", Met: true},
	)
	goalOutC, goalOutD, goalStats := projectGoalVerdict(goalCriteria, goalDoD, goalVerdict)
	if goalOutC[0].Status != task.CritMet || goalOutD[0].Status != task.CritMet || goalStats.Applied != 2 {
		t.Errorf("goal scope: want both met, Applied=2; got criteria=%+v dod=%+v stats=%+v", goalOutC, goalOutD, goalStats)
	}
}

// --- ProjectionStats.add: the merge helper projectGoalVerdict relies on.

func TestProjectionStats_Add(t *testing.T) {
	a := ProjectionStats{Applied: 1, EphemeralNoOps: 2, UnresolvedNoOps: 3, Unchanged: 4, DuplicateIDs: 5}
	b := ProjectionStats{Applied: 10, EphemeralNoOps: 20, UnresolvedNoOps: 30, Unchanged: 40, DuplicateIDs: 50}

	got := a.add(b)
	want := ProjectionStats{Applied: 11, EphemeralNoOps: 22, UnresolvedNoOps: 33, Unchanged: 44, DuplicateIDs: 55}
	if got != want {
		t.Fatalf("add: want %+v, got %+v", want, got)
	}
}
