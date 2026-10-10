package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// U8 open point (b) - FR-027 / ADR D7: an answer wakes the eligible idle OWNER of
// the source conversation once, whoever sent the request; a stopped, hidden or
// unreadable owner gets no wake; the answer is retained in the transcript either way.

func wireLifecycle(t *testing.T, f *addrFixture) *session.LifecycleStore {
	t.Helper()
	lc := session.NewLifecycleStore(t.TempDir())
	f.al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
	return lc
}

func answerRetained(t *testing.T, f *addrFixture, src string) {
	t.Helper()
	entries, err := f.al.GetSessionStore().ReadTranscript(src)
	if err != nil || len(entries) != 1 || entries[0].Content != "the answer" {
		t.Fatalf("the answer must be retained in the source transcript: %v, %v", entries, err)
	}
}

func TestReply_HumanRequest_WakesIdleSourceOwnerOnce(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{}) // human sender: no agent
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err != nil {
		t.Fatal(err)
	}
	in, ok := addrDrainInbound(t, f.bus)
	if !ok || in.SessionID != src || in.Metadata["agent_id"] != "ann" || in.Metadata["workspace_id"] != addrWS ||
		in.UserInitiated || in.OperatorPrompt || in.Channel != "webchat" || in.Metadata[ownerWakeMetadataKey] == "" {
		t.Fatalf("wake = %+v ok=%v, want one non-user-initiated inbound to the owner ann in %s", in, ok, addrWS)
	}
	if _, again := addrDrainInbound(t, f.bus); again {
		t.Fatal("more than one wake for one answer")
	}
}

// Positive control + mutation guard: the wake targets the conversation's owner
// (meta.AgentID), never the request's sender field.
func TestReply_PeerRequest_WakesSourceOwnerNotSenderField(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{WorkspaceID: addrWS, AgentID: "ray"}) // sender ray, owner ann
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err != nil {
		t.Fatal(err)
	}
	in, ok := addrDrainInbound(t, f.bus)
	if !ok || in.SessionID != src || in.Metadata["agent_id"] != "ann" {
		t.Fatalf("wake = %+v ok=%v, want the OWNER ann (not the sender ray)", in, ok)
	}
}

func TestReply_StoppedOwner_NoWake_AnswerRetained(t *testing.T) {
	f := newAddrFixture(t)
	lc := wireLifecycle(t, f)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: src, Generation: 2, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: addrWS, AgentID: "ann",
		Stop: &session.Stop{At: time.Now(), Generation: 2, By: session.Principal{Kind: session.PrincipalKindHuman}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err != nil {
		t.Fatal(err)
	}
	if in, woke := addrDrainInbound(t, f.bus); woke {
		t.Fatalf("a stopped owner must not be woken: %+v", in)
	}
	answerRetained(t, f, src)
}

func TestReply_HiddenMainOwner_NoWake(t *testing.T) {
	f := newAddrFixture(t)
	recv, _ := conversationSetup(t, f, addressing.Pair{})
	// The source is the owner's MAIN, and that main is no longer eligible (FR-003).
	ownerMain, err := f.al.GetSessionStore().GetOrCreateMainSession(addrWS, "ann")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.al.RequestLedger().Resolve(recv, addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, "q7")
	if err != nil {
		t.Fatal(err)
	}
	c.Source.SessionID = ownerMain.ID
	c.RequestID = "q8"
	if err := f.al.RequestLedger().Put(c); err != nil {
		t.Fatal(err)
	}
	f.deps.eligible[addrWS+"/ann"] = false
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q8")); err != nil {
		t.Fatal(err)
	}
	if in, woke := addrDrainInbound(t, f.bus); woke {
		t.Fatalf("a hidden main's owner must not be woken: %+v", in)
	}
	answerRetained(t, f, ownerMain.ID)
}

func TestReply_UnreadableLifecycle_NoWake(t *testing.T) {
	f := newAddrFixture(t)
	lc := wireLifecycle(t, f)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: src, Generation: 1, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: addrWS, AgentID: "ann",
	}); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(lc.Dir(), src+".jsonl")
	if err := os.Chmod(journal, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(journal, 0o600) })
	if fh, err := os.Open(journal); err == nil {
		fh.Close()
		t.Fatal("instrument check: the journal must really be unreadable (not root)")
	}
	before := wakeSkippedUnreadable.Load()
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err != nil {
		t.Fatal(err)
	}
	if in, woke := addrDrainInbound(t, f.bus); woke {
		t.Fatalf("an unreadable lifecycle record must never be guessed idle: %+v", in)
	}
	if wakeSkippedUnreadable.Load() != before+1 {
		t.Fatal("the skipped wake must be counted")
	}
	answerRetained(t, f, src)
}

// F3: a source chat moved to another workspace keeping the agent id is no
// longer the conversation the request came from.
func TestReply_SourceMovedToAnotherWorkspaceSameAgentRefuses(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	other := "ws-elsewhere"
	if err := f.al.GetSessionStore().SetMeta(src, session.MetaPatch{WorkspaceID: &other}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err == nil {
		t.Fatal("a reply into a chat that moved to another workspace must refuse")
	}
	if entries, _ := f.al.GetSessionStore().ReadTranscript(src); len(entries) != 0 {
		t.Fatalf("nothing may be appended to the moved chat: %v", entries)
	}
	if len(f.deps.published) != 0 {
		t.Fatal("nothing may be published")
	}
	if _, woke := addrDrainInbound(t, f.bus); woke {
		t.Fatal("no wake after a refusal")
	}
}

// T18: a silent owner (no text to give) leaves no empty bubble: nothing is
// published outbound and no assistant entry is written.
func TestReply_GuestAnswer_OwnerSilence_NoEmptyBubble(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err != nil {
		t.Fatal(err)
	}
	wake, ok := addrDrainInbound(t, f.bus)
	if !ok {
		t.Fatal("no wake to drive")
	}
	// The owner's turn ends with no text.
	f.al.publishResponseIfNeeded(context.Background(), nil, wake.Channel, wake.ChatID, "", wake.SessionID)
	if out, got := addrDrainOutbound(f.bus); got {
		t.Fatalf("a silent owner must publish nothing, got %+v", out)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Role == "assistant" && e.AgentID != addrReceiver {
			t.Fatalf("an empty owner bubble was written: %+v", e)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("only the guest's answer may be in the source transcript, got %d entries", len(entries))
	}
}

var _ = bus.InboundMessage{}
