// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_engine_g3_fixwave_test.go is the regression suite for the G3 fix wave:
// seven deferred review findings against plan_engine.go, all in the Stop /
// wake / supervision-escalation path.
//
// ⚠ EVERY test here asserts an OUTCOME the user or operator could observe —
// the plan's terminal state, whether a turn actually ran, whether Stop reported
// success. That is not stylistic. This branch has shipped controls whose tests
// were green while the control did nothing (the fake canceller that returns
// (true, nil) unconditionally; the fake notifier that records an event three
// hops upstream of the guard discarding it), so an assertion of the form "the
// engine called the collaborator" is worth nothing here and is avoided.
//
// The specific shapes each test refuses to accept as passing:
//
//   - a Stop that cancelled nothing and returned nil;
//   - a plan parked with its deadline disarmed that stays running forever;
//   - a plan that reads Done while its closing synthesis was never written.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Doubles ----------------------------------------------------------------

// scriptedCanceller is a sessionCanceller whose per-session answer the test
// chooses. Unlike fakeSessionCanceller (plan_stop_test.go), which returns
// (true, false, nil) for everything, it can express the results that fan-out
// code used to discard: an ERROR (the cancel was never issued), a clean
// fired=false/armed=false (the cancel was issued and interrupted nothing, and
// nothing is pending), and fired=false/armed=true (a pre-registration cancel
// latch now stands in for a turn that has not registered yet).
//
// fn's own return shape stays (bool, error) — armed is expressed by a
// SEPARATE, optional armedFn so existing scripts (fn only) do not have to
// grow a third return value they never asked to distinguish; a script that
// wants to express armed sets armedFn explicitly.
type scriptedCanceller struct {
	mu      sync.Mutex
	calls   []string
	fn      func(sessionID string) (bool, error)
	armedFn func(sessionID string) bool
}

func (c *scriptedCanceller) RequestCancelForSession(_ context.Context, sessionID, _, _ string) (fired bool, armed bool, err error) {
	c.mu.Lock()
	c.calls = append(c.calls, sessionID)
	fn := c.fn
	armedFn := c.armedFn
	c.mu.Unlock()
	if armedFn != nil {
		armed = armedFn(sessionID)
	}
	if fn != nil {
		fired, err = fn(sessionID)
		return fired, armed, err
	}
	return true, armed, nil
}

// failingPlanNotifier is an AsyncNotifier whose delivery always fails — a
// downed bus, a closed transport, a rejected destination.
type failingPlanNotifier struct{ err error }

func (f *failingPlanNotifier) Notify(_ context.Context, _ AsyncNotifyEvent) error { return f.err }

// --- Shared seeding ----------------------------------------------------------

// seedParkedPlan creates a RUNNING plan already parked at
// awaiting_supervision, with members, and arms the F2 round-burn gate with the
// members' REAL signature (not a placeholder string) so processPlan reaches
// evaluateSupervisionDeadlineLocked instead of opening a fresh judge round.
// members may be empty — that is the zero-member case, whose real signature is
// the empty string.
func seedParkedPlan(t *testing.T, h *planEngineHarness, planID string, members ...*task.Task) *plan.Plan {
	t.Helper()
	p := mustCreatePlan(t, h.plans, &plan.Plan{
		ID: planID, Title: planID, WorkspaceID: "ws", OwnerAgentID: "owner",
		State:     plan.StateRunning,
		PlanPhase: plan.PhaseAwaitingSupervision,
		DoD:       []task.AcceptanceCriterion{planProseCriterion("the plan is done")},
	})
	for _, m := range members {
		m.PlanID = planID
		if m.WorkspaceID == "" {
			m.WorkspaceID = "ws"
		}
		mustCreateTask(t, h.tasks, m)
	}
	current, err := h.tasks.List(task.Filter{PlanID: planID})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	sig := planTerminalSignature(current)
	if _, err := h.plans.Update(planID, plan.Patch{LastUnmetTerminalSignature: &sig}); err != nil {
		t.Fatalf("persist unmet signature: %v", err)
	}
	h.pe.recordUnmetTerminalSignature(planID, sig)
	p.LastUnmetTerminalSignature = sig
	return p
}

