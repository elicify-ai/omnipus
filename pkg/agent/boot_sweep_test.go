// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"slices"
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

// TestBootSweep_NonTerminalToFailedInterrupted keeps the ordinary sweep's
// exact checkpoint/message/generation and failed-hook contract. Frozen
// ADR-20260928 D8.3 withdraws failed(interrupted) for steered records: those
// same queued/running fixtures must remain completely untouched by this writer.
func TestBootSweep_NonTerminalToFailedInterrupted(t *testing.T) {
	h := newBootSweepHarness(t)
	protected := make(map[string]bootSweepRecordSnapshot)
	for _, state := range []session.LifecycleState{session.LifecycleRunning, session.LifecycleQueued} {
		for _, steered := range []bool{false, true} {
			id := "sess-" + string(state)
			rec := &session.LifecycleRecord{
				SessionID: id, Generation: 1, State: state,
				WorkspaceID: "ws", AgentID: "agent-1", OwnerScopeKind: session.OwnerScopeHuman,
				Origin:            &session.Origin{Kind: session.OriginKindTask, TaskID: "task-" + id},
				LastCheckpointRef: "ckpt-abc", UndeliveredMessageIDs: []string{"msg-1", "msg-2"},
				CreatedAt: time.Now().Add(-time.Hour),
			}
			if steered {
				id += "-steered"
				rec.SessionID = id
				rec.Origin = &session.Origin{Kind: session.OriginKindDelegate}
				rec.SteeredBy = &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"}
			}
			persistLifecycle(t, h.ls, rec)
			if steered {
				protected[id] = snapshotBootSweepRecord(t, h.ls, id)
			}
		}
	}
	// Both terminal classes remain outside the non-terminal scan.
	for _, id := range []string{"sess-done", "sess-done-steered"} {
		rec := &session.LifecycleRecord{
			SessionID: id, Generation: 1, State: session.LifecycleCompleted,
			WorkspaceID: "ws", AgentID: "agent-1", OwnerScopeKind: session.OwnerScopeHuman,
		}
		if id == "sess-done-steered" {
			rec.SteeredBy = &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"}
		}
		persistLifecycle(t, h.ls, rec)
		protected[id] = snapshotBootSweepRecord(t, h.ls, id)
	}

	var failedHooked []string
	h.pe.SetSessionFailedHook(func(sid, reason string) {
		if reason != failedReasonInterrupted {
			t.Errorf("hook(%q) reason = %q, want %q", sid, reason, failedReasonInterrupted)
		}
		failedHooked = append(failedHooked, sid)
	})
	res := h.pe.runBootSweep(context.Background())
	for id, before := range protected {
		assertBootSweepRecordUntouched(t, h.ls, id, before)
	}
	if res.Scanned != 4 {
		t.Errorf("Scanned = %d, want 4 (ordinary and steered running/queued; both terminal records excluded)", res.Scanned)
	}
	wantSwept := []string{"sess-queued", "sess-running"}
	slices.Sort(res.SweptToFailed)
	if !slices.Equal(res.SweptToFailed, wantSwept) {
		t.Errorf("SweptToFailed = %v, want exactly %v — only the non-steered records belong to this sweep (D8.3)", res.SweptToFailed, wantSwept)
	}
	slices.Sort(failedHooked)
	if !slices.Equal(failedHooked, wantSwept) {
		t.Errorf("session.failed hook ids = %v, want exactly %v — no hook may report a steered record failed", failedHooked, wantSwept)
	}
	for _, id := range wantSwept {
		swept, err := h.ls.Load(id)
		if err != nil {
			t.Fatalf("load ordinary swept record %q: %v", id, err)
		}
		if swept.State != session.LifecycleFailed || swept.FailedReason != failedReasonInterrupted {
			t.Errorf("ordinary %q state/reason = %q/%q, want failed/%q", id, swept.State, swept.FailedReason, failedReasonInterrupted)
		}
		if swept.LastCheckpointRef != "ckpt-abc" {
			t.Errorf("ordinary %q checkpoint = %q, want ckpt-abc", id, swept.LastCheckpointRef)
		}
		if !slices.Equal(swept.UndeliveredMessageIDs, []string{"msg-1", "msg-2"}) {
			t.Errorf("ordinary %q undelivered = %v, want exactly [msg-1 msg-2]", id, swept.UndeliveredMessageIDs)
		}
		if swept.Generation != 1 {
			t.Errorf("ordinary %q generation = %d, want 1 (a sweep never mints)", id, swept.Generation)
		}
	}
}

