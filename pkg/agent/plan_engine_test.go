// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Fakes -----------------------------------------------------------------

// fakePlanJudge is a controllable planJudge. resultFn, when set, computes the
// result per call; the zero value always returns a MET verdict.
type fakePlanJudge struct {
	mu       sync.Mutex
	calls    []JudgeCriteriaInput
	resultFn func(in JudgeCriteriaInput) JudgeCriteriaResult
}

func (f *fakePlanJudge) JudgeCriteria(_ context.Context, in JudgeCriteriaInput) JudgeCriteriaResult {
	f.mu.Lock()
	f.calls = append(f.calls, in)
	fn := f.resultFn
	f.mu.Unlock()
	if fn != nil {
		return fn(in)
	}
	return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: true}}
}

func (f *fakePlanJudge) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakePlanDispatcher is a controllable planTaskDispatcher. By default it
// claims the task (next -> in_progress) against the real store, mirroring
// TaskExecutor.ExecuteTask's own guard (errors if the task is not `next`).
type fakePlanDispatcher struct {
	mu    sync.Mutex
	calls []string
	store *task.Store
	// onDispatch overrides the default claim behavior when set.
	onDispatch func(taskID string) error
	// clearedStreaks records every ClearEvidenceGateStreak call (fix-wave
	// item ii regression coverage — see plan_stop_test.go).
	clearedStreaks []string
}

func (f *fakePlanDispatcher) ExecuteTask(_ context.Context, taskID string, _ *int64) error {
	f.mu.Lock()
	f.calls = append(f.calls, taskID)
	fn := f.onDispatch
	f.mu.Unlock()
	if fn != nil {
		return fn(taskID)
	}
	t, err := f.store.Get(taskID)
	if err != nil {
		return err
	}
	if t.Status != task.StatusNext {
		return fmt.Errorf("fakePlanDispatcher: task %q is %s, not next", taskID, t.Status)
	}
	inProgress := task.StatusInProgress
	_, err = f.store.Update(taskID, task.Patch{Status: &inProgress})
	return err
}

// ClearEvidenceGateStreak satisfies planTaskDispatcher's widened contract
// (ADR-052 fix-wave item ii) — mirrors *TaskExecutor.ClearEvidenceGateStreak
// closely enough for cancelMemberLocked's test coverage to assert on.
func (f *fakePlanDispatcher) ClearEvidenceGateStreak(taskID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearedStreaks = append(f.clearedStreaks, taskID)
}

// executeTaskPlanVerified satisfies planTaskDispatcher's bypass method (S1
// plan-gate follow-up, task_executor.go's requirePlanExecuting). This fake
// has no plan-gate logic of its own to bypass — every test in this file that
// exercises dispatchReadyMembers via fakePlanDispatcher is testing the plan
// ENGINE's own decisions, not TaskExecutor's gate — so this is a plain alias
// for ExecuteTask, recorded identically in f.calls.
func (f *fakePlanDispatcher) executeTaskPlanVerified(ctx context.Context, taskID string) error {
	return f.ExecuteTask(ctx, taskID, nil)
}

func (f *fakePlanDispatcher) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakePlanNotifier records every Notify call.
type fakePlanNotifier struct {
	mu     sync.Mutex
	events []AsyncNotifyEvent
}

func (f *fakePlanNotifier) Notify(_ context.Context, event AsyncNotifyEvent) error {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
	return nil
}

func (f *fakePlanNotifier) eventList() []AsyncNotifyEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AsyncNotifyEvent(nil), f.events...)
}

// fakePlanClock is a settable PlanEngineClock.
type fakePlanClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakePlanClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakePlanClock) Set(t time.Time) {
	f.mu.Lock()
	f.now = t
	f.mu.Unlock()
}

// --- Test harness ------------------------------------------------------

type planEngineHarness struct {
	pe    *PlanEngine
	plans *plan.Store
	tasks *task.Store
	judge *fakePlanJudge
	disp  *fakePlanDispatcher
	notif *fakePlanNotifier
	clock *fakePlanClock
	ls    *session.LifecycleStore
}

// newTestPlanEngine builds a PlanEngine with fakes for every collaborator, by
// direct struct-literal construction (same package) rather than
// NewPlanEngine, which requires a fully-booted *AgentLoop. agentLoop is left
// nil throughout: planningConfig()/resolveGlobalCap() both nil-guard it and
// fall back to the package Default* bounds (config.DefaultGlobalActiveLoopCap
// == 16, config.DefaultPlanJudgeMaxRounds == 20, etc.), which is exactly what
// these tests rely on unless a test overrides a plan's own Bounds.
func newTestPlanEngine(t *testing.T) *planEngineHarness {
	t.Helper()
	dir := t.TempDir()
	ps := plan.New(filepath.Join(dir, "plans"))
	ts := task.New(filepath.Join(dir, "tasks"))
	fj := &fakePlanJudge{}
	fd := &fakePlanDispatcher{store: ts}
	fn := &fakePlanNotifier{}
	fc := &fakePlanClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}

	pe := &PlanEngine{
		planStore:        ps,
		taskStore:        ts,
		dispatcher:       fd,
		judge:            fj,
		notifier:         fn,
		clock:            fc,
		tickInterval:     defaultPlanEngineTickInterval,
		activeCounters:   make(map[string]ActiveCounterFunc),
		verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema:        newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	return &planEngineHarness{pe: pe, plans: ps, tasks: ts, judge: fj, disp: fd, notif: fn, clock: fc}
}

func mustCreatePlan(t *testing.T, ps *plan.Store, p *plan.Plan) *plan.Plan {
	t.Helper()
	if err := ps.Create(p); err != nil {
		t.Fatalf("create plan %q: %v", p.ID, err)
	}
	return p
}

// mustCreateRunningPlan builds a running plan WITH a chat origin, which is the
// ordinary case: a plan started from a conversation (create_plan records the
// tool call's channel + chat id). Owner wakes for such a plan take the
// notifier/bus leg, so tests that assert on delivered wake events keep
// measuring the real path.
//
// The origin-LESS case — a plan created through the Plans UI, where both
// fields are legitimately absent — is a first-class state with its own
// delivery rules, covered in plan_wake_delivery_test.go.
func mustCreateRunningPlan(t *testing.T, ps *plan.Store, id, owner string) *plan.Plan {
	t.Helper()
	return mustCreatePlan(t, ps, &plan.Plan{
		ID: id, Title: id, WorkspaceID: "ws", OwnerAgentID: owner, State: plan.StateRunning,
		SourceChannel: "telegram", SourceChatID: "chat-" + id,
	})
}

