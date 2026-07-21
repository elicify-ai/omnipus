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
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
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

func (f *fakePlanDispatcher) ExecuteTask(_ context.Context, taskID string) error {
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

func (f *fakePlanDispatcher) clearedStreakList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.clearedStreaks...)
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

func mustCreateRunningPlan(t *testing.T, ps *plan.Store, id, owner string) *plan.Plan {
	t.Helper()
	return mustCreatePlan(t, ps, &plan.Plan{
		ID: id, Title: id, WorkspaceID: "ws", OwnerAgentID: owner, State: plan.StateRunning,
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
	ok, active, cap := h.pe.Admit("plan")
	if !ok || active != 15 || cap != 16 {
		t.Fatalf("at 15 active: ok=%v active=%d cap=%d, want ok=true active=15 cap=16", ok, active, cap)
	}

	mustCreateRunningPlan(t, h.plans, "p-16", "owner") // 16th running plan
	ok, active, cap = h.pe.Admit("plan")
	if ok || active != 16 || cap != 16 {
		t.Fatalf("at 16 active (17th admission attempt): ok=%v active=%d cap=%d, want ok=false active=16 cap=16",
			ok, active, cap)
	}
}

func TestGlobalCap_RegisteredCountersContributeToTotal(t *testing.T) {
	h := newTestPlanEngine(t)
	for i := 0; i < 10; i++ {
		mustCreateRunningPlan(t, h.plans, fmt.Sprintf("p-%02d", i), "owner")
	}
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 4, nil })
	h.pe.RegisterActiveCounter("loop", func() (int, error) { return 2, nil })

	ok, active, cap := h.pe.Admit("goal")
	if ok || active != 16 || cap != 16 {
		t.Fatalf("ok=%v active=%d cap=%d, want ok=false active=16 cap=16 (10 plans + 4 goal + 2 loop == cap)",
			ok, active, cap)
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

	ok, active, cap := h.pe.Admit("goal")
	if ok {
		t.Fatalf("expected admission to be denied (fail-closed) when a registered counter errors, "+
			"got ok=true active=%d cap=%d", active, cap)
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
	if got.PlanPhase != plan.PhaseDispatching {
		t.Fatalf("plan_phase = %q, want dispatching", got.PlanPhase)
	}
	if got.HandoverText == "" {
		t.Fatal("expected steering text to be persisted in HandoverText")
	}
	events := h.notif.eventList()
	found := false
	for _, e := range events {
		if e.SourceKind == "plan_judge_unmet" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a plan_judge_unmet wake event, got %+v", events)
	}
}

func TestPlanEngine_JudgeRoundsExhausted_FailsPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	one := 1
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
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
	if !h.pe.HasActivePlansOwnedBy("owner-a") {
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
	if h.pe.HasActivePlansOwnedBy("owner") {
		t.Fatal("expected false: the only plan owned by this agent is draft, not running")
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
