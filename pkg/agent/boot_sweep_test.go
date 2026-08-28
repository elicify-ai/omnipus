// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newBootSweepHarness builds a plan engine harness WITH a lifecycle store
// wired, so boot-sweep tests can persist session records and run the sweep.
func newBootSweepHarness(t *testing.T) *planEngineHarness {
	t.Helper()
	h := newTestPlanEngine(t)
	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	h.pe.SetLifecycleStore(ls)
	h.pe.SetBootSweepBudget(5 * time.Second)
	h.pe.SetSnapshotMaxBytes(DefaultSnapshotMaxBytes)
	h.ls = ls
	return h
}

// persistLifecycle is a test helper that persists a lifecycle record.
func persistLifecycle(t *testing.T, ls *session.LifecycleStore, rec *session.LifecycleRecord) {
	t.Helper()
	if err := ls.Persist(rec); err != nil {
		t.Fatalf("persist lifecycle %q: %v", rec.SessionID, err)
	}
}

// --- FR-118/G-13: boot sweep reconciles non-terminal sessions ---------------

// TestBootSweep_NonTerminalToFailedInterrupted verifies the core sweep: every
// persisted non-terminal session with no live runtime turn becomes
// failed(interrupted) carrying its last checkpoint + undelivered messages,
// within budget (BDD "Boot sweep reconciles non-terminal sessions").
func TestBootSweep_NonTerminalToFailedInterrupted(t *testing.T) {
	h := newBootSweepHarness(t)

	// A running session stranded by a crash, with a checkpoint + undelivered
	// messages that MUST be carried into the failed record.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-running", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind:        session.OwnerScopeHuman,
		LastCheckpointRef:     "ckpt-abc",
		UndeliveredMessageIDs: []string{"msg-1", "msg-2"},
		CreatedAt:             time.Now().Add(-1 * time.Hour),
	})
	// A queued session — also non-terminal, also swept.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-queued", Generation: 1, State: session.LifecycleQueued,
		WorkspaceID: "ws", AgentID: "agent-1", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind: session.OwnerScopeHuman,
	})
	// A terminal session — MUST be left alone.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-done", Generation: 1, State: session.LifecycleCompleted,
		WorkspaceID: "ws", AgentID: "agent-1", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind: session.OwnerScopeHuman,
	})

	var failedHooked []string
	h.pe.SetSessionFailedHook(func(sid, reason string) {
		if reason != failedReasonInterrupted {
			t.Errorf("hook reason = %q, want %q", reason, failedReasonInterrupted)
		}
		failedHooked = append(failedHooked, sid)
	})

	res := h.pe.runBootSweep(context.Background())

	if res.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2 (running+queued; terminal excluded by filter)", res.Scanned)
	}
	if len(res.SweptToFailed) != 2 {
		t.Fatalf("SweptToFailed = %v, want 2 sessions", res.SweptToFailed)
	}
	if len(failedHooked) != 2 {
		t.Errorf("session.failed hook fired %d times, want 2", len(failedHooked))
	}

	// The swept record carries its checkpoint + undelivered messages.
	swept, err := h.ls.Load("sess-running")
	if err != nil {
		t.Fatalf("load swept record: %v", err)
	}
	if swept.State != session.LifecycleFailed {
		t.Errorf("swept state = %q, want failed", swept.State)
	}
	if swept.FailedReason != failedReasonInterrupted {
		t.Errorf("swept failed_reason = %q, want %q", swept.FailedReason, failedReasonInterrupted)
	}
	if swept.LastCheckpointRef != "ckpt-abc" {
		t.Errorf("swept checkpoint = %q, want ckpt-abc", swept.LastCheckpointRef)
	}
	if len(swept.UndeliveredMessageIDs) != 2 {
		t.Errorf("swept undelivered = %v, want 2 msgs", swept.UndeliveredMessageIDs)
	}
	// Same generation (no follow_up mint on a sweep).
	if swept.Generation != 1 {
		t.Errorf("swept generation = %d, want 1", swept.Generation)
	}

	// Terminal session untouched.
	done, _ := h.ls.Load("sess-done")
	if done.State != session.LifecycleCompleted {
		t.Errorf("terminal session changed state: %q", done.State)
	}
}

// --- FR-119/R§8.6: needs_input reconstructability exemption ---------------