func mustCreateTask(t *testing.T, ts *task.Store, tk *task.Task) *task.Task {
	t.Helper()
	if err := ts.Create(tk); err != nil {
		t.Fatalf("create task %q: %v", tk.Title, err)
	}
	return tk
}

// planProseCriterion mirrors judge_test.go's proseCriterion (kept distinct to
// avoid a same-package redeclaration — that helper takes an explicit id and
// stamps a different author).
func planProseCriterion(text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		Kind:   task.KindProse,
		Text:   text,
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
	}
}

// --- Global cap boundary (R5) ------------------------------------------

func TestGlobalCap_AdmitBoundary_15_16_17(t *testing.T) {
	h := newTestPlanEngine(t)

	for i := 0; i < 15; i++ {
		mustCreateRunningPlan(t, h.plans, fmt.Sprintf("p-%02d", i), "owner")
	}
	ok, active, capOut := h.pe.Admit("plan")
	if !ok || active != 15 || capOut != 16 {
		t.Fatalf("at 15 active: ok=%v active=%d cap=%d, want ok=true active=15 cap=16", ok, active, capOut)
	}

	mustCreateRunningPlan(t, h.plans, "p-16", "owner") // 16th running plan
	ok, active, capOut = h.pe.Admit("plan")
	if ok || active != 16 || capOut != 16 {
		t.Fatalf("at 16 active (17th admission attempt): ok=%v active=%d cap=%d, want ok=false active=16 cap=16",
			ok, active, capOut)
	}
}

func TestGlobalCap_RegisteredCountersContributeToTotal(t *testing.T) {
	h := newTestPlanEngine(t)
	for i := 0; i < 10; i++ {
		mustCreateRunningPlan(t, h.plans, fmt.Sprintf("p-%02d", i), "owner")
	}
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 4, nil })
	h.pe.RegisterActiveCounter("loop", func() (int, error) { return 2, nil })

	ok, active, capOut := h.pe.Admit("goal")
	if ok || active != 16 || capOut != 16 {
		t.Fatalf("ok=%v active=%d cap=%d, want ok=false active=16 cap=16 (10 plans + 4 goal + 2 loop == cap)",
			ok, active, capOut)
	}
}

// TestGlobalCap_AdmitFailsClosedOnCounterError is review r1 silent-failure
// MEDIUM 3: a registered ActiveCounterFunc error must deny admission
// (fail-closed), never silently count 0 for that kind and admit past the
// cap on the (wrong) remainder — well under the numeric cap here (2 running
// plans vs. cap 16) proves the denial comes from the error itself, not from
// genuinely being at/over cap.
func TestGlobalCap_AdmitFailsClosedOnCounterError(t *testing.T) {
	h := newTestPlanEngine(t)
	for i := 0; i < 2; i++ {
		mustCreateRunningPlan(t, h.plans, fmt.Sprintf("p-%02d", i), "owner")
	}
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 0, errors.New("boom: counter unavailable") })

	ok, active, capOut := h.pe.Admit("goal")
	if ok {
		t.Fatalf("expected admission to be denied (fail-closed) when a registered counter errors, "+
			"got ok=true active=%d cap=%d", active, capOut)
	}
}

// TestGlobalCap_AdmitFailsClosedOnPlanListError mirrors the counter-error
// test above for the plan-store List call itself.
func TestGlobalCap_AdmitFailsClosedOnPlanListError(t *testing.T) {
	h := newTestPlanEngine(t)
	// Corrupt the plans directory so plan.Store.List returns an error rather
	// than an empty/zero result — a real read fault, not a contrived stub.
	plansDir := h.pe.planStore.Dir()
	if err := os.RemoveAll(plansDir); err != nil {
		t.Fatalf("remove plans dir: %v", err)
	}
	if err := os.WriteFile(plansDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace plans dir with a file: %v", err)
	}

	ok, _, _ := h.pe.Admit("plan")
	if ok {
		t.Fatal("expected admission to be denied (fail-closed) when the plan store List call errors")
	}
}

// --- approved -> running auto-tick + cap-waiting (R1/O2) ------------------

func TestPlanEngine_ApprovedAutoTicksToRunning(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateApproved,
	})

	h.pe.Tick(context.Background())

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want running", got.State)
	}
	if got.LastActivityAt == "" {
		t.Fatal("expected LastActivityAt to be stamped on start")
	}
}

func TestPlanEngine_ApprovedStaysCapWaitingWhenCapFull(t *testing.T) {
	h := newTestPlanEngine(t)
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("running-%02d", i)
		mustCreateRunningPlan(t, h.plans, id, "owner")
		// Give each filler plan one still-in-flight (non-terminal) member task
		// so Tick's own processPlan pass does not see an (vacuously, for zero
		// tasks) "ready to judge" plan and race the cap check below by
		// completing one of these fillers via the fake judge's default MET
		// result before tryStartApprovedPlan runs.
		mustCreateTask(t, h.tasks, &task.Task{
			Title: "filler", WorkspaceID: "ws", PlanID: id, Status: task.StatusInProgress,
		})
	}
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "waiting", Title: "Waiting", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateApproved,
	})

	h.pe.Tick(context.Background())

	got, err := h.plans.Get("waiting")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateApproved {
		t.Fatalf("state = %q, want approved (cap-waiting)", got.State)
	}
}

// --- dispatch + plan-judge met/unmet/rounds-exhausted (FR-058..060) -------

func TestPlanEngine_DispatchesReadyMemberTask(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	tk := mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})

	h.pe.processPlan(context.Background(), "p1")

	calls := h.disp.callList()
	if len(calls) != 1 || calls[0] != tk.ID {
		t.Fatalf("dispatcher calls = %v, want exactly [%s]", calls, tk.ID)
	}
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastActivityAt == "" {
		t.Fatal("expected LastActivityAt to be bumped after a dispatch")
	}
}

func TestPlanEngine_JudgeMet_TransitionsSynthesizingThenDone(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		SourceChannel: "telegram", SourceChatID: "chat-p1",
		DoD: []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone, Result: "all good",
	})
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          true,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "confirmed"}},
		}}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait() // deterministic: wait for the async judge-round goroutine

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateDone {
		t.Fatalf("state = %q, want done", got.State)
	}
	if got.JudgeRounds != 1 {
		t.Fatalf("judge_rounds = %d, want 1", got.JudgeRounds)
	}
	if h.judge.callCount() != 1 {
		t.Fatalf("judge called %d times, want 1", h.judge.callCount())
	}
	events := h.notif.eventList()
	found := false
	for _, e := range events {
		if e.SourceKind == "plan_judge_met" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a plan_judge_met wake event, got %+v", events)
	}
}

