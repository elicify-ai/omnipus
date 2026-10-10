package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Session-core U8 (C-ADDRESS / C-REPLY, FR-027/028/045; BDD-08.1, 08.2, 08.9).
// Oracles are spec phrases: a refused peer/reply admits and sends nothing; a
// valid reply goes to the original source authored by the responder; the
// request id is the admitted message id; no new permission map.

const (
	addrWS       = "ws-1"
	addrReceiver = testDefaultAgentID // "mia": the one registered agent
)

type fakeAddressDeps struct {
	mu        sync.Mutex
	eligible  map[string]bool
	err       error
	published []session.TranscriptEntry
}

func (f *fakeAddressDeps) PairEligible(ws, agentID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.eligible[ws+"/"+agentID], nil
}

func (f *fakeAddressDeps) PublishGuestReply(_ string, e session.TranscriptEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, e)
}

func setSendMessagePolicy(t *testing.T, al *AgentLoop, agentID string, p config.ToolPolicy) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok {
		t.Fatalf("agent %q not registered", agentID)
	}
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": p}})
}

type addrFixture struct {
	al   *AgentLoop
	bus  *bus.MessageBus
	deps *fakeAddressDeps
	r    AddressRouter
}

func newAddrFixture(t *testing.T) *addrFixture {
	t.Helper()
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	t.Cleanup(cleanup)
	deps := &fakeAddressDeps{eligible: map[string]bool{addrWS + "/" + addrReceiver: true}}
	al.SetAddressDeps(deps)
	setSendMessagePolicy(t, al, addrReceiver, config.ToolPolicyAllow)
	return &addrFixture{al: al, bus: msgBus, deps: deps, r: al.NewAddressRouter()}
}

func (f *addrFixture) peerReq() tools.PeerRequest {
	return tools.PeerRequest{
		RecipientWorkspaceID: addrWS, RecipientAgentID: addrReceiver, Content: "please review X",
		Sender:          tools.SendOrigin{WorkspaceID: addrWS, AgentID: "ray"},
		SenderSessionID: "sender-session",
	}
}

func addrDrainInbound(t *testing.T, b *bus.MessageBus) (bus.InboundMessage, bool) {
	t.Helper()
	select {
	case m := <-b.InboundChan():
		return m, true
	case <-time.After(300 * time.Millisecond):
		return bus.InboundMessage{}, false
	}
}

func addrDrainOutbound(b *bus.MessageBus) (bus.OutboundMessage, bool) {
	select {
	case m := <-b.OutboundChan():
		return m, true
	case <-time.After(300 * time.Millisecond):
		return bus.OutboundMessage{}, false
	}
}

