package anthropicmessages

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_responses_common"
)

// #1081 standalone Anthropic call-loss regression, not R1 algorithm coverage.
// Oracle: a real archived tool call must retain its ID, name and complete input
// in the provider request, including when its tool result follows it. Anthropic
// represents that call as a tool_use block with id, name and object-valued input.
// The canonical fixture deliberately has no flattened Name or Arguments fields:
// protocoltypes.ToolCall excludes both convenience fields from JSON.
//
// Plan: canonical reconstruction, live convenience fields, mixed ordering,
// three malformed-name characterizations, and three sibling positive controls.
// No numeric/sized boundary is introduced by this adapter regression. Network
// response parsing, invalid argument JSON and actual archive I/O are outside it.
// CHECK, by a fresh instance, must prove GREEN and kill mutations that drop the
// canonical call, replace its input with {}, or emit a genuinely nameless call.
const calllossCanonicalArguments = `{"query":"recovered-query","limit":2,"enabled":true}`

func TestBuildRequestBodyPreservesReconstructedToolCalls(t *testing.T) {
	t.Run("anthropic/function_only_from_canonical_json", testCalllossCanonicalRequest)
	t.Run("anthropic/live_convenience_fields", testCalllossLiveRequest)
	t.Run("anthropic/mixed_calls_keep_order", testCalllossMixedRequest)

	// Characterization tests: preserve the existing malformed-name boundary,
	// not a new error policy. TestBuildRequestBodyEdgeCases already requires
	// skipping truly nameless calls; Bedrock and Responses also skip them.
	malformed := []struct {
		name string
		call ToolCall
	}{
		{
			name: "empty_name_nil_function_is_skipped",
			call: ToolCall{ID: "call-malformed"},
		},
		{
			name: "empty_name_empty_function_is_skipped",
			call: ToolCall{ID: "call-malformed", Function: &FunctionCall{}},
		},
		{
			name: "whitespace_name_nil_function_is_skipped",
			call: ToolCall{ID: "call-malformed", Name: " \t\n"},
		},
	}
	for _, tc := range malformed {
		t.Run("anthropic/"+tc.name, func(t *testing.T) {
			testCalllossMalformedRequest(t, tc.call)
		})
	}

	t.Run("openai_compat/preserves_equivalent_canonical_call", testCalllossOpenAIControl)
	t.Run("bedrock/preserves_equivalent_canonical_call", testCalllossBedrockControl)
	t.Run("responses/preserves_equivalent_canonical_call", testCalllossResponsesControl)
}

func calllossCanonicalMessages(t *testing.T) []Message {
	t.Helper()
	const archiveJSON = `[
		{"role":"user","content":"Use some_tool."},
		{"role":"assistant","content":"Calling tool.","tool_calls":[
			{"id":"call-archived","type":"function","function":{
				"name":"some_tool",
				"arguments":"{\"query\":\"recovered-query\",\"limit\":2,\"enabled\":true}"
			}}
		]},
		{"role":"tool","tool_call_id":"call-archived","content":"tool-result"}
	]`
	var messages []Message
	if err := json.Unmarshal([]byte(archiveJSON), &messages); err != nil {
		t.Fatalf("decode canonical archive fixture: %v", err)
	}
	want := []Message{
		{Role: "user", Content: "Use some_tool."},
		{Role: "assistant", Content: "Calling tool.", ToolCalls: []ToolCall{{
			ID: "call-archived", Type: "function",
			Function: &FunctionCall{Name: "some_tool", Arguments: calllossCanonicalArguments},
		}}},
		{Role: "tool", ToolCallID: "call-archived", Content: "tool-result"},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("canonical reconstruction = %#v, want %#v (Name empty, Arguments nil)", messages, want)
	}
	return messages
}

