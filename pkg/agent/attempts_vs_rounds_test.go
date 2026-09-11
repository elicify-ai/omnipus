package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestAttemptsVsRounds_DistinctBrakes pins FR-178: the per-member/task
// attempts brake (Task.AttemptCount, sole writer
// TaskExecutor.consumeAttemptOrExhaust) and the per-goal/plan judge-rounds
// brake (plan.JudgeRounds, sole writer PlanEngine.applyJudgeRoundOutcome)
// are TWO DISTINCT counters — never conflated, and whichever trips first stops
// its OWN scope locally. The matrix row 58 claimed this test; it did not exist.
//
// Direction A (rounds): exhausting JudgeRounds fails the PLAN on
// judge_rounds_exhausted WITHOUT consuming a member-task attempt.
// Direction B (attempts): exhausting AttemptCount fails the TASK WITHOUT
// tripping the plan's rounds brake (the plan stays running, JudgeRounds intact).
func TestAttemptsVsRounds_DistinctBrakes(t *testing.T) {
	t.Run("rounds_brake_does_not_consume_an_attempt", func(t *testing.T) {
		// Direction A: drive a plan's judge rounds to exhaustion (the same path
		// TestPlanEngine_JudgeRoundsExhausted_FailsPlan exercises) and assert a
		// member task's AttemptCount is UNCHANGED — the rounds brake never
		// touches the attempts counter.
		h := newTestPlanEngine(t)
		one := 1
		mustCreatePlan(t, h.plans, &plan.Plan{
			ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
			DoD:    []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
			Bounds: &plan.PlanBounds{PlanJudgeMaxRounds: &one},
		})
		// Member task seeded with a NON-zero AttemptCount (3 prior attempts) so
		// any conflation (rounds-exhaustion consuming an attempt) would show as
		// a change. Status `done` so allMembersTerminal==true and the judge runs.
		const priorAttempts = 3
		mustCreateTask(t, h.tasks, &task.Task{
			Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
			AttemptCount: priorAttempts,
		})
		h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
			return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
				Met:          false,
				PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "still not yet"}},
			}}
		}

		// Round 1: consumes the only allowed round, ends unmet.
		h.pe.processPlan(context.Background(), "p1")
		h.pe.judgeWG.Wait()

		// Round 2: JudgeRounds(1) >= maxRounds(1) -> plan fails on rounds,
		// WITHOUT a second judge call.
		h.pe.processPlan(context.Background(), "p1")

		failedPlan, err := h.plans.Get("p1")
		if err != nil {
			t.Fatal(err)
		}
		if failedPlan.State != plan.StateFailed {
			t.Fatalf("plan state = %q, want failed (rounds brake tripped)", failedPlan.State)
		}
		if failedPlan.FailedReason != plan.FailedReasonJudgeRoundsExhausted {
			t.Fatalf("failed_reason = %q, want judge_rounds_exhausted", failedPlan.FailedReason)
		}
		if failedPlan.JudgeRounds != 1 {
			t.Errorf("plan JudgeRounds = %d, want 1 (the rounds brake consumed a round, not an attempt)", failedPlan.JudgeRounds)
		}

		// THE FR-178 ASSERTMENT: the member task's AttemptCount is UNCHANGED —
		// exhausting rounds did NOT consume an attempt (never conflated).
		member, err := h.tasks.List(task.Filter{PlanID: "p1"})
		if err != nil {
			t.Fatalf("list plan members: %v", err)
		}
		if len(member) != 1 {
			t.Fatalf("expected 1 member, got %d", len(member))
		}
		if member[0].AttemptCount != priorAttempts {
			t.Errorf("member AttemptCount = %d, want %d — the rounds brake MUST NOT consume an attempt (FR-178)",
				member[0].AttemptCount, priorAttempts)
		}
		if member[0].Status != task.StatusDone {
			t.Errorf("member status = %q, want done (rounds brake fails the PLAN, not the member task)", member[0].Status)
		}
	})

	t.Run("attempts_brake_does_not_trip_the_rounds_brake", func(t *testing.T) {
		// Direction B: drive a member task's attempts to exhaustion via the SOLE
		// AttemptCount writer (consumeAttemptOrExhaust) and assert the OWNING
		// PLAN is untouched — JudgeRounds intact, still running, NOT failed on
		// rounds. The two brakes write to different stores/fields; the
		// attempts brake fails the TASK, it does not trip the plan rounds brake.
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

		// A plan record in its OWN store, seeded at JudgeRounds=0/running. The
		// TaskExecutor never receives a reference to this store — the very
		// architecture that keeps the two brakes distinct — so asserting it is
		// untouched proves non-conflation.
		planDir := t.TempDir()
		planStore := plan.New(filepath.Join(planDir, "plans"))
		if err := planStore.Create(&plan.Plan{
			ID: "plan-attempts", Title: "Attempts plan", WorkspaceID: "default",
			OwnerAgentID: "native-agent", State: plan.StateRunning,
		}); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		maxAttempts := 2
		tk := &task.Task{
			Title: "attempts-brake member", Prompt: "do it", Action: task.ActionLLM,
			AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
			Status:       task.StatusNext,
			PlanID:       "plan-attempts",
			MaxAttempts:  &maxAttempts,
			AttemptCount: maxAttempts - 1, // one consumeAttemptOrExhaust call exhausts it
		}
		if err := al.taskStore.Create(tk); err != nil {
			t.Fatalf("create task: %v", err)
		}
		// consumeAttemptOrExhaust CASes against in_progress — establish it.
		inProg := task.StatusInProgress
		if _, err := al.taskStore.Update(tk.ID, task.Patch{Status: &inProg}); err != nil {
			t.Fatalf("set in_progress: %v", err)
		}
		fresh, err := al.taskStore.Get(tk.ID)
		if err != nil {
			t.Fatalf("reload task: %v", err)
		}

		// Sole AttemptCount writer, verdict=nil (no-signal unmet outcome).
		al.taskExecutor.consumeAttemptOrExhaust(context.Background(), fresh, "", "claim summary", nil, nil)

		// The TASK failed on attempts (the attempts brake tripped for ITS scope).
		final, err := al.taskStore.Get(tk.ID)
		if err != nil {
			t.Fatalf("get final task: %v", err)
		}
		if final.Status != task.StatusFailed {
			t.Fatalf("task status = %q, want failed (attempts brake exhausted the task)", final.Status)
		}
		if final.AttemptCount != maxAttempts {
			t.Errorf("task AttemptCount = %d, want %d", final.AttemptCount, maxAttempts)
		}

		// THE FR-178 ASSERTMENT: the owning PLAN is untouched — JudgeRounds
		// intact, still running, NOT failed on rounds. The attempts brake did
		// NOT trip the rounds brake (never conflated).
		untouched, err := planStore.Get("plan-attempts")
		if err != nil {
			t.Fatalf("reload plan: %v", err)
		}
		if untouched.JudgeRounds != 0 {
			t.Errorf("plan JudgeRounds = %d, want 0 — exhausting attempts MUST NOT trip the rounds brake (FR-178)",
				untouched.JudgeRounds)
		}
		if untouched.State != plan.StateRunning {
			t.Errorf("plan state = %q, want running — the attempts brake fails the TASK, not the plan on rounds",
				untouched.State)
		}
		if untouched.FailedReason == plan.FailedReasonJudgeRoundsExhausted {
			t.Error("plan must NOT be failed on judge_rounds_exhausted when only attempts exhausted")
		}
	})
}