// tickPastDeadline advances the fake clock past the plan's supervision
// deadline and runs one engine pass.
func tickPastDeadline(t *testing.T, h *planEngineHarness, planID string) *plan.Plan {
	t.Helper()
	p, err := h.plans.Get(planID)
	if err != nil {
		t.Fatalf("get plan %q: %v", planID, err)
	}
	h.clock.Set(h.clock.Now().Add(h.pe.supervisionTurnTimeout(p) + time.Second))
	h.pe.processPlan(context.Background(), planID)
	h.pe.judgeWG.Wait()
	got, err := h.plans.Get(planID)
	if err != nil {
		t.Fatalf("get plan %q after tick: %v", planID, err)
	}
	return got
}

// breakPlanStore makes every plan.Store operation on planDir fail, and returns
// the restore func (also registered as a t.Cleanup, so a t.Fatalf between the
// break and the restore cannot leave the temp dir wedged). Calling the
// returned func twice is safe; the second call is a no-op.
//
// ⚠ DO NOT "SIMPLIFY" THIS BACK TO os.Chmod(planDir, 0o500). It was written
// that way and it made this test silently uid-dependent: it passed in a dev
// shell as an unprivileged user and FAILED on the CI worker, which runs as
// root. Root holds CAP_DAC_OVERRIDE, which bypasses directory permission bits
// on Linux, so the 0500 chmod did not make the write fail there at all — the
// receipt landed, the failure path this test exists to pin never executed, and
// the precondition assertion below collapsed. Any permission-bit-based
// injection has that same hole; only a break the kernel enforces structurally
// is uid-independent.
//
// The mechanism: swap the store's DIRECTORY for a regular file. Every
// open/stat of <planDir>/<anything> then fails with ENOTDIR, which no
// capability overrides — root and dev get the identical error. It needs no
// privileges to set up, is deterministic, and does not depend on the
// filesystem backing TMPDIR (tmpfs, ext4 and overlayfs behave alike).
//
// Honest scope note: this is a STRONGER break than the chmod aimed at. The
// chmod let plan.Store.load succeed and failed only the atomic write;
// ENOTDIR fails the load too, so Update errors a few lines earlier inside the
// store. That is deliberate — root's DAC bypass means there is no way to fail
// only the write without privileged setup (a read-only mount or chattr +i),
// neither of which is available to an unprivileged test. It does not weaken
// what is under test: the seam wakeSupervisor branches on is
// `pe.planStore.Update(...) != nil`, and the assertions below are about what
// the plan record looks like afterwards, neither of which can tell the two
// apart.
func breakPlanStore(t *testing.T, planDir string) (restore func()) {
	t.Helper()
	stashed := planDir + ".stashed"
	if err := os.Rename(planDir, stashed); err != nil {
		t.Fatalf("stash the plan dir: %v", err)
	}
	if err := os.WriteFile(planDir, []byte("not a directory"), 0o600); err != nil {
		if rerr := os.Rename(stashed, planDir); rerr != nil {
			t.Errorf("restore the plan dir after a failed break: %v", rerr)
		}
		t.Fatalf("plant the blocking file: %v", err)
	}
	var once sync.Once
	restore = func() {
		once.Do(func() {
			if err := os.Remove(planDir); err != nil {
				t.Errorf("remove the blocking file: %v", err)
				return
			}
			if err := os.Rename(stashed, planDir); err != nil {
				t.Errorf("restore the plan dir: %v", err)
			}
		})
	}
	t.Cleanup(restore)
	return restore
}

// --- Finding 1: Stop must not report success on a cancel it never issued ----

// TestStopPlan_UnissuableSessionCancelIsNotReportedAsSuccess is the outcome
// test for the discarded "did it fire" result.
//
// Pre-fix, cancelSessions threw away BOTH halves of
// RequestCancelForSession's result: a cancel that errored — meaning the request
// never reached the turn, which may still be running against a plan the user
// has just been told is stopped — was logged at Warn and StopPlan returned nil.
// The SPA then rendered a clean stop. The member-write leg already refused to
// do that (aggregateMemberCancelErrors); the session leg did it anyway.
func TestStopPlan_UnissuableSessionCancelIsNotReportedAsSuccess(t *testing.T) {
	h := newTestPlanEngine(t)
	canceller := &scriptedCanceller{fn: func(sessionID string) (bool, error) {
		if sessionID == "sess-m1" {
			return false, errors.New("cancel bus unavailable")
		}
		return true, nil
	}}
	h.pe.canceller = canceller

	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress, SessionID: "sess-m1",
	})

	updated, err := h.pe.StopPlan(context.Background(), "p1", "tester", "system")
	if err == nil {
		t.Fatal("StopPlan reported an unqualified success while a session cancel could not be issued — " +
			"the turn for that session may still be running against a plan the caller believes is stopped")
	}
	if !strings.Contains(err.Error(), "sess-m1") {
		t.Errorf("the partial-stop error must name the session whose cancel failed, got: %v", err)
	}
	// The stop itself still HAPPENED — this is a qualified success, not an
	// aborted fan-out. Both facts must be true at once or the REST layer
	// (handlePlanStop) cannot tell a partial stop from a failed one.
	if updated == nil {
		t.Fatal("StopPlan must still return the updated plan on a partial fan-out failure")
	}
	if updated.State != plan.StateFailed || updated.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("plan = %q(%s), want failed(stopped_by_user) despite the partial fan-out",
			updated.State, updated.FailedReason)
	}
}

