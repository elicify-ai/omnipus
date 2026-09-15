// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_keeper_stop_pause_test.go pins the founder decision of 2026-09-14
// (UAT B-1 run 4): an explicit chat Stop pauses the goal keeper until the
// next turn runs on the session. In that UAT run the user pressed Stop and
// ~66 s later the idle keeper started a new turn on its own, which then
// worked for 9 minutes.
//
// Oracles are the keeper's observable actions (async-notifier dispatches and
// the persisted push counter), not internal flags: while the pause holds the
// keeper dispatches nothing; once a genuine user turn has run, the keeper
// behaves as before. The goal itself stays ACTIVE — a Stop is not an ending.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// dispatchesFor returns every async notify event captured by obs.
func dispatchesFor(obs *[]AsyncNotifyEvent, mu *sync.Mutex) []AsyncNotifyEvent {
	mu.Lock()
	defer mu.Unlock()
	out := make([]AsyncNotifyEvent, len(*obs))
	copy(out, *obs)
	return out
}

// TestKeeperStopPause_StopSuppressesIdleKeeperUntilANewTurnRuns is the UAT B-1
// run 4 scenario, compressed: a goal whose quiet window has elapsed, a Stop,
// a keeper sweep that must do NOTHING, then a user turn, then a keeper sweep
// that behaves normally again.
func TestKeeperStopPause_StopSuppressesIdleKeeperUntilANewTurnRuns(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	// Last activity far beyond the quiet window: the keeper WOULD fire.
	gid := armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"),
		0, time.Now().Add(-5*goalIdleQuietWindow))
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	var mu sync.Mutex
	var dispatched []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, evt)
	})

	// (a) The Stop — exactly what RequestCancel does for every cancel surface.
	al.pauseGoalKeeperForStop(sid, "web")
	if !al.goalKeeperPausedByStop(sid) {
		t.Fatal("setup: the Stop did not pause the keeper")
	}

	// (b) Keeper sweep during the quiet window: must dispatch NOTHING and
	// consume no push — the UAT defect is exactly this dispatch.
	al.goalQuietWindowSettle(time.Now())
	if got := dispatchesFor(&dispatched, &mu); len(got) != 0 {
		t.Fatalf("the keeper dispatched %d turn(s) while the session was Stop-paused (UAT B-1 run 4); want none: %+v", len(got), got)
	}
	if pushes := goalRecordForSession(t, sid).ZeroOutputPushes; pushes != 0 {
		t.Fatalf("the paused keeper consumed a continue-push (ZeroOutputPushes=%d); want 0", pushes)
	}

	// (c) The goal stays ACTIVE — a Stop is a pause, not an ending.
	if rec := goalRecordForSession(t, sid); rec.State != "active" {
		t.Fatalf("goal state = %q; the Stop must not end the goal (only /goal clear does)", rec.State)
	}

	// (d) The user sends a new message after the Stop — saved to the transcript
	// on arrival, exactly as the web handler does — and its turn runs. The
	// pause lifts.
	if err := store.AppendTranscript(sid, session.TranscriptEntry{
		ID: "user-after-stop", Role: "user", AgentID: agentInst.ID,
		Content: "ok, carry on", Timestamp: time.Now().UTC().Add(time.Second),
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "picking this back up"})
	if al.goalKeeperPausedByStop(sid) {
		t.Fatal("a genuine user turn must lift the Stop-pause so the keeper resumes")
	}

	// (e) The next sweep behaves normally again: the continue-push dispatches.
	// Re-age the record first — the user's turn in (d) legitimately re-armed
	// the quiet window, and this step is about the keeper RESUMING, not about
	// the window math.
	if _, err := resolveGoalRecordStore().Update(gid, func(cur *goal.Goal) error {
		cur.LastActivityAt = time.Now().Add(-5 * goalIdleQuietWindow)
		return nil
	}); err != nil {
		t.Fatalf("re-age goal record: %v", err)
	}
	al.goalQuietWindowSettle(time.Now())
	got := dispatchesFor(&dispatched, &mu)
	if len(got) != 1 {
		t.Fatalf("after the user's turn the keeper dispatched %d turn(s); want the normal 1 continue-push: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Content, "Continue working toward the goal") {
		t.Errorf("resumed dispatch content = %q; want the continue-push prompt", got[0].Content)
	}
}