// --- FR-119/R§8.6: needs_input reconstructability exemption ---------------

// TestBootSweep_NeedsInputReconstructable_Preserved verifies exemption (a): a
// parked needs_input session that IS reconstructable at boot is preserved.
func TestBootSweep_NeedsInputReconstructable_Preserved(t *testing.T) {
	h := newBootSweepHarness(t)
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-ni", Generation: 1, State: session.LifecycleNeedsInput,
		WorkspaceID: "ws", AgentID: "agent-1",
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

// TestBoot_ParkedRecoverableWithoutCheckpoint verifies ADR-091 FR-D-006:
// NeedsInput itself is the durable recovery record; a checkpoint is optional.
func TestBoot_ParkedRecoverableWithoutCheckpoint(t *testing.T) {
	h := newBootSweepHarness(t)
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-ni-nockpt", Generation: 1, State: session.LifecycleNeedsInput,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		NeedsInput:     &session.NeedsInput{CorrelationID: "corr-1", TTLDeadline: time.Now().Add(24 * time.Hour)},
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.PreservedNeedsInput) != 1 || res.PreservedNeedsInput[0] != "sess-ni-nockpt" {
		t.Fatalf("PreservedNeedsInput = %v, want [sess-ni-nockpt]", res.PreservedNeedsInput)
	}
	if len(res.SweptToFailed) != 0 {
		t.Fatalf("SweptToFailed = %v, want none", res.SweptToFailed)
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
		{"no checkpoint remains reconstructable", func(r *session.LifecycleRecord) { r.LastCheckpointRef = "" }, true},
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
		SessionID: "sess-owner", Generation: 1, State: session.LifecycleStopped,
		WorkspaceID: "ws", AgentID: "owner-agent",
		OwnerScopeKind: session.OwnerScopeHuman, // human, not plan_id
		OwnsPlanID:     "plan-1",                // the named linkage
		// D2/CRIT-001: persistLocked now requires a StopNote on any record
		// landing LifecycleStopped. This fixture predates that invariant.
		// cause redirect_pause: this durably-parked "awaiting correction"
		// owner is the pre-rename `paused` state, D2/D6's closest match per
		// delegate_park.go's own precedent comment (a fresh instruction
		// superseding the current generation, not a stop/cascade/restart/
		// timeout).
		StopNote: &session.StopNote{At: time.Now().UTC(), By: session.StopActorSystem, Seq: 1, Cause: session.StopCauseRedirectPause},
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.PreservedAwaitingCorrection) != 1 || res.PreservedAwaitingCorrection[0] != "sess-owner" {
		t.Fatalf("PreservedAwaitingCorrection = %v, want [sess-owner]", res.PreservedAwaitingCorrection)
	}
	if len(res.SweptToFailed) != 0 {
		t.Fatalf("SweptToFailed = %v, want none (owner exempt)", res.SweptToFailed)
	}
}

