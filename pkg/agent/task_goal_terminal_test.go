// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_goal_terminal_test.go is the behavioural guard for UAT defect D-2:
// a task-owned goal record was never closed when its task terminated.
//
// The evidence, from a live gateway run: three terminated tasks (two `done`,
// one `failed`) left three `state: active`, `round: 0` goal records behind, and
// the SAME criterion read `met` on the task and `pending` on its goal — two
// contradictory sources of truth for "was this met", which is exactly what
// ADR-086 set out to eliminate by making the goal the single record.
//
// Why a stale `active` record is worse than untidy. `activateTaskGoal`
// (task_executor.go) switches on the record's phase: `IsDefining()` ->
// Activate, `IsTerminal()` -> Reactivate, and `default:` -> "Already active —
// defensive no-op". So a record left ACTIVE by a finished run makes the NEXT
// run of that task silently skip Goal.Reactivate entirely — attempts_used,
// rounds_used, the per-criterion statuses, latest_reason, the keeper's own
// ZeroOutputPushes/QuestionRoundsUsed budgets and active_session_id all stay
// at the previous run's values, and no TerminalHistory entry is ever appended.
// R-04's whole re-entry contract is dead for every re-run. That is the
// assertion TestReRunOfTerminatedTaskReactivatesItsGoal below pins, and it is
// the reason this defect is not cosmetic.
//
// Note on the outcome vocabulary: no specification prescribes a task-outcome ->
// GoalState mapping (goal-entity-spec.md's FR-015/FR-027/FR-028 are silent on
// it). The mapping under test is derived from Goal.yaml's own definitions of
// the four terminal states and mirrors what clearGoalStatus already writes for
// the chat equivalent of each ending — see goalStateForTerminalTask.
package agent

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestTerminatedTaskEndsItsGoalRecord is UAT defect D-2's oracle.
//
// Each row drives a REAL terminal writer. These are the three that live in
// THIS package — they are NOT all the terminal writers there are, and the
// sentence that used to claim they were is what review finding C1 is about:
//
//   - completeTaskWithResult: the judged done/failed outcome and the
//     attempts-exhausted wind-down;
//   - failTask: an infrastructure failure;
//   - PlanEngine.cancelMemberLocked: a user Stop.
//
// Four more live outside pkg/agent and are covered elsewhere, because a test
// in this package cannot reach them: PATCH /api/v1/tasks/{id} and boot
// reconciliation (pkg/gateway/rest_tasks_goal_terminal_test.go), and the two
// update_task tools (pkg/tools, pkg/sysagent/tools). The complete, enforced
// list is pkg/tools/task_goal_terminal_guard_test.go's registry, which fails
// when a new task-status writer appears unclassified — precisely so the next
// reader of THIS comment is not misled into thinking three is the whole set.
// All seven now route through the one shared transition,
// tools.TerminateTaskGoalRecord.
//
// The `defining` row is the differentiation control, and it is the one that
// stops this test being satisfiable by "just terminate the record always": a
// task stopped before it ever ran holds its goal in the definition phase
// (Goal.yaml: "a task holds its goal in this phase from task creation until
// the task starts"), and forcing that record terminal would invent an
// adjudication that never happened.
func TestTerminatedTaskEndsItsGoalRecord(t *testing.T) {
	cases := []struct {
		name string
		// activateGoal seeds the paired record in the ACTIVE phase (a task
		// that actually ran). When false the record is left `defining`.
		activateGoal bool
		// terminate performs the real terminal write under test.
		terminate func(t *testing.T, al *AgentLoop, tk *task.Task, sessionID string)

		wantTaskStatus task.Status
		wantGoalState  generated.GoalState
	}{
		{
			name:         "a_judged_done_task_ends_its_goal_as_met",
			activateGoal: true,
			terminate: func(t *testing.T, al *AgentLoop, tk *task.Task, sessionID string) {
				t.Helper()
				if !al.taskExecutor.completeTaskWithResult(
					tk, sessionID, task.StatusInProgress, true, "the export endpoint returns CSV", nil) {
					t.Fatal("completeTaskWithResult did not apply — the fixture's CAS expectation is wrong")
				}
			},
			wantTaskStatus: task.StatusDone,
			wantGoalState:  generated.GoalStateMet,
		},
		{
			name:         "an_attempts_exhausted_task_ends_its_goal_as_exhausted",
			activateGoal: true,
			terminate: func(t *testing.T, al *AgentLoop, tk *task.Task, sessionID string) {
				t.Helper()
				if !al.taskExecutor.completeTaskWithResult(
					tk, sessionID, task.StatusInProgress, false, "attempts exhausted without a met verdict", nil) {
					t.Fatal("completeTaskWithResult did not apply — the fixture's CAS expectation is wrong")
				}
			},
			wantTaskStatus: task.StatusFailed,
			wantGoalState:  generated.GoalStateExhausted,
		},
		{
			name:         "an_infrastructure_failure_ends_its_goal_as_exhausted",
			activateGoal: true,
			terminate: func(t *testing.T, al *AgentLoop, tk *task.Task, _ string) {
				t.Helper()
				al.taskExecutor.failTask(tk.ID, "could not persist attempt increment")
			},
			wantTaskStatus: task.StatusFailed,
			wantGoalState:  generated.GoalStateExhausted,
		},
		{
			name:         "a_user_stop_ends_its_goal_as_cleared",
			activateGoal: true,
			terminate: func(t *testing.T, al *AgentLoop, tk *task.Task, _ string) {
				t.Helper()
				pe := &PlanEngine{taskStore: GetTaskStore(al)}
				if _, err := pe.cancelMemberLocked(tk.ID, "operator"); err != nil {
					t.Fatalf("cancelMemberLocked: %v", err)
				}
			},
			wantTaskStatus: task.StatusFailed,
			wantGoalState:  generated.GoalStateCleared,
		},
		{
			name: "a_task_stopped_before_it_ever_ran_leaves_its_goal_defining",
			// The record was never activated: the task never started, so
			// there is no adjudication to record a terminal outcome for.
			activateGoal: false,
			terminate: func(t *testing.T, al *AgentLoop, tk *task.Task, _ string) {
				t.Helper()
				pe := &PlanEngine{taskStore: GetTaskStore(al)}
				if _, err := pe.cancelMemberLocked(tk.ID, "operator"); err != nil {
					t.Fatalf("cancelMemberLocked: %v", err)
				}
			},
			wantTaskStatus: task.StatusFailed,
			wantGoalState:  generated.GoalStateDefining,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

			stored := mustCreateInProgressTask(t, al, &task.Task{
				AgentID: "native-agent", WorkspaceID: "test-ws",
				Title:    "ship the CSV export",
				Prompt:   "make the export endpoint return CSV",
				Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
			})
			_, taskSessionID := newGoalTestSession(t, al, "native-agent")

			seeded := seedTaskGoal(t, stored.ID, dodTestCriterionText, dodTestDoDText)
			if tc.activateGoal {
				activateSeededTaskGoal(t, seeded.GoalID, taskSessionID)
			}

			tc.terminate(t, al, stored, taskSessionID)

			final, err := GetTaskStore(al).Get(stored.ID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if final.Status != tc.wantTaskStatus {
				t.Fatalf("fixture broken: task status = %q, want %q — the terminal write under test "+
					"did not land, so nothing about the goal record is being proven",
					final.Status, tc.wantTaskStatus)
			}

			rec := readTaskGoal(t, stored.ID)
			if rec.State != tc.wantGoalState {
				t.Fatalf("goal record state = %q, want %q (task reached %q).\n"+
					"UAT defect D-2: three terminated tasks left three state=active, round=0 goal records "+
					"behind. goal_loop.go::terminateGoalRecordByID is owner-kind agnostic, but its only "+
					"callers are the two CHAT surfaces (clearGoal, goalIdleExpirySweep) — nothing on the "+
					"task terminal path ever ended the record. A record left ACTIVE also makes the next "+
					"run of this task skip Goal.Reactivate entirely (activateTaskGoal's default branch is "+
					"a no-op for an already-active record).",
					rec.State, tc.wantGoalState, final.Status)
			}
			if tc.wantGoalState == generated.GoalStateDefining {
				return
			}
			if rec.TerminalReason == "" {
				t.Error("the terminal goal record carries no TerminalReason — GOAL-FR-027 requires the " +
					"record to survive WITH its reason, and a reader must be able to tell why the goal " +
					"ended without going to find the task")
			}
		})
	}
}

