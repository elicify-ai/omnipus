package openai_compat

import (
	"fmt"
	"strings"
	"testing"
)

// WP-B (thinking-and-reasoning-effort): the streaming path parses reasoning
// deltas to count bytes for the stall watchdog, then drops the text (spec
// CF1). D5a requires the reasoning TEXT to be kept: the final LLMResponse
// must carry it spelling for spelling, exactly as the non-streaming path
// (common.ParseResponse) already does for equivalent input.
//
// Oracles (never the implementation):
//   - docs/internal/specs/thinking-reasoning-spec.md §2.2 openai_compat row
//     ("keeps the reasoning text as ReasoningContent", D5a);
//   - §1 C3 capture order: Reasoning first, falling back to ReasoningContent;
//   - reasoningDeltaBytes' doc comment: per delta the first non-empty
//     spelling wins (reasoning > reasoning_content > reasoning_details) so
//     OpenRouter's duplicated string+details payload is kept once, not twice.
//
// Spelling → field mapping mirrors common.ParseResponse: reasoning →
// Reasoning, reasoning_content → ReasoningContent. Text-bearing
// reasoning_details have no same-named string field; their display text is
// asserted to land in ReasoningContent (derived mapping — spec names
// ReasoningContent as where kept streaming reasoning text goes).

// reasoningCaptureChunks is the fixture reasoning text, streamed as three
// partial deltas the way DeepSeek/Z.AI stream reasoning_content: the final
// accumulated value must be the concatenation, never one chunk alone.
var reasoningCaptureChunks = []string{"First, check the invoice", " then sum the totals", " carefully."}

var wantReasoningText = strings.Join(reasoningCaptureChunks, "")

func TestParseStreamResponse_KeepsStreamedReasoningTextInResponseFields(t *testing.T) {
	cases := []struct {
		name                 string
		chunkJSON            func(string) string
		wantReasoning        string
		wantReasoningContent string
	}{
		{
			name: "deepseek/zai reasoning_content deltas accumulate into ReasoningContent",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning_content":%q}`, c)
			},
			wantReasoning:        "",
			wantReasoningContent: wantReasoningText,
		},
		{
			name: "openrouter reasoning deltas accumulate into Reasoning",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning":%q}`, c)
			},
			wantReasoning:        wantReasoningText,
			wantReasoningContent: "",
		},
		{
			// OpenRouter sends the normalised string AND the structured array
			// in the same delta with the same text. The text must be kept
			// once: wantReasoningText, not a doubled copy.
			name: "openrouter duplicate reasoning string plus reasoning_details is kept once",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning":%q,"reasoning_details":[{"type":"reasoning.text","text":%q,"format":"unknown","index":0}]}`, c, c)
			},
			wantReasoning:        wantReasoningText,
			wantReasoningContent: "",
		},
		{
			// Encrypted detail bytes are opaque ciphertext, not display text —
			// the reasoning capture carries display text, never signature or
			// ciphertext data (spec §2.2 callback row). They must reach
			// neither display field.
			name: "encrypted reasoning_details carry no display text",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning_details":[{"type":"reasoning.encrypted","data":%q}]}`, c)
			},
			wantReasoning:        "",
			wantReasoningContent: "",
		},
		{
			name: "text-only reasoning_details land in ReasoningContent",
			chunkJSON: func(c string) string {
				return fmt.Sprintf(`{"reasoning_details":[{"type":"reasoning.text","text":%q,"format":"unknown","index":0}]}`, c)
			},
			wantReasoning:        "",
			wantReasoningContent: wantReasoningText,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var textCallbacks []string
			resp, err := parseStreamResponse(
				t.Context(),
				strings.NewReader(reasoningOnlyStream(reasoningCaptureChunks, tc.chunkJSON)),
				func(acc string) { textCallbacks = append(textCallbacks, acc) },
				nil, nil, nil)
			if err != nil {
				t.Fatalf("parseStreamResponse() error = %v", err)
			}

			if resp.Reasoning != tc.wantReasoning {
				t.Errorf("resp.Reasoning = %q, want %q", resp.Reasoning, tc.wantReasoning)
			}
			if resp.ReasoningContent != tc.wantReasoningContent {
				t.Errorf("resp.ReasoningContent = %q, want %q", resp.ReasoningContent, tc.wantReasoningContent)
			}

			// The reasoning text must stay out of the answer and out of the
			// live text stream: only the final content delta counts as text
			// (pin from reasoning_progress_test.go, must survive the fix).
			if resp.Content != "Done." {
				t.Errorf("resp.Content = %q, want %q (reasoning must never become answer text)", resp.Content, "Done.")
			}
			if len(textCallbacks) != 1 || textCallbacks[0] != "Done." {
				t.Errorf("text callbacks = %q, want exactly [%q]", textCallbacks, "Done.")
			}
			if resp.FinishReason != "stop" {
				t.Errorf("resp.FinishReason = %q, want %q", resp.FinishReason, "stop")
			}
		})
	}
}

// A stream may mix spellings across deltas. The precedence order is per
// delta (reasoningDeltaBytes' doc): each delta's winning text accumulates
// into that spelling's own response field, and no text is dropped.
func TestParseStreamResponse_MixedReasoningSpellingsAccumulatePerField(t *testing.T) {
	var b strings.Builder
	fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":{\"reasoning\":%q}}]}\n\n", "Route A first. ")
	fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", "Then route B.")
	b.WriteString(`data: {"choices":[{"delta":{"content":"Done."},"finish_reason":"stop"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")

	resp, err := parseStreamResponse(t.Context(), strings.NewReader(b.String()), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}

	if resp.Reasoning != "Route A first. " {
		t.Errorf("resp.Reasoning = %q, want %q", resp.Reasoning, "Route A first. ")
	}
	if resp.ReasoningContent != "Then route B." {
		t.Errorf("resp.ReasoningContent = %q, want %q", resp.ReasoningContent, "Then route B.")
	}
	if resp.Content != "Done." {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Done.")
	}
}
