// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_stop_frame_test.go pins the server-announces-the-Stop contract: when a
// person presses Stop on a goal-bearing session the goal keeper is paused
// (keeperPausedByStop) and the goal stays "active" on the record. The SPA's
// single Stop predicate keys on the goal_status state, so the pause MUST be
// announced as the contract's `waiting_on_user` ("needs_input/paused for any
// other reason", GoalStatusFrame.yaml) — otherwise the Stop button stays
// visible after the person already stopped (E2E llm-goal-work-first,
// goal-card-position.spec.ts, goal-work-first.spec.ts).
//
// Oracles are the goal_status events observed on the event bus, driven through
// the real cancel entry point (RequestCancelForSession -> StopSession), never
// the pause helper directly.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalStates returns the ordered pill states emitted for sid.
func stopFrameStates(c *eventCollector, sid string) []string {
	ps := goalStatusPayloadsFor(c, sid)
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.State)
	}
	return out
}

// waitForStopFrameState polls the collector until the last state emitted for sid is
// want, or fails after a bounded wait (the collector is fed asynchronously).
func waitForStopFrameState(t *testing.T, c *eventCollector, sid, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := stopFrameStates(c, sid); len(s) > 0 && s[len(s)-1] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("last goal_status state for %s never became %q; saw %v", sid, want, stopFrameStates(c, sid))
}

// settleEvents gives the asynchronous collector time to receive any frame that
// WAS emitted, so a "nothing was published" assertion is not vacuous.
func settleEvents() { time.Sleep(150 * time.Millisecond) }

// TestGoalStopFrame_StopBetweenTurnsAnnouncesWaitingOnUser: no turn is running
// (the goal is idle between turns); the Stop must still publish
// waiting_on_user for the session, carrying the goal id.
func TestGoalStopFrame_StopBetweenTurnsAnnouncesWaitingOnUser(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	if _, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web"); err != nil {
		t.Fatalf("RequestCancelForSession: %v", err)
	}
	waitForStopFrameState(t, c, sid, goalPillWaitingOnUser)
	ps := goalStatusPayloadsFor(c, sid)
	if got := ps[len(ps)-1].GoalID; got != gid {
		t.Errorf("waiting_on_user frame goal_id = %q; want %q", got, gid)
	}
	if rec := goalRecordForSession(t, sid); rec.State != "active" {
		t.Errorf("record state = %q; a Stop must leave the goal active", rec.State)
	}

	// A repeated Stop is not a new transition: no duplicate frame.
	n := len(stopFrameStates(c, sid))
	if _, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web"); err != nil {
		t.Fatalf("second RequestCancelForSession: %v", err)
	}
	settleEvents()
	if got := len(stopFrameStates(c, sid)); got != n {
		t.Errorf("a repeated Stop published %d extra goal_status frame(s); want none", got-n)
	}
}

// TestGoalStopFrame_StopMidTurnAnnouncesWaitingOnUser: a genuinely live turn
// (a verifier turn blocked inside a tool call, the harness FR-082 uses) bound
// to a goal-bearing session is Stopped; the frame must be published.
func TestGoalStopFrame_StopMidTurnAnnouncesWaitingOnUser(t *testing.T) {
	const taskID = "t-goal-stop-frame-midturn"
	al, resultCh, blockTool, _, registry := setUpBlockedVerifierTurn(t, taskID)
	sessions := registry.SessionsFor(verifierUnitForTask(taskID))
	if len(sessions) != 1 {
		t.Fatalf("setup: expected one live session, got %v", sessions)
	}
	sid := sessions[0]
	armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	fired, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web")
	if err != nil || !fired {
		t.Fatalf("mid-turn RequestCancelForSession: fired=%v err=%v", fired, err)
	}
	waitForStopFrameState(t, c, sid, goalPillWaitingOnUser)

	select {
	case <-blockTool.ctxErr:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel never reached the in-flight tool call")
	}
	select {
	case <-resultCh:
	case <-time.After(15 * time.Second):
		t.Fatal("the cancelled adjudication never returned")
	}
}