// TestStopPlan_BenignNonFireIsNotAnError pins the OTHER half of finding 1, the
// half that must NOT change. was_fired=false is ambiguous by construction and
// benign in routine cases (a supervision session whose adjudication turn
// already finished — StopPlan's own fan-out comment says exactly that; an idle
// owner session; a member between its turn ending and its terminal write).
//
// handlePlanStop maps ANY non-nil StopPlan error to HTTP 500, so escalating a
// non-fire would 500 the single most common Stop in the product: stopping a
// parked plan. A fix that makes this test fail is not a fix.
func TestStopPlan_BenignNonFireIsNotAnError(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.canceller = &scriptedCanceller{fn: func(string) (bool, error) { return false, nil }}

	p := mustCreateRunningPlan(t, h.plans, "p1", "owner")
	if _, err := h.plans.Update(p.ID, plan.Patch{
		OwnerSessionID:       strPtr("sess-owner"),
		SupervisionSessionID: strPtr("sess-supervision"),
		SupervisionWakeAt:    strPtr(h.clock.Now().UTC().Format(time.RFC3339)),
	}); err != nil {
		t.Fatalf("arm the plan's sessions: %v", err)
	}

	updated, err := h.pe.StopPlan(context.Background(), "p1", "tester", "system")
	if err != nil {
		t.Fatalf("stopping a parked plan whose turns had already finished must be a clean success, got: %v", err)
	}
	if updated.State != plan.StateFailed {
		t.Fatalf("plan state = %q, want failed", updated.State)
	}
}

// TestCancelSessions_ArmedIsBucketedSeparatelyFromNotFired is the direct-call
// regression test for the sessionCanceller widening (RequestCancelForSession
// (bool, error) -> (bool, bool, error)): before this fix, cancelSessions could
// not even OBSERVE armed (the interface didn't carry it), so a session whose
// cancel armed a pre-registration latch — CancelOutcome.Armed, cancel.go: the
// NEXT turn to register for that session WILL still be cancelled, within
// cancelPreArmTTL — was structurally indistinguishable from one that hit a
// genuinely benign non-fire. That is the exact shape of the user-visible
// failure this closes: a Stop that reports success while a turn about to
// register for an "armed" session keeps running, uncancelled, if the caller
// only ever checked "did anything fire".
func TestCancelSessions_ArmedIsBucketedSeparatelyFromNotFired(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.canceller = &scriptedCanceller{
		fn: func(string) (bool, error) { return false, nil },
		armedFn: func(sessionID string) bool {
			return sessionID == "sess-armed"
		},
	}

	report := h.pe.cancelSessions(context.Background(), []string{"sess-armed", "sess-benign"}, "tester", "system")

	if report.fired != 0 {
		t.Errorf("fired = %d, want 0 (neither session interrupted a live turn)", report.fired)
	}
	if len(report.failed) != 0 {
		t.Errorf("failed = %v, want none (both calls succeeded)", report.failed)
	}
	if got := report.armed; len(got) != 1 || got[0] != "sess-armed" {
		t.Fatalf("armed = %v, want exactly [\"sess-armed\"]", got)
	}
	if got := report.notFired; len(got) != 1 || got[0] != "sess-benign" {
		t.Fatalf("notFired = %v, want exactly [\"sess-benign\"] — sess-armed must NOT also appear here "+
			"(that is precisely the bucketing bug this fixes)", got)
	}
}

