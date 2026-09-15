package openai_compat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// Founder decision 2026-09-14 (UAT E-15c): reasoning counts as activity. A
// task attempt reasoned for up to ~21 minutes per call through OpenRouter; the
// parser read only content and tool-call deltas, so every one of those minutes
// was indistinguishable from a hung call. These tests pin that reasoning
// deltas now produce progress events, in each of the three spellings
// OpenAI-compatible providers use, without the reasoning text travelling
// anywhere.

// reasoningOnlyStream builds an SSE stream where each chunk is rendered by
// chunkJSON, followed by one short content delta and a clean finish.
func reasoningOnlyStream(chunks []string, chunkJSON func(string) string) string {
	var b strings.Builder
	for _, c := range chunks {
		fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":%s}]}\n\n", chunkJSON(c))
	}
	b.WriteString(`data: {"choices":[{"delta":{"content":"Done."},"finish_reason":"stop"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestParseStreamResponse_ReasoningDeltasCountAsProgress(t *testing.T) {
	// Chunk lengths are the oracle: the expected running total after chunk i is
	// the sum of len(chunks[0..i]), computed here from the fixture itself.
	chunks := []string{"Let me think", " about the invoice totals", strings.Repeat("r", 700)}

	cases := []struct {
		name      string
		chunkJSON func(string) string
	}{
		{
			// OpenRouter sends the normalised string AND the structured array
			// with the same text. Each byte must be counted once, not twice.
			name: "openrouter reasoning plus duplicate reasoning_details",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"content":"","reasoning":%q,"reasoning_details":[{"type":"reasoning.text","text":%q,"format":"unknown","index":0}]}`, c, c)
			},
		},
		{
			name: "zai and deepseek reasoning_content",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning_content":%q}`, c)
			},
		},
		{
			name: "encrypted reasoning_details only",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning_details":[{"type":"reasoning.encrypted","data":%q}]}`, c)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var textCallbacks []string
			var progress []protocoltypes.ToolCallProgress

			resp, err := parseStreamResponse(
				t.Context(),
				strings.NewReader(reasoningOnlyStream(chunks, tc.chunkJSON)),
				func(acc string) { textCallbacks = append(textCallbacks, acc) },
				func(p protocoltypes.ToolCallProgress) { progress = append(progress, p) }, nil)
			if err != nil {
				t.Fatalf("parseStreamResponse(, nil) error = %v", err)
			}

			if len(progress) != len(chunks) {
				t.Fatalf("expected one progress event per reasoning delta (%d), got %d: %+v",
					len(chunks), len(progress), progress)
			}
			want := 0
			for i, p := range progress {
				want += len(chunks[i])
				if p.ReasoningBytes != want {
					t.Errorf("progress[%d].ReasoningBytes = %d, want running total %d", i, p.ReasoningBytes, want)
				}
				if p.Index != protocoltypes.ReasoningProgressIndex {
					t.Errorf("progress[%d].Index = %d, want ReasoningProgressIndex (%d)",
						i, p.Index, protocoltypes.ReasoningProgressIndex)
				}
				if p.Name != "" || p.ArgsBytes != 0 || p.TotalArgsBytes != 0 {
					t.Errorf("progress[%d] claims a tool call during pure reasoning: %+v", i, p)
				}
			}

			// The reasoning text must not leak into the answer or the text
			// stream: only the final "Done." content counts as text.
			if resp.Content != "Done." {
				t.Errorf("resp.Content = %q, want %q (reasoning text must not become content)", resp.Content, "Done.")
			}
			if len(textCallbacks) != 1 || textCallbacks[0] != "Done." {
				t.Errorf("text callbacks = %q, want exactly [\"Done.\"]", textCallbacks)
			}
		})
	}
}

func TestParseStreamResponse_ToolCallProgressCarriesReasoningTotal(t *testing.T) {
	thinking := []string{"plan the file", " layout first"}
	wantReasoning := len(thinking[0]) + len(thinking[1])

	var b strings.Builder
	for _, c := range thinking {
		fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", c)
	}
	b.WriteString(toolArgsOnlyStream("write_file", []string{`{"path":"a.txt",`, `"content":"hi"}`}))

	var progress []protocoltypes.ToolCallProgress
	if _, err := parseStreamResponse(
		t.Context(),
		strings.NewReader(b.String()),
		nil,
		func(p protocoltypes.ToolCallProgress) { progress = append(progress, p) }, nil); err != nil {
		t.Fatalf("parseStreamResponse(, nil) error = %v", err)
	}

	var toolEvents int
	for _, p := range progress {
		if p.Index == protocoltypes.ReasoningProgressIndex {
			continue
		}
		toolEvents++
		if p.ReasoningBytes != wantReasoning {
			t.Errorf("tool-call event %+v carries ReasoningBytes %d, want the response's reasoning total %d",
				p, p.ReasoningBytes, wantReasoning)
		}
		if p.Name != "write_file" || p.ArgsBytes == 0 {
			t.Errorf("tool-call event lost its tool identity: %+v", p)
		}
	}
	if toolEvents != 2 {
		t.Fatalf("expected 2 tool-call argument events, got %d (all: %+v)", toolEvents, progress)
	}
}

// A content-only stream carries no reasoning and no tool call, so it must not
// start emitting progress events now that reasoning is read.
func TestParseStreamResponse_ContentOnlyStreamEmitsNoProgress(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":" there","reasoning":""},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	var progress []protocoltypes.ToolCallProgress
	if _, err := parseStreamResponse(
		t.Context(),
		strings.NewReader(stream),
		nil,
		func(p protocoltypes.ToolCallProgress) { progress = append(progress, p) }, nil); err != nil {
		t.Fatalf("parseStreamResponse(, nil) error = %v", err)
	}
	if len(progress) != 0 {
		t.Fatalf("content-only stream emitted %d progress events: %+v", len(progress), progress)
	}
}
