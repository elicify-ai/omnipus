// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_keeper_wedged_turn_test.go pins the ONE behaviour that turned a wedged
// turn from a bounded problem into an unbounded one.
//
// THE DEFECT, stated as the operator observed it: a plan member's turn hangs,
// the task stays in_progress, no terminal record is ever written, and the plan
// renders "Running 5/6" for twenty-five minutes and counting. The only thing
// the system says about it is one INFO line, once per tick, forever:
// "goal idle settle: suppressed, turn in flight".
//
// THE MECHANISM. maybeSettleGoalIdle's live-turn branch (goal_triggers.go)
// suppressed the keeper whenever a turn was merely REGISTERED AND re-armed the
// goal record's LastActivityAt while it did. That clock is not cosmetic: it is
// what goalIdleExpirySweep measures, and under operator decision D-A that
// sweep is the SOLE remaining terminator of a quiet goal (D13 retired the
// claimless adjudication path; the continue-push ladder is bounded at 2). The
// predicate behind the re-arm asked "is a turn REGISTERED" — never "is that
// turn DOING anything" — so a registered-but-wedged turn pushed the goal's
// only terminator forward on every tick and the goal became literally
// eternal. The keeper was manufacturing the evidence that kept it silent.
//
// THE ORACLE. Not "a field changed" — the end-to-end consequence: drive the
// real periodic sweep on a cadence SHORTER than the idle-expiry bound (which
// is production's shape: a 30 s tick against a 7-day brake) and require the
// goal to reach its terminal expiry anyway. On the unfixed keeper it never
// does, at any horizon, because every pass re-arms the clock the expiry is
// measured from. That is what makes this test fail for the right reason
// rather than on a timestamp comparison an implementation could satisfy by
// accident.
//
// WHAT THIS TEST DELIBERATELY DOES NOT ASSERT: that anything kills, judges,
// pushes or cancels the wedged turn. It must not, and the D13 assertions below
// pin that it does not. A turn blocked inside one genuinely long tool call is
// indistinguishable from a wedged one at this layer (turn.go's
// clearToolCallProgress documents exactly that), so the only honest action is
// to stop LYING about it and let the calendar brake do its job — once the
// turn has ended (issue #1306: no clock ends a turn that is still registered;
// Stop is the exit for a genuinely wedged one).
package agent

import (
	"context"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// registerWedgedTurn registers a turnState for sessionID that is alive and
// will never advance a single work counter — the in-memory shape of a
// goroutine that is blocked and is never coming back.
//
// It is built the same way the live-turn row of TestKeeperSuppressionsApplyToTasks
// builds its own (a bare struct literal with routingSessionID set), because
// that IS the shape the keeper sees: collectDescendantTurnIDs matches on
// routingSessionID and liveTurnStatesAmong asks only isFinished.
func registerWedgedTurn(t *testing.T, al *AgentLoop, sessionID, turnID string) *turnState {
	t.Helper()
	ts := &turnState{
		turnID:              turnID,
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(sessionID) })
	if !ts.IsAlive() {
		t.Fatal("arrange: the registered turn must read as alive, or the suppression under test never engages")
	}
	return ts
}

