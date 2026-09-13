// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_keeper_repairs_test.go covers ADR-081 D6's keeper repairs (lane W2b):
// D6a in-flight suppression (FR-013), D6a parked-card suppression (FR-016),
// the FR-014b zero-adjudicable-output triple and its bounded continue-push
// ladder, D6b's un-wedged Ralph push (FR-015), D6c's recordless-goal nudge
// ladder to the D7 engine fallback (FR-017), and FR-031's persisted +
// rehydrated goal routing. Mirrors goal_triggers_test.go's newGoalLoopTestLoop
// harness (a fake Judge LLM provider swapped onto the seeded Judge agent).
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// setGoalRecordArmed is setGoalRoundsArmed's RECORDED-goal counterpart: a
// non-empty criteria ladder on the goal record (one prose criterion), so the
// zero-output triple path (FR-014b) is reached instead of the recordless
// nudge ladder (FR-014/D6c).
//
// ADR-086: the ladder now lives on the goal's own pkg/goal record, not on
// session meta's retired GoalCriteriaJSON string — see armGoalRecord
// (goal_triggers_test.go) for the full mapping.
func setGoalRecordArmed(t *testing.T, _ *session.UnifiedStore, sid, condition string, roundsUsed int, lastActivity time.Time) {
	t.Helper()
	armGoalRecord(t, sid, condition, recordedGoalCriteria(condition), roundsUsed, lastActivity)
}

// rewindGoalActivity simulates "a new idle cycle can fire" between two
// goalQuietWindowSettle calls in tests that dispatch a nudge/push and never
// drive the resulting async-notify turn through processSystemMessage (that
// full real-turn drive is TestKeeper_SenderGateUnwedged_TwoFullIdleCycles's
// own, narrower concern — D6b). Production clears idleSettling ONLY via
// genuine activity (a dispatched turn actually completing and reaching
// checkGoalLoopAfterTurn); this helper does the SAME two things a completed
// dispatch would have done — push GoalLastActivityAt into the past (the
// quiet-window-elapsed precondition) and clear the idleSettling marker (the
// FR-102 re-arm) — without needing a full turn round-trip, so the ladder
// tests can assert cycle-over-cycle counter behavior in isolation. idleSettling
// is keyed by GOAL ID (DD-7(b), E13), so this re-reads the record's GoalID
// rather than assuming it equals sid.
//
// ADR-086: the activity clock is the goal record's own LastActivityAt
// (GOAL-FR-004), not session meta's retired GoalLastActivityAt.
func rewindGoalActivity(t *testing.T, al *AgentLoop, store *session.UnifiedStore, sid string) {
	t.Helper()
	gid := rewindGoalActivityTimeOnly(t, store, sid)
	al.goalMarkIdleSettling(gid, false)
}

// rewindGoalActivityTimeOnly is rewindGoalActivity's real-clearing
// counterpart (review-round-1, keeper-wedge fix): it pushes the goal
// record's LastActivityAt into the past (the quiet-window-elapsed
// precondition) but deliberately does NOT touch the idleSettling marker. Use
// this instead of rewindGoalActivity whenever the point of the test IS
// whether the production code path (not test scaffolding) actually clears
// the marker — rewindGoalActivity's manual al.goalMarkIdleSettling(gid,
// false) would mask a real wedge instead of proving it's fixed.
//
// Returns the goal id it rewound, so rewindGoalActivity can key the marker
// off it without a second lookup.
func rewindGoalActivityTimeOnly(t *testing.T, _ *session.UnifiedStore, sid string) string {
	t.Helper()
	g := goalRecordForSession(t, sid)
	past := time.Now().Add(-1 * time.Hour).UTC()
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
		cur.LastActivityAt = past
		return nil
	}); err != nil {
		t.Fatalf("rewindGoalActivityTimeOnly(%q): %v", sid, err)
	}
	return g.GoalID
}

// =========================== D6a: in-flight suppression (FR-013) ==========

