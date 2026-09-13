// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_adjudicate_claim_test.go covers TaskExecutor.adjudicateClaim's
// two 7-reviewer-gate fixes (ADR-052 fix wave): item 3 (an empty completion
// claim fails closed BEFORE any verifier dispatch — never a slow judge/
// verifier turn for a claim with nothing to adjudicate) and item 4 (FR-014
// member-path parity — a judge verdict computed while the task concurrently
// left in_progress, e.g. via a Stop, must be dropped rather than silently
// overwriting the Stop outcome or consuming an attempt for a run that was
// actually cancelled).

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestTaskExecutor_AdjudicateClaim_EmptyClaimFailsClosedWithoutVerifierDispatch
// proves item 3: a completion claim with no real content (whitespace-only)
// is rejected fail-closed BEFORE al.JudgeCriteria/runVerifierAdjudication is
// ever reached — the empty-output regression's root cause, per the fix
// wave's finding, was exactly this path dispatching a full verifier turn
// for a claim with nothing to check evidence against.
func TestTaskExecutor_AdjudicateClaim_EmptyClaimFailsClosedWithoutVerifierDispatch(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-empty-claim", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "empty claim test",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	redispatch := al.taskExecutor.adjudicateClaim(context.Background(), tk, "", "   ", nil)

	if fake.callCount() != 0 {
		t.Fatalf("an empty completion claim must never dispatch the verifier; callCount=%d", fake.callCount())
	}
	if redispatch == "" {
		t.Fatal("expected a re-dispatch id (attempt 1 of the default 3, consumed via the goal loop)")
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == task.StatusDone {
		t.Fatal("an empty claim must never resolve to done")
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 (the fail-closed empty-claim reason still consumes an attempt "+
			"like any other unmet outcome)", final.AttemptCount)
	}
	if !strings.Contains(final.Result, "empty claim summary") {
		t.Errorf("result = %q, want it to explain the empty-claim fail-closed reason", final.Result)
	}
}

// TestTaskExecutor_AdjudicateClaim_FR014_DropsStaleVerdictAfterConcurrentStop
// proves item 4: JudgeCriteria's verifier turn runs OUTSIDE any lock, so a
// concurrent Stop (PlanEngine.StopTask, plan_engine.go) can flip the task
// out of in_progress WHILE the judge call is still in flight. adjudicateClaim
// must re-check the task's current status before applying the verdict —
// dropping it (no verdict write, no attempt consumption) rather than
// clobbering the Stop outcome, mirroring plan_engine.go's
// verdictStillApplicable at the plan-round level.
func TestTaskExecutor_AdjudicateClaim_FR014_DropsStaleVerdictAfterConcurrentStop(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	registered := make(chan struct{})
	proceed := make(chan struct{})
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		close(registered)
		<-proceed
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-fr014", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "fr014 test",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the thing is done")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		done <- al.taskExecutor.adjudicateClaim(context.Background(), tk, "", "I finished the thing", nil)
	}()

	<-registered
	// Simulate a concurrent Stop (mirrors plan_engine.go's cancelMemberLocked)
	// landing WHILE the judge call above is still blocked on <-proceed.
	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	stopResult := "[reason:stopped_by_user] Cancelled by tester via Stop."
	if _, err := taskStore.Update(tk.ID, task.Patch{
		Status: &failedStatus, CancelReason: &cancelReason, Result: &stopResult,
	}); err != nil {
		t.Fatalf("simulate concurrent Stop: %v", err)
	}
	close(proceed)
	redispatch := <-done

	if redispatch != "" {
		t.Errorf("adjudicateClaim must not request a re-dispatch once the verdict was dropped, got %q", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("task status=%q cancel_reason=%q, want the Stop outcome UNCHANGED by the stale verdict",
			final.Status, final.CancelReason)
	}
	if final.Result != stopResult {
		t.Errorf("result = %q, want it UNCHANGED from the Stop's own write (%q) — "+
			"the stale MET verdict must never overwrite it", final.Result, stopResult)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a dropped stale verdict must not consume an attempt", final.AttemptCount)
	}
}

