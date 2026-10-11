package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder ruling 2026-10-11 (R1 #5): a request for a STOPPED agent is held and
// delivered when that agent runs again; the asker is told it is waiting. The
// durable ledger is the store (a delivered mark, no second queue). A held
// request whose asker is gone by delivery time is dropped visibly, never
// delivered into a void.

// heldSetup builds a stopped receiver main and a real asker conversation owned
// by agent "ray".
func heldSetup(t *testing.T) (f *addrFixture, recv, asker string) {
	t.Helper()
	f = newAddrFixture(t)
	lc := wireLifecycle(t, f)
	store := f.al.GetSessionStore()
	r, err := store.GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	src, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	ws := addrWS
	if err := store.SetMeta(src.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatal(err)
	}
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: r.ID, Generation: 2, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: addrWS, AgentID: addrReceiver,
		Stop: &session.Stop{At: time.Now(), Generation: 2, By: session.Principal{Kind: session.PrincipalKindHuman}},
	}); err != nil {
		t.Fatal(err)
	}
	return f, r.ID, src.ID
}

func heldAsk(t *testing.T, f *addrFixture, asker string) (reqID string, held bool) {
	t.Helper()
	req := f.peerReq()
	req.SenderSessionID = asker
	got, err := f.r.SendPeer(context.Background(), req)
	if err != nil {
		t.Fatalf("SendPeer: %v", err)
	}
	return got.RequestID, got.Held
}

// forgetInMemory simulates a gateway restart: only the ledger survives.
func forgetInMemory(al *AgentLoop) {
	al.pendingRequests.Range(func(k, _ any) bool { al.pendingRequests.Delete(k); return true })
	al.deliveredRequests.Range(func(k, _ any) bool { al.deliveredRequests.Delete(k); return true })
	al.remindedRequests.Range(func(k, _ any) bool { al.remindedRequests.Delete(k); return true })
}

func TestHeldRequest_AskingAStoppedAgent_IsHeldAndTheAskerIsTold(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, held := heldAsk(t, f, asker)
	if !held {
		t.Fatal("receipt.Held = false: the asker is not told its request is waiting")
	}
	pending, err := f.al.RequestLedger().Undelivered(recv)
	if err != nil || len(pending) != 1 || pending[0].RequestID != id {
		t.Fatalf("held = %+v err=%v, want exactly request %s kept in the ledger", pending, err, id)
	}
	entries, _ := f.al.GetSessionStore().ReadTranscript(recv)
	if len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("receiver transcript = %v, want the request kept", entries)
	}
}

func TestHeldRequest_AskingARunnableAgent_IsNotHeld(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	if _, held := heldAsk(t, f, "sender-session"); held {
		t.Fatal("a request to an agent that is not stopped was reported as held")
	}
}

// "Runs again" = the next admitted turn on the stopped agent's session. The
// in-memory maps are wiped first: the ledger alone must carry the request across
// a restart.
func TestHeldRequest_DeliveredWhenTheAgentRunsAgain_AcrossARestart(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, _ := heldAsk(t, f, asker)
	forgetInMemory(f.al)

	f.al.deliverHeldRequests(recv)

	if left, _ := f.al.RequestLedger().Undelivered(recv); len(left) != 0 {
		t.Fatalf("still held after the agent ran again: %+v", left)
	}
	c, ok, err := f.al.RequestLedger().Get(recv, id)
	if err != nil || !ok || !c.Delivered || c.Discarded {
		t.Fatalf("capture = %+v ok=%v err=%v, want delivered and answerable", c, ok, err)
	}
	// Reminder tracking is back: an unanswered delivered request gets its one nudge.
	if got := f.al.takeUnansweredRequests(recv); len(got) != 1 || got[0] != id {
		t.Fatalf("unanswered requests = %v, want [%s] tracked again after delivery", got, id)
	}
	// Delivery happens once.
	f.al.deliverHeldRequests(recv)
	if got := f.al.takeUnansweredRequests(recv); len(got) != 0 {
		t.Fatalf("second delivery re-tracked the request: %v", got)
	}
}

func TestHeldRequest_AskerGone_DroppedNotDeliveredIntoAVoid(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, _ := heldAsk(t, f, asker)
	forgetInMemory(f.al)
	if err := f.al.GetSessionStore().DeleteSession(asker); err != nil {
		t.Fatalf("SETUP: delete the asker's chat: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("SETUP: enable file logging: %v", err)
	}
	restoreConsole := logger.DisableConsole()
	f.al.deliverHeldRequests(recv)
	restoreConsole()
	logger.DisableFileLogging()
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "held request dropped, not delivered") || !strings.Contains(string(logged), id) {
		t.Fatalf("the drop must be visible in the log with the request id; log = %q", logged)
	}

	if left, _ := f.al.RequestLedger().Undelivered(recv); len(left) != 0 {
		t.Fatalf("a request with no asker is still held: %+v", left)
	}
	_, err = f.al.RequestLedger().Resolve(recv, addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, id)
	if !errors.Is(err, addressing.ErrDiscarded) {
		t.Fatalf("Resolve = %v, want ErrDiscarded: the request must no longer be answerable", err)
	}
	if got := f.al.takeUnansweredRequests(recv); len(got) != 0 {
		t.Fatalf("a dropped request is tracked for a reminder: %v", got)
	}
	if _, delivered := f.al.deliveredRequests.Load(id); delivered {
		t.Fatal("a dropped request was marked delivered")
	}
}

// Wiring: a real resume of a stopped session delivers its held requests, and the
// model sees the request in the resumed turn.
func TestHeldRequest_HumanResumeOfTheStoppedAgentDeliversIt(t *testing.T) {
	al, original, provider := r5AdmitRoot(t)
	r5StopRoot(t, al, original)
	provider.open(0)
	joinGoalFixtureRuns(t, al)
	asker, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	ws := addrWS
	if err := al.GetSessionStore().SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatal(err)
	}
	c := addressing.Capture{
		RequestID: "held-1", ReceiverSessionID: original.SessionID,
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: testDefaultAgentID},
		Source: addressing.Source{
			Kind: addressing.SourceConversation, Owner: addressing.Pair{WorkspaceID: addrWS, AgentID: "ray"}, SessionID: asker.ID,
		},
		AdmittedAt: time.Now().UTC(),
	}
	if err := al.RequestLedger().Put(c); err != nil {
		t.Fatalf("SETUP: hold a request: %v", err)
	}

	msg := rootHumanMessage(original.SessionID)
	msg.GatewayUserID = "r5-owner"
	if err := al.enqueueSteeringFromMessage(msg); err != nil {
		t.Fatalf("resume: %v", err)
	}
	r1AwaitProvider(t, provider, 1)

	got, ok, err := al.RequestLedger().Get(original.SessionID, "held-1")
	if err != nil || !ok || !got.Delivered {
		t.Fatalf("held request after the resume = %+v ok=%v err=%v, want delivered", got, ok, err)
	}
	provider.open(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	al.WaitForActiveRequestsContext(ctx)
}
