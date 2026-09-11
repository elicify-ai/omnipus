// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_goal_test.go is wave E12's activation oracle (GOAL-FR-009,
// FR-010, FR-012, FR-023, FR-028/R-04): proves that
// TaskExecutor.activateTaskGoal (called from createTaskSessionSync and
// StartTaskNow's inline session-creation block) actually binds a task's
// pre-created, defining-phase pkg/goal record into the active phase against
// the session the task mints, mirrors it onto that session's meta so the
// keeper/claim machinery (goal_loop.go, goal_triggers.go) picks it up, and
// leaves a task with no paired goal record to run exactly as it always did.
// Distinct from T3's task_executor_goal_loop_test.go, which is the attempt-
// accounting oracle — this file owns activation only.
package agent

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedDefiningTaskGoal builds a task record plus its paired, DEFINING-phase
// pkg/goal record (GOAL-FR-009/FR-012: created at task creation, well
// before the task ever runs) — the precondition activateTaskGoal expects to
// find, mirroring rest_tasks.go's syncTaskGoalRecord.
func seedDefiningTaskGoal(t *testing.T, al *AgentLoop, taskID, agentID string) (*task.Task, *goal.Goal) {
	t.Helper()
	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: taskID, AgentID: agentID, WorkspaceID: "test-ws", Title: "goal activation test task",
		Status: task.StatusNext,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.TaskExplicit,
		"finish the thing", "",
		[]task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Text: "the thing is finished", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		[]task.AcceptanceCriterion{{
			ID: "dod-floor", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Text: "no secrets leaked", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "system"},
		}},
		10, time.Now().UTC())
	if err != nil {
		t.Fatalf("goal.New: %v", err)
	}
	gs := goal.NewStore(config.OmnipusHomeDir())
	if err := gs.Create(g); err != nil {
		t.Fatalf("goal Create: %v", err)
	}
	if g.State != generated.GoalStateDefining {
		t.Fatalf("seeded goal state = %q, want defining", g.State)
	}
	return tk, g
}

// TestActivateTaskGoal_BindsDefiningRecordAndIsSessionReachable proves
// GOAL-FR-010/FR-012: createTaskSessionSync's call to activateTaskGoal
// transitions the task's defining-phase goal record to active, bound to the
// session just minted, and leaves it REACHABLE BY SESSION ID so the
// keeper/claim machinery in goal_loop.go/goal_triggers.go finds it —
// GOAL-FR-013's "one code path".
//
// ADR-086 re-point (this was TestActivateTaskGoal_BindsDefiningRecordAndMirrorsMeta):
// the session-meta MIRROR this test was named for is retired. Pre-ADR-086 the
// keeper read its entry condition off session.UnifiedMeta's Goal* fields, so
// a task-owned goal had to be copied there; wave S6 deleted those fields and
// activeGoalForSession (goal_record_wiring.go) — a lookup keyed on the
// record's own ActiveSessionID — is the single entry predicate both owner
// kinds now share. The five mirrored values are asserted below on the RECORD,
// reached through exactly that predicate, so the property the mirror existed
// to provide ("the keeper can find this task-owned goal from its session, with
// its real criteria, budget and round") is still proven end to end. Per D-F
// there is no migration path to a mirror to preserve.
func TestActivateTaskGoal_BindsDefiningRecordAndIsSessionReachable(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	tk, g := seedDefiningTaskGoal(t, al, "t-activate-1", "native-agent")

	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync: %v", err)
	}
	if sid == "" {
		t.Fatal("createTaskSessionSync returned an empty session id")
	}

	gs := goal.NewStore(config.OmnipusHomeDir())
	activated, err := gs.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get goal: %v", err)
	}
	if activated.State != generated.GoalStateActive {
		t.Fatalf("goal state = %q, want active", activated.State)
	}
	if activated.ActiveSessionID != sid {
		t.Fatalf("goal active_session_id = %q, want %q", activated.ActiveSessionID, sid)
	}

	// GOAL-FR-013: the SAME session-keyed predicate the keeper/claim
	// machinery runs must find this task-owned goal.
	found := activeGoalForSession(sid)
	if found == nil {
		t.Fatal("activeGoalForSession must find the task-owned goal bound to this session — " +
			"the keeper/claim machinery has nothing to work with otherwise (GOAL-FR-013)")
	}
	if found.GoalID != g.GoalID {
		t.Fatalf("found.GoalID = %q, want %q", found.GoalID, g.GoalID)
	}
	if found.Prompt != "finish the thing" {
		t.Fatalf("found.Prompt = %q, want the goal prompt", found.Prompt)
	}
	if goalRecordCompiledJSON(found) == "" {
		t.Fatal("the record's criteria ladder must be non-empty — a task-owned goal's real criteria are " +
			"already fixed at creation, so it must never read as the D3 chat-only recordless/unregistered " +
			"state (GOAL-FR-020: the nudge ladder stays unreachable for a task-owned goal)")
	}
	if found.MaxRounds != 10 {
		t.Fatalf("found.MaxRounds = %d, want 10 (stamped at goal creation)", found.MaxRounds)
	}
	if found.Round != 0 {
		t.Fatalf("found.Round = %d, want 0 on fresh activation", found.Round)
	}
}