// TestReRunOfTerminatedTaskReactivatesItsGoal pins the CONSEQUENCE of D-2 that
// makes it a live functional bug rather than a tidiness issue.
//
// R-04 (the joint delivery plan's addition to FR-028) and Goal.yaml both state
// that a task-owned goal's only re-entry edge is terminal -> active on task
// re-run, "resetting attempts_used and rounds_used to 0, clearing latest_reason
// and the current verdict, and appending the prior verdict to a retained
// history array rather than overwriting it". Goal.Reactivate enforces that it
// may only be called on a TERMINAL record.
//
// activateTaskGoal's phase switch therefore silently degrades when the previous
// run left the record ACTIVE: neither IsDefining() nor IsTerminal() holds, so
// it takes the `default:` no-op branch. The second run inherits the first run's
// counters, its per-criterion statuses, its latest_reason and its session id,
// and no TerminalHistory entry is ever written — the whole re-entry contract
// is dead, with no error anywhere.
//
// This test runs the real two-run sequence: terminate run 1 through the real
// terminal writer, then re-activate through the real activateTaskGoal.
func TestReRunOfTerminatedTaskReactivatesItsGoal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	stored := mustCreateInProgressTask(t, al, &task.Task{
		AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "ship the CSV export",
		Prompt:   "make the export endpoint return CSV",
		Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
	})
	_, firstSession := newGoalTestSession(t, al, "native-agent")

	seeded := seedTaskGoal(t, stored.ID, dodTestCriterionText, dodTestDoDText)
	activateSeededTaskGoal(t, seeded.GoalID, firstSession)

	// Give run 1 some state worth losing, so "the counters were reset" is an
	// observable event rather than a comparison of two zeroes.
	gs := goal.NewStore(config.OmnipusHomeDir())
	if _, err := gs.Update(seeded.GoalID, func(cur *goal.Goal) error {
		cur.AttemptsUsed = 2
		cur.Round = 3
		cur.LatestReason = "run 1 fell short"
		cur.ZeroOutputPushes = 1
		cur.QuestionRoundsUsed = 1
		return nil
	}); err != nil {
		t.Fatalf("seed run-1 counters: %v", err)
	}

	// --- run 1 ends through the real terminal writer -----------------------

	if !al.taskExecutor.completeTaskWithResult(
		stored, firstSession, task.StatusInProgress, false, "attempts exhausted", nil) {
		t.Fatal("completeTaskWithResult did not apply")
	}

	afterRun1 := readTaskGoal(t, stored.ID)
	if !afterRun1.IsTerminal() {
		t.Fatalf("after run 1 the goal record is %q, not terminal — Goal.Reactivate refuses a "+
			"non-terminal record outright, so the re-run below cannot possibly reset anything",
			afterRun1.State)
	}

	// --- run 2 re-activates through the real activateTaskGoal --------------

	reRun, err := GetTaskStore(al).Get(stored.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	_, secondSession := newGoalTestSession(t, al, "native-agent")
	if aerr := al.taskExecutor.activateTaskGoal(reRun, secondSession); aerr != nil {
		t.Fatalf("activateTaskGoal on the re-run: %v", aerr)
	}

	afterRun2 := readTaskGoal(t, stored.ID)

	if afterRun2.State != generated.GoalStateActive {
		t.Fatalf("goal state after the re-run = %q, want %q", afterRun2.State, generated.GoalStateActive)
	}
	if afterRun2.ActiveSessionID != secondSession {
		t.Errorf("active_session_id = %q, want the SECOND run's session %q — a record left active by "+
			"run 1 keeps pointing at run 1's session, so every keeper push and cancel for run 2 is "+
			"routed to a session that is already archived",
			afterRun2.ActiveSessionID, secondSession)
	}
	// R-04's own named resets, plus the two keeper budgets Reactivate's doc
	// comment calls out as "a task whose FIRST run spent them came back with
	// both bounds already tripped".
	if afterRun2.AttemptsUsed != 0 {
		t.Errorf("attempts_used = %d after the re-run, want 0 (R-04)", afterRun2.AttemptsUsed)
	}
	if afterRun2.Round != 0 {
		t.Errorf("rounds_used = %d after the re-run, want 0 (R-04)", afterRun2.Round)
	}
	if afterRun2.LatestReason != "" {
		t.Errorf("latest_reason = %q after the re-run, want it cleared (R-04)", afterRun2.LatestReason)
	}
	if afterRun2.ZeroOutputPushes != 0 || afterRun2.QuestionRoundsUsed != 0 {
		t.Errorf("keeper budgets after the re-run: zero_output_pushes = %d, question_rounds_used = %d; "+
			"want 0 and 0 — Goal.Reactivate resets them because they are per-RUN budgets on a record "+
			"that outlives the run",
			afterRun2.ZeroOutputPushes, afterRun2.QuestionRoundsUsed)
	}
	// THE kill assertion for the silent-degrade: an already-active record
	// takes activateTaskGoal's no-op branch, so no history is ever appended.
	if len(afterRun2.TerminalHistory) != 1 {
		t.Fatalf("terminal_history has %d entries after one completed run, want exactly 1.\n"+
			"Goal.Reactivate appends the prior run's outcome BEFORE resetting the counters (R-04). "+
			"It is only reachable from a TERMINAL record — so when run 1 leaves the record ACTIVE "+
			"(UAT defect D-2), activateTaskGoal takes its `default:` no-op branch and the entire "+
			"re-entry contract silently does nothing.",
			len(afterRun2.TerminalHistory))
	}
	if got := afterRun2.TerminalHistory[0]; got.Round != 3 || got.AttemptsUsed != 2 {
		t.Errorf("archived run-1 history = {round: %d, attempts_used: %d}, want {3, 2} — the history "+
			"entry must capture run 1's real counters, taken before the reset",
			got.Round, got.AttemptsUsed)
	}
}

