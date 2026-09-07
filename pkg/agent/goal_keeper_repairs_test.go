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
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// setGoalRecordArmed is setGoalRoundsArmed's RECORDED-goal counterpart: a
// non-empty GoalCriteriaJSON (one prose criterion), so the zero-output
// triple path (FR-014b) is reached instead of the recordless nudge ladder
// (FR-014/D6c).
func setGoalRecordArmed(t *testing.T, store *session.UnifiedStore, sid, condition string, roundsUsed int, lastActivity time.Time) {
	t.Helper()
	criteria := `{"intent":"x","prompt":"x","criteria":[{"id":"c1","kind":"prose","text":"do it","judgment":"boolean"}]}`
	maxRounds := 5
	past := lastActivity.UTC().Format(time.RFC3339)
	if err := store.SetMeta(sid, session.MetaPatch{
		GoalCondition:      &condition,
		GoalCriteriaJSON:   &criteria,
		GoalRoundsUsed:     &roundsUsed,
		GoalMaxRounds:      &maxRounds,
		GoalLastActivityAt: &past,
		GoalStartedAt:      &past,
	}); err != nil {
		t.Fatal(err)
	}
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
// tests can assert cycle-over-cycle counter behavior in isolation.
func rewindGoalActivity(t *testing.T, al *AgentLoop, store *session.UnifiedStore, sid string) {
	t.Helper()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	if err := store.SetMeta(sid, session.MetaPatch{GoalLastActivityAt: &past}); err != nil {
		t.Fatal(err)
	}
	al.goalMarkIdleSettling(sid, false)
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
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
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
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalLastActivityAt == "" {
		t.Fatal("in-flight suppression must re-arm the quiet window (bump GoalLastActivityAt)")
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
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
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
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
	// A RECORDED goal (not recordless — setGoalRecordArmed): once suppression
	// lifts, the FIRST-ever triple observation trivially holds (nothing to
	// compare against yet), so the expected next action is a bounded
	// continue-push, not an immediate Judge call — proving suppression
	// lifted and normal keeper processing resumed, without conflating this
	// test with FR-014b's separate bounded-push-ladder tests.
	setGoalRecordArmed(t, store, sid, "goal parked card", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must not fire while a card is parked")
	judgeInst.Provider = cp
	reg := &fakeParkedCardRegistry{pending: map[string]bool{sid: true}}
	al.SetAskUserRegistry(reg)

	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 0 {
		t.Fatalf("parked card must suppress idle settlement; Judge calls = %d, want 0", got)
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalZeroOutputPushes != 0 {
		t.Fatalf("parked card must ALSO suppress the nudge/push ladder; GoalZeroOutputPushes = %d, want 0", after.GoalZeroOutputPushes)
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
	after2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after2.GoalZeroOutputPushes != 1 {
		t.Fatalf("after the card clears: GoalZeroOutputPushes = %d, want 1 (suppression must lift and normal keeper processing resume)",
			after2.GoalZeroOutputPushes)
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
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal wedge regression", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("not yet")
	judgeInst.Provider = cp

	// This test targets D6b's sender-gate fix specifically, not FR-014b's
	// zero-output triple (which has its own dedicated tests) — prime a
	// watermark + a genuine prior transcript entry so the triple evaluates
	// FALSE from the start (a fresh goal-id's true FIRST observation would
	// otherwise trivially dispatch a bounded push rather than a real
	// adjudication, per goalZeroOutputTripleHolds' own degenerate-baseline
	// rule).
	seedWatermark := time.Now().Add(-2 * time.Hour)
	goalTriggersSingleton.mu.Lock()
	goalTriggersSingleton.outputWatermarks[sid] = seedWatermark
	goalTriggersSingleton.mu.Unlock()
	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "seed-work", Type: session.EntryTypeMessage, Role: "assistant",
		Content: "earlier real work", Timestamp: time.Now().Add(-90 * time.Minute), AgentID: agentInst.ID,
	}); err != nil {
		t.Fatal(err)
	}

	// --- Cycle 1: idle fires, unmet verdict, steer re-injected. ---
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 1 {
		t.Fatalf("cycle 1: Judge calls = %d, want 1", got)
	}
	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.GoalRoundsUsed != 1 {
		t.Fatalf("cycle 1: rounds_used = %d, want 1", after1.GoalRoundsUsed)
	}

	var steerMsg bus.InboundMessage
	select {
	case steerMsg = <-al.bus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the idle-steer follow-up to be published")
	}
	if steerMsg.Channel != "system" {
		t.Fatalf("idle-steer channel = %q, want %q", steerMsg.Channel, "system")
	}
	if steerMsg.Sender.CanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("D6b: idle-steer sender = %q, want %q — the un-wedge fix stamps the goal-loop sentinel",
			steerMsg.Sender.CanonicalID, goalLoopFollowUpSenderID)
	}

	if _, err := al.processSystemMessage(context.Background(), steerMsg); err != nil {
		t.Fatalf("processSystemMessage: %v", err)
	}

	if al.goalIsIdleSettling(sid) {
		t.Fatal("D6b: idleSettling must clear once the steer turn passes the origin gate — the keeper must not wedge")
	}

	// --- Cycle 2: rewind activity and prove a SECOND full cycle completes. ---
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 2 {
		t.Fatalf("cycle 2: Judge calls = %d, want 2 (a second full idle cycle must complete — no wedge)", got)
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
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	al.recordGoalRouting(sid, "telegram", "chat-42", "sk-42", agentInst.ID)

	route := goalTriggers().routeFor(sid)
	if route.channel != "telegram" || route.chatID != "chat-42" {
		t.Fatalf("in-memory route = %+v, want channel=telegram chat_id=chat-42", route)
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalRouteChannel != "telegram" || meta.GoalRouteChatID != "chat-42" ||
		meta.GoalRouteSessionKey != "sk-42" || meta.GoalRouteAgentID != agentInst.ID {
		t.Fatalf("recordGoalRouting must persist all four GoalRoute* fields; got channel=%q chat_id=%q session_key=%q agent_id=%q",
			meta.GoalRouteChannel, meta.GoalRouteChatID, meta.GoalRouteSessionKey, meta.GoalRouteAgentID)
	}

	// Simulate a gateway restart: wipe ALL in-memory trigger state, INCLUDING
	// the routing map and the session-store resolver.
	resetGoalTriggerStateForTest()

	// A post-restart routine tick (goalQuietWindowSettle runs regardless of
	// any specific goal's own activation) re-wires the session-store
	// resolver, exactly as it would on a real cold boot — see
	// sessionStoreResolver's doc comment.
	al.goalQuietWindowSettle(time.Now())

	rehydrated := goalTriggers().routeFor(sid)
	if rehydrated.channel != "telegram" || rehydrated.chatID != "chat-42" {
		t.Fatalf("FR-031: rehydrated route = %+v, want channel=telegram chat_id=chat-42 — a restart must not silently disable routing", rehydrated)
	}

	goalTriggersSingleton.mu.Lock()
	_, cached := goalTriggersSingleton.routing[sid]
	goalTriggersSingleton.mu.Unlock()
	if !cached {
		t.Fatal("a rehydrated route must be cached back into the in-memory map for O(1) subsequent reads")
	}
}

// TestGoalRouting_MissingBothSides_WarnsAndSetsLatestReason proves FR-031's
// failure mode: a route missing on BOTH the in-memory map and the persisted
// fields must never degrade silently — it writes a one-line GoalLatestReason
// note.
func TestGoalRouting_MissingBothSides_WarnsAndSetsLatestReason(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	// Wire the resolver WITHOUT ever calling recordGoalRouting — a goal
	// whose routing was truly never captured (or lost) on either side.
	goalTriggersSingleton.mu.Lock()
	goalTriggersSingleton.sessionStoreResolver = al.GetSessionStore
	goalTriggersSingleton.mu.Unlock()

	route := goalTriggers().routeFor(sid)
	if route.channel != "" || route.chatID != "" {
		t.Fatalf("route = %+v, want the zero value (nothing was ever recorded)", route)
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	const wantReason = "keeper cannot reach the goal's channel — routing lost"
	if meta.GoalLatestReason != wantReason {
		t.Fatalf("GoalLatestReason = %q, want %q (FR-031: never degrade silently)", meta.GoalLatestReason, wantReason)
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
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "make the tests pass", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must never be called — a recordless goal is never judged")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // nudge 1
	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.GoalZeroOutputPushes != 1 {
		t.Fatalf("after nudge 1: GoalZeroOutputPushes = %d, want 1", after1.GoalZeroOutputPushes)
	}
	if after1.GoalCriteriaJSON != "" {
		t.Fatal("after nudge 1: the goal must still be recordless")
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // nudge 2
	after2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after2.GoalZeroOutputPushes != 2 {
		t.Fatalf("after nudge 2: GoalZeroOutputPushes = %d, want 2", after2.GoalZeroOutputPushes)
	}
	if after2.GoalCriteriaJSON != "" {
		t.Fatal("after nudge 2: the goal must still be recordless")
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // exhausted — engine fallback compile
	after3, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after3.GoalCriteriaJSON == "" {
		t.Fatal("FR-017: after N=2 nudges, the engine's fallback compile must register a record")
	}
	if after3.GoalZeroOutputPushes != 0 {
		t.Fatalf("engine-authored fallback registration must reset the shared push counter; got %d, want 0", after3.GoalZeroOutputPushes)
	}
	if got := cp.callCount(); got != 0 {
		t.Fatalf("Judge calls = %d, want 0 — a recordless goal is only ever nudged/fallback-compiled, never judged", got)
	}
}

// =============== FR-014b: the zero-adjudicable-output triple ==============

// TestZeroOutputTriple_RecordedGoal_BoundedPushesThenNormalAdjudication
// proves FR-014b's core shape: a RECORDED goal with zero evidence, zero
// goal-scoped diff (unbound goal — the diff term degenerates), and zero
// transcript output consumes no round and dispatches a bounded continue-push
// — at most goalZeroOutputPushMax (2) times — after which idle settlement
// adjudicates NORMALLY (a fail-closed verdict is legitimate, consumes a
// round, so the rounds bound eventually terminates the loop).
func TestZeroOutputTriple_RecordedGoal_BoundedPushesThenNormalAdjudication(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal zero output", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("only reached once the push budget is spent")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // push 1 (first-ever observation)
	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.GoalZeroOutputPushes != 1 {
		t.Fatalf("push 1: GoalZeroOutputPushes = %d, want 1", after1.GoalZeroOutputPushes)
	}
	if after1.GoalRoundsUsed != 0 || cp.callCount() != 0 {
		t.Fatalf("push 1 must consume no round and never call the Judge; rounds=%d judge_calls=%d",
			after1.GoalRoundsUsed, cp.callCount())
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // push 2
	after2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after2.GoalZeroOutputPushes != 2 {
		t.Fatalf("push 2: GoalZeroOutputPushes = %d, want 2", after2.GoalZeroOutputPushes)
	}
	if got := cp.callCount(); got != 0 {
		t.Fatalf("push 2 must still never call the Judge; got %d", got)
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // budget spent — normal adjudication
	after3, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got := cp.callCount(); got != 1 {
		t.Fatalf("after the push budget is spent, the Judge must be called; got %d, want 1", got)
	}
	if after3.GoalRoundsUsed != 1 {
		t.Fatalf("normal adjudication must consume a round; rounds_used = %d, want 1", after3.GoalRoundsUsed)
	}
}

// TestZeroOutputTriple_PushCounterSurvivesRestart is DS-3's restart row: a
// gateway restart between push 1 and push 2 must not reset the PERSISTED
// GoalZeroOutputPushes streak — the in-memory watermarks/diff-boundary state
// re-baselines, but the counter that actually bounds the loop survives.
func TestZeroOutputTriple_PushCounterSurvivesRestart(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal restart test", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("only once the post-restart counter is honored")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // push 1
	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.GoalZeroOutputPushes != 1 {
		t.Fatalf("push 1: GoalZeroOutputPushes = %d, want 1", after1.GoalZeroOutputPushes)
	}

	// --- Simulate a gateway restart: wipe ALL in-memory trigger state. ---
	resetGoalTriggerStateForTest()
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID) // routing is re-established the same way a fresh routeFor rehydration would

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // push 2, post-restart
	after2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after2.GoalZeroOutputPushes != 2 {
		t.Fatalf("push 2 (post-restart): GoalZeroOutputPushes = %d, want 2 — the persisted streak must survive a restart", after2.GoalZeroOutputPushes)
	}
	if got := cp.callCount(); got != 0 {
		t.Fatalf("push 2 must still never call the Judge; got %d", got)
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // 3rd cycle: budget spent, normal adjudication
	if got := cp.callCount(); got != 1 {
		t.Fatalf("3rd cycle (post-restart, budget spent): Judge calls = %d, want 1", got)
	}
}

// TestZeroOutputTriple_TranscriptOutputBreaksIt proves the triple correctly
// reads "not zero" once real transcript output lands between checks, and
// that the push counter resets to 0 (FR-014b's reset rule: "whenever the
// triple evaluates false at an idle").
func TestZeroOutputTriple_TranscriptOutputBreaksIt(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal with real output", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("real output must trigger normal adjudication")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now()) // push 1 (first-ever observation)
	if got := cp.callCount(); got != 0 {
		t.Fatalf("push 1: Judge calls = %d, want 0", got)
	}

	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "real-work", Type: session.EntryTypeMessage, Role: "assistant",
		Content: "made real progress", Timestamp: time.Now(), AgentID: agentInst.ID,
	}); err != nil {
		t.Fatal(err)
	}

	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now())
	if got := cp.callCount(); got != 1 {
		t.Fatalf("real transcript output must break the triple and trigger normal adjudication; Judge calls = %d, want 1", got)
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalZeroOutputPushes != 0 {
		t.Fatalf("the push counter must reset once the triple evaluates false at an idle; got %d, want 0", after.GoalZeroOutputPushes)
	}
}

// TestGoalZeroOutputTriple_DescendantTranscriptCounts proves US-6 A2/FR-014b:
// a goal whose agent delegated ALL of its work — the ROOT session's own
// transcript stays silent, but a delegated CHILD session's transcript has
// real output — must NOT read as zero output. Descendants are resolved via
// the DURABLE ParentSessionID chain (goalDescendantSessionIDs), not the
// in-memory turnState scan FR-013 uses.
func TestGoalZeroOutputTriple_DescendantTranscriptCounts(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	childMeta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
	if err != nil {
		t.Fatal(err)
	}
	childID := childMeta.ID
	if err := store.SetMeta(childID, session.MetaPatch{ParentSessionID: &sid}); err != nil {
		t.Fatal(err)
	}

	watermark := time.Now().Add(-1 * time.Hour)

	if al.goalHasTranscriptOutputSince(store, sid, watermark) {
		t.Fatal("precondition: neither session has any output yet")
	}

	if err := store.AppendTranscriptStrict(childID, session.TranscriptEntry{
		ID: "delegate-work", Type: session.EntryTypeMessage, Role: "assistant",
		Content: "delegate did the work", Timestamp: time.Now(), AgentID: agentInst.ID,
	}); err != nil {
		t.Fatal(err)
	}

	if !al.goalHasTranscriptOutputSince(store, sid, watermark) {
		t.Fatal("US-6 A2/FR-014b: a delegated child's output must count as the goal's own output — " +
			"a goal that delegated everything is WORKING, not empty")
	}
}

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
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
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
	if err := os.WriteFile(filepath.Join(workDir, "b.txt"), []byte("bravo"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "goal-b-work", AgentID: "agent-b"}, []string{"b.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("goal B commit: err=%v skipped=%v %v", err, res.Skipped, res.SkipReason)
	}
	if al.resolveGoalScopedDiffEmpty("goal-b-session", "shared-ws") {
		t.Fatal("goal B's own real commit must be detected as non-empty on the next look")
	}
}
