package openai_compat

// Issue #1901 (chief decision, 2026-11-19): a provider that answers a
// STREAMING request (ChatStream sends stream:true) with a plain, non-streamed
// JSON chat.completion body (HTTP 200, Content-Type application/json) must be
// ACCEPTED and shown — content through onChunk (the live view) and in the
// returned response, with finish reason and usage. On the pre-fix head the
// whole body is one scanner line that never starts with "data: ", so
// parseStreamResponse ends with no content, no tool calls and no
// finish_reason and returns common.NewEmptyStreamError: tests 1 and 2 are
// RED for exactly that reason (the saved failing run is the RED proof).
//
// The other side of the decision is #1664's rule, kept: a JSON reply that is
// TRULY empty (no content, no tool calls) must still FAIL with
// common.ErrEmptyStream — never a synthesized successful empty reply (test 3,
// green before and after the fix). Test 4 is the streaming control: a normal
// text/event-stream reply must keep working unchanged.
//
// Oracles (none read from the implementation's behaviour):
//   - Reply bodies are the issue's example shapes (OpenAI non-streamed
//     chat.completion); the usage numbers 3/2/5 are the body's own values.
//   - Usage normalization (chat shape, no provider id): PromptTokens from
//     prompt_tokens, CompletionTokens from completion_tokens, TotalTokens
//     recomputed as prompt+completion (pkg/providers/promptcache/
//     normalize.go::NormalizeUsage) — 3+0+0+2 = 5, consistent with the body.
//   - Tool-call shaping: pkg/providers/common/common.go::ParseResponse —
//     ID from id, Name from function.name, Arguments decoded from
//     function.arguments (a JSON object string) into a map; finish_reason
//     passes through normalizeFinishReason ("stop" and "tool_calls"
//     unchanged; only "length" becomes "truncated").
//   - onChunk's contract (provider.go::ChatStream doc comment): it receives
//     the ACCUMULATED text so far on each text delta, so the final call must
//     carry the whole text and every call must be a prefix of it.
//
// Not covered here (no spec source, deliberately): a JSON body served under a
// wrong or missing Content-Type — the chief decision names only
// application/json replies; pinning anything else would derive the expected
// value from an unwritten fix.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// TestIssue1901_ChatStream_AcceptsNonStreamedJSONReply: the plain reply is
// accepted and shown — no error, content, finish reason, usage, and the text
// reaches onChunk for the live view.
func TestIssue1901_ChatStream_AcceptsNonStreamedJSONReply(t *testing.T) {
	// The issue's example body, verbatim shape.
	const body = `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"plain answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	p := mustNewProvider(t, "key", server.URL, "")

	var chunks []string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := p.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil,
		func(accumulated string) { chunks = append(chunks, accumulated) }, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v, want nil: a plain non-streamed JSON reply to a streaming request must be accepted, not treated as an empty stream (#1901 chief decision)", err)
	}
	if resp == nil {
		t.Fatal("ChatStream() resp = nil, want a response")
	}
	if resp.Content != "plain answer" {
		t.Errorf("resp.Content = %q, want %q: the reply's message.content must be the response content", resp.Content, "plain answer")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("resp.FinishReason = %q, want %q: the body's finish_reason must pass through unchanged", resp.FinishReason, "stop")
	}
	if resp.Usage == nil {
		t.Fatalf("resp.Usage = nil, want the body's usage object (prompt_tokens 3, completion_tokens 2, total_tokens 5)")
	}
	if resp.Usage.PromptTokens != 3 {
		t.Errorf("resp.Usage.PromptTokens = %d, want 3 (body prompt_tokens)", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 2 {
		t.Errorf("resp.Usage.CompletionTokens = %d, want 2 (body completion_tokens)", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 5 {
		t.Errorf("resp.Usage.TotalTokens = %d, want 5 (body total_tokens; normalization recomputes prompt+completion = 3+2)", resp.Usage.TotalTokens)
	}
	// The live view still gets the text: onChunk delivers the accumulated
	// text, so the last call must be the whole reply and every call a
	// prefix of it.
	if len(chunks) == 0 {
		t.Fatal("onChunk was never called, want the reply text delivered to the live view")
	}
	if last := chunks[len(chunks)-1]; last != "plain answer" {
		t.Errorf("last onChunk accumulated = %q, want %q", last, "plain answer")
	}
	for i, c := range chunks {
		if !strings.HasPrefix("plain answer", c) {
			t.Errorf("onChunk call %d accumulated = %q, want a prefix of %q (accumulated-text contract)", i, c, "plain answer")
		}
	}
}

// TestIssue1901_ChatStream_JSONReplyWithToolCall: a non-streamed JSON reply
// carrying a tool call and no content is accepted and the tool call comes
// back shaped the way common.ParseResponse shapes it.
func TestIssue1901_ChatStream_JSONReplyWithToolCall(t *testing.T) {
	const body = `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1901","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	p := mustNewProvider(t, "key", server.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := p.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v, want nil: a non-streamed JSON reply with a tool call must be accepted and dispatched, not treated as an empty stream (#1901 chief decision)", err)
	}
	if resp == nil {
		t.Fatal("ChatStream() resp = nil, want a response")
	}
	if resp.Content != "" {
		t.Errorf("resp.Content = %q, want empty (the body carries a tool call and no content)", resp.Content)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("resp.FinishReason = %q, want %q (body finish_reason, unchanged by normalization)", resp.FinishReason, "tool_calls")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(resp.ToolCalls) = %d, want 1: the body's tool_calls[0] must come back as one call", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1901" {
		t.Errorf("ToolCalls[0].ID = %q, want %q (body tool_calls[0].id)", tc.ID, "call_1901")
	}
	if tc.Name != "get_weather" {
		t.Errorf("ToolCalls[0].Name = %q, want %q (body tool_calls[0].function.name)", tc.Name, "get_weather")
	}
	wantArgs := map[string]any{"city": "Paris"}
	if !reflect.DeepEqual(tc.Arguments, wantArgs) {
		t.Errorf("ToolCalls[0].Arguments = %v, want %v (function.arguments decoded into a map, as common.ParseResponse shapes it)", tc.Arguments, wantArgs)
	}
}

// TestIssue1901_ChatStream_EmptyJSONReplyStillFails: the #1664 rule survives
// the fix — a JSON reply with no content and no tool calls is a failed
// attempt (common.ErrEmptyStream), never a synthesized successful empty
// reply. This must stay green before AND after the fix.
func TestIssue1901_ChatStream_EmptyJSONReplyStillFails(t *testing.T) {
	const body = `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	p := mustNewProvider(t, "key", server.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := p.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
	if err == nil {
		t.Fatal("ChatStream() error = nil, want an error: a JSON reply that is truly empty (no content, no tool calls) must still fail (#1664 rule, restated in #1901)")
	}
	if !errors.Is(err, common.ErrEmptyStream) {
		t.Fatalf("ChatStream() error = %v, want errors.Is(err, common.ErrEmptyStream)", err)
	}
}

// TestIssue1901_ChatStream_SSEStillWorks: the control. A normal
// text/event-stream reply — two content deltas, a finish chunk, [DONE] —
// still returns the joined text, so the fix cannot break real streaming.
// Green before and after the fix.
func TestIssue1901_ChatStream_SSEStillWorks(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer server.Close()
	p := mustNewProvider(t, "key", server.URL, "")

	var chunks []string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := p.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil,
		func(accumulated string) { chunks = append(chunks, accumulated) }, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v, want nil: ordinary SSE streaming must be untouched by the #1901 fix", err)
	}
	if resp == nil {
		t.Fatal("ChatStream() resp = nil, want a response")
	}
	if resp.Content != "Hello" {
		t.Errorf("resp.Content = %q, want %q (the two deltas joined in order)", resp.Content, "Hello")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("resp.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if len(chunks) == 0 || chunks[len(chunks)-1] != "Hello" {
		t.Fatalf("last onChunk accumulated = %q (over %d calls), want %q", chunks, len(chunks), "Hello")
	}
}