// TestStopPlan_ArmedSessionDoesNotEscalateToAnError proves the OTHER half:
// like a benign notFired, an armed session must never turn a Stop into a
// reported failure — CancelOutcome.Armed's own contract is that the cancel
// WILL still fire (deferred, not lost), so escalating it would report a
// working control path as broken and 500 an ordinary Stop
// (handlePlanStop maps any non-nil StopPlan error to HTTP 500).
func TestStopPlan_ArmedSessionDoesNotEscalateToAnError(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.canceller = &scriptedCanceller{
		fn:      func(string) (bool, error) { return false, nil },
		armedFn: func(string) bool { return true },
	}

	p := mustCreateRunningPlan(t, h.plans, "p1", "owner")
	if _, err := h.plans.Update(p.ID, plan.Patch{
		OwnerSessionID: strPtr("sess-owner"),
	}); err != nil {
		t.Fatalf("arm the plan's owner session: %v", err)
	}

	updated, err := h.pe.StopPlan(context.Background(), "p1", "tester", "system")
	if err != nil {
		t.Fatalf("an armed (deferred, not failed) session cancel must not turn Stop into an error, got: %v", err)
	}
	if updated.State != plan.StateFailed || updated.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("plan = %q(%s), want failed(stopped_by_user)", updated.State, updated.FailedReason)
	}
}

func strPtr(s string) *string { return &s }

// --- Finding 2: a failed wake receipt must not park a plan forever ----------

// TestSupervision_FailedWakeReceiptStillReachesATerminalState drives the REAL
// failure (the plan store cannot be written when the receipt is stamped) and
// then asserts the plan does not sit parked forever with its deadline
// disarmed.
//
// Pre-fix: wakeSupervisor returned early on the receipt write failure, leaving
// supervision.wake_at empty; evaluateSupervisionDeadlineLocked returned
// immediately on exactly that condition; and both wake entry points are
// once-per-park, so nothing ever re-armed it. No deadline, no retry, no
// ceiling, no supervision_unavailable — and the field that would have recorded
// the fault (supervision.wake_error) was part of the write that failed. Only
// idle expiry rescued it, days later, with a reason that is not restartable.
func TestSupervision_FailedWakeReceiptStillReachesATerminalState(t *testing.T) {
	h := newTestPlanEngine(t)
	p := seedParkedPlan(t, h, "p1", &task.Task{Title: "member", Status: task.StatusDone})

	// Break the store so the receipt write cannot land, then run the real
	// wake. See breakPlanStore for why this is not a chmod.
	planDir := h.plans.Dir()
	restore := breakPlanStore(t, planDir)

	// Guard the injection itself: if the store is still writable here, the
	// rest of this test is theatre. This assertion is what the uid-dependent
	// chmod version lacked — it would have named the real problem on the CI
	// worker instead of failing as a confusing "precondition failed".
	probe := ""
	if _, err := h.plans.Update("p1", plan.Patch{SupervisionWakeError: &probe}); err == nil {
		t.Fatal("the plan store is still writable after breakPlanStore — the failure " +
			"injection is not working, so nothing below tests the failure path")
	}

	h.pe.planDecisionMu.Lock()
	h.pe.wakeSupervisor(p, "adjudicate this", "plan_judge_unmet", true)
	h.pe.planDecisionMu.Unlock()
	restore()

	stuck, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if stuck.Supervision != nil && stuck.Supervision.WakeAt != "" {
		t.Fatalf("precondition failed: the receipt write was supposed to fail, got %+v", stuck.Supervision)
	}
	if stuck.State != plan.StateRunning || stuck.PlanPhase != plan.PhaseAwaitingSupervision {
		t.Fatalf("precondition failed: the plan should still be parked and running, got %q/%q",
			stuck.State, stuck.PlanPhase)
	}

	// One pass must RE-ARM rather than skip: this is the difference between a
	// recoverable fault and a permanently stranded plan.
	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()
	rearmed, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan after the recovery pass: %v", err)
	}
	if rearmed.Supervision == nil || rearmed.Supervision.WakeAt == "" {
		t.Fatalf("the plan is parked with its deadline still disarmed after a full engine pass — " +
			"nothing will ever wake it again")
	}
	if rearmed.Supervision.Attempts != 1 {
		t.Errorf("supervision.attempts = %d after the re-arm, want 1 (a park whose first receipt never "+
			"landed starts a full ladder)", rearmed.Supervision.Attempts)
	}

	// And the ladder it re-armed is a REAL one: it terminates.
	deadline := 0
	for {
		got := tickPastDeadline(t, h, "p1")
		if got.State != plan.StateRunning {
			if got.FailedReason != plan.FailedReasonSupervisionUnavailable {
				t.Fatalf("failed_reason = %q, want supervision_unavailable", got.FailedReason)
			}
			return
		}
		deadline++
		if deadline > defaultSupervisionMaxAttempts+2 {
			t.Fatalf("the plan is STILL running after %d elapsed supervision deadlines — the re-armed "+
				"wake has no ceiling", deadline)
		}
	}
}

