// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// session-core DEL-18 removed the run-start minting of a goal record for an
// unpaired task: fresh creation owns the pairing. A plan's tail member (a
// correction's new work) is created by the plan engine, not by the create_task
// tool or REST, so it must be paired with a goal record there; otherwise its
// run has no goal loop and no goal_claim can ever complete it.

// requirePairedTailGoal fails unless taskID has a paired goal record in the
// defining phase carrying the task's own criteria and a non-empty Definition
// of Done.
func requirePairedTailGoal(t *testing.T, taskID string, wantCriteria []task.AcceptanceCriterion) {
	t.Helper()
	g, err := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		t.Fatalf("tail member %q has no paired goal record (%v) — its run would have no goal loop and could "+
			"never complete through goal_claim", taskID, err)
	}
	if len(g.DoD) == 0 {
		t.Errorf("tail member %q: paired goal has an empty Definition of Done", taskID)
	}
	if len(g.Criteria) != len(wantCriteria) {
		t.Fatalf("tail member %q: paired goal carries %d criteria, want %d (the member's own)",
			taskID, len(g.Criteria), len(wantCriteria))
	}
	for i := range wantCriteria {
		if g.Criteria[i].Text != wantCriteria[i].Text {
			t.Errorf("tail member %q: criterion %d text = %q, want %q",
				taskID, i, g.Criteria[i].Text, wantCriteria[i].Text)
		}
	}
	if !g.IsDefining() {
		t.Errorf("tail member %q: paired goal is not in the defining phase before the task runs", taskID)
	}
}

// BDD: Given a plan awaiting correction, When the supervisor appends a tail
// member, Then the member's task AND a paired goal record exist.
func TestCorrection_AppendedTailMemberGetsAPairedGoal(t *testing.T) {
	h := newCorrectionHarness(t)
	h.pe.dispatcher = &turnBoundDispatcher{store: h.tasks, release: make(chan struct{})}
	mustSeedAwaitingCorrection(t, h, "p-tailgoal", doneMember("m-done"))

	crit := []task.AcceptanceCriterion{planProseCriterion("floats are handled")}
	if _, err := h.pe.AppendCorrection(context.Background(), "p-tailgoal", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed the existing member covered the float case",
		TailMembers: []task.Task{{
			ID: "m-tail", Title: "handle the float case", WorkspaceID: "ws",
			Status: task.StatusNext, Criteria: crit,
		}},
	}); err != nil {
		t.Fatalf("AppendCorrection: %v", err)
	}
	if _, err := h.tasks.Get("m-tail"); err != nil {
		t.Fatalf("the tail member task was not created: %v", err)
	}
	requirePairedTailGoal(t, "m-tail", crit)
}

// BDD: Given a committed correction whose members were never created (a crash
// before apply), When boot replays it, Then each member has its task and a
// paired goal; and replaying again neither errors nor rewrites the goal.
func TestIntentReplay_TailMembersGetPairedGoals(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p-tg")

	crit := []task.AcceptanceCriterion{planProseCriterion("the replayed work is verified")}
	m1, m2 := tailMember("tg-m1", "p-tg"), tailMember("tg-m2", "p-tg")
	m1.Criteria, m2.Criteria = crit, crit
	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-tg", PlanID: "p-tg",
		Members: []task.Task{m1, m2},
		Edges:   []plan.IntentEdge{{FromTaskID: "tg-m1", ToTaskID: "tg-m2"}},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-tg", PlanID: "p-tg", Verb: plan.RevisionAppend,
			TailAdds: []string{"tg-m1", "tg-m2"}, CreatedAt: time.Now().UTC(),
		},
		Patch:     plan.IntentRecordPatch{ClearLastUnmetTerminalSignature: true, PlanPhase: plan.PhaseDispatching},
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	requirePairedTailGoal(t, "tg-m1", crit)
	requirePairedTailGoal(t, "tg-m2", crit)

	// Idempotent: a member that already exists and is already paired is left
	// alone by a second apply.
	before, _ := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, "tg-m1")
	if err := h.pe.applyIntentRecord(plan.IntentRecord{
		IntentID: "rev-tg", PlanID: "p-tg", Members: []task.Task{m1},
		Revision: plan.RevisionEntry{RevisionID: "rev-tg", PlanID: "p-tg", Verb: plan.RevisionAppend},
	}); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	after, err := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, "tg-m1")
	if err != nil || after.GoalID != before.GoalID {
		t.Fatalf("second apply replaced the paired goal: before=%v after=%v err=%v", before.GoalID, after, err)
	}
	var _ *goal.Goal = after
}
