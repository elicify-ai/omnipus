// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_dod_adjudicated_test.go is the behavioural guard for UAT defect D-1:
// a task's MANDATORY Definition of Done was never adjudicated.
//
// The evidence, from a live gateway run. Task creation REFUSES with 400 unless
// the caller supplies a DoD that is DISTINCT from the acceptance criteria
// (operator decision D-C, GOAL-FR-021/FR-047/FR-048). Every goal record that
// run produced then read `dod: [pending]` — including the record paired with a
// task that reached `done` with `criteria: [met]`. The verdict that was written
// listed `per_criterion` for the acceptance criterion only; the DoD item
// appeared nowhere in it.
//
// The DoD is where "did it report honestly / no secrets / claims grounded"
// lives — the floor-DoD items goal-dod-floor-no-secrets and
// goal-dod-floor-grounded-claims are precisely the protections ADR-084 exists
// to enforce. A mandatory field that is never checked is theatre, and a test
// that only asserted "the DoD reached the prompt" would be theatre too: row 2
// below is the one that matters, because it is the only one that can tell a
// DoD that is SCORED from a DoD that is merely SENT.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// perCriterionJudgeProvider answers a verifier prompt with a verdict decided
// PER CRITERION, keyed by the criterion's own text.
//
// Keying on text rather than id is deliberate: the ids under test are minted
// by task.NormalizeCriteria inside the two stores, so a test cannot know them
// before the fact, and hard-coding them would couple this fixture to the very
// id-plumbing the tests are checking. Text is what an author actually writes
// and is stable across both stores.
//
// A criterion whose text is absent from unmet[] is answered met. Any criterion
// the prompt asks about is always answered — a silently omitted id would be
// scored `criterion_unjudgeable: verifier did not return a verdict` by
// runVerifierAdjudication, which is a DIFFERENT unmet cause and would let row 2
// pass for the wrong reason.
type perCriterionJudgeProvider struct {
	mu       sync.Mutex
	calls    int
	contents []string
	// unmet holds the exact criterion texts to answer met:false for.
	unmet map[string]bool
}

func (p *perCriterionJudgeProvider) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	var user string
	for _, m := range messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	p.mu.Lock()
	p.calls++
	p.contents = append(p.contents, user)
	unmet := p.unmet
	p.mu.Unlock()

	asked := parseJudgeCriteriaBlock(user)
	items := make([]string, 0, len(asked))
	overall := true
	for _, c := range asked {
		met := !unmet[c.Text]
		if !met {
			overall = false
		}
		items = append(items, fmt.Sprintf(
			`{"id":%q,"met":%t,"reason":"scripted per-criterion fixture verdict"}`, c.ID, met))
	}
	return &providers.LLMResponse{
		Content: fmt.Sprintf(`{"met": %t, "criteria": [%s]}`, overall, strings.Join(items, ",")),
	}, nil
}

func (p *perCriterionJudgeProvider) GetDefaultModel() string { return "fake-judge-model" }

func (p *perCriterionJudgeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *perCriterionJudgeProvider) lastContent(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.contents) == 0 {
		t.Fatal("the Judge was never dispatched, so there is no user content to inspect")
	}
	return p.contents[len(p.contents)-1]
}

// parseJudgeCriteriaBlock pulls the id+text pairs out of the real verifier
// prompt buildJudgeUserContent assembles. Same technique (and same header) as
// judgeCriteriaSentToJudge in task_executor_adjudicate_claim_test.go, but
// non-fatal: this one runs inside the provider, where t is not available, and
// a prompt with no criteria block must produce an empty verdict rather than
// abort the process.
func parseJudgeCriteriaBlock(content string) []struct {
	ID   string `json:"id"`
	Text string `json:"text"`
} {
	var out []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	const header = "## Prose criteria to judge"
	hi := strings.Index(content, header)
	if hi < 0 {
		return out
	}
	bi := strings.Index(content[hi:], "[")
	if bi < 0 {
		return out
	}
	_ = json.NewDecoder(strings.NewReader(content[hi+bi:])).Decode(&out)
	return out
}