// TestWedgedTurnDoesNotDisarmTheGoalIdleExpirySweep is the regression anchor,
// updated for issue #1306 (founder: no clock ends work).
//
// Given an active goal whose session has a turn that is REGISTERED but doing
//
//	nothing at all
//
// When the periodic sweep runs on its normal cadence, repeatedly, for longer
//
//	than the idle-expiry bound
//
// Then the goal is NOT expired while that turn is registered — a turn blocked
//
//	in one genuinely long tool call is indistinguishable from a wedged one,
//	and no clock may end work; Stop is the exit — AND the keeper does not
//	re-arm the activity clock on the strength of mere registration, so the
//	moment the turn ends, the very next sweep expires the quiet goal.
func TestWedgedTurnDoesNotDisarmTheGoalIdleExpirySweep(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
	registerWedgedTurn(t, h.al, h.sid, "turn-wedged")

	// A 1-day brake swept every 6 hours. The RATIO is what reproduces
	// production (a sweep cadence far shorter than the brake), not the
	// absolute numbers — which are chosen only so the loop is five iterations
	// instead of twenty thousand.
	cfg := config.PlanningConfig{IdleExpiryDays: 1}
	const sweepEvery = 6 * time.Hour
	now := time.Now()

	// Sanity: the arrange really is past the quiet window, so every sweep
	// below genuinely reaches the live-turn branch rather than returning early
	// at the window check. Without this the test could pass vacuously.
	if now.Sub(h.armedAt) <= goalIdleQuietWindow {
		t.Fatalf("arrange: goal armed %s ago, quiet window is %s — the keeper would never reach the live-turn branch",
			now.Sub(h.armedAt), goalIdleQuietWindow)
	}

	for i := 0; i < 8; i++ {
		h.al.goalIdleExpirySweep(cfg, now)
		if got := h.record(t); got.State != generated.GoalStateActive {
			t.Fatalf("sweep %d: the goal left ACTIVE (%q) while its turn was still registered — a clock ended "+
				"work (issue #1306: only finishing or an explicit Stop may)", i, got.State)
		}
		now = now.Add(sweepEvery)
	}

	// End the synthetic registered-turn premise, then settle the genuine
	// queued task session through the same single-session Stop a user invokes.
	// Removing only the memory handle would leave independent queued work.
	beforeStop := h.record(t)
	lifecycle := h.al.GetSessionLifecycleStore()
	if lifecycle == nil {
		t.Fatal("arrange: the real queued task must have its lifecycle store")
	}
	queued, err := lifecycle.Load(h.sid)
	if err != nil || queued == nil || queued.State != session.LifecycleQueued || queued.Origin == nil || queued.Origin.TaskID != h.taskID {
		t.Fatalf("arrange: expected the real task's queued session before settlement: record=%+v err=%v", queued, err)
	}
	h.al.activeTurnStates.Delete(h.sid)
	fired, armed, err := h.al.StopSessionTurn(context.Background(), h.sid, "wedged-fixture-owner", "webchat")
	if err != nil || !fired || armed {
		t.Fatalf("settle queued task session with single Stop: fired=%t armed=%t err=%v", fired, armed, err)
	}
	stopped, err := lifecycle.Load(h.sid)
	if err != nil || stopped == nil || stopped.State != session.LifecycleStopped || stopped.Stop != nil || stopped.StopNote == nil ||
		stopped.StopNote.Cause != session.StopCauseStop || stopped.Generation != queued.Generation ||
		stopped.Origin == nil || stopped.Origin.TaskID != h.taskID {
		t.Fatalf("queued Stop must durably settle the same task session: record=%+v err=%v", stopped, err)
	}
	settledGoal := h.record(t)
	if settledGoal.State != generated.GoalStateActive || settledGoal.OwnerKind != generated.GoalOwnerKindTask ||
		settledGoal.OwnerID != h.taskID || settledGoal.GoalID != beforeStop.GoalID ||
		settledGoal.Round != beforeStop.Round || !settledGoal.LastActivityAt.Equal(beforeStop.LastActivityAt) {
		t.Fatalf("session Stop must preserve the active task goal and its original expiry clock: before=%+v after=%+v", beforeStop, settledGoal)
	}
	pendingTask, err := h.al.taskStore.Get(h.taskID)
	if err != nil || pendingTask == nil || pendingTask.Status != task.StatusNext {
		t.Fatalf("settling the queued session must not start or finish the task: task=%+v err=%v", pendingTask, err)
	}
	if busy, why := h.al.goalWorkInFlight(settledGoal, h.sid); busy {
		t.Fatalf("arrange: all work must actually be settled before expiry, not bypassed: %s", why)
	}

	// The keeper never re-armed the goal clock merely for registration, and
	// Stop did not reset it. The unchanged next sweep can now expire the goal.
	h.al.goalIdleExpirySweep(cfg, now)
	after := h.record(t)
	if after.State != generated.GoalStateExpired {
		t.Fatalf(
			"after the registered turn ended, the goal did not expire (state %q, last_activity_at %s): "+
				"the keeper must not re-arm the activity clock for a turn that is merely REGISTERED "+
				"(goalLiveTurnWorkFingerprint), or a quiet goal could never end.",
			after.State, after.LastActivityAt.Format(time.RFC3339))
	}

	// D13 / ADR-084 rev 9 is absolute: a quiet goal is never JUDGED without a
	// claim and never self-completes. The goal ended by the CALENDAR brake, and
	// by nothing else.
	if h.judge.callCount() != 0 {
		t.Fatalf("D13: the keeper invoked the Judge %d times against a claimless goal, want 0", h.judge.callCount())
	}
	if after.Round != 0 {
		t.Fatalf("D13: no keeper path may consume a round; rounds_used = %d, want 0", after.Round)
	}
	if n := len(h.dispatch.all()); n != 0 {
		t.Fatalf("the live-turn suppression must still SUPPRESS: the keeper dispatched %d follow-ups at a session "+
			"with a live turn, want 0. Contents: %q", n, h.dispatch.contents())
	}
	if after.State != generated.GoalStateExpired {
		t.Fatalf("the goal's terminal state = %q, want %q — expiry is the honest terminal for a goal nobody ever "+
			"claimed, and it must not be reported as a judged failure",
			after.State, generated.GoalStateExpired)
	}
}