// TestIdleSettlement_InFlightSuppression proves FR-013/D6a: a live turn for
// the goal's OWN session, or a delegated DESCENDANT's live turn, suppresses
// idle settlement entirely — no verdict, no nudge, no push — and re-arms the
// quiet window rather than firing again on the very next tick.
func TestIdleSettlement_InFlightSuppression(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "goal in-flight own turn", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must not fire while the turn is live")
	judgeInst.Provider = cp

	// The goal session's OWN root turn is still live (routingSessionID ==
	// sid — the "own turn" case).
	ts := &turnState{
		turnID:              "turn-root-live",
		transcriptSessionID: sid,
		routingSessionID:    session.RoutingSessionID(sid),
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sid, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(sid) })

	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("in-flight own turn must suppress idle settlement; Judge calls = %d, want 0", got)
	}
	after := goalRecordForSession(t, sid)
	if after.LastActivityAt.IsZero() {
		t.Fatal("in-flight suppression must re-arm the quiet window (bump the goal record's LastActivityAt)")
	}

	// Fire again immediately: the re-arm must prevent a second attempt even
	// though the live turn is still registered.
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("re-armed quiet window must not elapse immediately; Judge calls = %d, want 0", got)
	}
}

// TestIdleSettlement_InFlightSuppression_DelegatedDescendant proves the
// SAME suppression when only a delegated CHILD turn is live (own root turn
// absent) — a goal whose agent is waiting on a delegate is working, not
// idle (D6a). The child's routingSessionID is inherited from the chat root
// (ADR-057), which is the mechanism goalHasLiveTurn relies on — NOT its
// transcriptSessionID, which is its own distinct id post-ADR-057 D2/FR-011.
func TestIdleSettlement_InFlightSuppression_DelegatedDescendant(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "goal delegate in-flight", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must not fire while the delegate is live")
	judgeInst.Provider = cp

	childTS := &turnState{
		turnID:              "turn-child-live",
		transcriptSessionID: "child-session-xyz",           // its OWN distinct transcript session
		routingSessionID:    session.RoutingSessionID(sid), // inherited from the chat root
		parentTurnID:        "turn-root-already-finished",
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store("delegate-key", childTS)
	t.Cleanup(func() { al.activeTurnStates.Delete("delegate-key") })

	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("in-flight delegated child must suppress idle settlement; Judge calls = %d, want 0", got)
	}
}

// =========================== D6a: parked-card suppression (FR-016) ========

// fakeParkedCardRegistry is a minimal tools.AskUserQuestionRegistry double
// whose PendingForSession answer is toggled per-session by the test.
type fakeParkedCardRegistry struct {
	pending map[string]bool
}

func (f *fakeParkedCardRegistry) CreatePending(*askuser.PendingSet) error { return nil }
func (f *fakeParkedCardRegistry) PendingForSession(sessionID string) (*askuser.PendingSet, bool) {
	if f.pending[sessionID] {
		return &askuser.PendingSet{}, true
	}
	return nil, false
}
func (f *fakeParkedCardRegistry) CancelOnSessionStop(string) bool   { return false }
func (f *fakeParkedCardRegistry) CancelByUser(string, string) error { return nil }