// --- Finding 3: a failed owner delivery must not read as Done ---------------

// TestOwnerWake_DeliveryFailureStillRunsTheSynthesisTurn is the outcome test
// for the MET path's silent loss.
//
// Pre-fix, both chat-leg failures (no notifier wired, Notify returning an
// error) logged and returned. synthesizeAndComplete then transitioned the plan
// to `done` unconditionally, so: the closing synthesis was never written, the
// human was never told, and the board showed Done. The asymmetry is what makes
// it a defect rather than a trade-off — wakeOwner's own doc justifies its
// direct-dispatch leg with "'no chat to deliver to' must never mean 'no turn
// ran'", while a FAILED delivery meant exactly that.
//
// "The synthesis turn ran" is asserted as an LLM call on the owner agent's own
// provider plus a non-empty transcript in the plan's owner session — not as a
// recorded Notify call, which is the assertion that let this ship.
func TestOwnerWake_DeliveryFailureStillRunsTheSynthesisTurn(t *testing.T) {
	h := newPlanWakeHarness(t)
	h.pe.notifier = &failingPlanNotifier{err: errors.New("outbound bus is down")}

	p := runningPlanWithDoD("p1", "telegram", "chat-1")
	if err := h.plans.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.pe.judge.(*fakePlanJudge).resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          true,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "done"}},
		}}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	done, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if done.State != plan.StateDone {
		t.Fatalf("plan state = %q, want done (the mechanical outcome is unchanged by a delivery failure)",
			done.State)
	}
	if done.OwnerSessionID == "" {
		t.Fatal("the plan has no owner session, so no synthesis could have been persisted anywhere")
	}
	waitForTurn(t, h.owner, 1, "a plan that reads Done must have RUN its closing synthesis turn even "+
		"though the chat delivery failed")
	waitForPersistedTranscript(t, h.al, done.OwnerSessionID,
		"the closing synthesis of a plan that reads Done")
}

// --- Finding 4: a zero-member plan must stay on the escalation ladder -------

// TestZeroMemberPlan_StaysOnTheEscalationLadder drives a real UNMET judge
// verdict on a plan with no members and then asserts the plan still reaches a
// terminal state.
//
// planTerminalSignature(nil) is "", so a zero-member plan's park records an
// EMPTY unmet signature — legitimately; that IS its signature. The deadline
// predicate tested `LastUnmetTerminalSignature == ""` and read that as "the
// record moved, the adjudication turn produced something", which is true for a
// CLEARED signature and false for a NEVER-SET one. Such a plan parked, got
// exactly one wake, and then left the ladder entirely: no deadline, no retry,
// no terminal state short of idle expiry.
func TestZeroMemberPlan_StaysOnTheEscalationLadder(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning,
		DoD:   []task.AcceptanceCriterion{planProseCriterion("the plan is done")},
	})
	h.pe.judge.(*fakePlanJudge).resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "no evidence"}},
		}}
	}

	// The real park: an UNMET verdict on a vacuously all-terminal DAG.
	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()
	parked, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if parked.PlanPhase != plan.PhaseAwaitingSupervision {
		t.Fatalf("plan_phase = %q, want awaiting_supervision", parked.PlanPhase)
	}
	if parked.LastUnmetTerminalSignature != "" {
		t.Fatalf("precondition failed: a zero-member plan's signature must be empty, got %q",
			parked.LastUnmetTerminalSignature)
	}
	if parked.Supervision == nil || parked.Supervision.Attempts != 1 {
		t.Fatalf("the park must issue one wake, got %+v", parked.Supervision)
	}

	// Every elapsed deadline must move the plan. Pre-fix, attempts stayed at 1
	// forever and the plan stayed running forever.
	for i := 0; i < defaultSupervisionMaxAttempts+2; i++ {
		got := tickPastDeadline(t, h, "p1")
		if got.State != plan.StateRunning {
			if got.FailedReason != plan.FailedReasonSupervisionUnavailable {
				t.Fatalf("failed_reason = %q, want supervision_unavailable", got.FailedReason)
			}
			return
		}
		if got.Supervision.Attempts <= 1 && i > 0 {
			t.Fatalf("supervision.attempts is still %d after %d elapsed deadlines — a zero-member plan "+
				"has fallen out of the escalation ladder", got.Supervision.Attempts, i+1)
		}
	}
	t.Fatal("a zero-member plan never reached a terminal state despite every deadline elapsing")
}

