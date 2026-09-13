// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_dod_fail_closed_test.go is review finding C2's oracle: a storage fault
// must not be able to complete a task with its Definition of Done unjudged.
//
// The shape of the defect. taskGoalDoD answered every read failure with a bare
// nil and one WARN. At the call site (adjudicateClaim) nil is not a failure
// signal — it is the ordinary value for "this task has no Definition of Done":
//
//	dod := taskGoalDoD(t.ID)
//	judged := criteria
//	if len(dod) > 0 { judged = criteria ∪ dod }
//
// So an unreadable goal record silently narrowed the judged set to the
// acceptance criteria alone. Those pass, the overall verdict is an AND-fold
// over per-criterion results (judge.go), Met flows to completeTaskWithResult,
// and the task reads **Done** — with the mandatory DoD, including the floor
// items goal-dod-floor-no-secrets and goal-dod-floor-grounded-claims, never
// evaluated. Task creation REFUSES with 400 without a DoD; a gate that can be
// removed by one partially-written file is not a gate.
//
// The two fault rows are deliberately different, because one of them defeats
// the obvious fix. goal.Store.GetByOwner delegates to List and DISCARDS List's
// `skipped` return, so an UNPARSEABLE record resolves to goal.ErrOwnerNotFound
// — byte-identical to a task that genuinely never had a record. A fix that
// only faulted on "an error that is not ErrOwnerNotFound" passes row 2 and
// fails row 1, and row 1 is the exact scenario the review named (a partially
// written record under disk pressure, or a second process on a platform where
// fileutil.WithFlock is a no-op — ADR-054 §5).
//
// Row 3 is the differentiation control and it is what stops the fix
// over-reaching into "refuse every task that has no DoD": a legitimately
// record-less task (GOAL-FR-023 — a pre-D-C legacy task, or a Scratchpad card)
// must still be judged on its acceptance criteria alone and must still be able
// to reach done.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// corruptGoalRecordFile overwrites the on-disk file of the goal record paired
// with taskID with bytes that are not valid JSON — the partial-write state a
// crash or a second unsynchronised writer leaves behind.
//
// entity.Store.List skips an unreadable file (Warn + `skipped`) rather than
// failing, so after this the record is invisible to GetByOwner while still
// very much existing on disk. That gap is the whole point of the row.
func corruptGoalRecordFile(t *testing.T, taskID string) {
	t.Helper()
	gs := goal.NewStore(config.OmnipusHomeDir())
	rec, err := gs.GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		t.Fatalf("fixture: the task must have a paired goal record to corrupt: %v", err)
	}
	path := filepath.Join(gs.Dir(), rec.GoalID+".json")
	if wErr := os.WriteFile(path, []byte(`{"goal_id":"truncated mid-w`), 0o600); wErr != nil {
		t.Fatalf("fixture: corrupt the goal record file: %v", wErr)
	}
	// Prove the fixture produced the state the row is about, not some other
	// one: the record is unreadable AND indistinguishable from absent.
	if _, gErr := gs.GetByOwner(generated.GoalOwnerKindTask, taskID); gErr == nil {
		t.Fatal("fixture: GetByOwner still resolves the corrupted record")
	}
	_, skipped, lErr := gs.List()
	if lErr != nil {
		t.Fatalf("fixture: List itself failed, which is a different fault shape: %v", lErr)
	}
	if len(skipped) == 0 {
		t.Fatal("fixture: the corrupted record was not reported as skipped, so this row is not " +
			"exercising the unparseable-record path at all")
	}
}

// duplicateGoalRecordFile copies the goal record paired with taskID to a second
// file under a different goal id, leaving two records claiming one owner.
// GetByOwner refuses to guess between them and returns a real error that is NOT
// ErrOwnerNotFound — the other fault shape, and the one a naive fix catches.
func duplicateGoalRecordFile(t *testing.T, taskID string) {
	t.Helper()
	gs := goal.NewStore(config.OmnipusHomeDir())
	rec, err := gs.GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		t.Fatalf("fixture: the task must have a paired goal record to duplicate: %v", err)
	}
	raw, rErr := os.ReadFile(filepath.Join(gs.Dir(), rec.GoalID+".json"))
	if rErr != nil {
		t.Fatalf("fixture: read the goal record file: %v", rErr)
	}
	dupID := rec.GoalID + "-dup"
	if wErr := os.WriteFile(filepath.Join(gs.Dir(), dupID+".json"), raw, 0o600); wErr != nil {
		t.Fatalf("fixture: write the duplicate goal record file: %v", wErr)
	}
	if _, gErr := gs.GetByOwner(generated.GoalOwnerKindTask, taskID); gErr == nil {
		t.Fatal("fixture: the duplicate did not make GetByOwner fail")
	}
}