// TestKeeper_ParkedCardSuppression proves FR-016: a pending AskUserQuestion
// set on the goal session suppresses BOTH idle settlement AND the D6c nudge
// ladder — typed like the waiting_on_user pause (no round, no verdict, no
// push count consumed). Once the card clears, suppression lifts.
func TestKeeper_ParkedCardSuppression(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	// A RECORDED goal (not recordless — setGoalRecordArmed). JUDGE-FR-095/
	// FR-097 (D13, this wave): the idle path never adjudicates any more —
	// once suppression lifts, the expected next action is ALWAYS a bounded
	// continue-push, never a Judge call.
	setGoalRecordArmed(t, store, sid, "goal parked card", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must not fire — D13 retires idle adjudication")
	judgeInst.Provider = cp
	reg := &fakeParkedCardRegistry{pending: map[string]bool{sid: true}}
	al.SetAskUserRegistry(reg)

	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("parked card must suppress idle settlement; Judge calls = %d, want 0", got)
	}
	after := goalRecordForSession(t, sid)
	if after.ZeroOutputPushes != 0 {
		t.Fatalf("parked card must ALSO suppress the nudge/push ladder; goal record ZeroOutputPushes = %d, want 0", after.ZeroOutputPushes)
	}

	// The card is answered (the registry stops reporting it pending) — the
	// NEXT idle check can proceed: with nothing yet to compare against, the
	// keeper dispatches its first bounded continue-push rather than judging
	// immediately (FR-014b's own first-observation rule) — the proof that
	// suppression genuinely lifted.
	reg.pending[sid] = false
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("Judge calls = %d, want 0 (the first post-suppression check is a bounded push, not a verdict)", got)
	}
	after2 := goalRecordForSession(t, sid)
	if after2.ZeroOutputPushes != 1 {
		t.Fatalf("after the card clears: goal record ZeroOutputPushes = %d, want 1 (suppression must lift and normal keeper processing resume)",
			after2.ZeroOutputPushes)
	}
}

// ================ D6b: the un-wedged Ralph push (FR-015) ===================

// TestAsyncNotifyEvent_SenderCanonicalID_NonGoalSourcesKeepDefault is the
// negative half of FR-015 (round-2 m-4): a producer that does NOT set the
// new SenderCanonicalID override field must observe the identical
// "async:<kind>" composition as before — the goal-loop dispatch is the ONLY
// caller that sets it.
func TestAsyncNotifyEvent_SenderCanonicalID_NonGoalSourcesKeepDefault(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel: "slack", ChatID: "C1", SourceKind: "bash", Content: "build finished",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-msgBus.InboundChan():
		if msg.Sender.CanonicalID != "async:bash" {
			t.Fatalf("non-goal source sender = %q, want %q (default composition must be untouched)",
				msg.Sender.CanonicalID, "async:bash")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the notify to publish")
	}
}

// TestKeeper_SenderGateUnwedged_TwoFullIdleCycles is the regression test D6b
// itself demands: before the fix, the idle-steer turn was published with the
// notifier's default "async:goal_idle_settle" sender and dropped at
// checkGoalLoopAfterTurn's origin gate, so activity never bumped, idleSettling
// never cleared, and the keeper fired exactly once per goal, then wedged.
// This drives the re-injected steer turn through the EXACT function
// AgentLoop.Run's dispatch loop calls for Channel=="system"
// (async_notifier_test.go's own precedent) and proves a SECOND full idle
// cycle can fire afterward.
func TestKeeper_SenderGateUnwedged_TwoFullIdleCycles(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal wedge regression", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must never be called — D13 retires idle adjudication")
	judgeInst.Provider = cp

	// --- Cycle 1: idle fires, dispatches a bounded continue-push, steer
	// re-injected via the SAME async-notifier sender-gate mechanism D6b
	// fixed — JUDGE-FR-095/FR-097 (D13, this wave) means this is now the
	// ONLY thing the idle path ever does; it never reaches the Judge. ---
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("cycle 1: Judge calls = %d, want 0 (D13: idle never judges)", got)
	}
	after1 := goalRecordForSession(t, sid)
	if after1.ZeroOutputPushes != 1 {
		t.Fatalf("cycle 1: pushes = %d, want 1", after1.ZeroOutputPushes)
	}

	var steerMsg bus.InboundMessage
	select {
	case steerMsg = <-al.bus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the idle-push follow-up to be published")
	}
	if steerMsg.Channel != "system" {
		t.Fatalf("idle-push channel = %q, want %q", steerMsg.Channel, "system")
	}
	if steerMsg.Sender.CanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("D6b: idle-push sender = %q, want %q — the un-wedge fix stamps the goal-loop sentinel",
			steerMsg.Sender.CanonicalID, goalLoopFollowUpSenderID)
	}

	if _, processErr := al.processSystemMessage(context.Background(), steerMsg); processErr != nil {
		t.Fatalf("processSystemMessage: %v", processErr)
	}

	meta := goalRecordForSession(t, sid)
	if al.goalIsIdleSettling(meta.GoalID) {
		t.Fatal("D6b: idleSettling must clear once the push turn passes the origin gate — the keeper must not wedge")
	}

	// --- Cycle 2: rewind activity and prove a SECOND full cycle completes
	// (a second bounded push, still bounded by goalZeroOutputPushMax). ---
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("cycle 2: Judge calls = %d, want 0", got)
	}
	after2 := goalRecordForSession(t, sid)
	if after2.ZeroOutputPushes != 2 {
		t.Fatalf("cycle 2: pushes = %d, want 2 (a second full idle cycle must complete — no wedge)",
			after2.ZeroOutputPushes)
	}
}

