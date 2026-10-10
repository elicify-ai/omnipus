package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Session-core U8 tool half beyond the RED pack: oracles are C-REPLY / C-ADDRESS
// ("reply form excludes channel/chat/recipient destination"; "invalid pair
// refuses"; current workspace is the default recipient workspace) and ADR-091
// boundary 8 (a delegated session messages only its own conversation).

type fakePeer struct {
	calls []PeerRequest
	err   error
}

func (f *fakePeer) SendPeer(_ context.Context, r PeerRequest) (PeerReceipt, error) {
	f.calls = append(f.calls, r)
	return PeerReceipt{RequestID: "req-1", SessionID: "main"}, f.err
}

type fakeReply struct {
	calls []ReplyRequest
	err   error
}

func (f *fakeReply) Reply(_ context.Context, r ReplyRequest) (ReplyReceipt, error) {
	f.calls = append(f.calls, r)
	return ReplyReceipt{Destination: "the original conversation"}, f.err
}

func addrTool() (*MessageTool, *fakePeer, *fakeReply, *bool) {
	tool := NewMessageTool()
	peer, reply := &fakePeer{}, &fakeReply{}
	tool.SetPeerRouter(peer)
	tool.SetReplyRouter(reply)
	ordinarySent := false
	tool.SetSendCallback(func(_, _, _ string, _ SendOrigin) error { ordinarySent = true; return nil })
	return tool, peer, reply, &ordinarySent
}

func TestReplyForm_RoutesWithActingAgentFromContextAndTrimmedID(t *testing.T) {
	tool, _, reply, sent := addrTool()
	ctx := WithTranscriptSessionID(u8TurnCtx(), "jim-main")
	res := tool.Execute(ctx, map[string]any{"content": "done", "reply_to": "  q1  "})
	if res.IsError {
		t.Fatalf("unexpected refusal: %s", res.ForLLM)
	}
	if len(reply.calls) != 1 {
		t.Fatalf("router calls = %d, want 1", len(reply.calls))
	}
	got := reply.calls[0]
	if got.ReplyTo != "q1" || got.Content != "done" || got.Author.AgentID != "jim" || got.Author.WorkspaceID != "ws-1" ||
		got.ResponderSessionID != "jim-main" {
		t.Fatalf("request = %+v", got)
	}
	if *sent {
		t.Fatal("a reply must not take the ordinary send path")
	}
	// N2 (architect U8-OPEN): an accepted reply IS this round's send, so the
	// turn's plain closing text is not delivered a second time.
	if !tool.HasSentInRound() {
		t.Fatal("an accepted reply must mark the round as sent")
	}
}

func TestReplyForm_ExcludesEveryDestinationArgument(t *testing.T) {
	for _, extra := range []map[string]any{
		{"channel": "webchat"}, {"chat_id": "chat-1"}, {"workspace_id": "ws-2"}, {"agent_id": "ray"},
		{"workspace_id": "ws-2", "agent_id": "ray"},
	} {
		tool, peer, reply, sent := addrTool()
		args := map[string]any{"content": "x", "reply_to": "q1"}
		for k, v := range extra {
			args[k] = v
		}
		res := tool.Execute(u8TurnCtx(), args)
		if !res.IsError || !errors.Is(res.Err, ErrReplyForm) {
			t.Errorf("extra %v: want ErrReplyForm refusal, got %+v", extra, res)
		}
		if len(reply.calls)+len(peer.calls) != 0 || *sent {
			t.Errorf("extra %v: something was sent", extra)
		}
	}
}

