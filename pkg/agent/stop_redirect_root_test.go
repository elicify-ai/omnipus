package agent

// Founder decisions 2026-10-06: "/stop-redirect in the chat does not redirect
// a helper, it redirects the chat itself"; it works on ANY session the caller
// is in. For an ordinary (root) chat: stop THIS session's current turn (the
// one session Stop), then continue THIS chat with the instruction as its next
// user message — exactly one new turn carrying the exact instruction text.
// Written by backend-lead (no QA lane available; founder asked for speed).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const stopRedirectRootInstruction = "Stop that and summarise the plan in one line instead."

const stopRedirectRootHumanTask = "Draft the full rollout plan step by step."

// newStopRedirectRoot builds an ordinary chat the way a person's web chat
// runs: a real root (Launch/Dispatch) whose first turn finishes, then a
// person's web message — recorded the way the web intake records it — whose
// turn holds its provider call. The provider honours its context, so a Stop
// cancels that call. Returns the live human turn's result channel.
func newStopRedirectRoot(t *testing.T) (*AgentLoop, *session.LifecycleRecord, *r1CompletionProvider, <-chan error) {
	t.Helper()
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{
		answers: []string{"chat opened", "first plan draft", "redirected answer"},
		entered: make(chan int, 3), release: []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})},
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered root agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.openAll)
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "keep this ordinary conversation open",
		Origin: steer.Origin{Kind: steer.OriginKindChat}, Owner: "root-owner",
	})
	if err != nil {
		t.Fatalf("SETUP: real root Launch: %v", err)
	}
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("SETUP: real root Dispatch=%+v error=%v", dispatched, err)
	}
	r1AwaitProvider(t, provider, 0)
	provider.open(0)
	stopRedirectAwaitIdle(t, al, res.SessionID)

	msg := rootHumanMessage(res.SessionID)
	msg.Content = stopRedirectRootHumanTask
	if err := al.GetSessionStore().AppendTranscriptWithProvenance(res.SessionID, session.TranscriptEntry{
		ID: "human-1", Role: "user", Content: msg.Content, AgentID: testDefaultAgentID, Timestamp: time.Now().UTC(),
	}, "root-owner"); err != nil {
		t.Fatalf("SETUP: record the person's web message: %v", err)
	}
	humanDone := make(chan error, 1)
	go func() {
		_, _, perr := al.processMessage(context.Background(), msg)
		humanDone <- perr
	}()
	r1AwaitProvider(t, provider, 1)
	rec := rootReopenedRecord(t, al, res.SessionID)
	if rec.SteeredBy != nil || al.activeTurnForCancel(rec.SessionID, CancelScope{SessionID: rec.SessionID, TurnOnly: true}) == nil {
		t.Fatalf("SETUP: not a live ordinary chat turn: %+v", rec)
	}
	return al, rec, provider, humanDone
}

// stopRedirectAwaitIdle waits until the chat has no live turn and every
// dispatched turn has joined — without closing the loop's intake.
func stopRedirectAwaitIdle(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	idle := func() bool {
		if al.activeTurnForCancel(sessionID, CancelScope{SessionID: sessionID, TurnOnly: true}) != nil {
			return false
		}
		rec, err := al.GetSessionLifecycleStore().Load(sessionID)
		return err == nil && !lifecycleInFlightStopFence(rec)
	}
	for !idle() {
		if time.Now().After(deadline) {
			t.Fatal("the chat's turn did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	joinGoalFixtureRuns(t, al)
}

func stopRedirectUserCopies(messages []providers.Message, text string) int {
	n := 0
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, text) {
			n++
		}
	}
	return n
}

func stopRedirectTranscriptCopies(t *testing.T, al *AgentLoop, sessionID, text string) int {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("read the chat's transcript: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.Role == "user" && e.Content == text {
			n++
		}
	}
	return n
}