// TestPlanEngine_JudgeMet_PersistBeforeWake is the fix-wave regression for
// finding 2 (14-reviewer sign-off): synthesizeAndComplete must durably
// persist State=done (atomically with plan_phase=synthesizing) BEFORE waking
// the owner — never the other way around. Pre-fix, the owner was told the
// plan was MET via a first, separate write of plan_phase=synthesizing, and
// only a SECOND, later write actually flipped State to done; a failure on
// that second write was only logged, leaving the plan stuck at
// State=running forever even though the owner had already been notified.
//
// This is exercised with a plan.Store.OnChange hook (a real extension point,
// not a test-only seam) that makes the plans directory read-only the instant
// it observes a write landing with plan_phase=synthesizing while State is
// NOT YET done — exactly the intermediate on-disk state that only the
// pre-fix two-write sequence could ever produce. The fixed code persists
// both fields in one atomic write, so that intermediate state never hits
// disk, the hook never fires, and the plan reaches Done normally.
func TestPlanEngine_JudgeMet_PersistBeforeWake(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	dir := h.plans.Dir()
	h.plans.OnChange = func(p *plan.Plan) {
		if p.ID == "p1" && p.PlanPhase == plan.PhaseSynthesizing && p.State != plan.StateDone {
			if err := os.Chmod(dir, 0o555); err != nil {
				t.Fatalf("chmod plans dir read-only: %v", err)
			}
		}
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	h.pe.applyJudgeRoundOutcome("p1", JudgeCriteriaResult{
		Verdict: &task.JudgeVerdict{Met: true},
	}, false, "")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	wakeFired := false
	for _, e := range h.notif.eventList() {
		if e.SourceKind == "plan_judge_met" {
			wakeFired = true
		}
	}
	if got.State != plan.StateDone {
		t.Fatalf("state = %q, want done — a plan_judge_met wake fired (%v) claiming the plan is MET "+
			"while it was left at State=running is exactly finding 2's bug", got.State, wakeFired)
	}
	if !wakeFired {
		t.Fatal("expected the plan_judge_met wake to fire once State=done was durably persisted")
	}
}

func TestPlanEngine_JudgeUnmet_StoresSteeringAndWakesOwnerWithoutTerminal(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		DoD: []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "not yet"}},
		}}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want running (unmet, rounds remain)", got.State)
	}
	if got.JudgeRounds != 1 {
		t.Fatalf("judge_rounds = %d, want 1", got.JudgeRounds)
	}
	// ADR-053 C1/FR-147/INV-2: an UNMET verdict on an all-terminal DAG durably
	// parks the plan at plan_phase=awaiting_supervision (NOT dispatching —
	// that was the pre-C1 in-memory-only behavior). The durable phase is what
	// the boot sweep's exemption (b) and the pill crosswalk resolve against.
	if got.PlanPhase != plan.PhaseAwaitingSupervision {
		t.Fatalf("plan_phase = %q, want awaiting_supervision (C1 durable condition)", got.PlanPhase)
	}
	if got.LastUnmetTerminalSignature == "" {
		t.Fatal("last_unmet_terminal_signature must be persisted on an UNMET all-terminal verdict (C1)")
	}
	if got.HandoverText == "" {
		t.Fatal("expected steering text to be persisted in HandoverText")
	}
	// ADR-055/FR-012: the UNMET wake is a SUPERVISION wake — it goes to the
	// adjudicator by direct dispatch, so there is no notifier event to find
	// and nothing is published to the plan's origin chat. What IS observable
	// is the durable wake receipt the wake stamps, which arms FR-021's
	// deadline; assert that instead of a delivery mechanism.
	if got.Supervision == nil {
		t.Fatal("an UNMET verdict must stamp a supervision wake receipt on the plan")
	}
	if got.Supervision.WakeAt == "" {
		t.Fatal("supervision.wake_at must be stamped by the UNMET wake (it arms the deadline)")
	}
	if got.Supervision.Attempts != 1 {
		t.Fatalf("supervision.attempts = %d, want 1 after the first supervision wake", got.Supervision.Attempts)
	}
	for _, e := range h.notif.eventList() {
		if e.SourceKind == "plan_judge_unmet" {
			t.Fatalf("the UNMET wake must NOT be published to the plan's chat origin (H8/FR-016): %+v", e)
		}
	}
}

func TestPlanEngine_JudgeRoundsExhausted_FailsPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	one := 1
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		SourceChannel: "telegram", SourceChatID: "chat-p1",
		DoD:    []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
		Bounds: &plan.PlanBounds{PlanJudgeMaxRounds: &one},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "still not yet"}},
		}}
	}

	// Round 1: consumes the only allowed round (async), ends unmet.
	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	mid, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if mid.State != plan.StateRunning || mid.JudgeRounds != 1 {
		t.Fatalf("after round 1: state=%q judge_rounds=%d, want running/1", mid.State, mid.JudgeRounds)
	}

	// Round 2 attempt: JudgeRounds(1) >= maxRounds(1) -> fails synchronously,
	// WITHOUT a second judge call.
	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.FailedReason != plan.FailedReasonJudgeRoundsExhausted {
		t.Fatalf("failed_reason = %q, want judge_rounds_exhausted", got.FailedReason)
	}
	if h.judge.callCount() != 1 {
		t.Fatalf("judge called %d times, want exactly 1 (rounds-exhausted must not call the judge again)", h.judge.callCount())
	}
	events := h.notif.eventList()
	found := false
	for _, e := range events {
		if e.SourceKind == "plan_judge_rounds_exhausted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a plan_judge_rounds_exhausted wake event, got %+v", events)
	}
}