// ===========================================================================
// ADR-086 / GOAL-FR-026 and GOAL-FR-049 — wave T3
//
// FR-026: "The `2 x effective budget` hard ceiling
// (pkg/agent/task_executor.go::consumeAttemptOrExhaust) MUST apply to both
// owner kinds."
//
// FR-049: "Task-owned goals MUST NOT consume the 'goal' admission slot
// registered via pkg/agent/plan_engine.go::RegisterActiveCounter and bounded
// by config.DefaultGlobalActiveLoopCap (16). Chat goals MUST remain bounded
// by it unchanged."
//
// Both are BOUNDS, which is why they live beside the two brakes above: the
// failure mode they exist to prevent is a loop that never stops, and the
// failure mode a careless test has is passing while nothing bounds anything.
// Every case below therefore carries its own differentiation control — an
// input one step the other side of the bound that must produce the OPPOSITE
// outcome — so a gate that is permanently open and a gate that is
// permanently shut both go red.
// ===========================================================================

// t3ArmedGoalMaxRounds is this file's own goal-record budget for the
// owner-kind parity cases. Deliberately small and deliberately NOT
// config.DefaultGoalMaxRounds, so a bound accidentally read from the global
// default instead of from the record cannot pass by coincidence.
const t3ArmedGoalMaxRounds = 4