// ===========================================================================
// ADR-086 / GOAL-FR-022 and GOAL-FR-023 — wave T3
//
// FR-022: "The trust-the-claim branch in
// pkg/agent/task_executor.go::adjudicateClaim — which completes a task when
// its criteria set and its soft tier are both empty — MUST be deleted."
//
// FR-023: "A task created before FR-021 with no criteria MUST continue to
// run and MUST continue to be judged by pkg/agent/judge.go::SoftTierCriterion.
// FR-021 binds at creation and at edit only."
//
// Read together they are a single behavioural rule with two halves, and the
// tests below deliberately assert BOTH halves against the SAME entry point,
// because either half alone is satisfiable by a wrong implementation: a
// blanket refusal of every criteria-less task passes an FR-022-only test and
// breaks FR-023; the pre-ADR-086 trust-the-claim branch passes an
// FR-023-only test and breaks FR-022.
// ===========================================================================

// judgeCriteriaSentToJudge extracts the criterion ids and texts the verifier
// was actually asked to judge out of the user-message content
// buildJudgeUserContent (pkg/agent/judge.go) assembles. The block is a JSON
// array immediately after a fixed header; json.Decoder stops at the end of
// the array, so no end delimiter has to be guessed.
//
// This reads the REAL prompt the Judge received rather than a value the test
// itself supplied — the only way to prove WHICH criteria adjudicateClaim
// chose (explicit list vs. the ephemeral soft tier) without reaching into
// the function's internals.
func judgeCriteriaSentToJudge(t *testing.T, content string) []struct {
	ID   string `json:"id"`
	Text string `json:"text"`
} {
	t.Helper()
	const header = "## Prose criteria to judge"
	hi := strings.Index(content, header)
	if hi < 0 {
		t.Fatalf("judge user content carries no %q section; got:\n%s", header, content)
	}
	bi := strings.Index(content[hi:], "[")
	if bi < 0 {
		t.Fatalf("judge user content's criteria section carries no JSON array; got:\n%s", content[hi:])
	}
	var out []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(strings.NewReader(content[hi+bi:])).Decode(&out); err != nil {
		t.Fatalf("decode the criteria the Judge was sent: %v (from %q)", err, content[hi+bi:])
	}
	return out
}

// goalFR022JudgeProvider is a Judge provider that records every user-message
// content it is handed and answers with a verdict keyed on exactly the
// criterion ids it was ASKED about (so the per-criterion mapping can never
// be the reason a test fails), with the met/unmet outcome the caller chose.
type goalFR022JudgeProvider struct {
	mu       sync.Mutex
	calls    int
	contents []string
	met      bool
}

func (p *goalFR022JudgeProvider) Chat(
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
	met := p.met
	p.mu.Unlock()

	// Echo back one entry per id the prompt asked for. The ids are read out
	// of the prompt rather than hard-coded so this fixture stays honest for
	// BOTH the explicit-criteria and the soft-tier case; the assertions that
	// matter about WHICH ids those were live in the tests, not here.
	var ids []string
	const header = "## Prose criteria to judge"
	if hi := strings.Index(user, header); hi >= 0 {
		if bi := strings.Index(user[hi:], "["); bi >= 0 {
			var parsed []struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(strings.NewReader(user[hi+bi:])).Decode(&parsed); err == nil {
				for _, c := range parsed {
					ids = append(ids, c.ID)
				}
			}
		}
	}
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, fmt.Sprintf(`{"id":%q,"met":%t,"reason":"scripted fixture verdict"}`, id, met))
	}
	return &providers.LLMResponse{
		Content: fmt.Sprintf(`{"met": %t, "criteria": [%s]}`, met, strings.Join(items, ",")),
	}, nil
}

func (p *goalFR022JudgeProvider) GetDefaultModel() string { return "fake-judge-model" }

func (p *goalFR022JudgeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *goalFR022JudgeProvider) lastContent(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.contents) == 0 {
		t.Fatal("the Judge was never dispatched, so there is no user content to inspect")
	}
	return p.contents[len(p.contents)-1]
}

// mustCreateInProgressTask creates tk in al's task store and moves it to
// in_progress — the status consumeAttemptOrExhaust's compare-and-swap and
// adjudicateClaim's taskVerdictStillApplicable re-read both require.
func mustCreateInProgressTask(t *testing.T, al *AgentLoop, tk *task.Task) *task.Task {
	t.Helper()
	store := GetTaskStore(al)
	if err := store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	inProgress := task.StatusInProgress
	if _, err := store.Update(tk.ID, task.Patch{Status: &inProgress}); err != nil {
		t.Fatalf("move task to in_progress: %v", err)
	}
	fresh, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	return fresh
}