// TestPlanEngine_UnmetAllTerminal_NoReJudgeOnUnchangedIdleTick is the F2
// regression test (ADR-052 plan engine round-burn defect, acceptance G-9):
// on a plan whose members are ALL terminal but whose plan-level DoD is
// judged UNMET, repeated idle ticks over the SAME unchanged state must NOT
// re-invoke the plan judge or re-debit JudgeRounds — "one round then wait;
// no re-judge of unchanged state." The gate must still reopen once the
// all-terminal state materially changes (here: a member is added, mirroring
// an owner correction) or the plan resumes (TestPlanEngine_ApprovedAutoTicksToRunning
// covers the Play/resume clear path via tryStartApprovedPlan separately).
func TestPlanEngine_UnmetAllTerminal_NoReJudgeOnUnchangedIdleTick(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		DoD: []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "not yet"}},
		}}
	}

	// Round 1: the plan is all-terminal but DoD unmet -> consumes exactly one
	// JudgeRound, same as TestPlanEngine_JudgeUnmet_StoresSteeringAndWakesOwnerWithoutTerminal.
	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	mid, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if mid.State != plan.StateRunning || mid.JudgeRounds != 1 {
		t.Fatalf("after round 1: state=%q judge_rounds=%d, want running/1", mid.State, mid.JudgeRounds)
	}
	if h.judge.callCount() != 1 {
		t.Fatalf("after round 1: judge called %d times, want 1", h.judge.callCount())
	}

	// N (>=3) idle ticks over the exact same unchanged all-terminal state.
	// PRE-FIX: each of these silently re-invoked beginPlanJudgeRound and
	// burned another JudgeRound while nothing about the plan had changed.
	// POST-FIX: none of them may call the judge or touch JudgeRounds at all.
	const idleTicks = 5
	for i := 0; i < idleTicks; i++ {
		h.pe.processPlan(context.Background(), "p1")
		h.pe.judgeWG.Wait()
	}

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.JudgeRounds != 1 {
		t.Fatalf("after %d unchanged idle ticks: judge_rounds = %d, want 1 (round-burn gate must hold)",
			idleTicks, got.JudgeRounds)
	}
	if h.judge.callCount() != 1 {
		t.Fatalf("judge called %d times after %d unchanged idle ticks, want exactly 1 (not re-judged)",
			h.judge.callCount(), idleTicks)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want still running (awaiting supervision, not rounds-exhausted)", got.State)
	}

	// A material state change (here: an owner adds a member) must reopen the
	// gate — the engine re-judges once the all-terminal signature actually
	// changes, exactly as the spec's "member is added/reset" carve-out
	// requires.
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member2", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	final, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if final.JudgeRounds != 2 {
		t.Fatalf("after owner correction (member added): judge_rounds = %d, want 2 (gate must reopen)", final.JudgeRounds)
	}
	if h.judge.callCount() != 2 {
		t.Fatalf("judge called %d times after owner correction, want exactly 2", h.judge.callCount())
	}
}

func TestPlanEngine_JudgeUnavailable_ConsumesNoRoundAndRevertsToDispatching(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		DoD: []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.judge.resultFn = func(_ JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Unavailable: true, Reason: "judge_not_configured"}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want running", got.State)
	}
	if got.JudgeRounds != 0 {
		t.Fatalf("judge_rounds = %d, want 0 (unavailable consumes no round)", got.JudgeRounds)
	}
	if got.PlanPhase != plan.PhaseDispatching {
		t.Fatalf("plan_phase = %q, want dispatching (reverted after unavailability)", got.PlanPhase)
	}
	if got.LastActivityAt != "" {
		t.Fatal("expected LastActivityAt NOT to be bumped by an unavailable judge round (R9/m4)")
	}
}

// --- Owner lifecycle (FR-065) --------------------------------------------

func TestPlanEngine_PauseAndResumePlansOwnedBy(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner-a")
	mustCreateRunningPlan(t, h.plans, "p2", "owner-b")

	if err := h.pe.PausePlansOwnedBy("owner-a"); err != nil {
		t.Fatal(err)
	}
	got1, _ := h.plans.Get("p1")
	got2, _ := h.plans.Get("p2")
	if got1.PausedReason == "" {
		t.Fatal("expected p1 (owner-a) to be paused")
	}
	if got2.PausedReason != "" {
		t.Fatal("expected p2 (owner-b) to be untouched")
	}
	hasActive, err := h.pe.HasActivePlansOwnedBy("owner-a")
	if err != nil {
		t.Fatalf("HasActivePlansOwnedBy: unexpected error: %v", err)
	}
	if !hasActive {
		t.Fatal("HasActivePlansOwnedBy should still report true for a paused-but-running plan")
	}

	if err := h.pe.ResumePlansOwnedBy("owner-a"); err != nil {
		t.Fatal(err)
	}
	got1, _ = h.plans.Get("p1")
	if got1.PausedReason != "" {
		t.Fatalf("expected p1 to be resumed, paused_reason = %q", got1.PausedReason)
	}
}

func TestPlanEngine_PausedPlanIsNotDispatched(t *testing.T) {
	h := newTestPlanEngine(t)
	paused := "owner_disabled"
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		PausedReason: paused,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})

	h.pe.processPlan(context.Background(), "p1")

	if len(h.disp.callList()) != 0 {
		t.Fatalf("expected no dispatch for a paused plan, got %v", h.disp.callList())
	}
}

func TestPlanEngine_HasActivePlansOwnedBy_FalseWhenNoneRunning(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDraft,
	})
	hasActive, err := h.pe.HasActivePlansOwnedBy("owner")
	if err != nil {
		t.Fatalf("HasActivePlansOwnedBy: unexpected error: %v", err)
	}
	if hasActive {
		t.Fatal("expected false: the only plan owned by this agent is draft, not running")
	}
}

// TestPlanEngine_HasActivePlansOwnedBy_FailsClosedOnStoreError is the fix-wave
// regression for finding 1 (14-reviewer sign-off): a plan-store List() error
// (e.g. a transient read failure) must be propagated to the caller, NOT
// swallowed into a bare `false` ("no active plans"). The gateway's
// agent-delete guard relies on this to fail CLOSED — refusing the delete
// rather than assuming it's safe — when it cannot actually verify plan
// ownership.
func TestPlanEngine_HasActivePlansOwnedBy_FailsClosedOnStoreError(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner-a")

	// Uid-independent store-error injection: os.Chmod(dir, 0o000) forces a
	// permission error for a normal user, but the CI worker runs this suite
	// as uid=0 (root) — and root IGNORES permission bits entirely, so
	// List()'s os.ReadDir would still succeed, err would be nil, and this
	// test would report the exact "fail-open bug" it exists to catch, for a
	// bug that does not exist (a false RED, root-only, reproduced on the CI
	// worker but never on a non-root dev machine). Replacing the plans dir
	// with a REGULAR FILE at the same path is uid-independent: os.ReadDir on
	// a non-directory fails with ENOTDIR for root exactly as it does for any
	// other user — a structural error, not a permission check — so this
	// keeps real store-error coverage under CI's root user instead of
	// falling back to a root-guard t.Skip (see pkg/agent/list_all_sessions_test.go,
	// pkg/tools/write_file_reason_test.go for that fallback pattern, not
	// needed here since this injection works for every uid).
	dir := h.plans.Dir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove plans dir: %v", err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace plans dir with a regular file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dir) })

	hasActive, err := h.pe.HasActivePlansOwnedBy("owner-a")
	if err == nil {
		t.Fatal("expected an error when the plan store's List() call fails, got nil (fail-open bug)")
	}
	if hasActive {
		t.Fatal("expected false alongside the error — caller must gate on err, not this value")
	}
}