// t3ArmGoalRecord creates an ACTIVE goal record bound to sid for the given
// owner kind, with its round counter pre-set. Everything except ownerKind /
// ownerID / round is identical between the two owner kinds — that sameness
// is the point: it is what lets a difference in outcome be attributed to the
// owner kind and nothing else.
//
// The criterion ladder is the one the canned judge providers in
// goal_loop_test.go answer for (recordedGoalCriterionID plus newFloorDoD's
// two fixed floor items), so the adjudication reaches a real verdict rather
// than degrading into an unmatched-id case.
func t3ArmGoalRecord(
	t *testing.T, sid string, ownerKind generated.GoalOwnerKind, ownerID string, round int,
) *goal.Goal {
	t.Helper()
	now := time.Now().UTC()
	source := generated.ChatCompiled
	if ownerKind == generated.GoalOwnerKindTask {
		source = generated.TaskExplicit
	}
	criteria := []task.AcceptanceCriterion{{
		ID: recordedGoalCriterionID, Kind: task.KindProse, Judgment: task.JudgmentBoolean,
		Text: "the widget is green", Status: task.CritPending,
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
	}}
	g, err := goal.New(ownerKind, ownerID, source, "make the widget green", "",
		criteria, newFloorDoD(), t3ArmedGoalMaxRounds, now)
	if err != nil {
		t.Fatalf("t3ArmGoalRecord: goal.New(%s): %v", ownerKind, err)
	}
	gs := goal.NewStore(config.OmnipusHomeDir())
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("t3ArmGoalRecord: Create(%s): %v", ownerKind, cerr)
	}
	if _, uerr := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.Activate(sid, now)
	}); uerr != nil {
		t.Fatalf("t3ArmGoalRecord: Activate(%s): %v", ownerKind, uerr)
	}
	armed, uerr := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		cur.Round = round
		cur.MaxRounds = t3ArmedGoalMaxRounds
		return nil
	})
	if uerr != nil {
		t.Fatalf("t3ArmGoalRecord: arm round counter(%s): %v", ownerKind, uerr)
	}
	return armed
}