func TestReplyForm_RefusalsAndRouterErrorsSendNothingElse(t *testing.T) {
	tool, _, reply, sent := addrTool()
	if res := tool.Execute(u8TurnCtx(), map[string]any{"content": "  ", "reply_to": "q1"}); !res.IsError {
		t.Error("blank content must refuse")
	}
	reply.err = errors.New("no such request")
	res := tool.Execute(u8TurnCtx(), map[string]any{"content": "x", "reply_to": "q1"})
	if !res.IsError {
		t.Fatal("a router refusal must surface as a tool error")
	}
	if *sent {
		t.Fatal("a refused reply fell back to an ordinary send")
	}
	bare := NewMessageTool()
	if res := bare.Execute(u8TurnCtx(), map[string]any{"content": "x", "reply_to": "q1"}); !res.IsError {
		t.Error("an unwired reply router must refuse, not default")
	}
}

func TestPeerForm_DefaultsToCurrentWorkspaceAndValidatesPair(t *testing.T) {
	tool, peer, _, sent := addrTool()
	res := tool.Execute(WithTranscriptSessionID(u8TurnCtx(), "s-1"), map[string]any{"content": "hi", "agent_id": "ray"})
	if res.IsError {
		t.Fatalf("unexpected refusal: %s", res.ForLLM)
	}
	got := peer.calls[0]
	if got.RecipientWorkspaceID != "ws-1" || got.RecipientAgentID != "ray" || got.Sender.AgentID != "jim" || got.SenderSessionID != "s-1" {
		t.Fatalf("request = %+v", got)
	}
	if *sent || tool.HasSentInRound() {
		t.Fatal("a peer send must not use the ordinary path or suppress the turn's own reply")
	}

	for name, args := range map[string]map[string]any{
		"blank workspace":         {"content": "x", "workspace_id": " ", "agent_id": "ray"},
		"workspace without agent": {"content": "x", "workspace_id": "ws-2"},
		"plus in agent id":        {"content": "x", "workspace_id": "ws-2", "agent_id": "a+b"},
		"too long agent id":       {"content": "x", "workspace_id": "ws-2", "agent_id": string(make([]byte, 300))},
		"with a channel":          {"content": "x", "workspace_id": "ws-2", "agent_id": "ray", "channel": "telegram"},
		"non-string agent":        {"content": "x", "agent_id": 7},
		"blank content":           {"content": " ", "agent_id": "ray"},
	} {
		tool, peer, _, sent := addrTool()
		before := len(peer.calls)
		if res := tool.Execute(u8TurnCtx(), args); !res.IsError {
			t.Errorf("%s: want refusal", name)
		}
		if len(peer.calls) != before || *sent {
			t.Errorf("%s: something was sent", name)
		}
	}

	noWS := WithAgentID(WithToolContext(context.Background(), "webchat", "c"), "jim")
	tool2, peer2, _, _ := addrTool()
	if res := tool2.Execute(noWS, map[string]any{"content": "x", "agent_id": "ray"}); !res.IsError || len(peer2.calls) != 0 {
		t.Error("with no workspace in the turn and none named, the send must refuse")
	}
}

type fixedAudience struct{ a steer.Audience }

func (f fixedAudience) Audience(context.Context, string) (steer.Audience, steer.Class, error) {
	return f.a, steer.ClassUnreadable, nil
}

func TestAddressedForms_RefusedInDelegatedSessions(t *testing.T) {
	tool, peer, reply, _ := addrTool()
	tool.SetSteerAudienceResolver(fixedAudience{a: steer.AudienceSteeringSession})
	ctx := WithTranscriptSessionID(u8TurnCtx(), "child-1")
	for _, args := range []map[string]any{
		{"content": "x", "reply_to": "q1"},
		{"content": "x", "agent_id": "ray"},
	} {
		res := tool.Execute(ctx, args)
		if !res.IsError || !errors.Is(res.Err, ErrSteeredSessionOwnChatOnly) {
			t.Errorf("args %v: want the own-chat-only refusal, got %+v", args, res)
		}
	}
	if len(peer.calls)+len(reply.calls) != 0 {
		t.Fatal("a delegated session reached another conversation")
	}
	// An ordinary (user-facing) session is unaffected.
	tool.SetSteerAudienceResolver(fixedAudience{a: steer.AudienceUser})
	if res := tool.Execute(ctx, map[string]any{"content": "x", "agent_id": "ray"}); res.IsError {
		t.Errorf("ordinary session refused: %s", res.ForLLM)
	}
}