// --- Idle-expiry sweep (FR-064) -------------------------------------------

func TestPlanEngine_IdleExpirySweep_FailsIdlePlan(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	// State=running stamps LastActivityAt at Create time? No — Create does not
	// run the State-transition side effects (those are Update-only). Bump it
	// explicitly to a known "now" so the idle math is deterministic.
	h.pe.touchActivity("p1")

	h.clock.Set(h.clock.Now().Add(8 * 24 * time.Hour)) // 8 days later (> 7-day default)
	h.pe.idleExpirySweep()

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.FailedReason != plan.FailedReasonIdleExpired {
		t.Fatalf("failed_reason = %q, want idle_expired", got.FailedReason)
	}
	if got.HandoverText == "" {
		t.Fatal("expected a graceful handover to be written")
	}
	events := h.notif.eventList()
	found := false
	for _, e := range events {
		if e.SourceKind == "plan_idle_expired" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a plan_idle_expired wake event, got %+v", events)
	}
}

func TestPlanEngine_IdleExpirySweep_LeavesActivePlanAlone(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	h.pe.touchActivity("p1")

	h.clock.Set(h.clock.Now().Add(6 * 24 * time.Hour)) // < 7 days
	h.pe.idleExpirySweep()

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want still running (not yet idle-expired)", got.State)
	}
}

func TestPlanEngine_IdleExpirySweep_SkipsNonRunningPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDraft,
	})
	h.clock.Set(h.clock.Now().Add(365 * 24 * time.Hour))
	h.pe.idleExpirySweep() // must not panic or touch a non-running plan

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateDraft {
		t.Fatalf("state = %q, want draft (untouched)", got.State)
	}
}

// --- Single-instance overlap guard (FR-063) -------------------------------

func TestPlanEngine_Tick_OverlapGuardSkipsConcurrentTick(t *testing.T) {
	h := newTestPlanEngine(t)
	if !h.pe.claimTick() {
		t.Fatal("first claimTick should succeed")
	}
	defer h.pe.releaseTick()

	// A second Tick call while the first "tick" is still marked running must
	// no-op (return immediately without listing plans / doing any work).
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateApproved,
	})
	h.pe.Tick(context.Background())

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateApproved {
		t.Fatalf("state = %q, want still approved (overlapping tick must have been a no-op)", got.State)
	}
}

// --- Boot reconciliation (FR-061/062) -------------------------------------

func TestPlanReconcile_BootDoesNotDoubleDispatchInProgressTask(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "already running", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})

	h.pe.bootReconcile(context.Background())

	if calls := h.disp.callList(); len(calls) != 0 {
		t.Fatalf("expected zero dispatch calls for an already in_progress task, got %v", calls)
	}
}

func TestPlanReconcile_BootAdvancesBlockedTaskWithDoneDeps(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	dep := mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependency", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependent", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusBlocked, BlockedBy: []string{dep.ID},
	})

	h.pe.bootReconcile(context.Background())

	tasks, err := h.tasks.List(task.Filter{PlanID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	var dependentStatus task.Status
	for i := range tasks {
		if tasks[i].ID != dep.ID {
			dependentStatus = tasks[i].Status
		}
	}
	if dependentStatus != task.StatusInProgress {
		t.Fatalf("dependent task status = %q, want in_progress (advanced blocked->next, then dispatched)", dependentStatus)
	}
	if calls := h.disp.callList(); len(calls) != 1 {
		t.Fatalf("dispatcher calls = %v, want exactly one dispatch (the newly-advanced dependent)", calls)
	}
}

func TestPlanReconcile_BootResumesInterruptedJudgeRound(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		PlanPhase: plan.PhaseJudging, // simulates a crash mid plan-judge round
		DoD:       []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})

	h.pe.bootReconcile(context.Background())
	h.pe.judgeWG.Wait()

	if h.judge.callCount() != 1 {
		t.Fatalf("judge called %d times, want exactly 1 (boot must resume the interrupted round)", h.judge.callCount())
	}
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateDone { // fakePlanJudge's zero-value resultFn returns Met=true
		t.Fatalf("state = %q, want done (resumed round used the default MET fake result)", got.State)
	}
}

// --- Plan-state illegal transitions rejected (defensive, mirrors SD-B5) ---

func TestPlanEngine_FailPlan_NoOpOnAlreadyTerminalPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDone,
	})

	// idleExpireOneLocked re-Gets and bails on a non-running plan before ever
	// attempting a State patch — so no illegal done->failed transition is
	// ever attempted (ValidateStateTransition would reject it: StateDone's
	// only legal outbound edge is the done->done no-op).
	h.pe.planDecisionMu.Lock()
	h.pe.idleExpireOneLocked("p1", h.pe.planningConfig(), h.clock.Now().Add(365*24*time.Hour))
	h.pe.planDecisionMu.Unlock()

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateDone {
		t.Fatalf("state = %q, want unchanged done", got.State)
	}
}