// =========================== FR-031: persisted + rehydrated routing =======

// TestGoalRouting_PersistedAndRehydratedAfterRestart proves FR-031: the
// goal's routing is persisted alongside the record (not just held
// in-memory), and routeFor rehydrates it into a FRESH in-memory map after a
// simulated restart (resetGoalTriggerStateForTest wipes the singleton,
// mirroring a fresh process).
func TestGoalRouting_PersistedAndRehydratedAfterRestart(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := activateTestGoalRecord(t, sid, "route test")

	al.recordGoalRouting(sid, gid, "telegram", "chat-42", "sk-42", agentInst.ID)

	route := goalTriggers().routeFor(sid)
	if route.channel != "telegram" || route.chatID != "chat-42" {
		t.Fatalf("in-memory route = %+v, want channel=telegram chat_id=chat-42", route)
	}

	// GOAL-FR-032/FR-033/FR-034 (E12): channel/chatID now persist on the
	// goal's OWN record, not on four session-meta GoalRoute* fields.
	// sessionKey/agentID have no persisted copy any more — they survive
	// only in the in-memory map for this process's own lifetime.
	gs := goal.NewStore(config.OmnipusHomeDir())
	persisted, err := gs.Get(gid)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RouteChannel != "telegram" || persisted.RouteChatID != "chat-42" {
		t.Fatalf("recordGoalRouting must persist channel/chat_id onto the goal record; got channel=%q chat_id=%q",
			persisted.RouteChannel, persisted.RouteChatID)
	}

	// Simulate a gateway restart: wipe ALL in-memory trigger state, INCLUDING
	// the routing map.
	resetGoalTriggerStateForTest()

	rehydrated := goalTriggers().routeFor(sid)
	if rehydrated.channel != "telegram" || rehydrated.chatID != "chat-42" {
		t.Fatalf("FR-031/FR-034: rehydrated route = %+v, want channel=telegram chat_id=chat-42 — a restart must not silently disable routing", rehydrated)
	}

	goalTriggersSingleton.mu.Lock()
	_, cached := goalTriggersSingleton.routing[sid]
	goalTriggersSingleton.mu.Unlock()
	if !cached {
		t.Fatal("a rehydrated route must be cached back into the in-memory map for O(1) subsequent reads")
	}
}

