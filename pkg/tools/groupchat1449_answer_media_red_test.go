// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"strings"
	"testing"
)

// Oracles: docs/internal/specs/1449-group-chat-spec.md US-4, S-31, S-34,
// S-119, DS-1 rows 22–23, machine-verifiable answer_request rows.
// The reply router is the process edge. The tool itself is real.

func TestGroupChat1449_AnswerRequestSchemaDeclaresAtMost16Media(t *testing.T) {
	props, _ := answerRequestProperties(t)
	media, ok := props["media"].(map[string]any)
	if !ok {
		t.Fatal("answer_request parameters have no media array — required by the spec contract row for answer_request (US-4, S-29)")
	}
	if media["type"] != "array" {
		t.Fatalf("media type = %v, want array", media["type"])
	}
	maxItems, _ := media["maxItems"].(int)
	if maxItems == 0 {
		if f, ok := media["maxItems"].(float64); ok {
			maxItems = int(f)
		}
	}
	if maxItems != 16 {
		t.Fatalf("media maxItems = %d, want 16 (S-31, TD-3)", maxItems)
	}
}

func TestGroupChat1449_AnswerMediaFromAnotherWorkspacePostsNothing(t *testing.T) {
	// S-34 / machine-verifiable row: unusable media is a tool error and
	// nothing is appended, mirrored or forwarded.
	stub := &answerRouterStub{reply: ReplyReceipt{Destination: "the conversation it came from"}}
	msg := NewMessageTool()
	msg.SetReplyRouter(stub)
	tool := NewAnswerRequestTool(msg)

	res := tool.Execute(answerCtx(), map[string]any{
		"request_id": "req-1",
		"text":       "diagram",
		"media":      []any{"media://workspace/other-ws/file-1"},
	})
	if res == nil || !res.IsError {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Fatalf("S-34: other-workspace media was accepted (%q); want a tool error and nothing posted", got)
	}
	if len(stub.got) != 0 {
		t.Fatalf("S-34: the reply router was called %d times; nothing may be passed on", len(stub.got))
	}
	if !strings.Contains(strings.ToLower(res.ForLLM), "media") && !strings.Contains(strings.ToLower(res.ForLLM), "workspace") {
		t.Fatalf("S-34: tool error %q must name the media refusal", res.ForLLM)
	}
}

func TestGroupChat1449_AnswerTextOverTheLimitPostsNothing(t *testing.T) {
	// S-119, DS-1 row 23. 65,537 bytes is a tool error naming 65,536.
	// Nothing is passed on. 65,536 bytes of text alone is within the limit
	// and must still be delivered (row 22, without a file — the file half
	// needs the media parameter).
	stub := &answerRouterStub{reply: ReplyReceipt{Destination: "the conversation it came from"}}
	msg := NewMessageTool()
	msg.SetReplyRouter(stub)
	tool := NewAnswerRequestTool(msg)

	over := tool.Execute(answerCtx(), map[string]any{
		"request_id": "req-over",
		"text":       strings.Repeat("a", 65537),
	})
	if over == nil || !over.IsError || !strings.Contains(over.ForLLM, "65536") {
		got := ""
		if over != nil {
			got = over.ForLLM
		}
		t.Fatalf("S-119: over-limit result = error:%v %q, want a tool error naming 65536", over != nil && over.IsError, got)
	}
	if len(stub.got) != 0 {
		t.Fatalf("S-119: over-limit answer was passed on (%d router calls)", len(stub.got))
	}

	ok := tool.Execute(answerCtx(), map[string]any{
		"request_id": "req-max",
		"text":       strings.Repeat("b", 65536),
	})
	if ok == nil || ok.IsError {
		got := ""
		if ok != nil {
			got = ok.ForLLM
		}
		t.Fatalf("S-119: 65536-byte answer must be accepted, got %q", got)
	}
	if len(stub.got) != 1 || len(stub.got[0].Content) != 65536 {
		t.Fatalf("S-119: 65536-byte answer was not delivered once, router calls = %d", len(stub.got))
	}
}

func TestGroupChat1449_SeventeenAnswerFilesAreRefusedBeforeDelivery(t *testing.T) {
	// S-31, DS-12 row 18. 17 files is a definitive rejection. Nothing is posted.
	stub := &answerRouterStub{reply: ReplyReceipt{Destination: "the conversation it came from"}}
	msg := NewMessageTool()
	msg.SetReplyRouter(stub)
	tool := NewAnswerRequestTool(msg)
	media := make([]any, 17)
	for i := range media {
		media[i] = "media://workspace/ws-1/file"
	}
	res := tool.Execute(answerCtx(), map[string]any{
		"request_id": "req-17",
		"text":       "files",
		"media":      media,
	})
	if res == nil || !res.IsError {
		t.Fatal("S-31: 17 media entries must be a tool error")
	}
	if len(stub.got) != 0 {
		t.Fatalf("S-31: 17 media entries were passed on (%d router calls)", len(stub.got))
	}
}

func answerRequestProperties(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	params := NewAnswerRequestTool(NewMessageTool()).Parameters()
	props, _ := params["properties"].(map[string]any)
	if props == nil {
		t.Fatal("answer_request parameters have no properties object")
	}
	return props, params
}