// TestPlanEngine_BudgetExhausted_FailsPlan_M1 proves the silent-M1 fix: the
// app-level OVERALL token budget is debited by ALL workloads (member turns, the
// Judge) but only the GOAL loop surfaced failed(budget_exhausted). The plan
// engine now mirrors the goal loop's boundary gate at processPlan's dispatch
// chokepoint: when the pool is exhausted, the plan transitions to
// failed(budget_exhausted) with a handover instead of draining the pool without
// ever hitting the terminal. Verified at the single chokepoint so it covers BOTH
// member dispatch and plan-level judge rounds.
func TestPlanEngine_BudgetExhausted_FailsPlan_M1(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})

	// Wire an AgentLoop whose token budget is already exhausted. TokenBudget()
	// nil-guards its receiver and returns al.tokenBudget directly (no lock), so a
	// minimal struct literal is safe; the harness leaves agentLoop nil by
	// default (the brake is a no-op there), this test opts into the exhausted case.
	tb := NewTokenBudget(100, nil) // cap 100, no persister
	if _, exh := tb.Debit(100); !exh {
		t.Fatalf("setup: expected budget exhausted after debiting the cap, exh=%v", exh)
	}
	h.pe.agentLoop = &AgentLoop{tokenBudget: tb}

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("state = %q, want failed (budget brake must terminal the plan at the dispatch boundary)", got.State)
	}
	if got.FailedReason != plan.FailedReasonBudgetExhausted {
		t.Fatalf("failed_reason = %q, want budget_exhausted", got.FailedReason)
	}

	// The ready member task must NOT have been dispatched (the brake returns
	// before dispatchReadyMembers): it stays StatusNext.
	tasks, _ := h.tasks.List(task.Filter{PlanID: "p1"})
	if len(tasks) != 1 || tasks[0].Status != task.StatusNext {
		got := "<none>"
		if len(tasks) == 1 {
			got = string(tasks[0].Status)
		}
		t.Fatalf("member task status = %q, want %q (brake must fire before dispatch)", got, task.StatusNext)
	}

	// failPlanLocked wakes the owner with sourceKind plan_<reason>.
	events := h.notif.eventList()
	found := false
	for _, e := range events {
		if e.SourceKind == "plan_budget_exhausted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a plan_budget_exhausted wake event, got %+v", events)
	}

	// Negative control: with an UNBOUNDED budget (cap 0) the brake is a no-op
	// and the same plan would dispatch normally — proving the brake is specific
	// to exhaustion, not a blanket block. (Re-proved with a fresh plan/engine.)
	h2 := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h2.plans, "p2", "owner")
	mustCreateTask(t, h2.tasks, &task.Task{Title: "member2", WorkspaceID: "ws", PlanID: "p2", Status: task.StatusNext})
	h2.pe.agentLoop = &AgentLoop{tokenBudget: NewTokenBudget(0, nil)} // unbounded
	h2.pe.processPlan(context.Background(), "p2")
	got2, _ := h2.plans.Get("p2")
	if got2.State == plan.StateFailed {
		t.Fatalf("unbounded budget must NOT fail the plan, got failed (%s)", got2.FailedReason)
	}
}

// --- Standalone blocked-task self-heal (regression, code review on bc66345f) ---

// TestPlanEngine_Tick_PromotesStandaloneBlockedTaskWhenDepsAllDone proves the
// REGRESSION 1 fix: bc66345f's blocked-derivation fix turned `blocked` into
// an event-triggered latch (recomputeBlockedStateLocked only runs inside
// Create/updateLocked/RestartReset/AddDependency/SpawnReset), and only PLAN
// members got a per-tick defensive re-check (promoteReadyMembers, scoped by
// PlanID). A STANDALONE task's dependent left `blocked` by a lost cascade
// event (e.g. a crash between the blocker's `done` write and
// AdvanceBlockedDependents) had no self-heal at all: it is invisible to
// CheckQueuedTasks' task.Filter{Status: task.StatusNext}, and the store
// rejects a client PATCH back to `next` (ErrBlockedNotSettable) — permanently
// stuck. This test fails before the promoteReadyStandaloneTasks fix (Tick
// never re-examines a blocked standalone task) and passes after it.
func TestPlanEngine_Tick_PromotesStandaloneBlockedTaskWhenDepsAllDone(t *testing.T) {
	h := newTestPlanEngine(t)

	dep := mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependency", WorkspaceID: "ws", Status: task.StatusNext,
	})
	dependent := mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependent", WorkspaceID: "ws",
		Status: task.StatusNext, BlockedBy: []string{dep.ID},
	})
	// Create-time recompute (S2 UAT finding A) must already have latched this
	// to `blocked`, since dep is not yet done.
	setupGot, err := h.tasks.Get(dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if setupGot.Status != task.StatusBlocked {
		t.Fatalf("setup: dependent status = %q, want blocked (unmet dep at create time)", setupGot.Status)
	}

	// Complete the blocker via a plain Update — deliberately NOT calling
	// AdvanceBlockedDependents, simulating the lost-cascade event this
	// regression describes.
	if _, doneErr := h.tasks.Update(dep.ID, task.Patch{Status: ptrStatus(task.StatusDone)}); doneErr != nil {
		t.Fatal(doneErr)
	}

	// Confirm the lost event really did leave the dependent stuck, before any
	// re-check runs.
	stillBlocked, err := h.tasks.Get(dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillBlocked.Status != task.StatusBlocked {
		t.Fatalf("setup: dependent status = %q, want still blocked before any re-check runs", stillBlocked.Status)
	}

	h.pe.Tick(context.Background())

	final, err := h.tasks.Get(dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusNext {
		t.Fatalf("dependent status after Tick = %q, want next (standalone blocked task must self-heal every tick)", final.Status)
	}
}

// TestPlanEngine_PromoteReadyStandaloneTasks_SkipsPlanMembers is the negative
// control: a task with a non-empty PlanID must NEVER be touched by the
// standalone sweep, even when its own plan is not currently `running` (so
// promoteReadyMembers' own per-running-plan pass in processPlan cannot have
// reached it either) — plan members are exclusively promoteReadyMembers'
// concern, and this proves the standalone sweep does not race or
// double-promote them.
func TestPlanEngine_PromoteReadyStandaloneTasks_SkipsPlanMembers(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDraft,
	})
	dep := mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependency", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})
	member := mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusNext, BlockedBy: []string{dep.ID},
	})
	setupGot, err := h.tasks.Get(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if setupGot.Status != task.StatusBlocked {
		t.Fatalf("setup: member status = %q, want blocked (unmet dep at create time)", setupGot.Status)
	}

	if _, doneErr := h.tasks.Update(dep.ID, task.Patch{Status: ptrStatus(task.StatusDone)}); doneErr != nil {
		t.Fatal(doneErr)
	}

	// Call the standalone sweep directly — the plan is `draft`, so Tick's own
	// per-plan loop (State==approved/running only) would never process it
	// either, isolating this assertion to the sweep's own PlanID guard.
	h.pe.promoteReadyStandaloneTasks()

	got, err := h.tasks.Get(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusBlocked {
		t.Fatalf("member status = %q, want still blocked (a PlanID-bearing task must be skipped by the standalone sweep)", got.Status)
	}
}

// --- Round-1 UAT finding #5: "an executed plan silently stalls forever when
// its members are in inbox" -------------------------------------------------
//
// Root cause (confirmed in code before this fix): dispatchReadyMembers only
// ever looks at task.StatusNext; promoteReadyMembers only ever cascades a
// DONE member's blocked_by dependents. Neither ever looks at
// task.StatusInbox, which is exactly the status a task attached to a plan
// (via task detail, or created with plan_id set) starts in — so an inbox
// member of a running/approved plan was invisible to dispatch forever, with
// no self-heal and no signal ("Running 0/N" forever).

// TestPlanEngine_InboxMember_PromotedAndDispatched_AlreadyRunningPlan is the
// per-tick-sweep half of the fix: a member attached to an ALREADY-running
// plan (not just at Execute time) must be promoted and dispatched on the
// very next processPlan pass — mirroring how promoteReadyStandaloneTasks
// re-sweeps every tick rather than acting once. Before the fix, this test
// fails: the inbox member is never dispatched and its status never leaves
// inbox.
func TestPlanEngine_InboxMember_PromotedAndDispatched_AlreadyRunningPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	tk := mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInbox,
	})

	h.pe.processPlan(context.Background(), "p1")

	calls := h.disp.callList()
	if len(calls) != 1 || calls[0] != tk.ID {
		t.Fatalf("dispatcher calls = %v, want exactly [%s] (an inbox member of a running plan must be "+
			"promoted to next and dispatched, not left invisible forever)", calls, tk.ID)
	}
	got, err := h.tasks.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusInProgress {
		t.Fatalf("member status = %q, want in_progress (promoted next -> claimed by dispatch)", got.Status)
	}
}