// TestUnreadableDoDCannotCompleteTheTask_C2 is the oracle.
//
// The outcome for a fault row is EC-6's judge-unavailable shape, which
// adjudicateClaim already uses twice for the same reason: this is a claim that
// CANNOT be adjudicated, not a claim that is false. Non-terminal, no round and
// no attempt consumed, the task left in_progress with its run still open, and
// the Judge never dispatched at all — a claim that cannot be judged must not
// cost a verifier turn either.
//
// Why "no attempt consumed" is asserted and not merely "not done": failing a
// transient storage fault onto the worker's attempt budget would trade one
// silent wrong answer for another. The gate has to fail closed WITHOUT being
// punitive.
func TestUnreadableDoDCannotCompleteTheTask_C2(t *testing.T) {
	cases := []struct {
		name string
		// breakGoalStore mutates the goal store on disk after the record has
		// been seeded. nil leaves it intact.
		breakGoalStore func(t *testing.T, taskID string)
		// seedGoal is false for the record-less control row.
		seedGoal bool

		wantStatus     task.Status
		wantJudgeCalls int
	}{
		{
			name:     "an_unparseable_goal_record_blocks_completion",
			seedGoal: true,
			// The review's named scenario. This record exists on disk and
			// carries a DoD; it just cannot be read. GetByOwner reports it as
			// ErrOwnerNotFound, so "no record" and "unreadable record" are the
			// same answer unless the caller checks List's `skipped` itself.
			breakGoalStore: corruptGoalRecordFile,
			wantStatus:     task.StatusInProgress,
			wantJudgeCalls: 0,
		},
		{
			name:     "an_ambiguous_goal_record_blocks_completion",
			seedGoal: true,
			// The other fault shape: GetByOwner returns a real, non-
			// ErrOwnerNotFound error.
			breakGoalStore: duplicateGoalRecordFile,
			wantStatus:     task.StatusInProgress,
			wantJudgeCalls: 0,
		},
		{
			name:     "a_task_that_genuinely_has_no_goal_record_still_completes",
			seedGoal: false,
			// GOAL-FR-023. The control that stops the fix becoming "refuse
			// anything without a DoD": this task really does have none, so it
			// is judged on its acceptance criteria alone and reaches done.
			breakGoalStore: nil,
			wantStatus:     task.StatusDone,
			wantJudgeCalls: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			judge := &perCriterionJudgeProvider{}
			judgeInst.Provider = judge

			stored := mustCreateInProgressTask(t, al, &task.Task{
				AgentID: "native-agent", WorkspaceID: "test-ws",
				Title:    "ship the CSV export",
				Prompt:   "make the export endpoint return CSV",
				Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
			})
			_, taskSessionID := newGoalTestSession(t, al, "native-agent")

			if tc.seedGoal {
				seedActiveTaskGoalWithDoD(t, stored.ID, taskSessionID,
					"make the export endpoint return CSV",
					[]task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
					[]task.AcceptanceCriterion{proseCriterion("", dodTestDoDText)})
			}
			if tc.breakGoalStore != nil {
				tc.breakGoalStore(t, stored.ID)
			}

			al.taskExecutor.adjudicateClaim(context.Background(), stored, taskSessionID,
				"Implemented the CSV export and exercised the endpoint by hand.", nil)

			final, err := GetTaskStore(al).Get(stored.ID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if final.Status != tc.wantStatus {
				t.Errorf("task status = %q, want %q (result: %q).\n"+
					"A task's Definition of Done lives ONLY on its paired goal record. When that "+
					"record cannot be read, the judged set silently narrows to the acceptance "+
					"criteria alone — they pass, the AND-fold verdict comes back Met, and the task "+
					"completes with the operator's mandatory DoD never evaluated. The display path "+
					"already answers 500 on this identical unreadable record (toWireTask, SF-6); "+
					"the GATE must not be laxer than the display.",
					final.Status, tc.wantStatus, final.Result)
			}
			if got := judge.callCount(); got != tc.wantJudgeCalls {
				t.Errorf("Judge dispatched %d time(s), want %d — a claim that cannot be judged "+
					"against the authored gate must be refused BEFORE dispatch, exactly as the "+
					"empty-claim and judge-unregistered branches are, rather than spending a "+
					"verifier turn on a set we already know is incomplete",
					got, tc.wantJudgeCalls)
			}
			if tc.wantStatus == task.StatusInProgress && final.AttemptCount != 0 {
				t.Errorf("attempt_count = %d after an unadjudicable claim, want 0 — a transient "+
					"storage fault must not burn the worker's attempt budget. This is EC-6's "+
					"judge-unavailable shape: non-terminal, no round and no attempt consumed, the "+
					"task left in_progress for a later retry.",
					final.AttemptCount)
			}
		})
	}
}