// TestTrustTheClaimPathRemoved_GOALFR022 is the goal spec's row-22 oracle
// (FR-022, S-32). A structurally empty task — no acceptance criteria AND no
// text for SoftTierCriterion to synthesise one from — used to be COMPLETED
// on the worker's say-so. That branch is deleted: the claim is refused
// fail-closed, no verdict is fabricated, and the attempt is consumed like
// any other unmet outcome.
//
// The differentiation rows are what make this a test rather than a
// tautology: a task that still HAS something to judge must reach the Judge
// and must still be completable. An implementation that refused every claim
// would pass row 1 alone and fail rows 2 and 3.
//
// Why row 1 blanks the fields on the in-memory copy rather than storing a
// blank task: pkg/task's own store-layer validation (Store.normalize) makes
// Title mandatory and non-blank, so SoftTierCriterion can never return nil
// for a task READ BACK from the store. The structurally-empty state is
// therefore only reachable by handing adjudicateClaim a Task value whose
// text fields are empty — which is exactly the shape the deleted branch
// existed to handle, and exactly the shape a restored branch would trust.
func TestTrustTheClaimPathRemoved_GOALFR022(t *testing.T) {
	cases := []struct {
		name string
		// blankText empties Title/Description/Prompt on the value handed to
		// adjudicateClaim, making SoftTierCriterion return nil.
		blankText bool
		criteria  []task.AcceptanceCriterion
		judgeMet  bool

		wantJudgeCalls   int
		wantStatus       task.Status
		wantAttemptCount int
		wantRedispatch   bool
	}{
		{
			name:      "no_criteria_and_no_soft_tier_text_is_never_trusted",
			blankText: true,
			criteria:  nil,
			judgeMet:  true, // irrelevant: the Judge must never be reached at all
			// FR-022: refused BEFORE any verifier dispatch, so the claim can
			// neither be trusted nor judged against nothing.
			wantJudgeCalls: 0,
			// Not done, and not silently left in_progress either: the attempt
			// is consumed exactly like every other unmet outcome, so the task
			// re-dispatches (default budget 3, this is attempt 1 of 3).
			wantStatus:       task.StatusNext,
			wantAttemptCount: 1,
			wantRedispatch:   true,
		},
		{
			name:             "no_criteria_but_soft_tier_text_present_is_judged_and_completable",
			blankText:        false,
			criteria:         nil,
			judgeMet:         true,
			wantJudgeCalls:   1,
			wantStatus:       task.StatusDone,
			wantAttemptCount: 0,
			wantRedispatch:   false,
		},
		{
			name:             "explicit_criteria_are_judged_and_completable",
			blankText:        false,
			criteria:         []task.AcceptanceCriterion{proseCriterion("c1", "the widget is green")},
			judgeMet:         true,
			wantJudgeCalls:   1,
			wantStatus:       task.StatusDone,
			wantAttemptCount: 0,
			wantRedispatch:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			judge := &goalFR022JudgeProvider{met: tc.judgeMet}
			judgeInst.Provider = judge

			stored := mustCreateInProgressTask(t, al, &task.Task{
				ID: "t-fr022-" + tc.name, AgentID: "native-agent", WorkspaceID: "test-ws",
				Title: "paint the widget", Description: "it must end up green",
				Prompt: "make the widget green", Criteria: tc.criteria,
			})
			if tc.blankText {
				stored.Title, stored.Description, stored.Prompt = "", "", ""
			}

			redispatch := al.taskExecutor.adjudicateClaim(
				context.Background(), stored, "", "I finished it, trust me.", nil)

			if got := judge.callCount(); got != tc.wantJudgeCalls {
				t.Errorf("Judge dispatched %d time(s), want %d", got, tc.wantJudgeCalls)
			}
			if (redispatch != "") != tc.wantRedispatch {
				t.Errorf("re-dispatch id = %q, want re-dispatch = %v", redispatch, tc.wantRedispatch)
			}
			final, err := GetTaskStore(al).Get(stored.ID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if final.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (result: %q)", final.Status, tc.wantStatus, final.Result)
			}
			if final.AttemptCount != tc.wantAttemptCount {
				t.Errorf("attempt_count = %d, want %d", final.AttemptCount, tc.wantAttemptCount)
			}
			if tc.blankText {
				// The deleted branch's tell: it completed the task with the
				// worker's own claim as the result and consumed nothing.
				if final.Status == task.StatusDone {
					t.Fatal("a structurally empty task completed on the worker's claim alone — " +
						"the trust-the-claim branch GOAL-FR-022 deletes has been reintroduced")
				}
				if !strings.Contains(final.Result, "no soft-tier fallback") {
					t.Errorf("result = %q, want it to state the fail-closed reason (no criteria, no soft tier)",
						final.Result)
				}
			}
		})
	}
}

