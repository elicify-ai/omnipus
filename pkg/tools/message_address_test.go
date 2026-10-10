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
	if tool.HasSentInRound() {
		t.Fatal("a reply to another conversation must not suppress the turn's own automatic reply")
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