// seedActiveTaskGoalWithDoD creates the goal record paired with taskID
// carrying the supplied acceptance criteria AND Definition of Done, then
// activates it against sessionID — the same two-step
// rest_tasks.go::syncTaskGoalRecord + task_executor.go::activateTaskGoal
// perform in production.
//
// It returns the record as stored, so a caller reads back the ids pkg/goal
// actually minted rather than the ones it passed in (goal.New normalises both
// lists and mints its own).
func seedActiveTaskGoalWithDoD(
	t *testing.T, taskID, sessionID, prompt string,
	criteria, dod []task.AcceptanceCriterion,
) *goal.Goal {
	t.Helper()
	now := time.Now().UTC()
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.GoalSourceTaskExplicit,
		prompt, "", criteria, dod, config.DefaultGoalMaxRounds, now)
	if err != nil {
		t.Fatalf("seedActiveTaskGoalWithDoD: goal.New: %v", err)
	}
	gs := goal.NewStore(config.OmnipusHomeDir())
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("seedActiveTaskGoalWithDoD: Create: %v", cerr)
	}
	activated, uerr := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.Activate(sessionID, now)
	})
	if uerr != nil {
		t.Fatalf("seedActiveTaskGoalWithDoD: Activate: %v", uerr)
	}
	return activated
}

// readTaskGoal re-reads the goal record paired with taskID straight from the
// store, so every assertion below is about durable state rather than an
// in-memory value the test itself is holding.
func readTaskGoal(t *testing.T, taskID string) *goal.Goal {
	t.Helper()
	g, err := goal.NewStore(config.OmnipusHomeDir()).GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		t.Fatalf("read the goal record paired with task %q: %v", taskID, err)
	}
	return g
}

// criterionStatusByText finds the criterion with the given text in list and
// returns its projected status.
func criterionStatusByText(t *testing.T, list []task.AcceptanceCriterion, text string) task.CriterionStatus {
	t.Helper()
	for _, c := range list {
		if c.Text == text {
			return c.Status
		}
	}
	t.Fatalf("no criterion with text %q in %+v", text, list)
	return ""
}

const (
	dodTestCriterionText = "The CSV export endpoint returns a well-formed CSV body."
	dodTestDoDText       = "The final report states honestly whether the endpoint was actually exercised."
)

