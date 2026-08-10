package anthropicprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// toolUseStreamServer serves an Anthropic SSE stream whose only content is a
// single tool_use block whose JSON arguments arrive as many partial_json
// deltas. This is the exact shape a model produces when it answers by writing
// a large file: no text at all, kilobytes of tool-call arguments.
func toolUseStreamServer(t *testing.T, toolName string, argChunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			if _, err := w.Write([]byte(s)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"," +
			"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\"," +
			"\"stop_reason\":null,\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n")
		write(fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,"+
			"\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":%q,\"input\":{}}}\n\n", toolName))
		for _, c := range argChunks {
			// json.Marshal, not %q: Go quoting and JSON string escaping agree
			// on this test's ASCII payloads but not in general, and a stream
			// fixture that is subtly invalid JSON would fail for the wrong
			// reason.
			b, err := json.Marshal(c)
			if err != nil {
				t.Errorf("marshalling argument chunk: %v", err)
				return
			}
			write(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,"+
				"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n", b))
		}
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		write("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}," +
			"\"usage\":{\"output_tokens\":7}}\n\n")
		write("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
}

// TestChatStream_EmitsProgressForToolCallArguments is the Anthropic half of
// the "healthy worker looks hung" regression, driven through the real
// ChatStream entry point rather than an internal helper.
//
// It also pins the stale-accessor defect that made the first version of this
// reporting useless: AsToolUse() re-unmarshals from ContentBlockUnion.JSON.raw,
// which is only populated at content_block_start and re-marshalled at
// content_block_stop. The deltas in between mutate the union's direct fields
// and never touch raw.
//
// The assertion that catches this is MID-FLIGHT VISIBILITY, not event count.
// Measured against this same stream (anthropic-sdk-go v1.48.0): reading via
// the accessors yields exactly two events, [2, 360] — 2 bytes for "{}" when
// the block opens, then the whole 360 bytes at once when it closes. Reading
// the direct fields yields [2, 16, 32, 332, 340]. A naive "more than one
// event" check therefore passes on the broken version, because the useless
// close-time event counts. What distinguishes them is whether ANY event
// lands strictly between the opening and the final byte count — that is the
// difference between "I can see it working" and "I found out afterwards",
// and afterwards is what got a healthy worker killed.
func TestChatStream_EmitsProgressForToolCallArguments(t *testing.T) {
	argChunks := []string{`{"path":"a.svg",`, `"content":"<svg>`, strings.Repeat("x", 300), `</svg>"}`}
	server := toolUseStreamServer(t, "write_file", argChunks)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var progress []protocoltypes.ToolCallProgress
	var textCallbacks int
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "draw something"}},
		nil,
		"claude-sonnet-4.6",
		nil,
		func(string) { textCallbacks++ },
		func(pr protocoltypes.ToolCallProgress) { progress = append(progress, pr) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if textCallbacks != 0 {
		t.Errorf("expected no text callbacks for a tool-args-only stream, got %d", textCallbacks)
	}
	if len(progress) < len(argChunks) {
		t.Fatalf("got %d progress events for a %d-delta tool-argument stream — a generating worker is "+
			"indistinguishable from a hung one, which is the bug this guards",
			len(progress), len(argChunks))
	}

	var prev int
	for i, pr := range progress {
		if pr.ArgsBytes <= prev {
			t.Errorf("progress[%d].ArgsBytes=%d did not advance past %d", i, pr.ArgsBytes, prev)
		}
		prev = pr.ArgsBytes
		if pr.Name != "write_file" {
			t.Errorf("progress[%d].Name=%q, want write_file", i, pr.Name)
		}
	}
	final := progress[len(progress)-1].ArgsBytes
	if final < 300 {
		t.Errorf("final ArgsBytes=%d, want at least the 300-byte payload", final)
	}

	// Mid-flight visibility: at least one report strictly between the opening
	// "{}" and the finished argument. Without it the caller learns the size
	// only once the block has already closed, which is exactly as useless as
	// no reporting at all. This is the assertion that fails if anyone reads
	// through the As*() accessors again.
	var midFlight int
	for _, pr := range progress {
		if pr.ArgsBytes > 2 && pr.ArgsBytes < final {
			midFlight++
		}
	}
	if midFlight == 0 {
		t.Errorf("no progress reported BETWEEN the block opening (2 bytes) and closing (%d bytes): %v — "+
			"the caller only finds out after the fact, which is the stale-accessor blackout this guards",
			final, argsBytesOf(progress))
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if got := resp.ToolCalls[0].Name; got != "write_file" {
		t.Errorf("ToolCalls[0].Name = %q, want write_file", got)
	}
	content, _ := resp.ToolCalls[0].Arguments["content"].(string)
	if len(content) != len("<svg>")+300+len("</svg>") {
		t.Errorf("argument content corrupted or truncated by progress reporting: %d bytes", len(content))
	}
}

// TestChatStream_PanickingProgressHandlerDoesNotKillTheStream is the Anthropic
// production-caller half of ADR-059 AC-06: a broken consumer must not take
// down the turn it is monitoring.
func TestChatStream_PanickingProgressHandlerDoesNotKillTheStream(t *testing.T) {
	argChunks := []string{`{"path":"a.svg",`, `"content":"<svg>`, strings.Repeat("x", 128), `</svg>"}`}
	server := toolUseStreamServer(t, "write_file", argChunks)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var calls int
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "draw something"}},
		nil,
		"claude-sonnet-4.6",
		nil,
		nil,
		func(protocoltypes.ToolCallProgress) {
			calls++
			panic("consumer handler is broken")
		},
	)
	if err != nil {
		t.Fatalf("a panicking progress handler broke the stream: %v", err)
	}
	if calls < 2 {
		t.Errorf("handler invoked %d times — the guard must be per-call, not a one-shot that stops "+
			"reporting after the first panic", calls)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call after a panicking handler, got %d", len(resp.ToolCalls))
	}
	content, _ := resp.ToolCalls[0].Arguments["content"].(string)
	if len(content) != len("<svg>")+128+len("</svg>") {
		t.Errorf("tool arguments truncated by the panicking handler: %d bytes", len(content))
	}
}

// TestChatStream_NilProgressCallbackIsSafe guards the opt-out path.
func TestChatStream_NilProgressCallbackIsSafe(t *testing.T) {
	server := toolUseStreamServer(t, "write_file", []string{`{"a":`, `1}`})
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"claude-sonnet-4.6",
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
}

// argsBytesOf renders a progress sequence compactly for failure messages —
// the shape of the sequence is the diagnosis.
func argsBytesOf(ps []protocoltypes.ToolCallProgress) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.ArgsBytes
	}
	return out
}