// TestActivateTaskGoal_NoGoalRecord_TaskStillRuns proves GOAL-FR-023: a
// task with no paired goal record (pre-D-C task, or a test fixture that
// never seeded one) still gets a session created without error, and simply
// never enters the goal loop — activeGoalForSession finds nothing, exactly
// the "no active goal — fast path" checkGoalLoopAfterTurn's own entry gate
// already handles.
func TestActivateTaskGoal_NoGoalRecord_TaskStillRuns(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-no-goal-record", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title: "legacy criteria-less task", Status: task.StatusNext,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// Deliberately no goal.New/Create call — this task has NO paired
	// pkg/goal record at all (GOAL-FR-023's legacy/no-DoD case).

	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync must not fail for a goal-record-less task: %v", err)
	}
	if sid == "" {
		t.Fatal("createTaskSessionSync returned an empty session id")
	}

	if found := activeGoalForSession(sid); found != nil {
		t.Fatalf("activeGoalForSession found %q — a task with no goal record must never enter the goal loop", found.Prompt)
	}
}

// TestActivateTaskGoal_ReRunReactivatesTerminalRecord proves R-04
// (GOAL-FR-028): a task-owned goal that already reached a terminal state on
// a PRIOR run re-enters active via Reactivate — attempts_used and round
// reset to 0, and the prior outcome is retained in terminal_history — never
// a second goal record minted for the same task.
func TestActivateTaskGoal_ReRunReactivatesTerminalRecord(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	tk, g := seedDefiningTaskGoal(t, al, "t-rerun-1", "native-agent")

	firstSID, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync (first run): %v", err)
	}

	gs := goal.NewStore(config.OmnipusHomeDir())
	if _, err := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		cur.Round = 3
		cur.AttemptsUsed = 2
		return cur.Terminate(generated.GoalStateExhausted, "round bound reached", time.Now().UTC())
	}); err != nil {
		t.Fatalf("simulate terminal run: %v", err)
	}

	// Re-run: the task mints a SECOND session (a fresh task run mints its
	// own session each time, mirroring the real dispatch flow).
	tk.SessionID = ""
	secondSID, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync (re-run): %v", err)
	}
	if secondSID == firstSID {
		t.Fatal("a re-run must mint its own new session")
	}

	reactivated, err := gs.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get goal: %v", err)
	}
	if reactivated.State != generated.GoalStateActive {
		t.Fatalf("goal state = %q, want active (R-04 re-entry)", reactivated.State)
	}
	if reactivated.ActiveSessionID != secondSID {
		t.Fatalf("goal active_session_id = %q, want the re-run's session %q", reactivated.ActiveSessionID, secondSID)
	}
	if reactivated.Round != 0 || reactivated.AttemptsUsed != 0 {
		t.Fatalf("round/attempts_used = %d/%d, want 0/0 reset on re-entry", reactivated.Round, reactivated.AttemptsUsed)
	}
	if len(reactivated.TerminalHistory) != 1 {
		t.Fatalf("terminal_history has %d entries, want 1 (the prior run's outcome retained)", len(reactivated.TerminalHistory))
	}
	if reactivated.TerminalHistory[0].State != generated.GoalStateExhausted {
		t.Fatalf("terminal_history[0].state = %q, want exhausted", reactivated.TerminalHistory[0].State)
	}

	// The goal id (not a fresh one) must still be the id GetByOwner finds
	// for this task — no second record was minted.
	byOwner, err := gs.GetByOwner(generated.GoalOwnerKindTask, tk.ID)
	if err != nil {
		t.Fatalf("GetByOwner: %v", err)
	}
	if byOwner.GoalID != g.GoalID {
		t.Fatalf("GetByOwner returned a different goal id %q, want the original %q — R-04 must not mint a second record",
			byOwner.GoalID, g.GoalID)
	}
}