// TestBootSweep_NeedsInputReconstructable_Preserved verifies exemption (a): a
// parked needs_input session that IS reconstructable at boot is preserved.
func TestBootSweep_NeedsInputReconstructable_Preserved(t *testing.T) {
	h := newBootSweepHarness(t)
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-ni", Generation: 1, State: session.LifecycleNeedsInput,
		WorkspaceID: "ws", AgentID: "agent-1", LaunchProfile: session.LaunchProfileSpecialist,
		OwnerScopeKind:    session.OwnerScopeHuman,
		LastCheckpointRef: "ckpt-1",
		NeedsInput:        &session.NeedsInput{CorrelationID: "corr-1", TTLDeadline: time.Now().Add(24 * time.Hour)},
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.PreservedNeedsInput) != 1 || res.PreservedNeedsInput[0] != "sess-ni" {
		t.Fatalf("PreservedNeedsInput = %v, want [sess-ni]", res.PreservedNeedsInput)
	}
	if len(res.SweptToFailed) != 0 {
		t.Fatalf("SweptToFailed = %v, want none", res.SweptToFailed)
	}
	rec, _ := h.ls.Load("sess-ni")
	if rec.State != session.LifecycleNeedsInput {
		t.Errorf("preserved session state changed to %q", rec.State)
	}
}

// TestBootSweep_NeedsInputNotReconstructable_Swept verifies that a
// needs_input session failing any reconstructability clause is swept. Here
// clause (1) fails (no checkpoint at park).
func TestBootSweep_NeedsInputNotReconstructable_Swept(t *testing.T) {
	h := newBootSweepHarness(t)
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-ni-nockpt", Generation: 1, State: session.LifecycleNeedsInput,
		WorkspaceID: "ws", AgentID: "agent-1", LaunchProfile: session.LaunchProfileSpecialist,
		OwnerScopeKind: session.OwnerScopeHuman,
		// no LastCheckpointRef -> clause (1) fails
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-1", TTLDeadline: time.Now().Add(24 * time.Hour)},
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.SweptToFailed) != 1 {
		t.Fatalf("SweptToFailed = %v, want the non-reconstructable needs_input swept", res.SweptToFailed)
	}
}

// TestIsNeedsInputReconstructable_Predicate exercises the four R§8.6 clauses
// in isolation (BDD "needs_input reconstructability predicate").
func TestIsNeedsInputReconstructable_Predicate(t *testing.T) {
	h := newBootSweepHarness(t)
	base := &session.LifecycleRecord{
		SessionID: "s", State: session.LifecycleNeedsInput, AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman, LastCheckpointRef: "ckpt",
		NeedsInput: &session.NeedsInput{CorrelationID: "corr"},
	}
	cases := []struct {
		name string
		mut  func(*session.LifecycleRecord)
		want bool
	}{
		{"all four clauses satisfied", func(*session.LifecycleRecord) {}, true},
		{"clause1 no checkpoint", func(r *session.LifecycleRecord) { r.LastCheckpointRef = "" }, false},
		{"clause2 agent deleted", func(r *session.LifecycleRecord) { r.AgentID = "ghost" }, false},
		{"clause3 no correlation", func(r *session.LifecycleRecord) { r.NeedsInput.CorrelationID = "" }, false},
		{"clause3 no owner scope", func(r *session.LifecycleRecord) { r.OwnerScopeKind = "" }, false},
		{"not needs_input state", func(r *session.LifecycleRecord) { r.State = session.LifecycleRunning }, false},
	}
	h.pe.SetAgentResolver(func(id string) bool { return id != "ghost" })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := *base
			c.mut(&r)
			if got := h.pe.isNeedsInputReconstructable(&r, DefaultSnapshotMaxBytes); got != c.want {
				t.Errorf("isNeedsInputReconstructable = %v, want %v", got, c.want)
			}
		})
	}
}

// --- FR-147/C1: awaiting-supervision exemption + durable restart ------

// TestBootSweep_AwaitingCorrectionOwnerExempt verifies exemption (b): a paused
// plan-owner session whose plan is durably awaiting_supervision is NOT
// swept, resolved via OwnsPlanID -> plan.PlanPhase (NOT owner_scope).
func TestBootSweep_AwaitingCorrectionOwnerExempt(t *testing.T) {
	h := newBootSweepHarness(t)
	// A plan parked durably at awaiting_supervision.
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "plan-1", Title: "plan-1", WorkspaceID: "ws", OwnerAgentID: "owner-agent",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "sig-xyz",
	})
	// The plan-owner session: paused, owner_scope=human (so owner_scope CANNOT
	// identify the plan), but OwnsPlanID names the awaiting-correction plan.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-owner", Generation: 1, State: session.LifecyclePaused,
		WorkspaceID: "ws", AgentID: "owner-agent", LaunchProfile: session.LaunchProfileSpecialist,
		OwnerScopeKind: session.OwnerScopeHuman, // human, not plan_id
		OwnsPlanID:     "plan-1",                // the named linkage
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.PreservedAwaitingCorrection) != 1 || res.PreservedAwaitingCorrection[0] != "sess-owner" {
		t.Fatalf("PreservedAwaitingCorrection = %v, want [sess-owner]", res.PreservedAwaitingCorrection)
	}
	if len(res.SweptToFailed) != 0 {
		t.Fatalf("SweptToFailed = %v, want none (owner exempt)", res.SweptToFailed)
	}
}