// TestTaskDefinitionOfDoneIsAdjudicated_GOALFR047 is UAT defect D-1's oracle.
//
// GOAL-FR-047/FR-048 + operator decision D-C make a DoD mandatory at task
// creation; ADR-080 D-DOD's judged-set union is what makes it mean something.
// goal_compile.go::compiledGoalCriteriaFor states the rule for the chat path in
// its own doc comment — "Without this union the DoD would be defined,
// confirmed, and never scored (ADR-080 R-C1)" — and GOAL-FR-013 requires ONE
// code path for both owner kinds. TaskExecutor.adjudicateRunClaim judged
// t.Criteria alone, and a task's DoD does not live there (there is no Task.Dod
// field at all), so on the task path the DoD was never scored.
//
// The three rows are chosen so that no single wrong implementation passes them
// all:
//
//   - row 1 pins that the DoD is SENT and SCORED (its status is projected onto
//     the goal record, which is the only place a task's DoD lives);
//   - row 2 pins that it has TEETH — acceptance criteria all met, DoD violated,
//     and the task must NOT complete. An implementation that unioned the DoD
//     into the prompt but dropped it from the outcome passes row 1 and fails
//     row 2;
//   - row 3 is the differentiation control: a task with no paired goal record
//     (GOAL-FR-023 legacy/Scratchpad) must still be judged on its acceptance
//     criteria ALONE and must still complete. An implementation that refused
//     any task lacking a DoD, or that injected a phantom DoD, passes rows 1-2
//     and fails row 3.
func TestTaskDefinitionOfDoneIsAdjudicated_GOALFR047(t *testing.T) {
	cases := []struct {
		name string
		// seedGoal controls whether a paired goal record (and therefore a
		// DoD) exists at all.
		seedGoal bool
		// unmetTexts are the criterion texts the Judge answers met:false for.
		unmetTexts []string

		wantJudgedTexts []string
		wantStatus      task.Status
		wantCritStatus  task.CriterionStatus
		// wantDoDStatus is checked only when seedGoal is true.
		wantDoDStatus task.CriterionStatus
	}{
		{
			name:            "dod_is_judged_alongside_the_criteria_and_scored",
			seedGoal:        true,
			unmetTexts:      nil,
			wantJudgedTexts: []string{dodTestCriterionText, dodTestDoDText},
			wantStatus:      task.StatusDone,
			wantCritStatus:  task.CritMet,
			wantDoDStatus:   task.CritMet,
		},
		{
			name:       "an_unmet_dod_blocks_completion_even_when_every_criterion_is_met",
			seedGoal:   true,
			unmetTexts: []string{dodTestDoDText},
			// Same judged set — what changes is the outcome, which is exactly
			// the point: the DoD must be able to FAIL the claim.
			wantJudgedTexts: []string{dodTestCriterionText, dodTestDoDText},
			// An unmet verdict spends a goal try and the worker keeps going
			// in the same run: the task stays in_progress.
			wantStatus:     task.StatusInProgress,
			wantCritStatus: task.CritMet,
			wantDoDStatus:  task.CritUnmet,
		},
		{
			name:            "a_task_with_no_paired_goal_record_is_judged_on_its_criteria_alone",
			seedGoal:        false,
			unmetTexts:      nil,
			wantJudgedTexts: []string{dodTestCriterionText},
			wantStatus:      task.StatusDone,
			wantCritStatus:  task.CritMet,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			unmet := make(map[string]bool, len(tc.unmetTexts))
			for _, txt := range tc.unmetTexts {
				unmet[txt] = true
			}
			judge := &perCriterionJudgeProvider{unmet: unmet}
			judgeInst.Provider = judge

			stored := mustCreateInProgressTask(t, al, &task.Task{
				AgentID: "native-agent", WorkspaceID: "test-ws",
				Title:    "ship the CSV export",
				Prompt:   "make the export endpoint return CSV",
				Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
			})

			_, taskSessionID := newGoalTestSession(t, al, "native-agent")
			if tc.seedGoal {
				// The goal record carries the task's OWN criteria, ids and all:
				// task creation writes one set for both records (GOAL-FR-007),
				// and a claim is judged against the record's criteria, so the
				// verdict projects onto the task's list by those shared ids.
				seeded := seedActiveTaskGoalWithDoD(t, stored.ID, taskSessionID,
					"make the export endpoint return CSV",
					stored.Criteria,
					[]task.AcceptanceCriterion{proseCriterion("", dodTestDoDText)})
				if len(seeded.DoD) != 1 {
					t.Fatalf("fixture broken: seeded goal record carries %d DoD items, want 1", len(seeded.DoD))
				}
			}

			al.taskExecutor.adjudicateRunClaim(context.Background(), stored, taskSessionID,
				"Implemented the CSV export and exercised the endpoint by hand.", nil, &taskRunState{})

			// --- what the Judge was actually ASKED --------------------------

			if got := judge.callCount(); got != 1 {
				t.Fatalf("Judge dispatched %d time(s), want exactly 1", got)
			}
			var judgedTexts []string
			for _, c := range judgeCriteriaSentToJudge(t, judge.lastContent(t)) {
				judgedTexts = append(judgedTexts, c.Text)
			}
			if !sameStringSet(judgedTexts, tc.wantJudgedTexts) {
				t.Errorf("the Judge was asked to judge %q, want %q.\n"+
					"ADR-080 D-DOD's judged-set union (goal_compile.go::compiledGoalCriteriaFor's own doc "+
					"comment) requires Criteria UNION DoD, and GOAL-FR-013 requires the task path to use "+
					"the SAME code path as chat. A task's DoD lives ONLY on its paired goal record "+
					"(there is no Task.Dod field), so judging t.Criteria alone never scores it.",
					judgedTexts, tc.wantJudgedTexts)
			}

			// --- what the verdict actually DID ------------------------------

			final, err := GetTaskStore(al).Get(stored.ID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if final.Status != tc.wantStatus {
				t.Errorf("task status = %q, want %q (result: %q)", final.Status, tc.wantStatus, final.Result)
			}
			if got := criterionStatusByText(t, final.Criteria, dodTestCriterionText); got != tc.wantCritStatus {
				t.Errorf("acceptance criterion status on the TASK = %q, want %q", got, tc.wantCritStatus)
			}

			if !tc.seedGoal {
				return
			}

			rec := readTaskGoal(t, stored.ID)
			if got := criterionStatusByText(t, rec.DoD, dodTestDoDText); got != tc.wantDoDStatus {
				t.Errorf("Definition-of-Done status on the GOAL RECORD = %q, want %q.\n"+
					"This is UAT defect D-1 verbatim: every goal record that run produced read "+
					"dod: [pending], including one whose task reached `done` with criteria: [met]. "+
					"GOAL-FR-036/FR-040/FR-041 require the verdict to be projected onto BOTH lists, "+
					"and a task's DoD list lives on the goal record.", got, tc.wantDoDStatus)
			}
			if rec.LatestVerdict == nil {
				t.Error("the goal record carries no LatestVerdict after a real adjudication — " +
					"Goal.Terminate's contract is that the record survives WITH its verdict, and " +
					"Goal.Reactivate builds the next run's TerminalHistory entry out of exactly " +
					"these fields (goal_triggers.go's review finding 9, on the chat path)")
			}
		})
	}
}

