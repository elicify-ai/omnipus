package providers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// Forwarding coverage for the two wrapper providers.
//
// ClaudeProvider and HTTPProvider hold their real provider as an unexported,
// NON-EMBEDDED field and forward every call by hand. Satisfying
// StreamingProvider is therefore not the same as working: a wrapper can accept
// onProgress and quietly pass nil. It compiles, compliance.go's assertions
// still hold — they assert interface satisfaction, not behaviour — and
// every install behind that wrapper silently reports zero tool-call progress.
//
// This is not hypothetical. The same class already occurred on this branch:
// ClaudeProvider had no ChatStream at all, the agent loop's type assertion
// failed, and ALL Anthropic traffic took the non-streaming path while the
// compliance test stayed green (see ClaudeProvider.ChatStream's doc comment).
// It was caught before the branch was pushed, so it never reached a release —
// but nothing in the suite was going to catch it. The compliance assertions
// were then fixed to name the factory-returned type, which closed "does it
// satisfy the interface". This closes "does what it accepts actually arrive"
// — the half that was still open, and the half the progress signal depends on.
//
// Both tests drive the wrapper's real ChatStream against a real SSE server, so
// they exercise the production path end to end rather than asserting on source
// text.

// openAIToolArgsSSE is an OpenAI-compatible stream whose only content is one
// tool call with arguments split across several deltas.
func openAIToolArgsSSE(argChunks []string) string {
	var b strings.Builder
	b.WriteString(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1",` +
		`"function":{"name":"write_file","arguments":""}}]}}]}` + "\n\n")
	for _, c := range argChunks {
		fmt.Fprintf(&b, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`+"\n\n", c)
	}
	b.WriteString(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// anthropicToolArgsSSE is the Anthropic equivalent: a single tool_use block
// whose JSON arrives as input_json_delta events.
func anthropicToolArgsSSE(argChunks []string) string {
	var b strings.Builder
	b.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\"," +
		"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\"," +
		"\"stop_reason\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
	b.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0," +
		"\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"write_file\",\"input\":{}}}\n\n")
	for _, c := range argChunks {
		fmt.Fprintf(&b, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\","+
			"\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\n", c)
	}
	b.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	b.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\"," +
		"\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":4}}\n\n")
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := w.Write([]byte(body)); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestHTTPProvider_ChatStream_ForwardsOnProgress drives the wrapper's own
// ChatStream. It fails if http_provider.go forwards nil instead of its
// onProgress parameter.
func TestHTTPProvider_ChatStream_ForwardsOnProgress(t *testing.T) {
	srv := sseServer(t, openAIToolArgsSSE([]string{`{"path":"a.svg",`, `"content":"`, strings.Repeat("x", 64), `"}`}))

	p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", srv.URL, "", "", 30, nil)
	if err != nil {
		t.Fatalf("constructing HTTPProvider: %v", err)
	}

	var events []protocoltypes.ToolCallProgress
	if _, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil, "gpt-4o", nil, nil,
		func(pr protocoltypes.ToolCallProgress) { events = append(events, pr) },
	); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if len(events) == 0 {
		t.Fatal("HTTPProvider accepted onProgress and never delivered a single event — it forwards " +
			"nil (or a different callback) to its delegate, which silently kills the progress signal " +
			"for every install behind this wrapper while the rest of the suite stays green")
	}
	if last := events[len(events)-1]; last.Name != "write_file" || last.ArgsBytes < 64 {
		t.Errorf("forwarded events look wrong: %+v", last)
	}
}

// TestClaudeProvider_ChatStream_ForwardsOnProgress is the same check for the
// Anthropic wrapper — the one whose missing ChatStream caused the original
// silent outage.
func TestClaudeProvider_ChatStream_ForwardsOnProgress(t *testing.T) {
	srv := sseServer(t, anthropicToolArgsSSE([]string{`{"path":"a.svg",`, `"content":"`, strings.Repeat("x", 64), `"}`}))

	p := newClaudeProviderWithDelegate(anthropicprovider.NewProviderWithBaseURL("token", srv.URL))

	var events []protocoltypes.ToolCallProgress
	if _, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil, "claude-sonnet-4.6", nil, nil,
		func(pr protocoltypes.ToolCallProgress) { events = append(events, pr) },
	); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if len(events) == 0 {
		t.Fatal("ClaudeProvider accepted onProgress and never delivered a single event — this is the " +
			"same wrapper whose missing ChatStream once made every Anthropic call non-streaming with " +
			"no failing test anywhere")
	}
	if last := events[len(events)-1]; last.Name != "write_file" || last.ArgsBytes < 64 {
		t.Errorf("forwarded events look wrong: %+v", last)
	}
}

// TestOpenAICompatProvider_ChatStream_ForwardsOnProgress closes the third
// gap: every existing ChatStream test in that package passes nil for
// onProgress, and its progress tests call the internal parseStreamResponse
// directly — so the ChatStream-to-parser hand-off had no coverage at all.
func TestOpenAICompatProvider_ChatStream_ForwardsOnProgress(t *testing.T) {
	srv := sseServer(t, openAIToolArgsSSE([]string{`{"a":`, `1}`}))

	p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", srv.URL, "", "", 30, nil)
	if err != nil {
		t.Fatalf("constructing provider: %v", err)
	}

	var events int
	if _, err := p.delegate.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil, "gpt-4o", nil, nil,
		func(protocoltypes.ToolCallProgress) { events++ },
	); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if events == 0 {
		t.Fatal("openai_compat.Provider.ChatStream did not pass onProgress to parseStreamResponse")
	}
}