// --- Finding 5: the stall -> correct cycle must have a budget ---------------

// TestStallCorrectionCycle_IsBounded proves the loop terminates.
//
// On the stall path EVERY brake was defeated at once: JudgeRounds never
// advances (a non-terminal DAG never reaches beginPlanJudgeRound, its sole
// writer), supervision.attempts is reset to 0 by each applied correction,
// correction_rounds advanced but nothing read it, and touchActivity keeps idle
// expiry away. So: append tail work -> it runs -> the plan is still blocked ->
// it stalls again -> a fresh wake (the HandoverText dedup misses, because the
// correction cleared the note) -> unbounded, with no terminal state except the
// adjudicator voluntarily choosing `abandon`.
//
// The budget is deliberately measured against plan_judge_max_rounds, which is
// why this test sets that bound to 1: it is the same question ("how many
// adjudication cycles may this plan have") asked on the path that had no answer.
func TestStallCorrectionCycle_IsBounded(t *testing.T) {
	h := newTestPlanEngine(t)
	one := 1
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner",
		State:  plan.StateRunning,
		Bounds: &plan.PlanBounds{PlanJudgeMaxRounds: &one},
		DoD:    []task.AcceptanceCriterion{planProseCriterion("the plan is done")},
	})
	// A member blocked behind something OUTSIDE the plan: the plan can never
	// unblock it itself, which is the stall condition, and it is not a dead end
	// (the blocker may still resolve), so the honest-exit check does not fire.
	outside := mustCreateTask(t, h.tasks, &task.Task{Title: "outside blocker", WorkspaceID: "ws", Status: task.StatusInbox})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "stuck member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusBlocked, BlockedBy: []string{outside.ID},
	})

	// Pass 1: the plan stalls and the adjudicator is woken.
	h.pe.processPlan(context.Background(), "p1")
	stalled, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if stalled.PlanPhase != plan.PhaseStalled {
		t.Fatalf("plan_phase = %q, want stalled", stalled.PlanPhase)
	}

	// The adjudicator corrects it: tail work that will itself fail, leaving the
	// plan blocked exactly as before. This is the cycle's single iteration.
	if _, cerr := h.pe.AppendCorrection(context.Background(), "p1", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "the outside blocker would resolve itself",
		TailMembers: []task.Task{{
			ID: "tail-1", Title: "unblock it", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("the blocker is resolved")},
		}},
	}); cerr != nil {
		t.Fatalf("correction on a stalled plan: %v", cerr)
	}
	corrected, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan after correction: %v", err)
	}
	if corrected.Supervision == nil || corrected.Supervision.CorrectionRounds != 1 {
		t.Fatalf("the correction must advance correction_rounds, got %+v", corrected.Supervision)
	}
	if corrected.Supervision.Attempts != 0 {
		t.Fatalf("precondition: the correction resets attempts to 0 (that is why the attempt ceiling "+
			"cannot bound this cycle), got %d", corrected.Supervision.Attempts)
	}

	// The tail work runs and fails; the plan is blocked again, exactly as before.
	failed := task.StatusFailed
	if _, uerr := h.tasks.Update("tail-1", task.Patch{Status: &failed}); uerr != nil {
		t.Fatalf("fail the tail member: %v", uerr)
	}

	// Pass 2: the plan re-stalls. With its correction budget spent, this must
	// TERMINATE rather than issue another wake and go round again.
	h.pe.processPlan(context.Background(), "p1")
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan after the re-stall: %v", err)
	}
	if got.State == plan.StateRunning {
		t.Fatalf("the plan re-stalled with its correction budget spent and is STILL running "+
			"(phase %q, correction_rounds %d) — the stall/correct cycle has no terminal state",
			got.PlanPhase, got.Supervision.CorrectionRounds)
	}
	if got.FailedReason != plan.FailedReasonDoDUnreachable {
		t.Fatalf("failed_reason = %q, want dod_unreachable — rounds may well remain, so "+
			"judge_rounds_exhausted would be a different (and false) fact", got.FailedReason)
	}
	if !strings.Contains(got.HandoverText, "correction") {
		t.Errorf("the handover must say the corrections stopped converging, got %q", got.HandoverText)
	}
}

