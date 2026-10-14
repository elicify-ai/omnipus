package providers

// Provider hotfix H5: the chatgpt.com/backend-api/codex Responses stream
// delivers the answer ONLY in `response.output_item.done` events and closes
// with a `response.completed` whose `output` is empty whenever the request
// carries tools (captured 2026-10-13, see the Captured* tests below).
// Reading the answer from the completed event alone dropped it: the agent
// saw an empty response ("The model returned an empty response").

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// writeSSEEvents writes each event as one `event:`/`data:` SSE frame, then
// the [DONE] terminator.
func writeSSEEvents(w http.ResponseWriter, events ...map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	for i, evt := range events {
		evt["sequence_number"] = i + 1
		b, err := json.Marshal(evt)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(w, "event: %s\n", evt["type"])
		fmt.Fprintf(w, "data: %s\n\n", string(b))
	}
	fmt.Fprintf(w, "data: [DONE]\n\n")
}

func completedEvent(output []map[string]any) map[string]any {
	return map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": "resp_h5", "object": "response", "status": "completed",
			"output": output,
			"usage": map[string]any{
				"input_tokens": 15000, "output_tokens": 7, "total_tokens": 15007,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 0},
			},
		},
	}
}

func messageItem(id, text string) map[string]any {
	return map[string]any{
		"id": id, "type": "message", "role": "assistant", "status": "completed",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
	}
}

func itemDoneEvent(index int, item map[string]any) map[string]any {
	return map[string]any{"type": "response.output_item.done", "output_index": index, "item": item}
}

func textDeltaEvent(itemID, delta string) map[string]any {
	return map[string]any{
		"type": "response.output_text.delta", "item_id": itemID,
		"output_index": 0, "content_index": 0, "delta": delta, "logprobs": []any{},
	}
}

func codexStreamProvider(t *testing.T, events ...map[string]any) *CodexProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSEEvents(w, events...)
	}))
	t.Cleanup(server.Close)
	p := NewCodexProvider("test-token", "acc-123")
	p.client = createOpenAITestClient(server.URL, "test-token", "acc-123")
	return p
}

func TestCodexProvider_Chat_UsesOutputItemDoneWhenCompletedOutputEmpty(t *testing.T) {
	p := codexStreamProvider(t,
		itemDoneEvent(0, messageItem("msg_1", "pong from output_item.done")),
		completedEvent([]map[string]any{}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "ping"}}, nil, "gpt-5.4", nil)

	require.NoError(t, err)
	assert.Equal(t, "pong from output_item.done", resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)
	require.NotNil(t, resp.Usage, "usage still comes from the completed event")
	assert.Equal(t, 15007, resp.Usage.TotalTokens)
	require.NotNil(t, resp.Diagnostics)
	assert.Equal(t, "output_item.done", resp.Diagnostics.OutputSource)
}

func TestCodexProvider_Chat_UsesFunctionCallFromOutputItemDone(t *testing.T) {
	p := codexStreamProvider(t,
		itemDoneEvent(0, map[string]any{
			"id": "fc_1", "type": "function_call", "status": "completed",
			"call_id": "call_1", "name": "read_file", "arguments": `{"path":"a.txt"}`,
		}),
		completedEvent([]map[string]any{}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "read"}}, nil, "gpt-5.4", nil)

	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "read_file", resp.ToolCalls[0].Name)
	assert.Equal(t, "call_1", resp.ToolCalls[0].ID)
	assert.Equal(t, map[string]any{"path": "a.txt"}, resp.ToolCalls[0].Arguments)
	assert.Equal(t, "tool_calls", resp.FinishReason)
}