func testCalllossCanonicalRequest(t *testing.T) {
	messages := calllossCanonicalMessages(t)
	body, err := buildRequestBody(messages, nil, "test-model", map[string]any{"max_tokens": 128})
	if err != nil {
		t.Fatalf("buildRequestBody(canonical call) returned unexpected error: %v", err)
	}
	assertCalllossJSON(t, body, `{
		"model":"test-model","max_tokens":128,"messages":[
			{"role":"user","content":"Use some_tool."},
			{"role":"assistant","content":[
				{"type":"text","text":"Calling tool."},
				{"type":"tool_use","id":"call-archived","name":"some_tool",
				 "input":{"query":"recovered-query","limit":2,"enabled":true}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call-archived",
				 "content":[{"type":"text","text":"tool-result"}]}
			]}
		]
	}`)
}

func testCalllossLiveRequest(t *testing.T) {
	body, err := buildRequestBody([]Message{{
		Role: "assistant", Content: "Calling tool.", ToolCalls: []ToolCall{{
			ID: "call-live", Name: "live_tool", Arguments: map[string]any{"query": "live-query"},
		}},
	}}, nil, "test-model", map[string]any{"max_tokens": 128})
	if err != nil {
		t.Fatalf("buildRequestBody(live call) returned unexpected error: %v", err)
	}
	assertCalllossJSON(t, body, `{
		"model":"test-model","max_tokens":128,"messages":[
			{"role":"assistant","content":[
				{"type":"text","text":"Calling tool."},
				{"type":"tool_use","id":"call-live","name":"live_tool","input":{"query":"live-query"}}
			]}
		]
	}`)
}

func testCalllossMixedRequest(t *testing.T) {
	archived := calllossCanonicalMessages(t)[1].ToolCalls[0]
	body, err := buildRequestBody([]Message{{
		Role: "assistant", Content: "Calling tool.", ToolCalls: []ToolCall{
			{ID: "call-live", Name: "live_tool", Arguments: map[string]any{"query": "live-query"}},
			archived,
		},
	}}, nil, "test-model", map[string]any{"max_tokens": 128})
	if err != nil {
		t.Fatalf("buildRequestBody(mixed calls) returned unexpected error: %v", err)
	}
	assertCalllossJSON(t, body, `{
		"model":"test-model","max_tokens":128,"messages":[
			{"role":"assistant","content":[
				{"type":"text","text":"Calling tool."},
				{"type":"tool_use","id":"call-live","name":"live_tool","input":{"query":"live-query"}},
				{"type":"tool_use","id":"call-archived","name":"some_tool",
				 "input":{"query":"recovered-query","limit":2,"enabled":true}}
			]}
		]
	}`)
}

func testCalllossMalformedRequest(t *testing.T, malformed ToolCall) {
	t.Helper()
	body, err := buildRequestBody([]Message{{
		Role: "assistant", Content: "Calling tool.", ToolCalls: []ToolCall{
			malformed,
			{ID: "call-live", Name: "live_tool", Arguments: map[string]any{"query": "live-query"}},
		},
	}}, nil, "test-model", map[string]any{"max_tokens": 128})
	if err != nil {
		t.Fatalf("buildRequestBody(malformed neighbor) returned unexpected error: %v", err)
	}
	assertCalllossJSON(t, body, `{
		"model":"test-model","max_tokens":128,"messages":[
			{"role":"assistant","content":[
				{"type":"text","text":"Calling tool."},
				{"type":"tool_use","id":"call-live","name":"live_tool","input":{"query":"live-query"}}
			]}
		]
	}`)
}