func TestSendPeer_AdmitsToEligibleMainWithCaptureAndEnvelope(t *testing.T) {
	f := newAddrFixture(t)
	got, err := f.r.SendPeer(context.Background(), f.peerReq())
	if err != nil {
		t.Fatalf("SendPeer: %v", err)
	}
	wantMain, _ := session.MainSessionID(addrWS, addrReceiver)
	if got.SessionID != wantMain || got.RequestID == "" {
		t.Fatalf("receipt = %+v, want session %q and a request id", got, wantMain)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(wantMain)
	if err != nil || len(entries) != 1 {
		t.Fatalf("receiver transcript = %v, %v; want exactly the request", entries, err)
	}
	e := entries[0]
	if e.ID != got.RequestID {
		t.Fatalf("request id %q is not the admitted entry id %q", got.RequestID, e.ID)
	}
	if !strings.Contains(e.Content, `reply_to="`+got.RequestID+`"`) || !strings.Contains(e.Content, "please review X") {
		t.Fatalf("envelope must carry the id to reply to and the body; got %q", e.Content)
	}
	c, err := f.al.RequestLedger().Resolve(wantMain, addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, got.RequestID)
	if err != nil {
		t.Fatalf("capture not answerable: %v", err)
	}
	if c.Source.Kind != addressing.SourceConversation || c.Source.SessionID != "sender-session" ||
		c.Source.Owner.AgentID != "ray" || c.Sender.Agent.AgentID != "ray" {
		t.Fatalf("capture = %+v", c)
	}
	in, ok := addrDrainInbound(t, f.bus)
	if !ok {
		t.Fatal("no inbound published: the receiver's turn was never started")
	}
	if in.SessionID != wantMain || in.TranscriptEntryID != got.RequestID || in.UserInitiated || in.OperatorPrompt ||
		in.Metadata["agent_id"] != addrReceiver {
		t.Fatalf("inbound = %+v", in)
	}
}

func TestSendPeer_RefusalsAdmitNothing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*addrFixture, *tools.PeerRequest)
	}{
		{"not an eligible main", func(f *addrFixture, _ *tools.PeerRequest) { f.deps.eligible = map[string]bool{} }},
		{"membership unreadable", func(f *addrFixture, _ *tools.PeerRequest) { f.deps.err = errors.New("disk") }},
		{"receiver policy deny", func(f *addrFixture, _ *tools.PeerRequest) {
			setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyDeny)
		}},
		{"receiver policy ask, not approved", func(f *addrFixture, _ *tools.PeerRequest) {
			setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
		}},
		{"to itself", func(_ *addrFixture, r *tools.PeerRequest) { r.Sender.AgentID = addrReceiver }},
		{"sender has no session", func(_ *addrFixture, r *tools.PeerRequest) { r.SenderSessionID = "" }},
		{"bad pair", func(_ *addrFixture, r *tools.PeerRequest) { r.RecipientAgentID = " " }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAddrFixture(t)
			req := f.peerReq()
			tt.setup(f, &req)
			if _, err := f.r.SendPeer(context.Background(), req); err == nil {
				t.Fatal("want a refusal")
			}
			if _, ok := addrDrainInbound(t, f.bus); ok {
				t.Fatal("a refused request started a turn")
			}
			mainID, _ := session.MainSessionID(addrWS, addrReceiver)
			if _, err := f.al.GetSessionStore().GetMeta(mainID); err == nil {
				t.Fatal("a refused request created the receiver's main session")
			}
		})
	}
}

// Policy comes from the RECEIVER, not the sender: a sender that could not call
// send_message itself is irrelevant; a global deny with an agent allow still
// resolves through the existing strictest-wins resolver (no new map).
func TestSendPeer_ReceiverGlobalDenyWinsOverAgentAllow(t *testing.T) {
	f := newAddrFixture(t)
	inst, _ := f.al.GetRegistry().GetAgent(addrReceiver)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow},
		GlobalPolicies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyDeny},
	})
	if _, err := f.r.SendPeer(context.Background(), f.peerReq()); err == nil {
		t.Fatal("global deny must refuse even with an agent allow")
	}
}

func putConnectorCapture(t *testing.T, f *addrFixture, id string) (sessionID string) {
	t.Helper()
	meta, err := f.al.GetSessionStore().GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	c := addressing.Capture{
		RequestID: id, ReceiverSessionID: meta.ID,
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver},
		Sender:   addressing.Sender{CanonicalID: "telegram:42"},
		Source: addressing.Source{
			Kind: addressing.SourceConnector, Owner: addressing.Pair{WorkspaceID: addrWS, AgentID: "ann"},
			SessionID: "ann-main", InstanceID: "telegram.a", ChatID: "chat-1", PlatformMessageID: "m9",
		},
		AdmittedAt: time.Now().UTC(),
	}
	if err := f.al.RequestLedger().Put(c); err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

func (f *addrFixture) replyReq(sessionID, replyTo string) tools.ReplyRequest {
	return tools.ReplyRequest{
		ReplyTo: replyTo, Content: "the answer",
		Author:             tools.SendOrigin{WorkspaceID: addrWS, AgentID: addrReceiver},
		ResponderSessionID: sessionID,
	}
}

func TestReply_ConnectorSourceReturnsThroughSourceOwnerAuthoredByGuest(t *testing.T) {
	f := newAddrFixture(t)
	sid := putConnectorCapture(t, f, "q1")
	if _, err := f.r.Reply(context.Background(), f.replyReq(sid, "q1")); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	out, ok := addrDrainOutbound(f.bus)
	if !ok {
		t.Fatal("nothing queued")
	}
	if out.Channel != "telegram.a" || out.ChatID != "chat-1" || out.ReplyToMessageID != "m9" || out.Content != "the answer" {
		t.Fatalf("destination not taken from the capture: %+v", out)
	}
	if out.AgentID != addrReceiver || out.OwnershipChecked {
		t.Fatalf("author must be the guest and must not claim an ownership decision: %+v", out)
	}
	if out.Return == nil || out.Return.OwnerAgentID != "ann" || out.Return.OwnerWorkspaceID != addrWS || out.Return.RequestID != "q1" {
		t.Fatalf("return route must carry the SOURCE OWNER captured at admission: %+v", out.Return)
	}
}

