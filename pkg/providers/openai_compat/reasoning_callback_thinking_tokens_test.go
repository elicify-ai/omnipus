package openai_compat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// WP-B (thinking-and-reasoning-effort), two behaviours:
//
//  1. The new gated accumulated-text reasoning callback (spec
//     docs/internal/specs/thinking-reasoning-spec.md §2.2 protocoltypes
//     provider-streaming-callback row, D5a/D5b): invoked with the reasoning
//     display text accumulated so far on each reasoning-bearing delta,
//     implemented spellings-independently, nil-safe (nil = the gate is off),
//     carrying display text only — the func(string) shape has nowhere for a
//     signature or ciphertext — and never touching the existing
//     ReasoningBytes watchdog-progress path.
//
//  2. Usage-totals thinking tokens (spec §1 C7): an OpenAI-compatible usage
//     object's completion_tokens_details.reasoning_tokens lands in
//     UsageInfo.ThinkingTokens when the provider reports one, and stays zero
//     (never a guessed default) when it does not. Thinking tokens are a
//     SUBSET of completion tokens (C7) — the parse must not adjust
//     CompletionTokens. The parse is shared by both paths through
//     common.OpenAINonStreamUsage.ToUsageInfo (openai_compat's SSE final
//     usage chunk and common.ParseResponse's non-streaming body), so one
//     unit test pins the mapping and one end-to-end test per path pins the
//     wiring.

// TestParseStreamResponse_ReasoningCallbackCarriesAccumulatedText asserts the
// callback fires once per reasoning-bearing delta with the running display
// text, for each spelling, and that content deltas never fire it.
func TestParseStreamResponse_ReasoningCallbackCarriesAccumulatedText(t *testing.T) {
	chunks := []string{"Think ", "hard", " about it."}
	wantCalls := []string{"Think ", "Think hard", "Think hard about it."}
	full := strings.Join(chunks, "")

	cases := []struct {
		name      string
		chunkJSON func(string) string
		// which response field must carry the same text at stream end
		wantField string // "Reasoning" or "ReasoningContent"
	}{
		{
			name:      "reasoning_content deltas",
			chunkJSON: func(c string) string { return fmt.Sprintf(`{"reasoning_content":%q}`, c) },
			wantField: "ReasoningContent",
		},
		{
			name:      "openrouter reasoning deltas",
			chunkJSON: func(c string) string { return fmt.Sprintf(`{"reasoning":%q}`, c) },
			wantField: "Reasoning",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			for _, c := range chunks {
				fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":%s}]}\n\n", tc.chunkJSON(c))
			}
			b.WriteString(`data: {"choices":[{"delta":{"content":"Answer."},"finish_reason":"stop"}]}` + "\n\n")
			b.WriteString("data: [DONE]\n\n")

			var calls []string
			resp, err := parseStreamResponse(
				t.Context(),
				strings.NewReader(b.String()),
				nil,
				nil,
				func(accumulated string) { calls = append(calls, accumulated) },
				nil)
			if err != nil {
				t.Fatalf("parseStreamResponse() error = %v", err)
			}

			// Exactly one call per reasoning delta, each with the cumulative
			// value — not the per-delta fragment, and not fired by the
			// content delta.
			if len(calls) != len(wantCalls) {
				t.Fatalf("reasoning callback fired %d times, want %d; calls=%q", len(calls), len(wantCalls), calls)
			}
			for i, call := range calls {
				if call != wantCalls[i] {
					t.Errorf("reasoning callback call[%d] = %q, want %q", i, call, wantCalls[i])
				}
			}

			switch tc.wantField {
			case "Reasoning":
				if resp.Reasoning != full {
					t.Errorf("resp.Reasoning = %q, want %q", resp.Reasoning, full)
				}
			case "ReasoningContent":
				if resp.ReasoningContent != full {
					t.Errorf("resp.ReasoningContent = %q, want %q", resp.ReasoningContent, full)
				}
			}
		})
	}
}