// --- Finding 6: a failed supervision reset must not shorten the next park ---

// TestNextParkGetsAFullLadderAfterAFailedSupervisionReset seeds the exact
// residue countCorrectionAndClearWake leaves behind when its write fails —
// the plan moved on (phase dispatching, set by the correction's own separate,
// successful write) while the supervision counters still hold the previous
// park's spent values — and asserts the NEXT park is not charged for it.
//
// countCorrectionAndClearWake's comment anticipated only a stale wake receipt.
// The durable consequence it missed: supervision.attempts is reset by that one
// write and nothing else, so the next park started its ladder part-spent and
// died failed(supervision_unavailable) after a single adjudication turn — a
// false diagnosis of a healthy PlanSupervisor, and not a restartable reason.
func TestNextParkGetsAFullLadderAfterAFailedSupervisionReset(t *testing.T) {
	h := newTestPlanEngine(t)
	stale := h.clock.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner",
		State:     plan.StateRunning,
		PlanPhase: plan.PhaseDispatching,
		Supervision: &plan.Supervision{
			WakeAt:           stale,
			Attempts:         defaultSupervisionMaxAttempts,
			SessionID:        "sess-previous-park",
			CorrectionRounds: 1,
		},
	})
	outside := mustCreateTask(t, h.tasks, &task.Task{Title: "outside blocker", WorkspaceID: "ws", Status: task.StatusInbox})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "stuck member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusBlocked, BlockedBy: []string{outside.ID},
	})

	h.pe.processPlan(context.Background(), "p1")
	parked, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if parked.PlanPhase != plan.PhaseStalled {
		t.Fatalf("plan_phase = %q, want stalled", parked.PlanPhase)
	}
	if parked.Supervision.Attempts != 1 {
		t.Fatalf("supervision.attempts = %d on the FIRST wake of a new park, want 1 — the ladder "+
			"started pre-spent from a park that had already ended", parked.Supervision.Attempts)
	}

	// The consequence that matters: the plan survives its first deadline lapse
	// instead of being declared unadjudicable after one turn.
	afterOne := tickPastDeadline(t, h, "p1")
	if afterOne.State != plan.StateRunning {
		t.Fatalf("the plan is %q(%s) after ONE elapsed supervision deadline — it inherited a spent ladder",
			afterOne.State, afterOne.FailedReason)
	}
	if afterOne.Supervision.Attempts != 2 {
		t.Errorf("supervision.attempts = %d after one re-issue, want 2", afterOne.Supervision.Attempts)
	}
}

// --- Finding 7: an unresolvable owner must not be impersonated --------------

// TestOwnerWake_UnresolvableOwnerIsNotAuthoredByAnotherAgent covers the
// notifier leg's missing agent-resolution guard.
//
// dispatchPlanTurn pre-resolves the agent precisely so a plan wake is never run
// by whichever agent happens to be default. The notifier leg had no equivalent:
// processSystemMessage falls back to GetDefaultAgent() when the named
// AsyncOriginAgentID does not resolve, so a plan whose owner agent had been
// deleted got its closing synthesis written by an unrelated roster member, in
// that member's own persona, and delivered to the requester as the plan's
// answer. Agent deletion is guarded for the owners of RUNNING plans, but
// failPlanLocked and StopPlan both move a plan to `failed` before waking.
//
// Positive evidence, not the absence of a log line: with the guard removed this
// test observes an outbound message ACTUALLY REACHING the requester's chat
// ("Mock response", authored by the registry's default agent), plus the two
// named agents' own recording providers to catch the case where the default
// resolves to one of them instead. Both limbs are asserted because which agent
// wins GetDefaultAgent() is not this test's subject — that ANY agent but the
// plan's own owner answered for it is.
func TestOwnerWake_UnresolvableOwnerIsNotAuthoredByAnotherAgent(t *testing.T) {
	h := newPlanWakeHarness(t)

	p := runningPlanWithDoD("p1", "telegram", "chat-1")
	p.OwnerAgentID = "ghost-agent-deleted-since"
	if err := h.plans.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.pe.judge.(*fakePlanJudge).resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          true,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "done"}},
		}}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()
	h.pe.wakeWG.Wait()

	// Give the bus pump the same window a real delivery would have had.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.owner.callCount() > 0 || h.supervisor.callCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.owner.callCount(); got != 0 {
		t.Errorf("the plan-owner agent ran %d turn(s) for a plan owned by a DIFFERENT, unresolvable "+
			"agent — a closing synthesis in the wrong persona, delivered to the requester as the "+
			"plan's own answer", got)
	}
	if got := h.supervisor.callCount(); got != 0 {
		t.Errorf("the PlanSupervisor ran %d turn(s) authoring an unresolvable owner's synthesis", got)
	}
	for _, m := range drainOutbound(h.msgBus) {
		if m.Channel == "telegram" && m.ChatID == "chat-1" {
			t.Fatalf("a synthesis authored by some other agent reached the requester's chat: %+v", m)
		}
	}
}