// N2: an accepted reply counts as the round's send, so a plain closing reply in
// the same (unbound, per-chat) turn is not sent a second time.
func TestReplyForm_AcceptedReplyMarksTheRoundSent(t *testing.T) {
	tool, _, reply, _ := addrTool()
	if tool.HasSentInRound() {
		t.Fatal("precondition")
	}
	if res := tool.Execute(u8TurnCtx(), map[string]any{"content": "x", "reply_to": "q1"}); res.IsError {
		t.Fatal(res.ForLLM)
	}
	if !tool.HasSentInRound() {
		t.Fatal("an accepted reply must mark the round as sent")
	}
	tool2, _, reply2, _ := addrTool()
	reply2.err = errors.New("refused")
	_ = reply
	tool2.Execute(u8TurnCtx(), map[string]any{"content": "x", "reply_to": "q1"})
	if tool2.HasSentInRound() {
		t.Fatal("a refused reply must not mark the round as sent")
	}
}

// Security review r1 F1: in a main, the ORDINARY send form must not reach a
// connector either - a connector is reached only by reply_to there.
func TestOrdinarySend_RefusedToAConnectorFromAMain(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"default destination":        {"content": "confidential"},
		"explicit channel":           {"content": "confidential", "channel": "telegram.a", "chat_id": "victim"},
		"channel only (turn's chat)": {"content": "confidential", "channel": "telegram.a"},
	} {
		tool := NewMessageTool()
		sent := false
		tool.SetSendCallback(func(_, _, _ string, _ SendOrigin) error { sent = true; return nil })
		tool.SetMainConnectorGuard(func(sessionID, channel string) bool { return sessionID == "main-1" && channel == "telegram.a" })
		ctx := WithTranscriptSessionID(WithToolContext(WithWorkspaceID(WithAgentID(context.Background(), "jim"), "ws-1"), "telegram.a", "chat-1"), "main-1")
		res := tool.Execute(ctx, args)
		if !res.IsError || !errors.Is(res.Err, ErrMainConnectorReplyOnly) {
			t.Errorf("%s: want ErrMainConnectorReplyOnly, got %+v", name, res)
		}
		if sent || tool.HasSentInRound() {
			t.Errorf("%s: the ordinary send reached the callback", name)
		}
	}
	// Controls: the same tool outside a main, to webchat from a main, and a
	// valid reply_to from a main all still work.
	tool, _, reply, sent := addrTool()
	tool.SetMainConnectorGuard(func(sessionID, channel string) bool { return sessionID == "main-1" && channel == "telegram.a" })
	other := WithTranscriptSessionID(WithToolContext(WithAgentID(context.Background(), "jim"), "telegram.a", "chat-1"), "chat-session")
	if res := tool.Execute(other, map[string]any{"content": "hi"}); res.IsError || !*sent {
		t.Errorf("a non-main session must keep its ordinary send: %+v", res)
	}
	mainCtx := WithTranscriptSessionID(WithToolContext(WithAgentID(context.Background(), "jim"), "telegram.a", "chat-1"), "main-1")
	if res := tool.Execute(mainCtx, map[string]any{"content": "x", "reply_to": "q1"}); res.IsError || len(reply.calls) != 1 {
		t.Errorf("a valid reply_to from a main must still work: %+v", res)
	}
	webMain := WithTranscriptSessionID(WithToolContext(WithAgentID(context.Background(), "jim"), "webchat", "w"), "main-1")
	*sent = false
	if res := tool.Execute(webMain, map[string]any{"content": "hi"}); res.IsError || !*sent {
		t.Errorf("a main's webchat send must keep working: %+v", res)
	}
}