// TestChatStream_NilReasoningCallbackIsSafeAndKeepsProgressAlive asserts the
// gate-off shape end to end: onReasoning == nil must not panic, must not
// change the parsed response, and must not touch the ReasoningBytes
// watchdog-progress events (they keep firing for the stall monitor).
func TestChatStream_NilReasoningCallbackIsSafeAndKeepsProgressAlive(t *testing.T) {
	thinking := []string{"Weigh the options ", "then pick one."}
	full := strings.Join(thinking, "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)
		for _, c := range thinking {
			fmt.Fprintf(bw, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", c)
		}
		fmt.Fprintf(bw, "data: {\"choices\":[{\"delta\":{\"content\":\"The answer.\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	var textCallbacks []string
	var progress []protocoltypes.ToolCallProgress
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		nil,
		func(accumulated string) { textCallbacks = append(textCallbacks, accumulated) },
		func(tp protocoltypes.ToolCallProgress) { progress = append(progress, tp) },
		nil, // onReasoning: the gate is off — nil must be safe and change nothing
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if resp.ReasoningContent != full {
		t.Errorf("resp.ReasoningContent = %q, want %q (nil onReasoning must not change the response)", resp.ReasoningContent, full)
	}
	if len(textCallbacks) != 1 || textCallbacks[0] != "The answer." {
		t.Errorf("text callbacks = %q, want exactly [%q]", textCallbacks, "The answer.")
	}

	// The watchdog's byte-count progress path must be untouched by the gated
	// text callback: one event per reasoning delta with running totals.
	if len(progress) != len(thinking) {
		t.Fatalf("progress events = %d, want %d (ReasoningBytes path must keep firing): %+v", len(progress), len(thinking), progress)
	}
	want := 0
	for i, tp := range progress {
		want += len(thinking[i])
		if tp.ReasoningBytes != want {
			t.Errorf("progress[%d].ReasoningBytes = %d, want running total %d", i, tp.ReasoningBytes, want)
		}
	}
}

// TestOpenAINonStreamUsage_ReasoningTokensBecomeThinkingTokens pins the
// shared parse (both the streaming final-usage chunk and the non-streaming
// body funnel through ToUsageInfo). Whole-struct equality: the new field
// must carry the reported value and every existing field must be unchanged —
// CompletionTokens stays the provider's own total (C7: thinking tokens are a
// subset of output, never added on top).
func TestOpenAINonStreamUsage_ReasoningTokensBecomeThinkingTokens(t *testing.T) {
	cases := []struct {
		name      string
		usageJSON string
		want      UsageInfo
	}{
		{
			name:      "reported reasoning_tokens lands in ThinkingTokens, CompletionTokens untouched",
			usageJSON: `{"prompt_tokens":25,"completion_tokens":100,"total_tokens":125,"completion_tokens_details":{"reasoning_tokens":42}}`,
			want:      UsageInfo{PromptTokens: 25, CompletionTokens: 100, TotalTokens: 125, ThinkingTokens: 42},
		},
		{
			name:      "unreported reasoning_tokens leaves ThinkingTokens zero, never a guessed default",
			usageJSON: `{"prompt_tokens":25,"completion_tokens":100,"total_tokens":125}`,
			want:      UsageInfo{PromptTokens: 25, CompletionTokens: 100, TotalTokens: 125},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var u common.OpenAINonStreamUsage
			if err := json.Unmarshal([]byte(tc.usageJSON), &u); err != nil {
				t.Fatalf("json.Unmarshal(usage) error = %v", err)
			}
			got := u.ToUsageInfo()
			if got == nil {
				t.Fatal("ToUsageInfo() = nil, want a populated UsageInfo")
			}
			if *got != tc.want {
				t.Errorf("ToUsageInfo() = %+v, want %+v", *got, tc.want)
			}
		})
	}
}

// TestChatStream_FinalUsageChunkThinkingTokens asserts the streaming wiring
// end to end: the SSE final usage chunk (stream_options.include_usage
// shape) carries completion_tokens_details.reasoning_tokens into
// resp.Usage.ThinkingTokens.
func TestChatStream_FinalUsageChunkThinkingTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)
		fmt.Fprintf(bw, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\n")
		fmt.Fprintf(bw, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(bw, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":25,\"completion_tokens\":100,\"total_tokens\":125,\"completion_tokens_details\":{\"reasoning_tokens\":42}}}\n\n")
		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil, "gpt-4o", nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if resp.Usage == nil {
		t.Fatal("resp.Usage = nil, want the final usage chunk parsed")
	}
	if resp.Usage.ThinkingTokens != 42 {
		t.Errorf("resp.Usage.ThinkingTokens = %d, want 42", resp.Usage.ThinkingTokens)
	}
	if resp.Usage.CompletionTokens != 100 {
		t.Errorf("resp.Usage.CompletionTokens = %d, want 100 (thinking tokens are a subset, never added on top)", resp.Usage.CompletionTokens)
	}
}

// TestParseResponse_NonStreamingReasoningAndThinkingTokens pins the
// non-streaming path end to end. The reasoning_content half is the ORACLE
// for TestParseStreamResponse_KeepsStreamedReasoningTextInResponseFields:
// for equivalent input the non-streaming path already keeps the text, and
// the streaming path's final value must equal it. The ThinkingTokens half
// pins C7's non-streaming parse point (same shared ToUsageInfo).
func TestParseResponse_NonStreamingReasoningAndThinkingTokens(t *testing.T) {
	body := strings.NewReader(`{
		"choices": [{
			"message": {"content": "The answer.", "reasoning_content": "silent thoughts"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 50, "total_tokens": 60,
			"completion_tokens_details": {"reasoning_tokens": 42}}
	}`)

	resp, err := common.ParseResponse(body)
	if err != nil {
		t.Fatalf("common.ParseResponse() error = %v", err)
	}

	// Control (passes today): non-streaming already keeps the reasoning text.
	if resp.ReasoningContent != "silent thoughts" {
		t.Errorf("resp.ReasoningContent = %q, want %q", resp.ReasoningContent, "silent thoughts")
	}

	if resp.Usage == nil {
		t.Fatal("resp.Usage = nil, want the usage object parsed")
	}
	if resp.Usage.ThinkingTokens != 42 {
		t.Errorf("resp.Usage.ThinkingTokens = %d, want 42", resp.Usage.ThinkingTokens)
	}
	if resp.Usage.CompletionTokens != 50 {
		t.Errorf("resp.Usage.CompletionTokens = %d, want 50", resp.Usage.CompletionTokens)
	}
}