// TestUnmetDoDIsTheReasonTheClaimFailed pins the CAUSE of row 2's failure, not
// just the fact of it.
//
// Row 2 above asserts the task did not complete when only the DoD was unmet.
// That assertion alone would also pass if the claim failed for some unrelated
// reason (a dropped verdict, a store conflict, a judge error). This test reads
// the steering text the worker is actually re-dispatched with and requires the
// DoD item to be the criterion named unmet in it — and, just as importantly,
// requires the MET acceptance criterion NOT to be named. Together those two
// make the DoD the demonstrated cause rather than a coincidence.
//
// The identification is by criterion ID because that is how buildSteeringText
// reports an unmet criterion ("- criterion <id>: <reason>"); it renders ids,
// not texts, for acceptance criteria and DoD items alike. The ids are read
// back off the seeded goal record rather than supplied by the test, since
// goal.New mints its own.
func TestUnmetDoDIsTheReasonTheClaimFailed(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = &perCriterionJudgeProvider{unmet: map[string]bool{dodTestDoDText: true}}

	stored := mustCreateInProgressTask(t, al, &task.Task{
		AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "ship the CSV export",
		Prompt:   "make the export endpoint return CSV",
		Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
	})
	_, taskSessionID := newGoalTestSession(t, al, "native-agent")
	// The task's own criteria, ids and all (GOAL-FR-007) — see the table test above.
	seeded := seedActiveTaskGoalWithDoD(t, stored.ID, taskSessionID, "make the export endpoint return CSV",
		stored.Criteria,
		[]task.AcceptanceCriterion{proseCriterion("", dodTestDoDText)})
	dodID := seeded.DoD[0].ID
	if dodID == "" {
		t.Fatal("fixture broken: the seeded goal record's DoD item carries no id")
	}

	step, steer, _ := al.taskExecutor.adjudicateRunClaim(context.Background(), stored, taskSessionID,
		"Implemented the CSV export and exercised the endpoint by hand.", nil, &taskRunState{})
	if step != runStepContinue {
		t.Fatalf("step = %v, want the run to continue with the Judge's feedback", step)
	}

	final, err := GetTaskStore(al).Get(stored.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	// steer is the text the worker's NEXT turn in this same run receives.
	if !strings.Contains(steer, dodID) {
		t.Errorf("the re-dispatch steering does not name the DoD item that failed (id %q).\n"+
			"got: %q\n"+
			"An unmet DoD must be reported as the reason the claim was refused — otherwise the "+
			"worker is re-dispatched blind and the DoD is unfixable as well as unexplained.",
			dodID, steer)
	}
	// The acceptance criterion was MET, so it must not be reported as a
	// reason. Without this half the test would also pass for an implementation
	// that failed the claim wholesale and blamed everything.
	for _, c := range final.Criteria {
		if c.Text == dodTestCriterionText && strings.Contains(steer, c.ID) {
			t.Errorf("the steering names the MET acceptance criterion (id %q) as unmet:\n%q\n"+
				"only the DoD item was judged unmet in this fixture", c.ID, steer)
		}
	}
}

// sameStringSet reports whether a and b hold the same values, ignoring order.
// Order is deliberately not asserted: ADR-080 D-DOD fixes the union's order
// (criteria first, then DoD) but nothing downstream depends on it, and pinning
// it here would make this test fail for a change that broke nothing.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}