func testCalllossOpenAIControl(t *testing.T) {
	server, captured := calllossRequestServer(t, "/chat/completions", `{
		"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]
	}`)
	provider, err := openai_compat.NewProvider("test-key", server.URL, "")
	if err != nil {
		t.Fatalf("construct OpenAI-compatible control: %v", err)
	}
	if _, err := provider.Chat(t.Context(), calllossCanonicalMessages(t), nil, "test-model", map[string]any{"max_tokens": 128}); err != nil {
		t.Fatalf("OpenAI-compatible control Chat returned unexpected error: %v", err)
	}
	assertCalllossCapturedJSON(t, captured, `{
		"model":"test-model","max_tokens":128,"messages":[
			{"role":"user","content":"Use some_tool."},
			{"role":"assistant","content":"Calling tool.","tool_calls":[
				{"id":"call-archived","type":"function","function":{
					"name":"some_tool",
					"arguments":"{\"query\":\"recovered-query\",\"limit\":2,\"enabled\":true}"
				}}
			]},
			{"role":"tool","tool_call_id":"call-archived","content":"tool-result"}
		]
	}`)
}

func testCalllossBedrockControl(t *testing.T) {
	server, captured := calllossRequestServer(t, "/model/test-model/converse", `{
		"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn"
	}`)
	provider, err := bedrock.NewProvider("test-key", bedrock.WithBaseEndpoint(server.URL))
	if err != nil {
		t.Fatalf("construct Bedrock control: %v", err)
	}
	if _, err := provider.Chat(t.Context(), calllossCanonicalMessages(t), nil, "test-model", map[string]any{"max_tokens": 128}); err != nil {
		t.Fatalf("Bedrock control Chat returned unexpected error: %v", err)
	}
	assertCalllossCapturedJSON(t, captured, `{
		"inferenceConfig":{"maxTokens":128},"messages":[
			{"role":"user","content":[{"text":"Use some_tool."}]},
			{"role":"assistant","content":[
				{"text":"Calling tool."},
				{"toolUse":{"toolUseId":"call-archived","name":"some_tool",
				 "input":{"query":"recovered-query","limit":2,"enabled":true}}}
			]},
			{"role":"user","content":[
				{"toolResult":{"toolUseId":"call-archived","content":[{"text":"tool-result"}]}}
			]}
		]
	}`)
}

func testCalllossResponsesControl(t *testing.T) {
	archived := calllossCanonicalMessages(t)[1].ToolCalls[0]
	input, instructions := openai_responses_common.TranslateMessages([]Message{{
		Role: "assistant", ToolCalls: []ToolCall{archived},
	}})
	if instructions != "" {
		t.Fatalf("Responses instructions = %q, want empty for a history with no system message", instructions)
	}
	if len(input) != 1 || input[0].OfFunctionCall == nil {
		t.Fatalf("Responses input = %#v, want exactly one function_call for call-archived", input)
	}
	call := input[0].OfFunctionCall
	got := []string{call.CallID, call.Name, call.Arguments}
	want := []string{"call-archived", "some_tool", calllossCanonicalArguments}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Responses [call_id, name, arguments] = %q, want %q", got, want)
	}
}

// Only the network edge is faked; both public Chat methods run their real builders.
func calllossRequestServer(t *testing.T, path, response string) (*httptest.Server, <-chan json.RawMessage) {
	t.Helper()
	captured := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != path {
			t.Errorf("control request = %s %s, want POST %s", r.Method, r.URL.Path, path)
			http.Error(w, "unexpected control endpoint", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read control request body: %v", err)
			http.Error(w, "unreadable control request", http.StatusBadRequest)
			return
		}
		captured <- json.RawMessage(body)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, response); err != nil {
			t.Errorf("write control response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server, captured
}

func assertCalllossCapturedJSON(t *testing.T, captured <-chan json.RawMessage, want string) {
	t.Helper()
	select {
	case body := <-captured:
		assertCalllossJSON(t, body, want)
	default:
		t.Fatal("control Chat made no captured provider request")
	}
}

func assertCalllossJSON(t *testing.T, value any, want string) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal actual provider request: %v", err)
	}
	var gotJSON, wantJSON any
	if err := json.Unmarshal(body, &gotJSON); err != nil {
		t.Fatalf("decode actual provider request: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatalf("decode independent expected request: %v", err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("provider request must preserve real tool calls and their complete input:\ngot: %s\nwant: %s", body, want)
	}
}