// seedTaskGoal creates (but does not activate) the goal record paired with
// taskID. Split from activateSeededTaskGoal so a test can hold a record in the
// `defining` phase, which is the legitimate resting state for a task that has
// not started.
func seedTaskGoal(t *testing.T, taskID, criterionText, dodText string) *goal.Goal {
	t.Helper()
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.TaskExplicit,
		"make the export endpoint return CSV", "",
		[]task.AcceptanceCriterion{proseCriterion("", criterionText)},
		[]task.AcceptanceCriterion{proseCriterion("", dodText)},
		config.DefaultGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("seedTaskGoal: goal.New: %v", err)
	}
	if cerr := goal.NewStore(config.OmnipusHomeDir()).Create(g); cerr != nil {
		t.Fatalf("seedTaskGoal: Create: %v", cerr)
	}
	return g
}

// activateSeededTaskGoal moves a seeded record into the active phase, the way
// task_executor.go::activateTaskGoal does when the task starts.
func activateSeededTaskGoal(t *testing.T, goalID, sessionID string) {
	t.Helper()
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(goalID, func(cur *goal.Goal) error {
		return cur.Activate(sessionID, time.Now().UTC())
	}); err != nil {
		t.Fatalf("activateSeededTaskGoal: %v", err)
	}
}