// TestUnreadableDoDDoesNotBlockAnUnrelatedTask is the blast-radius bound.
//
// The unparseable-record check keys on List's `skipped` set, which is store-
// wide: it cannot tell WHICH owner an unreadable file belonged to, because the
// owner is recorded inside the file. That is fine for a task whose own record
// could not be resolved — "cannot be proven absent" is not "absent". It would
// NOT be fine as a store-wide halt: one corrupt file must not stop every other
// task in the install from being adjudicated.
//
// So the rule is narrow by construction: a skipped record alongside a record
// we DID resolve for this task is irrelevant and does not fault.
func TestUnreadableDoDDoesNotBlockAnUnrelatedTask(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judge := &perCriterionJudgeProvider{}
	judgeInst.Provider = judge

	// The victim of the corruption — a different task entirely.
	broken := mustCreateInProgressTask(t, al, &task.Task{
		AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "some other task",
		Prompt:   "do something else",
		Criteria: []task.AcceptanceCriterion{proseCriterion("", "the other thing works")},
	})
	_, brokenSession := newGoalTestSession(t, al, "native-agent")
	seedActiveTaskGoalWithDoD(t, broken.ID, brokenSession, "do something else",
		[]task.AcceptanceCriterion{proseCriterion("", "the other thing works")},
		[]task.AcceptanceCriterion{proseCriterion("", "the other report is honest")})
	corruptGoalRecordFile(t, broken.ID)

	// The task under test: its own record is intact and resolvable.
	healthy := mustCreateInProgressTask(t, al, &task.Task{
		AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "ship the CSV export",
		Prompt:   "make the export endpoint return CSV",
		Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
	})
	_, healthySession := newGoalTestSession(t, al, "native-agent")
	seedActiveTaskGoalWithDoD(t, healthy.ID, healthySession,
		"make the export endpoint return CSV",
		[]task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
		[]task.AcceptanceCriterion{proseCriterion("", dodTestDoDText)})

	al.taskExecutor.adjudicateClaim(context.Background(), healthy, healthySession,
		"Implemented the CSV export and exercised the endpoint by hand.", nil)

	final, err := GetTaskStore(al).Get(healthy.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if final.Status != task.StatusDone {
		t.Fatalf("task status = %q, want %q — this task's own goal record is intact and its DoD "+
			"was resolvable, so an unrelated corrupt record elsewhere in the store must not stop it "+
			"being adjudicated. A fail-closed rule that halts the whole install on one bad file is a "+
			"denial of service, not a safeguard.", final.Status, task.StatusDone)
	}

	asked := judgeCriteriaSentToJudge(t, judge.lastContent(t))
	judgedTexts := make([]string, 0, len(asked))
	for _, c := range asked {
		judgedTexts = append(judgedTexts, c.Text)
	}
	if !sameStringSet(judgedTexts, []string{dodTestCriterionText, dodTestDoDText}) {
		t.Errorf("the Judge was asked to judge %q, want the criteria UNION the DoD %q — the whole "+
			"authored gate, not a narrowed one", judgedTexts, []string{dodTestCriterionText, dodTestDoDText})
	}
}