// TestBootSweep_StoppedOwnerNotAwaitingCorrection_StaysStopped pins D6 Boot
// and D8.2's unchanged landed stop note. D8.3 also preserves the running
// steered control: neither belongs to this sweep. An ordinary running task
// root is the positive control that prevents a disabled sweep from passing.
func TestBootSweep_StoppedOwnerNotAwaitingCorrection_StaysStopped(t *testing.T) {
	h := newBootSweepHarness(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "plan-2", Title: "plan-2", WorkspaceID: "ws", OwnerAgentID: "owner-agent",
		State: plan.StateRunning, PlanPhase: plan.PhaseDispatching,
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-owner-2", Generation: 1, State: session.LifecycleStopped,
		WorkspaceID: "ws", AgentID: "owner-agent",
		OwnerScopeKind: session.OwnerScopeHuman, OwnsPlanID: "plan-2",
		Origin:    &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-2", RootSessionID: "parent-2"},
		StopNote:  &session.StopNote{At: time.Now().UTC(), By: session.StopActorSystem, Seq: 1, Cause: session.StopCauseRedirectPause},
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-running-2", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "owner-agent", OwnerScopeKind: session.OwnerScopeHuman,
		Origin:    &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-2", RootSessionID: "parent-2"},
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-ordinary-2", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "owner-agent", OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "task-ordinary-2"},
	})
	ownerBefore := snapshotBootSweepRecord(t, h.ls, "sess-owner-2")
	runningBefore := snapshotBootSweepRecord(t, h.ls, "sess-running-2")

	res := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, "sess-owner-2", ownerBefore)
	assertBootSweepRecordUntouched(t, h.ls, "sess-running-2", runningBefore)
	if len(res.PreservedAwaitingCorrection) != 0 {
		t.Errorf("PreservedAwaitingCorrection = %v, want none — plan-2 is dispatching, so exemption (b) must not fire", res.PreservedAwaitingCorrection)
	}
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != "sess-ordinary-2" {
		t.Errorf("SweptToFailed = %v, want [sess-ordinary-2] only — stopped and running steered records both belong to SteerBootRecovery (D8.3)", res.SweptToFailed)
	}
	ordinary, err := h.ls.Load("sess-ordinary-2")
	if err != nil {
		t.Fatalf("load ordinary positive control: %v", err)
	}
	if ordinary.State != session.LifecycleFailed || ordinary.FailedReason != failedReasonInterrupted || ordinary.Generation != 1 {
		t.Errorf("ordinary control = %q/%q gen=%d, want failed/%q gen=1 (ordinary sweep unchanged)", ordinary.State, ordinary.FailedReason, ordinary.Generation, failedReasonInterrupted)
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
	// goroutine, tracked by judgeWG — beginPlanJudgeRound Add(1)s it
	// synchronously before the `go` statement, so the counter is already
	// non-zero by the time processPlan above returns). Waiting on judgeWG
	// blocks until the goroutine's OWN return — which is AFTER
	// applyJudgeRoundOutcome persists the phase/signature patch this test
	// asserts on, but that same goroutine then falls through into
	// wakeSupervisor(), which issues further planStore.Update writes
	// (SupervisionWakeAt/Attempts/WakeError) into this test's t.TempDir().
	// Polling only for the phase/signature patch (as this used to) observes
	// the goroutine mid-flight, not finished: the test would proceed to spin
	// up pe2 and eventually return while wakeSupervisor's trailing writes
	// were still landing in the SAME temp dir, racing t.TempDir()'s cleanup
	// RemoveAll ("directory not empty") — the goroutine, not the test body,
	// is what must fully exit before this function returns. judgeWG.Wait()
	// is the actual completion signal (see PlanEngine.Stop's doc comment,
	// and the same idiom in plan_engine_g3_fixwave_test.go /
	// plan_engine_adr055_fixwave_test.go / plan_stop_test.go).
	pe1.judgeWG.Wait()
	pe1.wakeWG.Wait()
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
	// Same drain as pe1 above: bootReconcile's processPlan can itself reach
	// beginPlanJudgeRound / wakeSupervisor synchronously (evaluateSupervisionDeadlineLocked's
	// "no wake receipt; re-arming" path does, on THIS engine's fresh, empty
	// in-memory state), but guard against any future path that spawns the
	// round goroutine here too — draining both WaitGroups is a documented
	// no-op when nothing was dispatched (PlanEngine.Stop's doc comment).
	pe2.judgeWG.Wait()
	pe2.wakeWG.Wait()

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
	// Stop() drains judgeWG/wakeWG (plan_engine.go's own doc comment on Stop
	// names this exact failure mode: "in tests, a wake turn writing its
	// session/transcript into an already-removed temp dir"). This test's
	// bootReconcile->beginPlanJudgeRound launches the judge round on its own
	// goroutine, and a met verdict here goes on to synthesize the plan and
	// dispatch an origin-less owner-wake turn on WAKEWG's own goroutine — both
	// still running after the assertions below return unless drained. Without
	// this, t.TempDir()'s automatic RemoveAll races that still-running
	// goroutine's writes into plans/, surfacing as a nondeterministic
	// "TempDir RemoveAll cleanup: ... directory not empty" failure (reproduced
	// under back-to-back stress, unrelated to the assertions this test makes).
	// Safe unconditionally: pe was never Start()ed, so Stop's WaitGroup drain
	// is the only thing that runs, and it returns immediately when both
	// counters are already zero.
	t.Cleanup(pe.Stop)
	pe.bootReconcile(context.Background())

	// The signature changed -> a round MUST fire (the persisted gate does not
	// match the new member set). The round runs on runPlanJudgeRound's own
	// goroutine (judgeWG-tracked); judge.callCount() increments the instant
	// JudgeCriteria is CALLED, well before that same goroutine finishes
	// synthesizeAndComplete's plan-store writes and the subsequent wakeOwner
	// call, so a poll keyed on callCount alone (as this used to be) lets the
	// test return — and t.TempDir() start its RemoveAll cleanup — while the
	// goroutine is still writing into this test's temp dir. Same race and
	// same fix as TestDurableC1_RestartNoRoundBurn above: wait for the
	// goroutine's own exit, not a proxy signal that fires mid-flight.
	pe.judgeWG.Wait()
	pe.wakeWG.Wait()
	if judge.callCount() != 1 {
		t.Fatalf("judge calls = %d, want 1 (changed all-terminal state MUST re-judge)", judge.callCount())
	}
}

// TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart retains the
// non-steered awaiting-correction exemption and stopped note. Frozen D8.3
// also leaves the unrelated steered worker untouched; only an ordinary
// task-root control is swept after reopening the durable lifecycle store.
func TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart(t *testing.T) {
	h := newBootSweepHarness(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "plan-rs", Title: "plan-rs", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "persisted-sig",
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "owner-rs", Generation: 1, State: session.LifecycleStopped,
		WorkspaceID: "ws", AgentID: "owner",
		OwnerScopeKind: session.OwnerScopeHuman, OwnsPlanID: "plan-rs",
		// D2/CRIT-001: see TestBootSweep_AwaitingCorrectionOwnerExempt's
		// sess-owner — same pre-rename `paused` owner fixture, same
		// redirect_pause justification.
		StopNote: &session.StopNote{At: time.Now().UTC(), By: session.StopActorSystem, Seq: 1, Cause: session.StopCauseRedirectPause},
	})
	// D8.3: a steered stray remains this other boot writer's responsibility.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "stray", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-rs", RootSessionID: "parent-rs"},
	})

	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "stray-ordinary", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "task-stray"},
	})
	ownerBefore := snapshotBootSweepRecord(t, h.ls, "owner-rs")
	strayBefore := snapshotBootSweepRecord(t, h.ls, "stray")
	h.ls = session.NewLifecycleStore(h.ls.Dir())
	h.pe.SetLifecycleStore(h.ls)

	res := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, "owner-rs", ownerBefore)
	assertBootSweepRecordUntouched(t, h.ls, "stray", strayBefore)
	if len(res.PreservedAwaitingCorrection) != 1 || res.PreservedAwaitingCorrection[0] != "owner-rs" {
		t.Errorf("PreservedAwaitingCorrection = %v, want [owner-rs] (ordinary exemption b unchanged)", res.PreservedAwaitingCorrection)
	}
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != "stray-ordinary" {
		t.Errorf("SweptToFailed = %v, want [stray-ordinary] only — the owner is exempt and the steered stray is untouched (D8.3)", res.SweptToFailed)
	}
	ordinary, err := h.ls.Load("stray-ordinary")
	if err != nil {
		t.Fatalf("load ordinary stray: %v", err)
	}
	if ordinary.State != session.LifecycleFailed || ordinary.FailedReason != failedReasonInterrupted {
		t.Errorf("ordinary stray = %q/%q, want failed/%q", ordinary.State, ordinary.FailedReason, failedReasonInterrupted)
	}
}

// --- N-15: live-upgrade re-baseline ---------------------------------------