// TestPlanEngine_ApprovedPlanWithInboxMembers_DispatchedOnExecute_NoManualDrag
// reproduces Tester A's exact repro shape: 3 tasks attached to a plan (each
// landing in inbox, task.normalize()'s default), each with a Prose criterion,
// then Execute (approved -> running admission). All three must dispatch
// immediately — no manual Inbox -> Next drag required. Before the fix, this
// test fails: tryStartApprovedPlan's dispatchReadyMembers call sees zero
// `next` members (all three are still inbox) and dispatches nothing.
func TestPlanEngine_ApprovedPlanWithInboxMembers_DispatchedOnExecute_NoManualDrag(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateApproved,
		DoD: []task.AcceptanceCriterion{planProseCriterion("all three members are done")},
	})
	var memberIDs []string
	for i := 0; i < 3; i++ {
		tk := mustCreateTask(t, h.tasks, &task.Task{
			Title: fmt.Sprintf("member-%d", i), WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInbox,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("done")},
		})
		memberIDs = append(memberIDs, tk.ID)
	}

	h.pe.Tick(context.Background())

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want running (approved plan must auto-tick to running)", got.State)
	}
	calls := h.disp.callList()
	if len(calls) != 3 {
		t.Fatalf("dispatcher calls = %v, want all 3 members dispatched on Execute with no manual drag", calls)
	}
	for _, id := range memberIDs {
		found := false
		for _, c := range calls {
			if c == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("member %q was never dispatched; calls = %v", id, calls)
		}
	}
}

