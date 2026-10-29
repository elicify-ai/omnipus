// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"
)

type answerRouterStub struct {
	got   []ReplyRequest
	fail  error
	reply ReplyReceipt
}

func (s *answerRouterStub) Reply(_ context.Context, r ReplyRequest) (ReplyReceipt, error) {
	s.got = append(s.got, r)
	return s.reply, s.fail
}

func answerCtx() context.Context {
	ctx := WithToolContext(context.Background(), "webchat", "chat-1")
	ctx = WithAgentID(ctx, "mia")
	ctx = WithWorkspaceID(ctx, "ws-1")
	return WithTranscriptSessionID(ctx, "sess-mia-main")
}

// Gap 5: answering a request is an explicit tool call. The tool reaches the
// same reply admission as send_message reply_to, authored by the acting agent
// from the turn (never from the arguments), and counts as the round's send.
func TestAnswerRequest_DeliversThroughTheReplyAdmission(t *testing.T) {
	stub := &answerRouterStub{reply: ReplyReceipt{Destination: "the conversation it came from"}}
	msg := NewMessageTool()
	msg.SetReplyRouter(stub)
	tool := NewAnswerRequestTool(msg)

	res := tool.Execute(answerCtx(), map[string]any{"request_id": "req-1", "text": "the answer"})
	if res.IsError {
		t.Fatalf("answer refused: %s", res.ForLLM)
	}
	if len(stub.got) != 1 {
		t.Fatalf("router calls = %d, want 1", len(stub.got))
	}
	g := stub.got[0]
	if g.ReplyTo != "req-1" || g.Content != "the answer" || g.Author.AgentID != "mia" ||
		g.Author.WorkspaceID != "ws-1" || g.ResponderSessionID != "sess-mia-main" {
		t.Fatalf("reply request = %+v", g)
	}
	if !msg.HasSentInRound() {
		t.Fatal("an accepted answer must count as the round's send, so the turn's plain closing reply is not also sent")
	}
}

// R1 delta T4: the tool's parameters are request_id and text ONLY, and the
// reply's author and responder session come from the TURN's context, never from
// the arguments. A hostile caller that supplies agent_id / author /
// workspace_id / reply_to of its own must be ignored — otherwise an agent could
// answer as another agent, in a workspace it is not acting in, or attach its
// answer to a request it was never sent.
func TestAnswerRequest_HostileExtraArgumentsAreIgnored(t *testing.T) {
	stub := &answerRouterStub{reply: ReplyReceipt{Destination: "the conversation it came from"}}
	msg := NewMessageTool()
	msg.SetReplyRouter(stub)
	tool := NewAnswerRequestTool(msg)

	res := tool.Execute(answerCtx(), map[string]any{
		"request_id": "req-1",
		"text":       "the answer",
		// Every one of these is either absent from the schema or a value the
		// tool must take from the turn instead.
		"agent_id":     "jim",
		"author":       map[string]any{"agent_id": "jim", "workspace_id": "ws-evil"},
		"workspace_id": "ws-evil",
		"reply_to":     "someone-elses-request",
		"from":         "jim",
		"session_id":   "sess-someone-else",
	})
	if res.IsError {
		t.Fatalf("a hostile extra argument must not break the answer: %s", res.ForLLM)
	}
	if len(stub.got) != 1 {
		t.Fatalf("router calls = %d, want 1", len(stub.got))
	}
	g := stub.got[0]
	if g.ReplyTo != "req-1" {
		t.Fatalf("ReplyTo = %q — the arguments' reply_to must be ignored; only request_id addresses the request", g.ReplyTo)
	}
	if g.Author.AgentID != "mia" || g.Author.WorkspaceID != "ws-1" {
		t.Fatalf("author = %+v — the author comes from the TURN, never from the arguments", g.Author)
	}
	if g.ResponderSessionID != "sess-mia-main" {
		t.Fatalf("ResponderSessionID = %q, want the turn's session", g.ResponderSessionID)
	}
}

func TestAnswerRequest_RefusalsAreVisibleAndSendNothing(t *testing.T) {
	cases := map[string]map[string]any{
		"blank id":   {"request_id": "  ", "text": "x"},
		"blank text": {"request_id": "req-1", "text": " "},
		"no id":      {"text": "x"},
		"no text":    {"request_id": "req-1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &answerRouterStub{}
			msg := NewMessageTool()
			msg.SetReplyRouter(stub)
			res := NewAnswerRequestTool(msg).Execute(answerCtx(), args)
			if !res.IsError || len(stub.got) != 0 {
				t.Fatalf("want a visible refusal and nothing sent; got err=%v calls=%d", res.IsError, len(stub.got))
			}
		})
	}
}

func TestAnswerRequest_RouterRefusalIsAnError(t *testing.T) {
	stub := &answerRouterStub{fail: context.Canceled}
	msg := NewMessageTool()
	msg.SetReplyRouter(stub)
	res := NewAnswerRequestTool(msg).Execute(answerCtx(), map[string]any{"request_id": "req-1", "text": "x"})
	if !res.IsError || msg.HasSentInRound() {
		t.Fatalf("a refused answer is an error and not a send: err=%v sent=%v", res.IsError, msg.HasSentInRound())
	}
}

func TestAnswerRequest_UnwiredRouterRefuses(t *testing.T) {
	res := NewAnswerRequestTool(NewMessageTool()).Execute(answerCtx(), map[string]any{"request_id": "r", "text": "x"})
	if !res.IsError {
		t.Fatal("no reply router configured: the answer must be refused, not dropped")
	}
}

func TestAnswerRequest_SchemaIsMinimalAndExact(t *testing.T) {
	tool := NewAnswerRequestTool(NewMessageTool())
	if tool.Name() != "answer_request" {
		t.Fatalf("name = %q", tool.Name())
	}
	props, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map[string]any", tool.Parameters()["properties"])
	}
	// #1449 FR-020: media is the third parameter. request_id and text stay required.
	if len(props) != 3 || props["request_id"] == nil || props["text"] == nil || props["media"] == nil {
		t.Fatalf("properties = %v, want exactly request_id, text and media", props)
	}
	media, _ := props["media"].(map[string]any)
	if media["type"] != "array" || media["maxItems"] != 16 {
		t.Fatalf("media = %v, want an array of at most 16 (TD-3)", props["media"])
	}
	required, _ := tool.Parameters()["required"].([]string)
	if len(required) != 2 || required[0] != "request_id" || required[1] != "text" {
		t.Fatalf("required = %v, want request_id and text (media stays optional)", required)
	}
}