func TestReply_RefusesForgedForeignAndDiscardedWithZeroSends(t *testing.T) {
	f := newAddrFixture(t)
	sid := putConnectorCapture(t, f, "q1")
	otherSession, _ := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", addrReceiver)

	cases := map[string]tools.ReplyRequest{
		"forged id":                f.replyReq(sid, "not-a-request"),
		"another session":          f.replyReq(otherSession.ID, "q1"),
		"wrong responder":          {ReplyTo: "q1", Content: "x", Author: tools.SendOrigin{WorkspaceID: addrWS, AgentID: "ray"}, ResponderSessionID: sid},
		"responder without ids":    {ReplyTo: "q1", Content: "x", ResponderSessionID: sid},
		"responder no session":     {ReplyTo: "q1", Content: "x", Author: tools.SendOrigin{WorkspaceID: addrWS, AgentID: addrReceiver}},
		"other workspace, same id": {ReplyTo: "q1", Content: "x", Author: tools.SendOrigin{WorkspaceID: "ws-2", AgentID: addrReceiver}, ResponderSessionID: sid},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := f.r.Reply(context.Background(), req); err == nil {
				t.Fatal("want a refusal")
			}
			if _, ok := addrDrainOutbound(f.bus); ok {
				t.Fatal("a refused reply was queued")
			}
		})
	}

	t.Run("discarded", func(t *testing.T) {
		if err := f.al.RequestLedger().Discard(sid, "q1"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.r.Reply(context.Background(), f.replyReq(sid, "q1")); err == nil {
			t.Fatal("a discarded request must refuse")
		}
		if _, ok := addrDrainOutbound(f.bus); ok {
			t.Fatal("a reply to a discarded request was queued")
		}
	})
}

func conversationSetup(t *testing.T, f *addrFixture, senderAgent addressing.Pair) (receiverSession, sourceSession string) {
	t.Helper()
	store := f.al.GetSessionStore()
	src, err := store.NewSession(session.SessionTypeChat, "webchat", "ann")
	if err != nil {
		t.Fatal(err)
	}
	recv, err := store.GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	c := addressing.Capture{
		RequestID: "q7", ReceiverSessionID: recv.ID,
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver},
		Sender:   addressing.Sender{Principal: "alice", Agent: senderAgent},
		Source: addressing.Source{
			Kind: addressing.SourceConversation, Owner: addressing.Pair{WorkspaceID: addrWS, AgentID: "ann"}, SessionID: src.ID,
		},
		AdmittedAt: time.Now().UTC(),
	}
	if err := f.al.RequestLedger().Put(c); err != nil {
		t.Fatal(err)
	}
	return recv.ID, src.ID
}

func TestReply_ConversationSourceAppendsGuestAuthoredEntryAndPublishesLive(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	rec, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7"))
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(src)
	if err != nil || len(entries) != 1 {
		t.Fatalf("source transcript = %v, %v", entries, err)
	}
	e := entries[0]
	if e.AgentID != addrReceiver || e.Role != "assistant" || e.ReplyToMessageID != "q7" || e.Content != "the answer" || e.ID != rec.ReplyID {
		t.Fatalf("guest entry wrong: %+v", e)
	}
	meta, _ := f.al.GetSessionStore().GetMeta(src)
	if meta.AgentID != "ann" {
		t.Fatalf("source owner changed to %q; the guest must not take the chat over", meta.AgentID)
	}
	if len(f.deps.published) != 1 || f.deps.published[0].ID != e.ID {
		t.Fatalf("live publish = %+v", f.deps.published)
	}
	if _, woke := addrDrainInbound(t, f.bus); woke {
		t.Fatal("a human-origin request must not wake the owner agent's turn")
	}
}

func TestReply_ConversationFromPeerAgentWakesTheSenderOnce(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{WorkspaceID: addrWS, AgentID: "ann"})
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err != nil {
		t.Fatal(err)
	}
	in, ok := addrDrainInbound(t, f.bus)
	if !ok || in.SessionID != src || in.Metadata["agent_id"] != "ann" || in.UserInitiated {
		t.Fatalf("wake = %+v ok=%v", in, ok)
	}
	if _, again := addrDrainInbound(t, f.bus); again {
		t.Fatal("more than one wake")
	}
}

