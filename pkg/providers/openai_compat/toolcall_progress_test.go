package openai_compat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// toolArgsOnlyStream builds an SSE stream that contains NO text deltas at all
// — only a tool call whose arguments arrive in many small pieces. This is the
// exact shape a model produces when it answers by writing a large file: a
// brief (or absent) preamble, then kilobytes of tool-call arguments.
func toolArgsOnlyStream(toolName string, argChunks []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":%q,"arguments":""}}]}}]}`+"\n\n",
		toolName)
	for _, c := range argChunks {
		fmt.Fprintf(&b, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`+"\n\n", c)
	}
	b.WriteString(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// TestParseStreamResponse_EmitsProgressForToolCallArguments is the regression
// test for the "healthy worker looks hung" outage.
//
// Previously onChunk was invoked ONLY from the text-delta branch; tool-call
// argument deltas were accumulated silently. A model spending 45 seconds
// emitting a multi-kilobyte SVG inside a tool argument therefore produced
// zero observable bytes on every surface, which is indistinguishable from a
// hung generation. An orchestrator polled such a worker 75 times, saw
// nothing, and killed it mid-write — repeatedly.
//
// This test asserts that a tool-args-only stream produces forward-progress
// signals. It fails on the old code, which emitted none.
func TestParseStreamResponse_EmitsProgressForToolCallArguments(t *testing.T) {
	argChunks := []string{`{"path":"a.svg",`, `"content":"<svg>`, strings.Repeat("x", 512), `</svg>"}`}
	stream := toolArgsOnlyStream("write_file", argChunks)

	var textCallbacks int
	var progress []protocoltypes.ToolCallProgress

	resp, err := parseStreamResponse(
		t.Context(),
		strings.NewReader(stream),
		func(string) { textCallbacks++ },
		func(p protocoltypes.ToolCallProgress) { progress = append(progress, p) },
	)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}

	// The stream has no text at all — this is the whole point.
	if textCallbacks != 0 {
		t.Fatalf("expected no text callbacks for a tool-args-only stream, got %d", textCallbacks)
	}

	if len(progress) == 0 {
		t.Fatal("no progress callbacks emitted while streaming tool-call arguments — " +
			"a generating worker is indistinguishable from a hung one, which is the bug this guards")
	}
	if len(progress) != len(argChunks) {
		t.Errorf("expected one progress event per argument delta (%d), got %d", len(argChunks), len(progress))
	}

	// Progress must be monotonic and must reach the full argument size, so a
	// consumer can tell forward motion from a stall.
	var prev int
	for i, p := range progress {
		if p.ArgsBytes <= prev {
			t.Errorf("progress[%d].ArgsBytes=%d did not advance past %d", i, p.ArgsBytes, prev)
		}
		prev = p.ArgsBytes
		if p.Index != 0 {
			t.Errorf("progress[%d].Index=%d, want 0", i, p.Index)
		}
		if p.Name != "write_file" {
			t.Errorf("progress[%d].Name=%q, want write_file", i, p.Name)
		}
	}

	wantBytes := 0
	for _, c := range argChunks {
		wantBytes += len(c)
	}
	last := progress[len(progress)-1]
	if last.ArgsBytes != wantBytes {
		t.Errorf("final ArgsBytes=%d, want %d", last.ArgsBytes, wantBytes)
	}
	if last.TotalArgsBytes != wantBytes {
		t.Errorf("final TotalArgsBytes=%d, want %d", last.TotalArgsBytes, wantBytes)
	}

	// The response itself must be unaffected by progress reporting.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if got := resp.ToolCalls[0].Name; got != "write_file" {
		t.Errorf("ToolCalls[0].Name = %q, want write_file", got)
	}
	if got := resp.ToolCalls[0].Arguments["path"]; got != "a.svg" {
		t.Errorf("ToolCalls[0].Arguments[path] = %v, want a.svg", got)
	}
	content, _ := resp.ToolCalls[0].Arguments["content"].(string)
	if !strings.HasPrefix(content, "<svg>") || !strings.HasSuffix(content, "</svg>") {
		t.Errorf("argument content corrupted by progress reporting: %.40q...", content)
	}
	if len(content) != len("<svg>")+512+len("</svg>") {
		t.Errorf("argument content truncated: got %d bytes", len(content))
	}
}

// TestParseStreamResponse_NilProgressCallbackIsSafe guards the opt-out path:
// every existing caller passes nil and must keep working untouched.
func TestParseStreamResponse_NilProgressCallbackIsSafe(t *testing.T) {
	stream := toolArgsOnlyStream("write_file", []string{`{"a":`, `1}`})
	resp, err := parseStreamResponse(t.Context(), strings.NewReader(stream), nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
}

// TestParseStreamResponse_PanickingProgressHandlerDoesNotKillTheStream is the
// production-caller half of ADR-059 AC-06.
//
// The handler runs synchronously inside this SSE read loop. Without a guard a
// panic in a consumer unwinds through the parser and takes the whole turn with
// it — a monitoring signal killing the work it monitors, strictly worse than
// the blindness the callback exists to fix. The stream must complete and the
// tool call must parse intact even when every single delta panics.
func TestParseStreamResponse_PanickingProgressHandlerDoesNotKillTheStream(t *testing.T) {
	argChunks := []string{`{"path":"a.svg",`, `"content":"<svg>`, strings.Repeat("x", 256), `</svg>"}`}
	stream := toolArgsOnlyStream("write_file", argChunks)

	var calls int
	resp, err := parseStreamResponse(
		t.Context(),
		strings.NewReader(stream),
		nil,
		func(protocoltypes.ToolCallProgress) {
			calls++
			panic("consumer handler is broken")
		},
	)
	if err != nil {
		t.Fatalf("a panicking progress handler broke the stream: %v", err)
	}
	if calls != len(argChunks) {
		t.Errorf("handler invoked %d times, want %d — the guard must be per-call, "+
			"not a one-shot that silently stops reporting after the first panic",
			calls, len(argChunks))
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call after a panicking handler, got %d", len(resp.ToolCalls))
	}
	content, _ := resp.ToolCalls[0].Arguments["content"].(string)
	if len(content) != len("<svg>")+256+len("</svg>") {
		t.Errorf("tool arguments truncated by the panicking handler: got %d bytes", len(content))
	}
}