func TestStopRedirectRoot_StopsCurrentTurnThenRunsExactlyOneTurnWithInstruction(t *testing.T) {
	al, rec, provider, humanDone := newStopRedirectRoot(t)

	if err := al.RedirectSessionTurn(context.Background(), rec.SessionID, stopRedirectRootInstruction, "root-owner", "webchat"); err != nil {
		t.Fatalf("/stop-redirect on an ordinary chat must be accepted, got %v", err)
	}
	// The current turn is stopped (its held provider call is cancelled) and
	// the chat continues with exactly one new turn carrying the instruction.
	select {
	case <-humanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the person's live turn did not end after /stop-redirect")
	}
	r1AwaitProvider(t, provider, 2)
	requests := provider.Requests()
	if got := stopRedirectUserCopies(requests[2], stopRedirectRootInstruction); got != 1 {
		t.Errorf("new turn's model input carries the instruction %d times, want exactly 1", got)
	}
	if got := stopRedirectUserCopies(requests[2], stopRedirectRootHumanTask); got != 1 {
		t.Errorf("new turn's model input carries the chat's earlier message %d times, want it once as history", got)
	}
	if last := requests[2][len(requests[2])-1]; last.Role != "user" || last.Content != stopRedirectRootInstruction {
		t.Errorf("new turn's latest input = %s %q, want the user's exact instruction %q", last.Role, last.Content, stopRedirectRootInstruction)
	}
	provider.open(2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("the redirected turn did not finish")
	}
	joinGoalFixtureRuns(t, al)
	if calls := len(provider.Requests()); calls != 3 {
		t.Errorf("provider calls = %d, want the opening turn, the stopped turn and exactly one redirected turn", calls)
	}
	if got := stopRedirectTranscriptCopies(t, al, rec.SessionID, stopRedirectRootInstruction); got != 1 {
		t.Errorf("chat transcript holds the instruction as a user message %d times, want exactly 1", got)
	}
	after := rootReopenedRecord(t, al, rec.SessionID)
	if after.State == session.LifecycleStopped || after.SteeredBy != nil {
		t.Errorf("chat after redirect = %s (steered=%v), want the same ordinary chat continued, not left stopped", after.State, after.SteeredBy != nil)
	}
}

func TestStopRedirectRoot_IdleChatRunsExactlyOneTurnWithInstruction(t *testing.T) {
	al, rec, provider, humanDone := newStopRedirectRoot(t)
	// Let the person's turn finish: nothing is running when the redirect arrives.
	provider.open(1)
	select {
	case err := <-humanDone:
		if err != nil {
			t.Fatalf("SETUP: the person's turn failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the person's turn did not finish")
	}
	stopRedirectAwaitIdle(t, al, rec.SessionID)
	if err := al.RedirectSessionTurn(context.Background(), rec.SessionID, stopRedirectRootInstruction, "root-owner", "webchat"); err != nil {
		t.Fatalf("/stop-redirect on an idle ordinary chat must be accepted, got %v", err)
	}
	r1AwaitProvider(t, provider, 2)
	requests := provider.Requests()
	if got := stopRedirectUserCopies(requests[2], stopRedirectRootInstruction); got != 1 {
		t.Errorf("new turn's model input carries the instruction %d times, want exactly 1", got)
	}
	if last := requests[2][len(requests[2])-1]; last.Role != "user" || last.Content != stopRedirectRootInstruction {
		t.Errorf("new turn's latest input = %s %q, want the user's exact instruction %q", last.Role, last.Content, stopRedirectRootInstruction)
	}
	provider.open(2)
	stopRedirectAwaitIdle(t, al, rec.SessionID)
	if calls := len(provider.Requests()); calls != 3 {
		t.Errorf("provider calls = %d, want the opening turn, the person's turn and exactly one redirected turn", calls)
	}
	if got := stopRedirectTranscriptCopies(t, al, rec.SessionID, stopRedirectRootInstruction); got != 1 {
		t.Errorf("chat transcript holds the instruction as a user message %d times, want exactly 1", got)
	}
}