// TestWorkingTurnStillRearmsTheGoalActivityClock is the differentiation half,
// and it is the guard that keeps the fix above from being a blunt "stop
// re-arming". S-07 / GOAL-FR-016 #5 requires a suppressing live turn to re-arm
// the activity clock, and that requirement is correct for a turn that is
// WORKING — it is only wrong as a statement about a turn that is merely
// registered.
//
// Given a live turn whose work counters ARE advancing
// When the keeper sweeps repeatedly, well past the stall grace
// Then the goal's activity clock keeps being re-armed and the goal never
//
//	idle-expires.
func TestWorkingTurnStillRearmsTheGoalActivityClock(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
	ts := registerWedgedTurn(t, h.al, h.sid, "turn-working")

	cfg := config.PlanningConfig{IdleExpiryDays: 1}
	const sweepEvery = 6 * time.Hour
	now := time.Now()

	for i := 0; i < 8; i++ {
		// Real work between sweeps: one more LLM round and more tokens billed
		// — the counters the agent loop advances as a side effect of working.
		ts.mu.Lock()
		ts.iteration++
		ts.turnTokens += 1200
		ts.mu.Unlock()

		h.al.goalIdleExpirySweep(cfg, now)

		if got := h.record(t); got.State != generated.GoalStateActive {
			t.Fatalf("sweep %d: a goal whose turn is actively working must stay ACTIVE; state = %q. "+
				"The stall grace must be measured against OBSERVABLE WORK (goalLiveTurnWorkFingerprint), "+
				"never against elapsed time alone — a legitimately slow member must never be brought down "+
				"by the keeper.", i, got.State)
		}
		if got := h.record(t); !got.LastActivityAt.After(h.armedAt.UTC()) {
			t.Fatalf("sweep %d: S-07/GOAL-FR-016 #5: a suppressing live turn that IS working must RE-ARM the "+
				"activity clock; last_activity_at = %s, armed at %s",
				i, got.LastActivityAt, h.armedAt.UTC())
		}
		now = now.Add(sweepEvery)
	}

	if n := len(h.dispatch.all()); n != 0 {
		t.Fatalf("a working live turn must suppress the keeper entirely: %d follow-ups dispatched, want 0. "+
			"Contents: %q", n, h.dispatch.contents())
	}
	if h.judge.callCount() != 0 {
		t.Fatalf("D13: the keeper invoked the Judge %d times, want 0", h.judge.callCount())
	}
}

// TestGoalLiveTurnWorkFingerprintDistinguishesWorkFromRegistration pins the
// predicate itself, at the level the rest of the fix rests on: the fingerprint
// must be STABLE across observations of an unchanged turn (or the stall grace
// never elapses and the fix is dead code) and must CHANGE on any of the work
// signals (or a working turn is misread as wedged).
func TestGoalLiveTurnWorkFingerprintDistinguishesWorkFromRegistration(t *testing.T) {
	resetGoalTriggerStateForTest()
	al := &AgentLoop{}
	sid := "sess-fingerprint"
	ts := registerWedgedTurn(t, al, sid, "turn-fp")

	live, first := al.goalLiveTurnWorkFingerprint(sid)
	if !live {
		t.Fatal("a registered, unfinished turn must read as live")
	}
	live, second := al.goalLiveTurnWorkFingerprint(sid)
	if !live || second != first {
		t.Fatalf("an unchanged turn must fingerprint identically across observations; %q then %q "+
			"— an unstable fingerprint resets the no-work clock every tick, so the stall grace never elapses "+
			"and the wedge stays invisible", first, second)
	}

	for name, mutate := range map[string]func(){
		"an LLM round completed": func() { ts.mu.Lock(); ts.iteration++; ts.mu.Unlock() },
		"tokens were billed":     func() { ts.mu.Lock(); ts.turnTokens += 10; ts.mu.Unlock() },
		"a delegate spawned": func() {
			ts.mu.Lock()
			ts.childTurnIDs = append(ts.childTurnIDs, "child")
			ts.mu.Unlock()
		},
		"the turn changed phase": func() { ts.mu.Lock(); ts.phase = TurnPhaseTools; ts.mu.Unlock() },
		"a tool call streamed an argument delta": func() {
			ts.toolCallProgress.lastActivityUnixNano.Store(time.Now().UnixNano())
		},
	} {
		_, before := al.goalLiveTurnWorkFingerprint(sid)
		mutate()
		_, got := al.goalLiveTurnWorkFingerprint(sid)
		if got == before {
			t.Fatalf("%s: the fingerprint did not change (%q) — that work signal is not being read, so a turn "+
				"doing exactly this would be misreported as producing nothing", name, got)
		}
	}

	// And the no-work clock itself: unchanged fingerprint accumulates, any
	// change resets to zero.
	base := time.Now()
	if d := al.observeGoalLiveTurnWork("g1", "fp-a", base); d != 0 {
		t.Fatalf("a first observation must read as zero elapsed, got %s", d)
	}
	if d := al.observeGoalLiveTurnWork("g1", "fp-a", base.Add(9*time.Minute)); d != 9*time.Minute {
		t.Fatalf("an unchanged fingerprint must accumulate; got %s, want 9m", d)
	}
	if d := al.observeGoalLiveTurnWork("g1", "fp-b", base.Add(10*time.Minute)); d != 0 {
		t.Fatalf("a changed fingerprint must RESET the no-work clock; got %s, want 0", d)
	}
	al.clearGoalLiveTurnWork("g1")
	if d := al.observeGoalLiveTurnWork("g1", "fp-b", base.Add(20*time.Minute)); d != 0 {
		t.Fatalf("a cleared observation must start over; got %s, want 0", d)
	}
}