// TestN15_GoalSemanticsRebaseline retains the ordinary versioned-goal
// classification controls. Frozen D8.3 supersedes failed(interrupted) for
// steered goal owners: their whole lifecycle/goal binding and journal stay
// unchanged, whether the version is unrecorded or current.
func TestN15_GoalSemanticsRebaseline(t *testing.T) {
	h := newBootSweepHarness(t)
	// D8.3: this unversioned STEERED goal owner is never failed by the
	// plan sweep. Its ordinary task-root counterpart still takes the old
	// unversioned sweep path, proving that path is not disabled globally.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-unversioned", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a",
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-1",
		Origin:    &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-g1", RootSessionID: "parent-g1"},
	})
	unversionedBefore := snapshotBootSweepRecord(t, h.ls, "goal-unversioned")
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-unversioned-ordinary", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", OwnerScopeKind: session.OwnerScopeHuman,
		GoalRef: "goal-ordinary-1", Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "task-goal-1"},
	})
	res := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, "goal-unversioned", unversionedBefore)
	if len(res.RebaselinedGoals) != 0 {
		t.Errorf("RebaselinedGoals = %v, want none (unversioned)", res.RebaselinedGoals)
	}
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != "goal-unversioned-ordinary" {
		t.Errorf("unversioned swept goals = %v, want [goal-unversioned-ordinary] only — steered goal owners are untouched (D8.3)", res.SweptToFailed)
	}
	ordinary, err := h.ls.Load("goal-unversioned-ordinary")
	if err != nil {
		t.Fatalf("load ordinary unversioned goal: %v", err)
	}
	if ordinary.State != session.LifecycleFailed || ordinary.FailedReason != failedReasonInterrupted || ordinary.GoalRef != "goal-ordinary-1" {
		t.Errorf("ordinary unversioned goal = %q/%q ref=%q, want failed/%q ref=goal-ordinary-1", ordinary.State, ordinary.FailedReason, ordinary.GoalRef, failedReasonInterrupted)
	}

	// Now wire a versioner that reports a STALE version for a fresh goal
	// session — it must be re-baselined, not swept. Override the build version
	// to 3 so a recorded version of 1 is genuinely stale (simulating a
	// post-bump build without waiting for a real bump).
	h.pe.currentSemanticsVersionOverride = 3
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-stale", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a",
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-2",
	})
	staleBefore := snapshotBootSweepRecord(t, h.ls, "goal-stale")
	h.pe.SetGoalSemanticsVersioner(func(sid string) int {
		if sid == "goal-stale" {
			return 1 // predates the current build (3)
		}
		return 3
	})
	res2 := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, "goal-unversioned", unversionedBefore)
	assertBootSweepRecordUntouched(t, h.ls, "goal-stale", staleBefore)
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

	// A current-version steered goal owner stays unchanged too; an ordinary
	// current-version task root still follows the ordinary interrupted sweep.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-current", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a",
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-3",
		Origin:    &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-g3", RootSessionID: "parent-g3"},
	})
	currentBefore := snapshotBootSweepRecord(t, h.ls, "goal-current")
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "goal-current-ordinary", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "a", OwnerScopeKind: session.OwnerScopeHuman,
		GoalRef: "goal-ordinary-3", Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "task-goal-3"},
	})
	h.pe.SetGoalSemanticsVersioner(func(sid string) int { return 3 })
	res3 := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, "goal-unversioned", unversionedBefore)
	assertBootSweepRecordUntouched(t, h.ls, "goal-current", currentBefore)
	assertBootSweepRecordUntouched(t, h.ls, "goal-stale", staleBefore)
	if len(res3.RebaselinedGoals) != 0 {
		t.Errorf("current-version goal rebaselined: %v", res3.RebaselinedGoals)
	}
	if len(res3.SweptToFailed) != 1 || res3.SweptToFailed[0] != "goal-current-ordinary" {
		t.Errorf("current-version swept goals = %v, want [goal-current-ordinary] only (D8.3)", res3.SweptToFailed)
	}
	currentOrdinary, loadErr := h.ls.Load("goal-current-ordinary")
	if loadErr != nil {
		t.Fatalf("load ordinary current-version goal: %v", loadErr)
	}
	if currentOrdinary.State != session.LifecycleFailed || currentOrdinary.FailedReason != failedReasonInterrupted || currentOrdinary.GoalRef != "goal-ordinary-3" {
		t.Errorf("ordinary current goal = %q/%q ref=%q, want failed/%q ref=goal-ordinary-3", currentOrdinary.State, currentOrdinary.FailedReason, currentOrdinary.GoalRef, failedReasonInterrupted)
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
