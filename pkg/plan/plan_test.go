// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// allStates enumerates every canonical Plan state, for exhaustive matrix
// coverage below.
var allStates = []State{StateDraft, StateApproved, StateRunning, StateDone, StateFailed} //nolint:gochecknoglobals

// TestPlan_StateTransitions exercises every legal + illegal (from, to) pair
// in the canonical matrix (spec Part A "State machine" / SD-A2), asserting
// that failed and done are BOTH terminal/frozen — no failed->running,
// failed->approved, done->anything-but-done, etc. — then exercises the same
// guard end-to-end through Store.Update, including the lifecycle-timestamp
// stamping side effects.
func TestPlan_StateTransitions(t *testing.T) {
	for _, from := range allStates {
		for _, to := range allStates {
			from, to := from, to
			legal := legalPlanTransitions[from][to]
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				err := ValidateStateTransition(from, to)
				if legal && err != nil {
					t.Fatalf("%s -> %s should be legal, got error: %v", from, to, err)
				}
				if !legal {
					if err == nil {
						t.Fatalf("%s -> %s should be illegal, got nil error", from, to)
					}
					if !errors.Is(err, ErrIllegalPlanTransition) {
						t.Fatalf("%s -> %s: expected ErrIllegalPlanTransition, got %v", from, to, err)
					}
				}
			})
		}
	}

	// Explicit frozen-terminal assertions, called out by name because they
	// are the crux of F1 r2 ("a failed plan is NOT retried; author a new
	// plan") and the done-is-terminal invariant it mirrors.
	terminalFrom := []State{StateDone, StateFailed}
	nonNoOpTargets := []State{StateDraft, StateApproved, StateRunning}
	for _, from := range terminalFrom {
		for _, to := range nonNoOpTargets {
			if err := ValidateStateTransition(from, to); !errors.Is(err, ErrIllegalPlanTransition) {
				t.Fatalf("%s -> %s must be rejected (terminal/frozen), got %v", from, to, err)
			}
		}
	}
	// done and failed reject transitioning into EACH OTHER too.
	if err := ValidateStateTransition(StateDone, StateFailed); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("done -> failed must be rejected, got %v", err)
	}
	if err := ValidateStateTransition(StateFailed, StateDone); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("failed -> done must be rejected, got %v", err)
	}
	// No-ops on both terminal states are legal.
	if err := ValidateStateTransition(StateDone, StateDone); err != nil {
		t.Fatalf("done -> done (no-op) should be legal, got %v", err)
	}
	if err := ValidateStateTransition(StateFailed, StateFailed); err != nil {
		t.Fatalf("failed -> failed (no-op) should be legal, got %v", err)
	}

	// running never goes backward to draft or approved.
	if err := ValidateStateTransition(StateRunning, StateDraft); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("running -> draft must be rejected, got %v", err)
	}
	if err := ValidateStateTransition(StateRunning, StateApproved); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("running -> approved must be rejected, got %v", err)
	}
	// draft never jumps straight to running/done/failed.
	if err := ValidateStateTransition(StateDraft, StateRunning); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("draft -> running must be rejected, got %v", err)
	}
	if err := ValidateStateTransition(StateDraft, StateDone); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("draft -> done must be rejected, got %v", err)
	}
	if err := ValidateStateTransition(StateDraft, StateFailed); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("draft -> failed must be rejected, got %v", err)
	}
	// approved reverts to draft (revoke) — legal.
	if err := ValidateStateTransition(StateApproved, StateDraft); err != nil {
		t.Fatalf("approved -> draft (revoke) should be legal, got %v", err)
	}

	// --- End-to-end through Store.Update, including stamping ---
	s := newStore(t)
	p := mkPlan("Transition Plan", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ApprovedAt != "" || p.StartedAt != "" || p.CompletedAt != "" {
		t.Fatalf("fresh draft plan should have no lifecycle timestamps stamped: %+v", p)
	}

	approved := StateApproved
	updated, err := s.Update(p.ID, Patch{State: &approved})
	if err != nil {
		t.Fatalf("draft->approved via Update: %v", err)
	}
	if updated.State != StateApproved || updated.ApprovedAt == "" {
		t.Fatalf("approved transition not stamped: %+v", updated)
	}
	if updated.StartedAt != "" || updated.CompletedAt != "" {
		t.Fatalf("approved transition must not stamp Started/CompletedAt: %+v", updated)
	}

	running := StateRunning
	updated, err = s.Update(p.ID, Patch{State: &running})
	if err != nil {
		t.Fatalf("approved->running via Update: %v", err)
	}
	if updated.State != StateRunning || updated.StartedAt == "" || updated.LastActivityAt == "" || !updated.ActiveLoop {
		t.Fatalf("running transition not stamped: %+v", updated)
	}

	done := StateDone
	updated, err = s.Update(p.ID, Patch{State: &done})
	if err != nil {
		t.Fatalf("running->done via Update: %v", err)
	}
	if updated.State != StateDone || updated.CompletedAt == "" || updated.ActiveLoop {
		t.Fatalf("done transition not stamped (CompletedAt set, ActiveLoop cleared): %+v", updated)
	}

	// done is frozen: done->failed must be rejected through Update too, and
	// the plan must remain unmodified on disk.
	failed := StateFailed
	if _, err := s.Update(p.ID, Patch{State: &failed}); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("done->failed via Update must be rejected, got %v", err)
	}
	reloaded, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get after rejected transition: %v", err)
	}
	if reloaded.State != StateDone {
		t.Fatalf("plan state must remain done after rejected transition, got %q", reloaded.State)
	}
}