// TestLegacyCriterialessTaskStillRuns_GOALFR023 is the goal spec's row-23
// oracle (FR-023, S-31): a task with no acceptance criteria — the shape
// FR-021 stops NEW tasks from having, but which pre-FR-021 tasks still carry
// — keeps running and is judged by judge.go::SoftTierCriterion, not blocked
// and not trusted.
//
// The assertion is on the criteria the Judge was ACTUALLY sent, read back
// out of the real prompt: exactly one criterion, carrying the fixed
// soft-tier id and text synthesised from the task per SoftTierCriterion's
// documented precedence (Prompt when present, else Title[: Description]).
// The explicit-criteria row is the differentiation control: the same code
// path must send the task's OWN ids and must NOT fall back to the soft tier.
func TestLegacyCriterialessTaskStillRuns_GOALFR023(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		description string
		prompt      string
		criteria    []task.AcceptanceCriterion

		wantIDs  []string
		wantText string
	}{
		{
			name:     "criteria_less_task_is_judged_by_the_soft_tier_from_its_prompt",
			title:    "paint the widget",
			prompt:   "the widget must end up green",
			criteria: nil,
			wantIDs:  []string{softTierCriterionID},
			// SoftTierCriterion: Prompt wins outright when non-empty.
			wantText: "the widget must end up green",
		},
		{
			name:        "criteria_less_task_with_no_prompt_falls_back_to_title_and_description",
			title:       "paint the widget",
			description: "green, not blue",
			prompt:      "",
			criteria:    nil,
			wantIDs:     []string{softTierCriterionID},
			// SoftTierCriterion: "title: description" when Prompt is empty.
			wantText: "paint the widget: green, not blue",
		},
		{
			name:     "explicit_criteria_are_judged_instead_of_the_soft_tier",
			title:    "paint the widget",
			prompt:   "the widget must end up green",
			criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the widget is green")},
			wantIDs:  []string{"c1"},
			wantText: "the widget is green",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			judge := &goalFR022JudgeProvider{met: true}
			judgeInst.Provider = judge

			stored := mustCreateInProgressTask(t, al, &task.Task{
				ID: "t-fr023-" + tc.name, AgentID: "native-agent", WorkspaceID: "test-ws",
				Title: tc.title, Description: tc.description, Prompt: tc.prompt, Criteria: tc.criteria,
			})

			al.taskExecutor.adjudicateClaim(
				context.Background(), stored, "", "[goal:evidence] painted it green", nil)

			if judge.callCount() != 1 {
				t.Fatalf("Judge dispatched %d time(s), want exactly 1 — a criteria-less legacy task "+
					"MUST continue to run and be judged (GOAL-FR-023), never blocked", judge.callCount())
			}
			sent := judgeCriteriaSentToJudge(t, judge.lastContent(t))
			gotIDs := make([]string, 0, len(sent))
			for _, c := range sent {
				gotIDs = append(gotIDs, c.ID)
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("Judge was sent %d criteria (%v), want %d (%v)",
					len(gotIDs), gotIDs, len(tc.wantIDs), tc.wantIDs)
			}
			for i := range tc.wantIDs {
				if gotIDs[i] != tc.wantIDs[i] {
					t.Errorf("criterion %d id = %q, want %q", i, gotIDs[i], tc.wantIDs[i])
				}
			}
			if sent[0].Text != tc.wantText {
				t.Errorf("criterion text sent to the Judge = %q, want %q", sent[0].Text, tc.wantText)
			}

			// FR-031: the ephemeral soft-tier criterion must never be
			// persisted back onto the task — the projection has no record to
			// write to. The task's own criteria list is unchanged in shape.
			final, err := GetTaskStore(al).Get(stored.ID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if len(final.Criteria) != len(tc.criteria) {
				t.Errorf("persisted criteria count = %d, want %d — the soft-tier criterion must stay "+
					"ephemeral and unpersisted (GOAL-FR-031)", len(final.Criteria), len(tc.criteria))
			}
			for _, c := range final.Criteria {
				if c.ID == softTierCriterionID {
					t.Errorf("the ephemeral soft-tier criterion %q was persisted onto the task "+
						"(GOAL-FR-031 forbids it)", softTierCriterionID)
				}
			}
			if final.Status != task.StatusDone {
				t.Errorf("status = %q, want %q — a met verdict on a legacy criteria-less task must still "+
					"complete it", final.Status, task.StatusDone)
			}
		})
	}
}