// TestBootSweep_PausedOwnerNotAwaitingCorrection_Swept verifies that a paused
// session whose plan is NOT awaiting-correction IS swept (the exemption is
// narrow — only the durable awaiting-correction condition exempts).
func TestBootSweep_PausedOwnerNotAwaitingCorrection_Swept(t *testing.T) {
	h := newBootSweepHarness(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "plan-2", Title: "plan-2", WorkspaceID: "ws", OwnerAgentID: "owner-agent",
		State: plan.StateRunning, PlanPhase: plan.PhaseDispatching, // not awaiting-correction
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-owner-2", Generation: 1, State: session.LifecyclePaused,
		WorkspaceID: "ws", AgentID: "owner-agent", LaunchProfile: session.LaunchProfileSpecialist,
		OwnerScopeKind: session.OwnerScopeHuman, OwnsPlanID: "plan-2",
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.SweptToFailed) != 1 {
		t.Fatalf("SweptToFailed = %v, want the paused non-awaiting owner swept", res.SweptToFailed)
	}
}

// TestDurableC1_RestartNoRoundBurn is THE standalone-F2 restart regression
// (FR-147/FR-193/INV-7 across INV-9). A plan durably parked at
// awaiting_supervision with a persisted last_unmet_terminal_signature
// survives a restart (simulated by a fresh PlanEngine with an empty in-memory
// gate): bootReconcile rehydrates the signature from the plan record, so the
// first post-restart processPlan does NOT burn a JudgeRound re-judging the
// unchanged all-terminal state.
func TestDurableC1_RestartNoRoundBurn(t *testing.T) {
	dir := t.TempDir()
	ps := plan.New(filepath.Join(dir, "plans"))
	ts := task.New(filepath.Join(dir, "tasks"))

	// --- "before restart" engine: drive a plan to awaiting-supervision ---
	judge1 := &fakePlanJudge{resultFn: func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: false, PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: false, Reason: "not done"},
		}}}
	}}
	pe1 := &PlanEngine{
		planStore: ps, taskStore: ts, dispatcher: &fakePlanDispatcher{store: ts},
		judge: judge1, clock: realPlanEngineClock{}, tickInterval: defaultPlanEngineTickInterval,
		activeCounters: make(map[string]ActiveCounterFunc), verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema: newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	mustCreatePlan(t, ps, &plan.Plan{
		ID: "plan-c1", Title: "plan-c1", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, DoD: []task.AcceptanceCriterion{planProseCriterion("must be done")},
	})
	// An all-terminal member set (one done task) so the judge fires.
	mustCreateTask(t, ts, &task.Task{ID: "t-c1", Title: "t-c1", WorkspaceID: "ws", PlanID: "plan-c1", Status: task.StatusDone})

	// processPlan -> beginPlanJudgeRound -> judge UNMET -> parks at
	// awaiting_supervision + persists last_unmet_terminal_signature.
	pe1.processPlan(context.Background(), "plan-c1")
	// Drain the async judge round goroutine (runPlanJudgeRound runs in its own
	// goroutine). Poll for the persisted phase rather than a fixed sleep.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p, _ := ps.Get("plan-c1")
		if p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision && p.LastUnmetTerminalSignature != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	parked, _ := ps.Get("plan-c1")
	if parked.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
		t.Fatalf("plan phase = %q, want awaiting_supervision", parked.EffectivePlanPhase())
	}
	if parked.LastUnmetTerminalSignature == "" {
		t.Fatal("last_unmet_terminal_signature not persisted")
	}
	roundsBefore := parked.JudgeRounds
	if judge1.callCount() != 1 {
		t.Fatalf("pre-restart judge calls = %d, want 1", judge1.callCount())
	}

	// --- simulate restart: a FRESH engine over the SAME stores, empty gate ---
	judge2 := &fakePlanJudge{} // a stray re-judge would record a call here
	pe2 := &PlanEngine{
		planStore: ps, taskStore: ts, dispatcher: &fakePlanDispatcher{store: ts},
		judge: judge2, clock: realPlanEngineClock{}, tickInterval: defaultPlanEngineTickInterval,
		activeCounters: make(map[string]ActiveCounterFunc), verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema: newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	// bootReconcile rehydrates the durable signature, then processPlan runs.
	pe2.bootReconcile(context.Background())

	if got := judge2.callCount(); got != 0 {
		t.Errorf("post-restart judge calls = %d, want 0 (durable gate MUST prevent re-judge of unchanged state)", got)
	}
	after, _ := ps.Get("plan-c1")
	if after.JudgeRounds != roundsBefore {
		t.Errorf("JudgeRounds changed across restart: %d -> %d (no round should be burned)", roundsBefore, after.JudgeRounds)
	}
	if after.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
		t.Errorf("plan drifted from awaiting_supervision across restart: %q", after.EffectivePlanPhase())
	}
}