func TestCodexProvider_Chat_ItemsKeepOutputIndexOrder(t *testing.T) {
	// Items may finish out of order; the answer follows output_index.
	p := codexStreamProvider(t,
		itemDoneEvent(1, messageItem("msg_2", "second")),
		itemDoneEvent(0, messageItem("msg_1", "first ")),
		completedEvent([]map[string]any{}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-5.4", nil)

	require.NoError(t, err)
	assert.Equal(t, "first second", resp.Content)
}

func TestCodexProvider_Chat_CompletedOutputWinsWithoutDuplicates(t *testing.T) {
	// When the completed event DOES carry the output, it is authoritative:
	// the streamed item is neither appended (no duplicate) nor substituted.
	// The two sources deliberately differ so "terminal wins" is observable.
	p := codexStreamProvider(t,
		textDeltaEvent("msg_1", "streamed draft"),
		itemDoneEvent(0, messageItem("msg_1", "streamed draft")),
		completedEvent([]map[string]any{messageItem("msg_1", "terminal answer")}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-5.4", nil)

	require.NoError(t, err)
	assert.Equal(t, "terminal answer", resp.Content)
	assert.Equal(t, "completed", resp.Diagnostics.OutputSource)
}

func TestCodexProvider_Chat_TrulyEmptyStreamStaysEmpty(t *testing.T) {
	// Control: nothing anywhere is still an empty answer (the agent's
	// empty-response handling, not this parser, decides what happens next).
	p := codexStreamProvider(t, completedEvent([]map[string]any{}))

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-5.4", nil)

	require.NoError(t, err)
	assert.Equal(t, "", resp.Content)
	assert.Empty(t, resp.ToolCalls)

	// The shape summary the agent's empty-answer warning logs (founder
	// decision 2026-10-13): what came back, never the content.
	require.NotNil(t, resp.Diagnostics)
	assert.Equal(t, map[string]int{"response.completed": 1}, resp.Diagnostics.EventCounts)
	assert.Empty(t, resp.Diagnostics.OutputItemTypes)
	assert.Equal(t, "none", resp.Diagnostics.OutputSource)
	assert.Equal(t, "completed", resp.Diagnostics.Status)
	assert.Equal(t, "", resp.Diagnostics.IncompleteReason)
}

func TestCodexProvider_Chat_DiagnosticsDescribeTheStream(t *testing.T) {
	p := codexStreamProvider(t,
		textDeltaEvent("msg_1", "Hi"),
		textDeltaEvent("msg_1", "!"),
		itemDoneEvent(0, map[string]any{"id": "rs_1", "type": "reasoning", "summary": []any{}}),
		itemDoneEvent(1, messageItem("msg_1", "Hi!")),
		completedEvent([]map[string]any{}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-5.4", nil)

	require.NoError(t, err)
	require.NotNil(t, resp.Diagnostics)
	assert.Equal(t, map[string]int{
		"response.output_text.delta": 2,
		"response.output_item.done":  2,
		"response.completed":         1,
	}, resp.Diagnostics.EventCounts)
	assert.Equal(t, []string{"reasoning", "message"}, resp.Diagnostics.OutputItemTypes)
	assert.Equal(t, "output_item.done", resp.Diagnostics.OutputSource)
}

// ── Shapes CAPTURED from the real chatgpt.com/backend-api/codex stream ──────
//
// 2026-10-13, model gpt-6.1-sol, release CodexProvider.Chat (receipts:
// provider-hotfix-logs/h5-release-*.json; text replaced, ids synthetic).
// With tools in the request the backend closed every stream with
// `response.completed` carrying "output": [] — the answer existed only in
// `response.output_item.done`. The release parser returned an empty answer
// for both captured cases below; a no-tools request carried its output in
// the completed event and was unaffected (the "pong" agent).

// Captured case b: 77 real builtin tools, no native search. Event counts:
// created 1, in_progress 1, output_item.added 1,
// function_call_arguments.delta 12, function_call_arguments.done 1,
// output_item.done 1 (function_call, status completed), completed 1
// (output []).
func TestCodexProvider_Chat_CapturedToolCallOnlyInOutputItemDone(t *testing.T) {
	fc := map[string]any{
		"id": "fc_cap", "type": "function_call", "status": "completed",
		"call_id": "call_cap", "name": "search_web", "arguments": `{"query":"Jakarta weather today"}`,
	}
	added := map[string]any{
		"id": "fc_cap", "type": "function_call", "status": "in_progress",
		"call_id": "call_cap", "name": "search_web", "arguments": "",
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_cap", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.in_progress", "response": map[string]any{"id": "resp_cap", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": added},
	}
	for i := 0; i < 12; i++ {
		events = append(events, map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": "fc_cap", "output_index": 0, "delta": "x",
		})
	}
	events = append(events,
		map[string]any{"type": "response.function_call_arguments.done", "item_id": "fc_cap", "output_index": 0,
			"arguments": `{"query":"Jakarta weather today"}`},
		itemDoneEvent(0, fc),
		completedEvent([]map[string]any{}),
	)
	p := codexStreamProvider(t, events...)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "What is the weather in Jakarta today?"}}, nil, "gpt-6.1-sol", nil)

	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1, "the release parser dropped this call: tool_calls 0, empty answer")
	assert.Equal(t, "search_web", resp.ToolCalls[0].Name)
	assert.Equal(t, map[string]any{"query": "Jakarta weather today"}, resp.ToolCalls[0].Arguments)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	assert.Equal(t, "output_item.done", resp.Diagnostics.OutputSource)
}

// Captured case c (and d, with a ~15k-token prompt): tools plus native web
// search. Done items: web_search_call (status completed, action search),
// then message (phase final_answer, one output_text); completed output [].
func TestCodexProvider_Chat_CapturedNativeSearchAnswerOnlyInOutputItemDone(t *testing.T) {
	webSearch := map[string]any{
		"id": "ws_cap", "type": "web_search_call", "status": "completed",
		"action": map[string]any{"type": "search", "query": "Jakarta weather today"},
	}
	answer := messageItem("msg_cap", "It is about 31°C and humid in Jakarta today.")
	answer["phase"] = "final_answer"
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_cap", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.in_progress", "response": map[string]any{"id": "resp_cap", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"id": "ws_cap", "type": "web_search_call", "status": "in_progress"}},
		{"type": "response.web_search_call.in_progress", "item_id": "ws_cap", "output_index": 0},
		{"type": "response.web_search_call.searching", "item_id": "ws_cap", "output_index": 0},
		{"type": "response.web_search_call.completed", "item_id": "ws_cap", "output_index": 0},
		itemDoneEvent(0, webSearch),
		{"type": "response.output_item.added", "output_index": 1,
			"item": map[string]any{"id": "msg_cap", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
		{"type": "response.content_part.added", "item_id": "msg_cap", "output_index": 1, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
		textDeltaEvent("msg_cap", "It is about 31°C and humid "),
		textDeltaEvent("msg_cap", "in Jakarta today."),
		{"type": "response.output_text.done", "item_id": "msg_cap", "output_index": 1, "content_index": 0,
			"text": "It is about 31°C and humid in Jakarta today.", "logprobs": []any{}},
		{"type": "response.content_part.done", "item_id": "msg_cap", "output_index": 1, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "It is about 31°C and humid in Jakarta today.", "annotations": []any{}}},
		itemDoneEvent(1, answer),
		completedEvent([]map[string]any{}),
	}
	p := codexStreamProvider(t, events...)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "What is the weather in Jakarta today?"}},
		nil, "gpt-6.1-sol", map[string]any{"native_search": true})

	require.NoError(t, err)
	assert.Equal(t, "It is about 31°C and humid in Jakarta today.", resp.Content,
		"exactly once — from the message item, not doubled by the text deltas")
	assert.Empty(t, resp.ToolCalls, "web_search_call is the backend's own search, not a tool call for the agent")
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Equal(t, []string{"web_search_call", "message"}, resp.Diagnostics.OutputItemTypes)
}