// TestHardCeilingAppliesToBothOwnerKinds_GOALFR026 is the goal spec's row-26
// oracle (FR-026, S-36).
//
// The requirement has two surfaces because the unified goal has two counters
// that ADR-086 R-03 keeps deliberately distinct — the task's own attempt
// ladder (Task.AttemptCount, sole writer consumeAttemptOrExhaust) and the
// goal record's round ladder (Goal.Round, advanced by the adjudication) —
// and the bound has to hold on BOTH, for BOTH owner kinds. The two subtests
// below take one surface each.
//
// What "hard ceiling" buys over the plain budget gate, and what is therefore
// actually under test: a counter that is ALREADY past its budget when the
// path is entered must still stop. The plain `newAttempt < maxAttempts`
// comparison already covers the ordinary walk up to the budget; only a
// pre-inflated counter (a duplicate re-dispatch, a record carried over from
// a prior run, a second gate disagreeing with the first) distinguishes a
// real ceiling from a loop that counts forever.
func TestHardCeilingAppliesToBothOwnerKinds_GOALFR026(t *testing.T) {
	t.Run("task_attempt_ladder_stops_at_and_beyond_2x_the_budget", func(t *testing.T) {
		cases := []struct {
			name           string
			budget         int
			seededAttempts int
			wantRedispatch bool
		}{
			// Control: comfortably below the budget, so the loop MUST keep
			// going. Without this row a permanently-shut gate passes.
			{"below_budget_redispatches", 3, 0, true},
			{"at_budget_stops", 3, 2, false},
			// At the hard ceiling exactly (2 x budget).
			{"at_2x_budget_stops", 3, 6, false},
			// Far past the ceiling — the runaway the belt-and-suspenders
			// ceiling exists for.
			{"far_past_2x_budget_stops", 3, 20, false},
			// A budget of 1 makes the ceiling 2: the smallest interval where
			// "budget" and "2 x budget" are different numbers at all.
			{"budget_one_at_2x_stops", 1, 2, false},
			// The unified ADR-086 budget (GOAL-FR-024's 20) at its ceiling.
			{"unified_goal_budget_at_2x_stops", config.DefaultGoalMaxRounds, 2 * config.DefaultGoalMaxRounds, false},
			{"unified_goal_budget_below_redispatches", config.DefaultGoalMaxRounds, 0, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
				budget := tc.budget
				tk := &task.Task{
					Title: "ceiling probe", Prompt: "do it", Action: task.ActionLLM,
					AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
					Status: task.StatusNext, MaxAttempts: &budget,
					AttemptCount: tc.seededAttempts,
					Criteria:     []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
				}
				if err := al.taskStore.Create(tk); err != nil {
					t.Fatalf("create task: %v", err)
				}
				inProgress := task.StatusInProgress
				if _, err := al.taskStore.Update(tk.ID, task.Patch{Status: &inProgress}); err != nil {
					t.Fatalf("set in_progress: %v", err)
				}
				fresh, err := al.taskStore.Get(tk.ID)
				if err != nil {
					t.Fatalf("reload task: %v", err)
				}

				redispatch := al.taskExecutor.consumeAttemptOrExhaust(
					context.Background(), fresh, "", "unmet: not there yet", nil, nil)

				if (redispatch != "") != tc.wantRedispatch {
					t.Fatalf("re-dispatch id = %q, want re-dispatch = %v (budget %d, attempts already used %d)",
						redispatch, tc.wantRedispatch, tc.budget, tc.seededAttempts)
				}
				final, gerr := al.taskStore.Get(tk.ID)
				if gerr != nil {
					t.Fatalf("reload task: %v", gerr)
				}
				// The attempt is consumed exactly once either way —
				// consumeAttemptOrExhaust is the sole writer of this counter
				// and increments by one per call, never skipping ahead to the
				// ceiling and never leaving it untouched.
				if final.AttemptCount != tc.seededAttempts+1 {
					t.Errorf("attempt_count = %d, want %d", final.AttemptCount, tc.seededAttempts+1)
				}
				wantStatus := task.StatusFailed
				if tc.wantRedispatch {
					wantStatus = task.StatusNext
				}
				if final.Status != wantStatus {
					t.Errorf("status = %q, want %q (result: %q)", final.Status, wantStatus, final.Result)
				}
			})
		}
	})

	t.Run("goal_round_ladder_bounds_a_task_owned_and_a_session_owned_goal_identically", func(t *testing.T) {
		cases := []struct {
			name      string
			ownerKind generated.GoalOwnerKind
			ownerID   string
		}{
			{"session_owned", generated.GoalOwnerKindSession, ""}, // owner id filled in per session below
			{"task_owned", generated.GoalOwnerKindTask, "t3-owner-task"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// Sub-case A: a round counter already at 2 x the record's own
				// budget MUST end the goal rather than spend another round.
				t.Run("already_past_the_budget_ends_the_goal", func(t *testing.T) {
					resetGoalTriggerStateForTest()
					al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
					judgeInst.Provider = unmetJudgeProvider("not there yet")
					agentInst, ok := al.GetRegistry().GetAgent("native-agent")
					if !ok {
						t.Fatal("native-agent not registered")
					}
					store, sid := newGoalTestSession(t, al, agentInst.ID)
					ownerID := tc.ownerID
					if ownerID == "" {
						ownerID = sid
					}
					rec := t3ArmGoalRecord(t, sid, tc.ownerKind, ownerID, 2*t3ArmedGoalMaxRounds)

					steered := false
					met := al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, rec,
						"[goal:evidence] painted it green", func(string) { steered = true })

					if met {
						t.Fatal("an unmet verdict must not report met")
					}
					if steered {
						t.Error("the steer was delivered for a goal already past its budget — " +
							"the round bound must end the goal instead of feeding it forward")
					}
					after := mustGoalRecord(t, rec.GoalID)
					if !after.IsTerminal() {
						t.Fatalf("goal state = %q, want a terminal state — a %s goal whose round counter "+
							"(%d) is already past 2x its budget (%d) must be ended, not continued "+
							"(GOAL-FR-026)", after.State, tc.ownerKind, 2*t3ArmedGoalMaxRounds, t3ArmedGoalMaxRounds)
					}
				})

				// Sub-case B (the differentiation control): the SAME goal, the
				// SAME owner kind, the SAME judge — only the counter differs —
				// must continue. Without this the bound could be "always end
				// the goal" and sub-case A would still pass.
				t.Run("within_the_budget_continues", func(t *testing.T) {
					resetGoalTriggerStateForTest()
					al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
					judgeInst.Provider = unmetJudgeProvider("not there yet")
					agentInst, ok := al.GetRegistry().GetAgent("native-agent")
					if !ok {
						t.Fatal("native-agent not registered")
					}
					store, sid := newGoalTestSession(t, al, agentInst.ID)
					ownerID := tc.ownerID
					if ownerID == "" {
						ownerID = sid
					}
					rec := t3ArmGoalRecord(t, sid, tc.ownerKind, ownerID, 0)

					steered := false
					al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, rec,
						"[goal:evidence] painted it green", func(string) { steered = true })

					if !steered {
						t.Error("an unmet verdict well inside the budget must feed its steer forward")
					}
					after := mustGoalRecord(t, rec.GoalID)
					if after.IsTerminal() {
						t.Fatalf("goal state = %q, want it still active — round 1 of %d is nowhere near "+
							"the bound", after.State, t3ArmedGoalMaxRounds)
					}
					if after.Round != 1 {
						t.Errorf("goal Round = %d, want 1 — one adjudication spends exactly one round",
							after.Round)
					}
				})
			})
		}
	})
}

