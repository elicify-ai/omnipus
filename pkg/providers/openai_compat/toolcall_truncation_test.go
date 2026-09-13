// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package openai_compat

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// TestParseStreamResponse_RefusesTruncatedToolCallArguments is the streaming
// half of the truncation gate, and the streaming path is where this fault
// actually lands: arguments arrive as a run of deltas appended into a buffer,
// and a generation that hits the output cap simply stops mid-run.
//
// Read what toolArgsOnlyStream appends after the argument chunks: a final
// frame carrying `finish_reason: "tool_calls"`. That is not an artifact of
// the fixture, it is the defect being guarded against. vLLM's streaming
// handler sets its "this choice produced tool calls" flag on any delta
// containing one, with NO completeness check, and then reports "tool_calls"
// in place of the engine's real "length" (vllm#47903, open; the proposed fix
// vllm#47963 unreviewed since 2026-07-08, and the merged non-streaming fix is
// gated on a flag the Hermes path does not set).
//
// So this stream affirmatively claims to have delivered a complete tool call
// while delivering a fragment. The test asserts we catch it anyway — because
// we decode the arguments rather than trusting the field the upstream
// overwrote.
//
// Before the fix this returned a response, nil — with the tool call present
// and its Arguments map carrying a fabricated "raw" key.
func TestParseStreamResponse_RefusesTruncatedToolCallArguments(t *testing.T) {
	cases := []struct {
		name      string
		argChunks []string
	}{
		{
			// Cut mid-key: the shape that motivated the fix.
			name:      "cut mid-key",
			argChunks: []string{`{"query`},
		},
		{
			// The llama.cpp shape (llama.cpp#21771): a single open brace.
			name:      "bare open brace",
			argChunks: []string{`{`},
		},
		{
			// Realistic large write cut mid-value — several deltas landed
			// before the cap, so the buffer looks substantial and is still
			// unusable.
			name:      "large write cut mid-value",
			argChunks: []string{`{"path":"a.svg",`, `"content":"<svg>`, strings.Repeat("x", 512)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := toolArgsOnlyStream("write_file", tc.argChunks)

			resp, err := parseStreamResponse(
				t.Context(), strings.NewReader(stream), nil, nil,
			)

			if err == nil {
				t.Fatalf("stream with truncated tool-call arguments was accepted; "+
					"finish_reason claimed \"tool_calls\" and we believed it: %+v", resp)
			}
			if !errors.Is(err, common.ErrToolArgumentsUndecodable) {
				t.Errorf("error %v does not wrap common.ErrToolArgumentsUndecodable", err)
			}
			if resp != nil {
				t.Errorf("resp = %+v, want nil so nothing can be dispatched", resp)
			}
		})
	}
}

// TestParseStreamResponse_NoStandInKeyReachesDispatch pins the defect by
// name on the streaming path: whatever else happens, a fabricated "raw"
// parameter must never survive into a tool call's arguments.
func TestParseStreamResponse_NoStandInKeyReachesDispatch(t *testing.T) {
	stream := toolArgsOnlyStream("write_file", []string{`{"query`})

	resp, _ := parseStreamResponse(t.Context(), strings.NewReader(stream), nil, nil)
	if resp == nil {
		return // refused outright, which is the intended outcome
	}
	for _, tc := range resp.ToolCalls {
		for _, key := range []string{"raw", "_raw"} {
			if _, present := tc.Arguments[key]; present {
				t.Errorf("tool %q dispatched with fabricated parameter %q = %v",
					tc.Name, key, tc.Arguments[key])
			}
		}
	}
}

// TestParseStreamResponse_AcceptsZeroParameterToolCall is the guard against
// over-correction on the streaming path.
//
// A zero-parameter tool streams either an explicit `{}` or no argument deltas
// at all. Both must still produce a dispatchable call — if the gate treated
// "no arguments" as "truncated", every list_mounts or browser_snapshot call
// would become a hard turn failure.
func TestParseStreamResponse_AcceptsZeroParameterToolCall(t *testing.T) {
	cases := []struct {
		name      string
		argChunks []string
	}{
		{"explicit empty object", []string{`{}`}},
		{"no argument deltas at all", nil},
		{"empty-string delta", []string{``}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := toolArgsOnlyStream("list_mounts", tc.argChunks)

			resp, err := parseStreamResponse(
				t.Context(), strings.NewReader(stream), nil, nil,
			)
			if err != nil {
				t.Fatalf("zero-parameter tool call must still stream cleanly, got: %v", err)
			}
			if len(resp.ToolCalls) != 1 {
				t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
			}
			call := resp.ToolCalls[0]
			if call.Name != "list_mounts" {
				t.Errorf("Name = %q, want list_mounts", call.Name)
			}
			if call.Arguments == nil {
				t.Error("Arguments = nil, want an empty non-nil map")
			}
			if len(call.Arguments) != 0 {
				t.Errorf("Arguments = %v, want empty", call.Arguments)
			}
		})
	}
}