func TestReply_ConversationSourceGoneOrTakenOverRefuses(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	if err := f.al.GetSessionStore().DeleteSession(src); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv, "q7")); err == nil {
		t.Fatal("a deleted source conversation must refuse")
	}
	if len(f.deps.published) != 0 {
		t.Fatal("published to a deleted conversation")
	}
}

// BDD-08.3 / BDD-08.4 / T10: one main consumes a web input and two same-platform
// inputs (distinct principals, chats, threads, request ids). Each reply goes to
// exactly its own original destination; nothing is broadcast; a capture with no
// usable correlation refuses with zero sends.
func TestReply_MixedSourcesAddressEachOriginalSenderOnly(t *testing.T) {
	f := newAddrFixture(t)
	store := f.al.GetSessionStore()
	recv, err := store.GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	webSrc, err := store.NewSession(session.SessionTypeChat, "webchat", "ann")
	if err != nil {
		t.Fatal(err)
	}
	owner := addressing.Pair{WorkspaceID: addrWS, AgentID: "ann"}
	put := func(id string, src addressing.Source, sender addressing.Sender) {
		t.Helper()
		if err := f.al.RequestLedger().Put(addressing.Capture{
			RequestID: id, ReceiverSessionID: recv.ID,
			Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver},
			Sender:   sender, Source: src, AdmittedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("u-web", addressing.Source{Kind: addressing.SourceConversation, Owner: owner, SessionID: webSrc.ID},
		addressing.Sender{Principal: "alice"})
	put("i1", addressing.Source{Kind: addressing.SourceConnector, Owner: owner, SessionID: "ann-main",
		InstanceID: "telegram.a", ChatID: "chat-1", PlatformMessageID: "m1"}, addressing.Sender{CanonicalID: "telegram:1"})
	put("i2", addressing.Source{Kind: addressing.SourceConnector, Owner: owner, SessionID: "ann-main",
		InstanceID: "telegram.a", ChatID: "chat-2", PlatformMessageID: "m2"}, addressing.Sender{CanonicalID: "telegram:2"})

	reply := func(id, text string) {
		t.Helper()
		req := f.replyReq(recv.ID, id)
		req.Content = text
		if _, err := f.r.Reply(context.Background(), req); err != nil {
			t.Fatalf("reply to %s: %v", id, err)
		}
	}

	reply("i1", "to one")
	out, ok := addrDrainOutbound(f.bus)
	if !ok || out.ChatID != "chat-1" || out.ReplyToMessageID != "m1" || out.Content != "to one" {
		t.Fatalf("reply to i1 = %+v ok=%v", out, ok)
	}
	if extra, more := addrDrainOutbound(f.bus); more {
		t.Fatalf("a reply to i1 also produced another send (broadcast): %+v", extra)
	}

	reply("i2", "to two")
	out, ok = addrDrainOutbound(f.bus)
	if !ok || out.ChatID != "chat-2" || out.ReplyToMessageID != "m2" || out.Content != "to two" {
		t.Fatalf("reply to i2 = %+v ok=%v", out, ok)
	}

	reply("u-web", "to web")
	if _, sent := addrDrainOutbound(f.bus); sent {
		t.Fatal("a reply to the web input must not touch any connector")
	}
	entries, err := store.ReadTranscript(webSrc.ID)
	if err != nil || len(entries) != 1 || entries[0].ReplyToMessageID != "u-web" || entries[0].Content != "to web" {
		t.Fatalf("web source transcript = %v, %v", entries, err)
	}

	// A capture whose return correlation is unusable: refuse, send nothing.
	bad := addressing.Source{Kind: addressing.SourceConnector, Owner: owner, SessionID: "ann-main", InstanceID: "telegram.a"} // no chat
	raw := addressing.Capture{RequestID: "bad", ReceiverSessionID: recv.ID,
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Source: bad}
	if err := f.al.RequestLedger().Put(raw); err == nil {
		t.Fatal("a capture with no chat must not be admitted to the ledger at all")
	}
	if _, err := f.r.Reply(context.Background(), f.replyReq(recv.ID, "bad")); err == nil {
		t.Fatal("reply to a request with no usable correlation must refuse")
	}
	if _, sent := addrDrainOutbound(f.bus); sent {
		t.Fatal("a reply with no usable correlation fell back to a send")
	}
}