// TestPlan_StateTransitions_FromRunningToFailed independently proves the
// running->failed edge (judge-rounds-exhausted / brake path) is legal and
// stamps CompletedAt + clears ActiveLoop, mirroring the running->done case
// above but for the failure exit.
func TestPlan_StateTransitions_FromRunningToFailed(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Fail Plan", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, running, failed := StateApproved, StateRunning, StateFailed
	if _, err := s.Update(p.ID, Patch{State: &approved}); err != nil {
		t.Fatalf("draft->approved: %v", err)
	}
	if _, err := s.Update(p.ID, Patch{State: &running}); err != nil {
		t.Fatalf("approved->running: %v", err)
	}
	reason := FailedReasonJudgeRoundsExhausted
	updated, err := s.Update(p.ID, Patch{State: &failed, FailedReason: &reason})
	if err != nil {
		t.Fatalf("running->failed: %v", err)
	}
	if updated.State != StateFailed || updated.CompletedAt == "" || updated.ActiveLoop {
		t.Fatalf("failed transition not stamped correctly: %+v", updated)
	}
	if updated.FailedReason != FailedReasonJudgeRoundsExhausted {
		t.Fatalf("failed_reason not applied: %+v", updated)
	}

	// failed is now frozen: no outbound edge succeeds, not even back to
	// running.
	if _, err := s.Update(p.ID, Patch{State: &running}); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("failed->running must be rejected, got %v", err)
	}
	if _, err := s.Update(p.ID, Patch{State: &approved}); !errors.Is(err, ErrIllegalPlanTransition) {
		t.Fatalf("failed->approved must be rejected, got %v", err)
	}
}

// fakeTaskLister is a minimal TaskLister stub for ComputeProgress tests —
// pkg/plan deliberately depends only on this interface, not a concrete
// *task.Store, so this test proves that decoupling actually works (no import
// of pkg/task's Store constructor needed here).
type fakeTaskLister struct {
	tasks []task.Task
	err   error
}

func (f *fakeTaskLister) List(filter task.Filter) ([]task.Task, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []task.Task
	for _, t := range f.tasks {
		if filter.PlanID != "" && t.PlanID != filter.PlanID {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func TestPlan_ComputeProgress(t *testing.T) {
	lister := &fakeTaskLister{tasks: []task.Task{
		{ID: "t1", PlanID: "plan-1", Status: task.StatusDone},
		{ID: "t2", PlanID: "plan-1", Status: task.StatusInProgress},
		{ID: "t3", PlanID: "plan-1", Status: task.StatusDone},
		{ID: "t4", PlanID: "plan-2", Status: task.StatusDone}, // different plan
	}}

	done, total, progress, err := ComputeProgress("plan-1", lister)
	if err != nil {
		t.Fatalf("ComputeProgress: %v", err)
	}
	if done != 2 || total != 3 {
		t.Fatalf("ComputeProgress: got done=%d total=%d, want done=2 total=3", done, total)
	}
	wantProgress := 2.0 / 3.0
	if progress != wantProgress {
		t.Fatalf("ComputeProgress: got progress=%v, want %v", progress, wantProgress)
	}

	// An empty plan (no member tasks) is 0% done, not 100%.
	done, total, progress, err = ComputeProgress("plan-empty", lister)
	if err != nil {
		t.Fatalf("ComputeProgress empty: %v", err)
	}
	if done != 0 || total != 0 || progress != 0 {
		t.Fatalf("ComputeProgress empty: got done=%d total=%d progress=%v, want all 0", done, total, progress)
	}
}

func TestPlan_DoD_ShapeValidation(t *testing.T) {
	s := newStore(t)
	p := mkPlan("DoD Plan", "ws-1", "agent-a")
	p.DoD = []task.AcceptanceCriterion{
		{
			Kind:   task.KindProse,
			Text:   "the feature works end to end",
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "dapicom"},
		},
	}
	if err := s.Create(p); err != nil {
		t.Fatalf("Create with valid DoD: %v", err)
	}
	if len(p.DoD) != 1 || p.DoD[0].ID == "" {
		t.Fatalf("DoD not normalized (missing server-set ID): %+v", p.DoD)
	}
	if p.DoD[0].Status != task.CritPending {
		t.Fatalf("DoD criterion status should default to pending, got %q", p.DoD[0].Status)
	}

	// An invalid criterion (missing author) is rejected.
	bad := mkPlan("Bad DoD Plan", "ws-1", "agent-a")
	bad.DoD = []task.AcceptanceCriterion{
		{Kind: task.KindProse, Text: "no author here"},
	}
	if err := s.Create(bad); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for DoD missing author, got %v", err)
	}
}