// terminalEvent is a non-completed terminal stream event (failed or
// incomplete) carrying an EMPTY output, the shape the backend uses.
func terminalEvent(eventType, status string, extra map[string]any) map[string]any {
	r := map[string]any{
		"id": "resp_h5", "object": "response", "status": status, "output": []any{},
		"usage": map[string]any{
			"input_tokens": 15000, "output_tokens": 9, "total_tokens": 15009,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
	}
	for k, v := range extra {
		r[k] = v
	}
	return map[string]any{"type": eventType, "response": r}
}

// Silent-failure F3: output recovered from items on a response the provider
// cut off under its CONTENT POLICY is a refusal, not a token-cap cutoff. It
// must not come back as a "length" answer the agent would auto-continue; it
// is the content-policy stream error (non-retriable, never a fallback).
func TestCodexProvider_Chat_ContentFilterAfterRecoveredItemsIsAPolicyRefusal(t *testing.T) {
	p := codexStreamProvider(t,
		itemDoneEvent(0, messageItem("msg_1", "partial text before the filter")),
		terminalEvent("response.incomplete", "incomplete",
			map[string]any{"incomplete_details": map[string]any{"reason": "content_filter"}}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-6.1-sol", nil)

	require.Error(t, err)
	assert.Nil(t, resp, "no answer to continue")
	var spe *common.StreamProviderError
	require.True(t, errors.As(err, &spe), "err = %v", err)
	assert.Equal(t, common.StreamErrorContentPolicy, spe.Kind())
	require.NotNil(t, spe.Usage, "token usage survives the refusal")
	assert.Equal(t, 15009, spe.Usage.TotalTokens)
	fe := ClassifyError(err, "openai-chatgpt", "gpt-6.1-sol")
	require.NotNil(t, fe)
	assert.Equal(t, FailoverFormat, fe.Reason)
	assert.False(t, fe.IsRetriable(), "a policy refusal never walks the fallback chain")
}

// Test-analyzer I5: a token-cap cutoff still reads as one after recovery —
// FinishReason "length" (truncation handling applies), usage kept, and the
// diagnostics name the incomplete reason.
func TestCodexProvider_Chat_IncompleteMaxTokensKeepsLengthAfterRecovery(t *testing.T) {
	p := codexStreamProvider(t,
		itemDoneEvent(0, messageItem("msg_1", "first half of a long answer")),
		terminalEvent("response.incomplete", "incomplete",
			map[string]any{"incomplete_details": map[string]any{"reason": "max_output_tokens"}}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-6.1-sol", nil)

	require.NoError(t, err)
	assert.Equal(t, "first half of a long answer", resp.Content)
	assert.Equal(t, "length", resp.FinishReason)
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 15009, resp.Usage.TotalTokens)
	assert.Equal(t, "incomplete", resp.Diagnostics.Status)
	assert.Equal(t, "max_output_tokens", resp.Diagnostics.IncompleteReason)
	assert.Equal(t, "output_item.done", resp.Diagnostics.OutputSource)
}

// Test-analyzer I5: items streamed before a `response.failed` are never an
// answer — in particular a recovered function_call must not be executed.
func TestCodexProvider_Chat_FailedTerminalAfterItemsIsAnError(t *testing.T) {
	p := codexStreamProvider(t,
		itemDoneEvent(0, map[string]any{
			"id": "fc_1", "type": "function_call", "status": "completed",
			"call_id": "call_1", "name": "write_file", "arguments": `{"path":"a.txt"}`,
		}),
		terminalEvent("response.failed", "failed",
			map[string]any{"error": map[string]any{"code": "server_error", "message": "upstream failed"}}),
	)

	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "x"}}, nil, "gpt-6.1-sol", nil)

	require.Error(t, err)
	assert.Nil(t, resp, "no tool call may be returned from a failed response")
	var spe *common.StreamProviderError
	require.True(t, errors.As(err, &spe), "err = %v", err)
	assert.Equal(t, "server_error", spe.Code)
}