// TestActiveGoalCounter_NonActiveGoalsCountZero_GOALFR049 is R-22's named
// T3 oracle: "creates cap+4 TERMINAL session-owned goals and asserts the
// counter reads 0."
//
// It is the half of FR-049 that E6's plan_engine_cap_test.go does not cover.
// That file proves the owner-kind exemption (task-owned goals never count);
// this one proves the STATE half — that the retained record ADR-086 leaves
// behind after a goal ends (FR-027: "ending a goal MUST be a status
// transition on a retained record") does not go on occupying an admission
// slot forever. R-22 records precisely this as the trap: "a retained
// terminal record still carries its condition, so the obvious port counts
// every goal ever created, forever, and the cap wedges at 16."
func TestActiveGoalCounter_NonActiveGoalsCountZero_GOALFR049(t *testing.T) {
	const surplus = 4
	total := config.DefaultGlobalActiveLoopCap + surplus

	cases := []struct {
		name string
		// arrange leaves `total` session-owned goal records in the store, in
		// the phase under test.
		arrange func(t *testing.T, s *goal.Store)
		// A goal in this phase must contribute this much to the counter.
		wantCounted  int
		wantAdmitted bool
	}{
		{
			name: "terminal_session_owned_goals_count_zero",
			arrange: func(t *testing.T, s *goal.Store) {
				t.Helper()
				for i := 0; i < total; i++ {
					g := mustCreateCapTestGoal(t, s, generated.GoalOwnerKindSession, "t3-term-"+taskOwnerID(i), true)
					if _, err := s.Update(g.GoalID, func(gg *goal.Goal) error {
						return gg.Terminate(generated.GoalStateMet, "done", time.Now())
					}); err != nil {
						t.Fatalf("terminate goal %d: %v", i, err)
					}
				}
			},
			wantCounted:  0,
			wantAdmitted: true,
		},
		{
			name: "defining_session_owned_goals_count_zero",
			arrange: func(t *testing.T, s *goal.Store) {
				t.Helper()
				for i := 0; i < total; i++ {
					// active=false leaves the record in its defining phase.
					mustCreateCapTestGoal(t, s, generated.GoalOwnerKindSession, "t3-def-"+taskOwnerID(i), false)
				}
			},
			wantCounted:  0,
			wantAdmitted: true,
		},
		{
			// The differentiation control. Identical fixtures, identical
			// count, only the STATE differs — and now the cap must bite. A
			// counter hardwired to 0 passes the two rows above and fails
			// this one.
			name: "active_session_owned_goals_count_and_the_cap_bites",
			arrange: func(t *testing.T, s *goal.Store) {
				t.Helper()
				for i := 0; i < total; i++ {
					mustCreateCapTestGoal(t, s, generated.GoalOwnerKindSession, "t3-act-"+taskOwnerID(i), true)
				}
			},
			wantCounted:  total,
			wantAdmitted: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestPlanEngine(t)
			s := newCapTestGoalStore(t)
			registerRealGoalCounter(h.pe, s)
			tc.arrange(t, s)

			// The predicate itself, read through the very accessor E11's
			// gateway.go closure calls (goal.Store.ListActiveByOwnerKind).
			counted, err := s.ListActiveByOwnerKind(generated.GoalOwnerKindSession)
			if err != nil {
				t.Fatalf("ListActiveByOwnerKind: %v", err)
			}
			if len(counted) != tc.wantCounted {
				t.Fatalf("the active-goal counter reads %d with %d session-owned goal records on file, want %d",
					len(counted), total, tc.wantCounted)
			}

			// And the same predicate as the plan engine actually consumes it.
			admitted, active, capOut := h.pe.Admit("goal")
			if admitted != tc.wantAdmitted {
				t.Errorf("Admit(\"goal\") = %v, want %v", admitted, tc.wantAdmitted)
			}
			if active != tc.wantCounted {
				t.Errorf("Admit(\"goal\") active = %d, want %d", active, tc.wantCounted)
			}
			if capOut != config.DefaultGlobalActiveLoopCap {
				t.Errorf("Admit(\"goal\") cap = %d, want the unmodified default %d",
					capOut, config.DefaultGlobalActiveLoopCap)
			}
		})
	}
}