// TestGoalRouting_MissingBothSides_WarnsAndSetsLatestReason proves FR-031's
// failure mode: a route missing on BOTH the in-memory map and the goal
// record's persisted fields must never degrade silently — it writes a
// one-line LatestReason note onto the goal record (GOAL-FR-034/FR-035,
// E12 — was session meta's GoalLatestReason before this wave).
func TestGoalRouting_MissingBothSides_WarnsAndSetsLatestReason(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	// An active goal record exists (as every real activation now creates
	// one), but recordGoalRouting was never called against it — routing is
	// missing on BOTH sides, exactly the precondition routeFor's WARN+write
	// branch requires.
	gid := activateTestGoalRecord(t, sid, "route test")

	route := goalTriggers().routeFor(sid)
	if route.channel != "" || route.chatID != "" {
		t.Fatalf("route = %+v, want the zero value (nothing was ever recorded)", route)
	}

	gs := goal.NewStore(config.OmnipusHomeDir())
	g, err := gs.Get(gid)
	if err != nil {
		t.Fatal(err)
	}
	if g.LatestReason != goal.RoutingLostReason {
		t.Fatalf("goal record LatestReason = %q, want %q (FR-031/FR-035: never degrade silently)", g.LatestReason, goal.RoutingLostReason)
	}
}

// TestGoalRouting_MissingRoute_NeverStompsAFreshVerdictReason is
// review-round-1 finding #10's test: a real judge verdict's reason must
// survive a routeFor call that finds the route missing on BOTH sides in the
// SAME settle pass (adjudicate -> deliverSteer -> dispatchGoalAsyncFollowUp
// -> routeFor). Before the fix, routeFor's missing-route branch
// unconditionally overwrote GoalLatestReason with the generic "routing
// lost" note — stomping the fresh, far more informative verdict reason the
// SAME adjudication call had just written moments earlier, and doing so on
// every single idle cycle for a pre-upgrade goal with no persisted route.
func TestGoalRouting_MissingRoute_NeverStompsAFreshVerdictReason(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	// NO recordGoalRouting call — routing is missing on BOTH sides, exactly
	// the precondition routeFor's WARN+write branch requires.
	goalTriggersSingleton.mu.Lock()
	goalTriggersSingleton.sessionStoreResolver = al.GetSessionStore
	goalTriggersSingleton.mu.Unlock()

	setGoalRecordArmed(t, store, sid, "goal missing route", 0, time.Now().Add(-1*time.Hour))
	meta := goalRecordForSession(t, sid)

	const verdictReason = "the criterion is not yet demonstrated"
	judgeInst.Provider = unmetJudgeProvider(verdictReason)

	// Mirrors the deferred claim path's own call shape (D13, this wave):
	// adjudicate (writes the fresh verdict reason) -> unmet -> deliverSteer
	// -> dispatchGoalAsyncFollowUp -> routeFor (finds routing missing on
	// both sides) — all inside this one call.
	al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, meta,
		"[goal:evidence] the criterion is not yet demonstrated",
		al.idleSteerDeliverer(sid, meta.GoalID, goalIdleSettleSourceKind))

	after := goalRecordForSession(t, sid)
	if after.LatestReason != verdictReason {
		t.Fatalf("finding #10: GoalLatestReason = %q, want the fresh verdict reason %q — "+
			"a missing-route steer delivery must never stomp it with the generic routing-lost note",
			after.LatestReason, verdictReason)
	}
}

// ================ D6c: the recordless-goal nudge ladder (FR-017) ==========