// TestGoalStopFrame_CronCancelAndGoallessStopPublishNothing: the cron watchdog
// is not the person stopping; and a Stop on a session with no active goal has
// no goal to announce.
func TestGoalStopFrame_CronCancelAndGoallessStopPublishNothing(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, goalSID := newGoalTestSession(t, al, agentInst.ID)
	armGoalRecord(t, goalSID, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	_, plainSID := newGoalTestSession(t, al, agentInst.ID)
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	if _, _, err := al.RequestCancelForSession(context.Background(), goalSID, "reaper", "cron"); err != nil {
		t.Fatalf("cron cancel: %v", err)
	}
	if _, _, err := al.RequestCancelForSession(context.Background(), plainSID, "tester", "web"); err != nil {
		t.Fatalf("goal-less Stop: %v", err)
	}
	settleEvents()
	if s := stopFrameStates(c, goalSID); len(s) != 0 {
		t.Errorf("a cron-channel cancel published goal_status %v; want nothing", s)
	}
	if s := stopFrameStates(c, plainSID); len(s) != 0 {
		t.Errorf("a Stop on a goal-less session published goal_status %v; want nothing", s)
	}
}

// TestGoalStopFrame_SnapshotWhilePausedReportsWaitingOnUser: a client that
// reattaches (EmitGoalStatusRehydrate, called by the gateway's attach path)
// while the pause holds gets waiting_on_user; once the pause is gone (a gateway
// restart forgets it — the pause is in memory only) the same snapshot reports
// the record's true state, active.
func TestGoalStopFrame_SnapshotWhilePausedReportsWaitingOnUser(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	if _, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForStopFrameState(t, c, sid, goalPillWaitingOnUser)
	before := len(stopFrameStates(c, sid))

	if !al.EmitGoalStatusRehydrate(sid) {
		t.Fatal("EmitGoalStatusRehydrate returned false for an active goal")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(stopFrameStates(c, sid)) <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := stopFrameStates(c, sid); len(s) <= before || s[len(s)-1] != goalPillWaitingOnUser {
		t.Fatalf("snapshot while Stop-paused = %v; want a fresh waiting_on_user", s)
	}

	// Gateway restart: the in-memory pause is gone; the record is still active.
	resetGoalTriggerStateForTest()
	before = len(stopFrameStates(c, sid))
	if !al.EmitGoalStatusRehydrate(sid) {
		t.Fatal("EmitGoalStatusRehydrate returned false after restart")
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(stopFrameStates(c, sid)) <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := stopFrameStates(c, sid); s[len(s)-1] != goalPillActive {
		t.Fatalf("snapshot after restart = %v; want active (the pause is not persisted)", s)
	}
}

// TestGoalStopFrame_StoppedTurnFinishingStaysWaitingOnUser: a graceful Stop
// lets the stopped turn end as a normal completion, whose after-turn hook
// emits `active` — that must not undo the announced pause.
func TestGoalStopFrame_StoppedTurnFinishingStaysWaitingOnUser(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	if err := store.AppendTranscript(sid, session.TranscriptEntry{
		ID: "user-before-stop", Role: "user", AgentID: agentInst.ID,
		Content: "write the report", Timestamp: time.Now().UTC().Add(-time.Second),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	if _, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "Stopping here as asked."})
	settleEvents()
	s := stopFrameStates(c, sid)
	if len(s) == 0 || s[len(s)-1] != goalPillWaitingOnUser {
		t.Fatalf("after the stopped turn finished, states = %v; the last must stay waiting_on_user", s)
	}
}

// gateProvider blocks every Chat call until release is closed, so a test can
// observe what is published WHILE a turn is running.
type gateProvider struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
}

func (g *gateProvider) Chat(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	g.once.Do(func() { close(g.started) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &providers.LLMResponse{Content: "carrying on", ToolCalls: []providers.ToolCall{}}, nil
}

func (g *gateProvider) GetDefaultModel() string { return "mock-model" }

// TestGoalStopFrame_NextUserMessagePublishesActiveAndGoalContinues: after the
// Stop, the person writes again. `active` must be published as the new turn
// STARTS (the Stop button must come back for the running turn, not only after
// it ends), the goal stays active, and the keeper is un-paused.
func TestGoalStopFrame_NextUserMessagePublishesActiveAndGoalContinues(t *testing.T) {
	resetGoalTriggerStateForTest()
	gate := &gateProvider{release: make(chan struct{}), started: make(chan struct{})}
	al, _ := newGoalLoopTestLoop(t, gate, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	if _, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForStopFrameState(t, c, sid, goalPillWaitingOnUser)

	// The new message is saved on arrival, before its turn starts (web handler).
	if err := store.AppendTranscript(sid, session.TranscriptEntry{
		ID: "user-after-stop", Role: "user", AgentID: agentInst.ID,
		Content: "carry on", Timestamp: time.Now().UTC().Add(time.Second),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		UserMessage: "carry on",
	}
	done := make(chan error, 1)
	go func() { _, err := al.runAgentLoop(context.Background(), agentInst, opts); done <- err }()

	select {
	case <-gate.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the new turn never reached the provider")
	}
	// The turn is still running (provider gated): active must already be out.
	waitForStopFrameState(t, c, sid, goalPillActive)
	if al.goalKeeperPausedByStop(sid) {
		t.Error("the keeper is still Stop-paused while the user's new turn runs")
	}

	close(gate.release)
	if err := <-done; err != nil {
		t.Fatalf("runAgentLoop: %v", err)
	}
	if s := stopFrameStates(c, sid); s[len(s)-1] != goalPillActive {
		t.Errorf("states after the continued turn = %v; want the goal active", s)
	}
	if rec := goalRecordForSession(t, sid); rec.State != "active" {
		t.Errorf("record state = %q; want active", rec.State)
	}
}

// stoppedGoalSession arms a goal, saves the user message that started the
// (now Stopped) turn BEFORE the Stop, and Stops through the real cancel path.
func stoppedGoalSession(t *testing.T, al *AgentLoop, agentID string) (*session.UnifiedStore, string, *eventCollector, func()) {
	t.Helper()
	store, sid := newGoalTestSession(t, al, agentID)
	armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	if err := store.AppendTranscript(sid, session.TranscriptEntry{
		ID: "user-before-stop", Role: "user", AgentID: agentID,
		Content: "write the report", Timestamp: time.Now().UTC().Add(-time.Second),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	c, cleanup := newEventCollector(t, al)
	if _, _, err := al.RequestCancelForSession(context.Background(), sid, "tester", "web"); err != nil {
		cleanup()
		t.Fatalf("Stop: %v", err)
	}
	waitForStopFrameState(t, c, sid, goalPillWaitingOnUser)
	return store, sid, c, cleanup
}

func assertNeverActive(t *testing.T, c *eventCollector, sid string) {
	t.Helper()
	settleEvents()
	for _, st := range stopFrameStates(c, sid) {
		if st == goalPillActive {
			t.Fatalf("an `active` goal_status was published while the Stop-pause should hold; states = %v", stopFrameStates(c, sid))
		}
	}
}

// TestGoalStopFrame_TurnsThatAreNotANewUserMessageKeepThePause drives the real
// runAgentLoop: the keeper's own follow-up (UserInitiated=false) — even with a
// newer user message in the transcript — and a user-initiated turn with NO
// newer user message must both leave the pause in place and publish no active.
func TestGoalStopFrame_TurnsThatAreNotANewUserMessageKeepThePause(t *testing.T) {
	for _, tc := range []struct {
		name         string
		userInit     bool
		sender       string
		newerMessage bool
	}{
		{"keeper follow-up with a newer user message", false, goalLoopFollowUpSenderID, true},
		{"user turn with no newer user message", true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetGoalTriggerStateForTest()
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			agentInst, _ := al.GetRegistry().GetAgent("native-agent")
			store, sid, c, cleanup := stoppedGoalSession(t, al, agentInst.ID)
			defer cleanup()
			if tc.newerMessage {
				if err := store.AppendTranscript(sid, session.TranscriptEntry{
					ID: "user-after-stop", Role: "user", AgentID: agentInst.ID,
					Content: "later", Timestamp: time.Now().UTC().Add(time.Second),
				}); err != nil {
					t.Fatalf("append: %v", err)
				}
			}
			opts := processOptions{
				TranscriptStore: store, TranscriptSessionID: sid,
				Channel: "webchat", ChatID: "c1", SessionKey: "sk1",
				UserInitiated: tc.userInit, SenderID: tc.sender, UserMessage: "x",
			}
			if _, err := al.runAgentLoop(context.Background(), agentInst, opts); err != nil {
				t.Fatalf("runAgentLoop: %v", err)
			}
			if !al.goalKeeperPausedByStop(sid) {
				t.Fatal("the Stop-pause was lifted by a turn that is not a new user message")
			}
			assertNeverActive(t, c, sid)
		})
	}
}

// TestGoalStopFrame_JudgingAndJudgeUnavailablePassThroughDuringPause: only
// `active` is remapped; the judge's own states keep the Stop button visible.
func TestGoalStopFrame_JudgingAndJudgeUnavailablePassThroughDuringPause(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid, c, cleanup := stoppedGoalSession(t, al, agentInst.ID)
	defer cleanup()
	gid := goalRecordForSession(t, sid).GoalID
	for _, st := range []string{goalPillJudging, goalPillJudgeUnavailable} {
		al.emitGoalStatusFrame(sid, gid, "ship the report", 0, 20, "r", st)
		waitForStopFrameState(t, c, sid, st)
	}
}

// TestGoalStopFrame_SnapshotOfWorkerParkedGoalReportsWaitingOnUser: the worker's
// own typed waiting_on_user park (goalIsWaitingOnUser) is also reported by a
// reattach snapshot, with no Stop involved.
func TestGoalStopFrame_SnapshotOfWorkerParkedGoalReportsWaitingOnUser(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, "ship the report", recordedGoalCriteria("ship the report"), 0, time.Now())
	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	al.EmitGoalStatusRehydrate(sid)
	waitForStopFrameState(t, c, sid, goalPillActive)
	al.goalSetWaitingOnUser(gid, true)
	al.EmitGoalStatusRehydrate(sid)
	waitForStopFrameState(t, c, sid, goalPillWaitingOnUser)

	al.goalSetWaitingOnUser(gid, false)
	al.EmitGoalStatusRehydrate(sid)
	waitForStopFrameState(t, c, sid, goalPillActive)
}