// --- Guard: the correction budget must not disturb the DoD-UNMET path -------

// TestCorrectionBudget_DoesNotPreemptTheJudgeRoundsCeiling pins the reasoning
// that made plan_judge_max_rounds a safe ceiling to reuse for finding 5's
// brake: on the DoD-UNMET path every correction is preceded by a judge round,
// so JudgeRounds >= CorrectionRounds always holds and the judge-rounds ceiling
// (checked first, unconditionally, in beginPlanJudgeRound) always fires first.
// A plan at the boundary must therefore terminate judge_rounds_exhausted, with
// its steering intact — not dod_unreachable.
func TestCorrectionBudget_DoesNotPreemptTheJudgeRoundsCeiling(t *testing.T) {
	h := newTestPlanEngine(t)
	two := 2
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner",
		State:        plan.StateRunning,
		PlanPhase:    plan.PhaseAwaitingSupervision,
		Bounds:       &plan.PlanBounds{PlanJudgeMaxRounds: &two},
		JudgeRounds:  2,
		HandoverText: "the judge found the DoD unmet",
		DoD:          []task.AcceptanceCriterion{planProseCriterion("the plan is done")},
		Supervision:  &plan.Supervision{CorrectionRounds: 2, WakeAt: h.clock.Now().UTC().Format(time.RFC3339)},
	})
	mustCreateTask(t, h.tasks, &task.Task{Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone})

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("plan state = %q, want failed at the round ceiling", got.State)
	}
	if got.FailedReason != plan.FailedReasonJudgeRoundsExhausted {
		t.Fatalf("failed_reason = %q, want judge_rounds_exhausted — the correction budget must not "+
			"pre-empt the judge-rounds ceiling on the path that already had one", got.FailedReason)
	}
}

// TestCorrectionBudget_TracksPlanJudgeMaxRounds documents the one number the
// correction budget depends on. If plan_judge_max_rounds ever stops being the
// plan-lifetime adjudication ceiling, correctionBudgetSpent must be revisited
// rather than silently inheriting the new meaning.
func TestCorrectionBudget_TracksPlanJudgeMaxRounds(t *testing.T) {
	h := newTestPlanEngine(t)
	p := &plan.Plan{ID: "p1", Supervision: &plan.Supervision{CorrectionRounds: config.DefaultPlanJudgeMaxRounds - 1}}
	if maxRounds, spent := h.pe.correctionBudgetSpent(p); spent || maxRounds != config.DefaultPlanJudgeMaxRounds {
		t.Fatalf("one below the default ceiling: spent=%v max=%d, want spent=false max=%d",
			spent, maxRounds, config.DefaultPlanJudgeMaxRounds)
	}
	p.Supervision.CorrectionRounds = config.DefaultPlanJudgeMaxRounds
	if _, spent := h.pe.correctionBudgetSpent(p); !spent {
		t.Fatal("at the default ceiling the correction budget must read spent")
	}
	// A plan that has never been corrected has no supervision record at all and
	// must never trip the brake.
	if _, spent := h.pe.correctionBudgetSpent(&plan.Plan{ID: "p2"}); spent {
		t.Fatal("a plan with no supervision record must not be treated as having spent a budget")
	}
}