// TestKeeperStopPause_TheStoppedTurnFinishingDoesNotLiftIt: a graceful Stop
// lets the stopped turn run one last tool-less round and end as a normal
// completion, so that very turn reaches checkGoalLoopAfterTurn with
// UserInitiated=true. Its user message was saved BEFORE the Stop, so it must
// not lift the pause — otherwise the Stop would be undone the instant it
// landed and the keeper would dispatch on the next sweep (the UAT defect).
func TestKeeperStopPause_TheStoppedTurnFinishingDoesNotLiftIt(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"),
		0, time.Now().Add(-5*goalIdleQuietWindow))
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	// The user's message that started the turn they are about to Stop.
	if err := store.AppendTranscript(sid, session.TranscriptEntry{
		ID: "user-before-stop", Role: "user", AgentID: agentInst.ID,
		Content: "write the report", Timestamp: time.Now().UTC().Add(-time.Second),
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	var mu sync.Mutex
	var dispatched []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, evt)
	})

	al.pauseGoalKeeperForStop(sid, "web")
	// The stopped turn's graceful last round completes normally.
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "Stopping here as asked."})

	if !al.goalKeeperPausedByStop(sid) {
		t.Fatal("the stopped turn's own graceful completion lifted the Stop-pause — the Stop was undone the instant it landed")
	}
	if _, err := resolveGoalRecordStore().Update(gid, func(cur *goal.Goal) error {
		cur.LastActivityAt = time.Now().Add(-5 * goalIdleQuietWindow)
		return nil
	}); err != nil {
		t.Fatalf("re-age goal record: %v", err)
	}
	al.goalQuietWindowSettle(time.Now())
	if got := dispatchesFor(&dispatched, &mu); len(got) != 0 {
		t.Fatalf("the keeper dispatched %d turn(s) after the stopped turn finished; want none until the user sends a new message: %+v", len(got), got)
	}
}

// TestKeeperStopPause_CronWatchdogDoesNotPause: the cron deadline reaper stops
// a stuck turn without the user choosing to stop the work — that cancel must
// NOT pause the keeper.
func TestKeeperStopPause_CronWatchdogDoesNotPause(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	al.pauseGoalKeeperForStop("session-cron", "cron")
	if al.goalKeeperPausedByStop("session-cron") {
		t.Fatal("a cron-watchdog cancel must not pause the goal keeper — it is not the user stopping the work")
	}
	al.pauseGoalKeeperForStop("session-web", "web")
	if !al.goalKeeperPausedByStop("session-web") {
		t.Fatal("a web Stop must pause the goal keeper")
	}
}

// TestKeeperStopPause_RequestCancelPausesTheSession is the wiring proof: a
// REAL fired cancel through RequestCancel (the canonical machine every Stop
// surface uses) pauses the keeper for the cancelled session. It cancels a
// genuinely live turn — a verifier turn blocked mid-tool-call — the same
// harness FR-082's tests use.
func TestKeeperStopPause_RequestCancelPausesTheSession(t *testing.T) {
	const taskID = "t-keeper-stop-wiring"
	al, resultCh, blockTool, _, registry := setUpBlockedVerifierTurn(t, taskID)

	sessions := registry.SessionsFor(verifierUnitForTask(taskID))
	if len(sessions) != 1 {
		t.Fatalf("setup: expected one live verifier session, got %v", sessions)
	}
	fired, _, err := al.RequestCancelForSession(context.Background(), sessions[0], "tester", "web")
	if err != nil || !fired {
		t.Fatalf("RequestCancelForSession(%q): fired=%v err=%v", sessions[0], fired, err)
	}
	if !al.goalKeeperPausedByStop(sessions[0]) {
		t.Fatal("a fired user cancel must pause the goal keeper for that session (the cancel.go wiring)")
	}

	// Join the cancelled adjudication before returning, exactly as
	// TestVerifierTurnCancel_EndsAdjudicationWithoutRetry does: it keeps
	// writing its verifier session's files after the cancel, and returning
	// early races t.TempDir's cleanup ("directory not empty").
	select {
	case <-blockTool.ctxErr:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel never reached the Judge's in-flight tool call")
	}
	select {
	case <-resultCh:
	case <-time.After(15 * time.Second):
		t.Fatal("the cancelled adjudication never returned")
	}
}

// TestKeeperStopPause_KeeperFollowUpDoesNotLiftThePause: the keeper's own
// follow-up turn (goalLoopFollowUpSenderID) must NOT lift the pause — only a
// user message or a task run's own turn does. This is what keeps a Stop from
// being undone by the very machinery it stopped.
func TestKeeperStopPause_KeeperFollowUpDoesNotLiftThePause(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	al.pauseGoalKeeperForStop(sid, "web")

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1",
		SenderID: goalLoopFollowUpSenderID, // the keeper's own re-injected turn
	}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "still working"})

	if !al.goalKeeperPausedByStop(sid) {
		t.Fatal("the keeper's own follow-up turn lifted the Stop-pause — only a user message or task run may do that")
	}
}