// TestPlanEngine_InboxMember_WithUnmetDependency_StaysBlockedNotDispatched
// pins requirement 2: a member with an unmet blocked_by dependency must
// never be promoted to a dispatchable state, even though it starts in
// inbox. promoteInboxMembers delegates to the ordinary task.Store.Update
// path, whose recomputeBlockedStateLocked runs unconditionally as the
// terminal step of every Update — so setting Status: next on a task with an
// unmet dependency is immediately re-derived to `blocked` by that SAME
// write. It must promote later via the existing cascade once the dependency
// completes, never before.
func TestPlanEngine_InboxMember_WithUnmetDependency_StaysBlockedNotDispatched(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	dep := mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependency", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})
	dependent := mustCreateTask(t, h.tasks, &task.Task{
		Title: "dependent", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusInbox, BlockedBy: []string{dep.ID},
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.tasks.Get(dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusBlocked {
		t.Fatalf("dependent status = %q, want blocked (an unmet dependency must never be promoted to dispatchable)",
			got.Status)
	}
	for _, id := range h.disp.callList() {
		if id == dependent.ID {
			t.Fatalf("dependent was dispatched despite an unmet dependency: calls = %v", h.disp.callList())
		}
	}
}

// TestPlanEngine_PromoteInboxMembers_GateRejectsNonDispatchablePlans is the
// mandated regression: requirement 3 says PermitsMemberDispatch() must
// remain the sole authority on whether a plan may promote/dispatch anything
// — a draft, terminal (done/failed), or paused-running plan must promote
// NOTHING. Calls promoteInboxMembers directly (rather than through
// processPlan/Tick, which already filter most of these states before ever
// reaching it) so the method's OWN internal gate is what's under test, not
// just its callers' outer filtering.
func TestPlanEngine_PromoteInboxMembers_GateRejectsNonDispatchablePlans(t *testing.T) {
	h := newTestPlanEngine(t)
	tk := mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInbox,
	})

	cases := []*plan.Plan{
		{ID: "p1", Title: "draft", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDraft},
		{ID: "p1", Title: "done", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDone},
		{
			ID: "p1", Title: "failed", WorkspaceID: "ws", OwnerAgentID: "owner",
			State: plan.StateFailed, FailedReason: plan.FailedReasonStoppedByUser,
		},
		{
			ID: "p1", Title: "paused-running", WorkspaceID: "ws", OwnerAgentID: "owner",
			State: plan.StateRunning, PausedReason: "owner_disabled",
		},
	}
	for _, p := range cases {
		if promoted := h.pe.promoteInboxMembers(p, []task.Task{*tk}); promoted {
			t.Fatalf("state=%s paused_reason=%q: promoteInboxMembers returned true, want false (must promote nothing)",
				p.State, p.PausedReason)
		}
	}

	got, err := h.tasks.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusInbox {
		t.Fatalf("task status = %q, want inbox (untouched by every non-dispatchable plan state)", got.Status)
	}
	if len(h.disp.callList()) != 0 {
		t.Fatalf("expected no dispatch for any non-dispatchable plan state, got %v", h.disp.callList())
	}
}

// --- Round-1 UAT finding #5, "ALSO" half: a genuinely stalled running plan
// must say so, not spin on "Running 0/N" forever --------------------------

// TestPlanEngine_StallSurfaced_WhenNoMemberIsDispatchableOrInFlight covers a
// stall that survives the inbox-promotion fix above: a member blocked on a
// dependency OUTSIDE this plan's own member set (a standalone task nobody is
// running) can never be resolved by this plan's own dispatch loop. The
// engine must persist a plain-language reason (HandoverText) and wake the
// owner exactly once per distinct condition (no repeat wake on an unchanged
// tick), then clear the note once the plan can make progress again.
func TestPlanEngine_StallSurfaced_WhenNoMemberIsDispatchableOrInFlight(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	// A standalone task OUTSIDE the plan that nobody is running — the
	// dependency this plan's own dispatch loop can never itself resolve.
	outsideBlocker := mustCreateTask(t, h.tasks, &task.Task{
		Title: "outside blocker", WorkspaceID: "ws", Status: task.StatusInbox,
	})
	stuck := mustCreateTask(t, h.tasks, &task.Task{
		Title: "stuck member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusBlocked, BlockedBy: []string{outsideBlocker.ID},
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.HandoverText, stallHandoverNotePrefix) {
		t.Fatalf("HandoverText = %q, want a stall note (prefix %q)", got.HandoverText, stallHandoverNotePrefix)
	}
	if !strings.Contains(got.HandoverText, stuck.ID) {
		t.Fatalf("HandoverText = %q, want it to name the stuck member %q", got.HandoverText, stuck.ID)
	}
	// Swimlane-board UAT fix: the stall must be wire-visible via plan_phase,
	// not just server-side HandoverText, so a "Running 0/N" plan renders a
	// distinct chip instead of looking indistinguishable from live progress.
	if got.PlanPhase != plan.PhaseStalled {
		t.Fatalf("PlanPhase = %q, want %q", got.PlanPhase, plan.PhaseStalled)
	}
	// ADR-055/FR-012: the stall wake is a SUPERVISION wake — direct dispatch
	// to the adjudicator, no notifier event and nothing published to the
	// plan's origin chat. The durable wake receipt is the observable, and it
	// is a strictly better dedup assertion than counting Notify calls: it is
	// what FR-021's deadline is armed from.
	if got.Supervision == nil || got.Supervision.WakeAt == "" {
		t.Fatalf("a stall must stamp a supervision wake receipt, got %+v", got.Supervision)
	}
	if got.Supervision.Attempts != 1 {
		t.Fatalf("supervision.attempts = %d, want 1 after the first stall wake", got.Supervision.Attempts)
	}
	firstWakeAt := got.Supervision.WakeAt
	for _, e := range h.notif.eventList() {
		if e.SourceKind == "plan_stalled" {
			t.Fatalf("the stall wake must NOT be published to the plan's chat origin (H8/FR-016): %+v", e)
		}
	}

	// A second pass with UNCHANGED state must not re-wake: the deadline has
	// not elapsed, so the attempt counter and the receipt stay put.
	h.clock.Set(h.clock.Now().Add(time.Minute))
	h.pe.processPlan(context.Background(), "p1")
	got, err = h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervision.Attempts != 1 || got.Supervision.WakeAt != firstWakeAt {
		t.Fatalf("expected the stall wake to be deduped on an unchanged tick within the deadline, got attempts=%d wake_at=%q (want 1/%q)",
			got.Supervision.Attempts, got.Supervision.WakeAt, firstWakeAt)
	}

	// Once the plan can make progress again (the blocking dependency
	// completes and the member becomes dispatchable), the stall note clears
	// and the member dispatches.
	done := task.StatusDone
	if _, uerr := h.tasks.Update(outsideBlocker.ID, task.Patch{Status: &done}); uerr != nil {
		t.Fatal(uerr)
	}
	if _, uerr := h.tasks.AdvanceUnblocked(stuck.ID); uerr != nil {
		t.Fatal(uerr)
	}
	h.pe.processPlan(context.Background(), "p1")

	got, err = h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(got.HandoverText, stallHandoverNotePrefix) {
		t.Fatalf("HandoverText = %q, want the stall note cleared once the plan is unstuck", got.HandoverText)
	}
	if got.PlanPhase != plan.PhaseDispatching {
		t.Fatalf("PlanPhase = %q, want it reverted to %q once the plan is unstuck", got.PlanPhase, plan.PhaseDispatching)
	}
	// FR-050: leaving the supervision-eligible phase set disarms the deadline
	// and resets the attempt counter, so a later re-stall genuinely re-wakes
	// instead of inheriting a spent budget.
	if got.Supervision.WakeAt != "" || got.Supervision.Attempts != 0 {
		t.Fatalf("leaving the supervision-eligible phase set must clear wake_at and reset attempts, got %+v", got.Supervision)
	}
	dispatchedStuck := false
	for _, id := range h.disp.callList() {
		if id == stuck.ID {
			dispatchedStuck = true
		}
	}
	if !dispatchedStuck {
		t.Fatalf("expected the previously-stuck member to be dispatched once unblocked, calls = %v", h.disp.callList())
	}
}

// TestPlanEngine_StallNotSurfaced_WhenMemberInFlight is the negative case: a
// non-terminal, non-all-done plan with an in_progress member is normal
// mid-flight progress, not a stall — HandoverText must stay untouched and no
// plan_stalled wake must fire.
func TestPlanEngine_StallNotSurfaced_WhenMemberInFlight(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "in flight", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.HandoverText != "" {
		t.Fatalf("HandoverText = %q, want empty (an in-flight member is not a stall)", got.HandoverText)
	}
	for _, e := range h.notif.eventList() {
		if e.SourceKind == "plan_stalled" {
			t.Fatalf("unexpected plan_stalled wake for a plan with an in-flight member: %+v", e)
		}
	}
}

// TestPlanEngine_StallNeverMasksAwaitingSupervision is the PRECEDENCE
// test for the swimlane-board UAT fix: awaiting_supervision (a
// plan-judge dead end, ADR-053 C1) is a strictly more specific condition
// than a generic stall and must NEVER be overwritten by one — testers
// praised that chip by name (see plan.PhaseStalled's doc comment for the
// full rationale). In production this scenario cannot arise (a plan only
// ever enters awaiting_supervision with an all-terminal member DAG,
// which processPlan's own allMembersTerminal check always intercepts before
// reaching surfaceStallIfAny) — this test exercises surfaceStallIfAny's own
// explicit guard directly, so the invariant is pinned even against a future
// refactor of that call order.
func TestPlanEngine_StallNeverMasksAwaitingSupervision(t *testing.T) {
	h := newTestPlanEngine(t)
	steeringText := "Plan judge round 1: the plan judge found the Definition of Done UNMET."
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		HandoverText: steeringText,
	})
	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	// A member set that WOULD read as stalled (planStallReason returns
	// non-empty) if the precedence guard were not in place.
	stalledLookingTasks := []task.Task{
		{ID: "t1", Title: "stuck", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusBlocked, BlockedBy: []string{"outside"}},
	}

	h.pe.surfaceStallIfAny(p, stalledLookingTasks)

	got, gerr := h.plans.Get("p1")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.PlanPhase != plan.PhaseAwaitingSupervision {
		t.Fatalf("PlanPhase = %q, want it to stay %q (never masked by a generic stall)",
			got.PlanPhase, plan.PhaseAwaitingSupervision)
	}
	if got.HandoverText != steeringText {
		t.Fatalf("HandoverText = %q, want the judge's steering text left untouched, got clobbered", got.HandoverText)
	}
	for _, e := range h.notif.eventList() {
		if e.SourceKind == "plan_stalled" {
			t.Fatalf("unexpected plan_stalled wake while awaiting_supervision holds: %+v", e)
		}
	}
}