// TestKeeper_NudgeLadderToFallback proves FR-017: a RECORDLESS active goal
// (empty GoalCriteriaJSON, D3's forcing predicate's legal transient state)
// is NEVER judged at idle — the keeper nudges up to goalZeroOutputPushMax
// (N=2) times, then the engine's own fallback compile registers a record
// itself, resetting the shared push counter for the recorded-goal ladder.
func TestKeeper_NudgeLadderToFallback(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "make the tests pass", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must never be called — nudging and the D13 push ladder never touch the Judge")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // nudge 1
	after1 := goalRecordForSession(t, sid)
	if after1.ZeroOutputPushes != 1 {
		t.Fatalf("after nudge 1: goal record ZeroOutputPushes = %d, want 1", after1.ZeroOutputPushes)
	}
	if goalRecordCompiledJSON(after1) != "" {
		t.Fatal("after nudge 1: the goal must still be recordless")
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // nudge 2
	after2 := goalRecordForSession(t, sid)
	if after2.ZeroOutputPushes != 2 {
		t.Fatalf("after nudge 2: goal record ZeroOutputPushes = %d, want 2", after2.ZeroOutputPushes)
	}
	if goalRecordCompiledJSON(after2) != "" {
		t.Fatal("after nudge 2: the goal must still be recordless")
	}
	if got := cp.callCount(); got != 0 {
		t.Fatalf("Judge-provider calls during the nudge phase = %d, want 0 — nudging never touches any model", got)
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // exhausted — engine fallback compile
	after3 := goalRecordForSession(t, sid)
	if goalRecordCompiledJSON(after3) == "" {
		t.Fatal("FR-017: after N=2 nudges, the engine's fallback compile must register a record")
	}
	if after3.ZeroOutputPushes != 0 {
		t.Fatalf("engine-authored fallback registration must reset the shared push counter; got %d, want 0", after3.ZeroOutputPushes)
	}
	// D7/FR-018 (wave-2 integration): the engine's fallback COMPILE runs on
	// the Judge system agent's model, so the judge PROVIDER legitimately
	// serves at most one call here — the compile, not an adjudication. Under
	// D13 that compile call is now the ONLY way this provider is ever
	// touched anywhere in this test — idle settlement itself never reaches
	// the Judge, so this bound is now exact rather than a "<=1" allowance.
	if got := cp.callCount(); got > 1 {
		t.Fatalf("Judge-provider calls = %d, want <=1 (the single D7 fallback compile) — a recordless goal is never adjudicated", got)
	}
	if after3.Round != 0 {
		t.Fatalf("GoalRoundsUsed = %d, want 0 — the fallback compile must not consume a verdict round", after3.Round)
	}
	callsAfterFallback := cp.callCount() // 0 or 1 (the compile, see the comment above) — baseline for the delta check below

	// review-round-1 (keeper wedge): dispatchGoalFallbackCompile never hands
	// off to a dispatched turn — it writes the record directly, engine-side
	// — so nothing downstream (checkGoalLoopAfterTurn's
	// bumpGoalActivityOnTurn) will ever clear the idleSettling marker
	// markGoalIdleFired set before this cycle ran. The fix makes
	// dispatchGoalFallbackCompile clear the marker itself, unconditionally,
	// on every exit. Prove the REAL clearing here — deliberately using
	// rewindGoalActivityTimeOnly (time-only, no manual marker clear) instead
	// of rewindGoalActivity's crutch, so a regression that drops the fix
	// wedges this assertion instead of being silently masked.
	rewindGoalActivityTimeOnly(t, store, sid)
	al.goalQuietWindowSettle(time.Now()) // must still fire post-fallback, or the keeper is wedged
	after4 := goalRecordForSession(t, sid)
	// The goal is now RECORDED (goalRecordCompiledJSON(after3) != ""), so a firing
	// cycle routes through the unified push ladder (JUDGE-FR-095/FR-097,
	// D13, this wave) — it dispatches a bounded continue-push, NEVER the
	// Judge. A wedged keeper would do NEITHER — it would leave the push
	// counter untouched (the goalIsIdleSettling early-return in
	// maybeSettleGoalIdle never reaches ANY of this code).
	if got := cp.callCount(); got != callsAfterFallback {
		t.Fatalf("post-fallback idle cycle invoked the Judge: calls = %d, want unchanged at %d (D13: idle never judges)",
			got, callsAfterFallback)
	}
	if after4.ZeroOutputPushes != 1 {
		t.Fatalf("post-fallback idle cycle did not fire (keeper wedged at goalIsIdleSettling): pushes = %d, want 1 "+
			"(the fallback registration reset the shared counter to 0, so this is the RECORDED ladder's own first push)",
			after4.ZeroOutputPushes)
	}
}

// TestKeeper_NudgeLadderToFallback_ChannelOrigin_GetsOneFormattedEcho is
// review-round-1 finding #11's test: a channel-routed RECORDLESS goal that
// exhausts its nudge budget and falls through to the engine-authored D7
// fallback compile must get exactly one formatted record echo (FR-020) —
// the fallback compile is the path MOST likely to serve channel goals (an
// agent on a channel origin that never called set_goal within two nudges),
// and the raw emitGoalStatusFrameWithCriteriaAndDoD call this call site
// used before the fix skipped the channel echo entirely.
func TestKeeper_NudgeLadderToFallback_ChannelOrigin_GetsOneFormattedEcho(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "telegram", "chat-fallback-1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "make the tests pass", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("fallback compile reason")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // nudge 1
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // nudge 2
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // exhausted — engine fallback compile

	after := goalRecordForSession(t, sid)
	if goalRecordCompiledJSON(after) == "" {
		t.Fatal("setup: the fallback compile must have registered a record")
	}

	select {
	case msg := <-al.bus.OutboundChan():
		if msg.Channel != "telegram" || msg.ChatID != "chat-fallback-1" {
			t.Fatalf("echo routed to %s/%s, want telegram/chat-fallback-1", msg.Channel, msg.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finding #11: a channel-origin fallback registration must get exactly one formatted echo, got none")
	}
	select {
	case msg := <-al.bus.OutboundChan():
		t.Fatalf("expected exactly ONE echo, got a second: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

// =============== JUDGE-FR-095/FR-097: the unified push ladder (D13) =======
//
// This section replaces the FR-014b zero-adjudicable-output-triple suite:
// ADR-084 revision 9 D13 retires claimless idle adjudication entirely (a
// `met` claim is the sole trigger now, JUDGE-FR-095), which collapses the
// triple's two branches — "zero output" and "genuine output but still
// quiet" — into one, since BOTH used to lead to real adjudication once the
// triple went false, and neither can any more (JUDGE-FR-097). The triple
// itself (goalZeroOutputTripleHolds), its evidence/diff/output terms
// (goalZeroEvidenceRecords, goalHasTranscriptOutputSince,
// sessionHasTranscriptOutputSince) and the watermark-priming helper this
// suite used to need are deleted outright, not merely unreferenced.

// TestPushLadder_RecordedGoal_BoundedThenQuiet proves FR-097's core shape: a
// RECORDED goal at idle dispatches a bounded continue-push — never a
// verdict, never a round — up to goalZeroOutputPushMax (2) times, after
// which the ladder goes quiet: NO further push, and (FR-095's own text)
// NEVER a fall-through to an adjudication. Past the budget the goal's sole
// remaining terminator is the multi-day idle-expiry sweep (D-A, owned by
// E8) — this test proves the "leave it quiet" half of that contract, not
// the sweep itself.
func TestPushLadder_RecordedGoal_BoundedThenQuiet(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal push ladder", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must never be called — FR-095 retires claimless adjudication")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // push 1
	after1 := goalRecordForSession(t, sid)
	if after1.ZeroOutputPushes != 1 || after1.Round != 0 {
		t.Fatalf("push 1: pushes=%d (want 1) rounds=%d (want 0)", after1.ZeroOutputPushes, after1.Round)
	}
	if cp.callCount() != 0 {
		t.Fatalf("push 1 must never call the Judge; got %d", cp.callCount())
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // push 2 (budget: goalZeroOutputPushMax == 2)
	after2 := goalRecordForSession(t, sid)
	if after2.ZeroOutputPushes != 2 {
		t.Fatalf("push 2: pushes = %d, want 2", after2.ZeroOutputPushes)
	}
	if got := cp.callCount(); got != 0 {
		t.Fatalf("push 2 must still never call the Judge; got %d", got)
	}

	// Budget now spent. FR-095/097: MUST NOT push a third time and MUST NOT
	// fall through to an adjudication — the ladder goes quiet.
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now())
	after3 := goalRecordForSession(t, sid)
	if after3.ZeroOutputPushes != 2 {
		t.Fatalf("after budget spent: pushes = %d, want unchanged at 2 (no third push)", after3.ZeroOutputPushes)
	}
	if after3.Round != 0 {
		t.Fatalf("after budget spent: rounds = %d, want 0 (FR-095/097 forbid the claimless adjudication fall-through)", after3.Round)
	}
	if got := cp.callCount(); got != 0 {
		t.Fatalf("after budget spent: Judge calls = %d, want 0 — the goal is left quiet, not judged", got)
	}
}

// JUDGE-FR-095's two required oracles (TestQuietWindow_MakesZeroJudgeCalls,
// TestRunGoalAdjudication_RejectsEmptyClaimText) live in
// goal_triggers_adr084_test.go, per the joint delivery plan's own naming.

// TestResolveGoalScopedDiffEmpty_UnboundGoalDegenerates proves FR-014b's
// stated degenerate case: an unbound chat goal (empty WorkspaceID) has no
// work-under-review workspace to diff — the term degenerates away and reads
// empty.
func TestResolveGoalScopedDiffEmpty_UnboundGoalDegenerates(t *testing.T) {
	al := &AgentLoop{}
	if !al.resolveGoalScopedDiffEmpty("sid", "") {
		t.Fatal("an unbound goal (empty WorkspaceID) must degenerate to diff-term-empty (FR-014b)")
	}
}

// TestResolveGoalScopedDiffEmpty_CoTenantWorkspaceNotMasked is round-2 B-4's
// named regression: two goals sharing ONE WorkspaceID, goal B produced
// nothing — B's goal-scoped diff term must still read empty even though the
// OLD unscoped AttemptDiff(nil) at that same moment shows goal A's commit as
// "the latest boundary" (the masking bug this function's scoping fixes).
func TestResolveGoalScopedDiffEmpty_CoTenantWorkspaceNotMasked(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workDir, err := workspace.EnsureWorkDir(home, "shared-ws")
	if err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	repo, err := gitevidence.Open(workDir, gitevidence.WithRedactor(func(s string) string { return s }))
	if err != nil {
		t.Fatalf("gitevidence.Open: %v", err)
	}
	al := &AgentLoop{}
	resetGoalTriggerStateForTest()

	// Goal A commits something FIRST — before goal B ever looks.
	if writeErr := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("alpha"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	res, err := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "goal-a-work", AgentID: "agent-a"}, []string{"a.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("goal A commit: err=%v skipped=%v %v", err, res.Skipped, res.SkipReason)
	}

	// Sanity: the OLD unscoped call WOULD show goal A's commit as the latest
	// boundary — proving this test demonstrates a real masking risk.
	naive, err := repo.AttemptDiff(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(naive.Files) == 0 {
		t.Fatal("test setup: AttemptDiff(nil) must show goal A's commit for this test to demonstrate anything")
	}

	// Goal B's FIRST observation: baselines to current HEAD (after A's
	// commit already landed) and reads empty — the masking never happens.
	if !al.resolveGoalScopedDiffEmpty("goal-b-session", "shared-ws") {
		t.Fatal("goal B's first look must read empty (baseline to current HEAD), " +
			"even though AttemptDiff(nil) shows goal A's commit")
	}

	// A second look with no new commits must still read empty.
	if !al.resolveGoalScopedDiffEmpty("goal-b-session", "shared-ws") {
		t.Fatal("goal B's second look (no new commits since its own boundary) must read empty")
	}

	// Goal B genuinely commits its OWN change — its NEXT look must detect it.
	if writeErr := os.WriteFile(filepath.Join(workDir, "b.txt"), []byte("bravo"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	res, err = repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "goal-b-work", AgentID: "agent-b"}, []string{"b.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("goal B commit: err=%v skipped=%v %v", err, res.Skipped, res.SkipReason)
	}
	if al.resolveGoalScopedDiffEmpty("goal-b-session", "shared-ws") {
		t.Fatal("goal B's own real commit must be detected as non-empty on the next look")
	}
}