// TestDurableC1_RestartChangedStateReJudges verifies the OTHER half of INV-7:
// after a restart, if the all-terminal state CHANGED (owner appended a
// correction), the engine DOES re-judge (recovery drives the loop, FR-118).
func TestDurableC1_RestartChangedStateReJudges(t *testing.T) {
	dir := t.TempDir()
	ps := plan.New(filepath.Join(dir, "plans"))
	ts := task.New(filepath.Join(dir, "tasks"))

	// Park a plan at awaiting-correction with a persisted signature for a
	// single done member.
	sig := planTerminalSignature([]task.Task{{ID: "t-a", Status: task.StatusDone}})
	mustCreatePlan(t, ps, &plan.Plan{
		ID: "plan-chg", Title: "plan-chg", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: sig,
		DoD:                        []task.AcceptanceCriterion{planProseCriterion("must be done")},
	})
	mustCreateTask(t, ts, &task.Task{ID: "t-a", Title: "t-a", WorkspaceID: "ws", PlanID: "plan-chg", Status: task.StatusDone})

	// Owner appended a correction: a NEW terminal member exists, so the
	// signature is now DIFFERENT from the persisted one.
	mustCreateTask(t, ts, &task.Task{ID: "t-b", Title: "t-b", WorkspaceID: "ws", PlanID: "plan-chg", Status: task.StatusDone})

	// judgeCalled is signaled the instant JudgeCriteria is invoked, so the
	// wait below is event-driven rather than a fixed wall-clock poll.
	// bootReconcile's beginPlanJudgeRound dispatches the round via `go
	// pe.runPlanJudgeRound(...)` (plan_engine.go), so SOME asynchrony is
	// unavoidable — this channel is what makes waiting for it deterministic
	// instead of racy against an arbitrary poll interval/deadline pair.
	// Buffered 1: fakePlanJudge.JudgeCriteria records the call under its own
	// mutex BEFORE invoking resultFn, so by the time this fires
	// judge.callCount() is already 1 — no separate poll of callCount is
	// needed to make that read safe.
	judgeCalled := make(chan struct{}, 1)
	judge := &fakePlanJudge{resultFn: func(in JudgeCriteriaInput) JudgeCriteriaResult {
		select {
		case judgeCalled <- struct{}{}:
		default:
		}
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: true}}
	}}
	pe := &PlanEngine{
		planStore: ps, taskStore: ts, dispatcher: &fakePlanDispatcher{store: ts},
		judge: judge, clock: realPlanEngineClock{}, tickInterval: defaultPlanEngineTickInterval,
		activeCounters: make(map[string]ActiveCounterFunc), verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema: newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	pe.bootReconcile(context.Background())

	// The signature changed -> a round MUST fire (the persisted gate does not
	// match the new member set). 5s is a generous safety net against a
	// genuine hang, not a tight bound to race against — the event fires
	// within microseconds of the goroutine launch on a healthy system, this
	// only needs to be large enough to absorb real scheduling contention
	// without itself becoming a source of flakes (a prior 2s wall-clock poll
	// budget for this same wait was observed failing under package-suite
	// load; see pkg/agent's flaky-suite root-cause report).
	select {
	case <-judgeCalled:
	case <-time.After(5 * time.Second):
		t.Fatalf("judge calls = %d, want 1 (changed all-terminal state MUST re-judge) — "+
			"JudgeCriteria never fired within 5s", judge.callCount())
	}
	if judge.callCount() != 1 {
		t.Fatalf("judge calls = %d, want 1 (changed all-terminal state MUST re-judge)", judge.callCount())
	}
}

// TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart ties the durable
// C1 fix to the boot sweep: a paused owner session of an
// awaiting-correction plan is NOT swept to failed across a restart (the full
// INV-7-preserved-across-INV-9 regression).
func TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart(t *testing.T) {
	h := newBootSweepHarness(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "plan-rs", Title: "plan-rs", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "persisted-sig",
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "owner-rs", Generation: 1, State: session.LifecyclePaused,
		WorkspaceID: "ws", AgentID: "owner", LaunchProfile: session.LaunchProfileSpecialist,
		OwnerScopeKind: session.OwnerScopeHuman, OwnsPlanID: "plan-rs",
	})
	// A stranded running session (no plan) that SHOULD be swept.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "stray", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind: session.OwnerScopeHuman,
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != "stray" {
		t.Fatalf("SweptToFailed = %v, want [stray] only (owner exempt)", res.SweptToFailed)
	}
	owner, _ := h.ls.Load("owner-rs")
	if owner.State != session.LifecyclePaused {
		t.Errorf("owner session swept to %q (must stay paused — exemption b)", owner.State)
	}
}

// --- N-15: live-upgrade re-baseline ---------------------------------------

// TestN15_GoalSemanticsRebaseline verifies the live-upgrade re-baseline hook:
// an in-flight goal predating a trigger-semantics change is quiesced/
// re-baselined (folded into the boot sweep), not swept to failed.
func TestN15_GoalSemanticsRebaseline(t *testing.T) {
	h := newBootSweepHarness(t)
	// A goal-bearing session. With no versioner wired, it is "unversioned"
	// -> swept normally (the mechanism is armed but no version is recorded yet).
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-unversioned", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-1",
	})
	res := h.pe.runBootSweep(context.Background())
	if len(res.RebaselinedGoals) != 0 {
		t.Errorf("RebaselinedGoals = %v, want none (unversioned)", res.RebaselinedGoals)
	}
	if len(res.SweptToFailed) != 1 {
		t.Errorf("unversioned goal should be swept normally: %v", res.SweptToFailed)
	}

	// Now wire a versioner that reports a STALE version for a fresh goal
	// session — it must be re-baselined, not swept. Override the build version
	// to 3 so a recorded version of 1 is genuinely stale (simulating a
	// post-bump build without waiting for a real bump).
	h.pe.currentSemanticsVersionOverride = 3
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-stale", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-2",
	})
	h.pe.SetGoalSemanticsVersioner(func(sid string) int {
		if sid == "goal-stale" {
			return 1 // predates the current build (3)
		}
		return 3
	})
	res2 := h.pe.runBootSweep(context.Background())
	if len(res2.RebaselinedGoals) != 1 || res2.RebaselinedGoals[0] != "goal-stale" {
		t.Fatalf("RebaselinedGoals = %v, want [goal-stale]", res2.RebaselinedGoals)
	}
	if len(res2.SweptToFailed) != 0 {
		t.Errorf("stale goal should NOT be swept: %v", res2.SweptToFailed)
	}
	// The re-baselined session is preserved as running (not swept).
	rec, _ := h.ls.Load("goal-stale")
	if rec.State != session.LifecycleRunning {
		t.Errorf("re-baselined goal state = %q, want running (preserved)", rec.State)
	}

	// A current-version goal is NOT re-baselined (swept normally, since it is
	// a stranded running session with no live turn).
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-current", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", LaunchProfile: session.LaunchProfileUtility,
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-3",
	})
	h.pe.SetGoalSemanticsVersioner(func(sid string) int { return 3 })
	res3 := h.pe.runBootSweep(context.Background())
	if len(res3.RebaselinedGoals) != 0 {
		t.Errorf("current-version goal rebaselined: %v", res3.RebaselinedGoals)
	}
}

// TestN15_ResolveAction_Predicate exercises resolveGoalSemanticsAction directly.
func TestN15_ResolveAction_Predicate(t *testing.T) {
	h := newBootSweepHarness(t)
	h.pe.currentSemanticsVersionOverride = 3
	h.pe.SetGoalSemanticsVersioner(func(sid string) int {
		if sid == "stale" {
			return 1
		}
		if sid == "current" {
			return 3
		}
		return 0 // unversioned
	})
	cases := []struct {
		name string
		rec  *session.LifecycleRecord
		want goalSemanticsAction
	}{
		{"stale goal -> rebaseline", &session.LifecycleRecord{GoalRef: "g", SessionID: "stale"}, goalActionRebaseline},
		{"current goal -> none", &session.LifecycleRecord{GoalRef: "g", SessionID: "current"}, goalActionNone},
		{"unversioned -> none", &session.LifecycleRecord{GoalRef: "g", SessionID: "unversioned"}, goalActionNone},
		{"not a goal -> none", &session.LifecycleRecord{SessionID: "stale"}, goalActionNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := h.pe.resolveGoalSemanticsAction(c.rec); got != c.want {
				t.Errorf("action = %v, want %v", got, c.want)
			}
		})
	}
}
