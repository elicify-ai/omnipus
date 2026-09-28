// Omnipus — WP-F RED pack, stage B: the mid-turn-rebuild half (spec Section 16
// test 14, US-5 AS 5 "Thinking-enabled Anthropic tool turn survives a mid-turn
// context rebuild").
//
// Spec sources:
//   - ADR-095 D6: "Round-trip echoes every block byte-exact and in order".
//   - ADR-095 D8 (F12's sweep, adjacent consumers): "pkg/agent/msg_normalize.go
//     preserves ReasoningContent through merges — it must preserve
//     ThinkingBlocks identically (byte-exact, order-stable); pkg/agent/
//     context_budget.go and pkg/utils/context.go count ReasoningContent — they
//     must count ThinkingBlocks (Thinking/Data lengths) or thinking is
//     under-counted against the budget".
//   - The merge rule mirrors the ReasoningContent precedent (first-non-empty
//     wins), the exact rule appendOrMergePlainText applies today.
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

var (
	// rebuildBlocksA/B are distinct block lists so "which list survived" is
	// observable byte-exact. Multi-byte text rides along (D8 requires
	// byte-exactness, not ASCII-only round-trips).
	rebuildBlocksA = []providers.ThinkingBlock{
		{Type: "thinking", Thinking: "plan: first think → 回答の前に", Signature: "sig-A=="},
		{Type: "redacted_thinking", Data: "opaque-A"},
	}
	rebuildBlocksB = []providers.ThinkingBlock{
		{Type: "thinking", Thinking: "a different thought", Signature: "sig-B=="},
	}
)

// Rule C: an assistant message carrying ToolCalls passes through verbatim —
// including the new blocks. A rebuild that dropped or reshaped them would
// break the next thinking-enabled request (and D8.5's guard would silently
// omit thinking from then on).
func TestNormalize_RuleC_AssistantToolCallsPassThroughWithBlocks(t *testing.T) {
	in := []providers.Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:           "assistant",
			Content:        "Working.",
			ThinkingBlocks: rebuildBlocksA,
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "SF"}},
			},
		},
		{Role: "tool", Content: `{"temp":72}`, ToolCallID: "call_1"},
	}

	out := normalizeMessagesForProvider(in)
	if len(out) != 3 {
		t.Fatalf("normalizeMessagesForProvider len = %d, want 3 — Rule C passes the assistant verbatim, Rule D keeps the paired tool result", len(out))
	}

	got := out[1]
	if got.Role != "assistant" {
		t.Fatalf("out[1].Role = %q, want assistant", got.Role)
	}
	if got.Content != "Working." {
		t.Errorf("out[1].Content = %q, want verbatim %q (Rule C)", got.Content, "Working.")
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" {
		t.Errorf("out[1].ToolCalls = %+v, want the one call verbatim (Rule C)", got.ToolCalls)
	}
	if len(got.ThinkingBlocks) != len(rebuildBlocksA) {
		t.Fatalf("out[1].ThinkingBlocks len = %d, want %d — Rule C must not drop the blocks", len(got.ThinkingBlocks), len(rebuildBlocksA))
	}
	for i := range rebuildBlocksA {
		if got.ThinkingBlocks[i] != rebuildBlocksA[i] {
			t.Errorf("out[1].ThinkingBlocks[%d] = %+v, want %+v (byte-exact, order-stable, ADR-095 D8)", i, got.ThinkingBlocks[i], rebuildBlocksA[i])
		}
	}
}

// A well-formed history that needs NO normalization rule must pass through
// unchanged — the blocks ride along untouched (D8: preserve byte-exact,
// order-stable) on the allocation-free happy path.
func TestNormalize_WellFormedHistoryWithBlocks_PassesThroughUnchanged(t *testing.T) {
	in := []providers.Message{
		{Role: "user", Content: "question"},
		{
			Role:           "assistant",
			Content:        "answer",
			ThinkingBlocks: rebuildBlocksA,
		},
	}

	out := normalizeMessagesForProvider(in)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d — nothing to normalize", len(out), len(in))
	}
	for i := range in {
		if out[i].Role != in[i].Role {
			t.Errorf("out[%d].Role = %q, want %q", i, out[i].Role, in[i].Role)
		}
		if len(out[i].ThinkingBlocks) != len(in[i].ThinkingBlocks) {
			t.Fatalf("out[%d].ThinkingBlocks len = %d, want %d", i, len(out[i].ThinkingBlocks), len(in[i].ThinkingBlocks))
		}
		for j := range in[i].ThinkingBlocks {
			if out[i].ThinkingBlocks[j] != in[i].ThinkingBlocks[j] {
				t.Errorf("out[%d].ThinkingBlocks[%d] = %+v, want %+v", i, j, out[i].ThinkingBlocks[j], in[i].ThinkingBlocks[j])
			}
		}
	}
}

// Rule B, carry direction: the incoming message's blocks fill the empty
// predecessor — the exact treatment ReasoningContent gets today
// (first-non-empty wins).
func TestNormalize_RuleB_MergeCarriesBlocksIntoEmptyPredecessor(t *testing.T) {
	in := []providers.Message{
		{Role: "assistant", Content: "first"},
		{
			Role:           "assistant",
			Content:        "second",
			ThinkingBlocks: rebuildBlocksA,
		},
	}

	out := normalizeMessagesForProvider(in)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1 — two consecutive plain-text assistants merge (Rule B)", len(out))
	}
	if out[0].Content != "first\nsecond" {
		t.Errorf("Content = %q, want %q — the merge itself must still happen", out[0].Content, "first\nsecond")
	}
	if len(out[0].ThinkingBlocks) != len(rebuildBlocksA) {
		t.Fatalf("ThinkingBlocks len = %d, want %d — the merge must carry the incoming blocks", len(out[0].ThinkingBlocks), len(rebuildBlocksA))
	}
	for i := range rebuildBlocksA {
		if out[0].ThinkingBlocks[i] != rebuildBlocksA[i] {
			t.Errorf("ThinkingBlocks[%d] = %+v, want %+v (byte-exact, order-stable)", i, out[0].ThinkingBlocks[i], rebuildBlocksA[i])
		}
	}
}

// Rule B, precedence: when both sides carry blocks the predecessor's list
// wins, mirroring ReasoningContent's first-non-empty rule; and a merge with a
// block-free incoming message leaves the predecessor's blocks untouched.
func TestNormalize_RuleB_MergeKeepsPredecessorBlocks(t *testing.T) {
	cases := []struct {
		name     string
		incoming []providers.Message
	}{
		{
			name: "both carry blocks — predecessor's list wins",
			incoming: []providers.Message{
				{Role: "assistant", Content: "first", ThinkingBlocks: rebuildBlocksA},
				{Role: "assistant", Content: "second", ThinkingBlocks: rebuildBlocksB},
			},
		},
		{
			name: "predecessor only — untouched by the merge",
			incoming: []providers.Message{
				{Role: "assistant", Content: "first", ThinkingBlocks: rebuildBlocksA},
				{Role: "assistant", Content: "second"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := normalizeMessagesForProvider(tc.incoming)
			if len(out) != 1 {
				t.Fatalf("len = %d, want 1 (Rule B merge)", len(out))
			}
			if len(out[0].ThinkingBlocks) != len(rebuildBlocksA) {
				t.Fatalf("ThinkingBlocks len = %d, want %d (the predecessor's list)", len(out[0].ThinkingBlocks), len(rebuildBlocksA))
			}
			for i := range rebuildBlocksA {
				if out[0].ThinkingBlocks[i] != rebuildBlocksA[i] {
					t.Errorf("ThinkingBlocks[%d] = %+v, want %+v — first-non-empty wins, mirroring ReasoningContent", i, out[0].ThinkingBlocks[i], rebuildBlocksA[i])
				}
			}
		})
	}
}

// D8: the budget must count blocks or thinking is under-counted. The oracle
// is derived, not observed: estimateMessageTokens already counts
// ReasoningContent rune-for-rune, so blocks totalling the same length must
// shift the estimate by exactly the same delta (ASCII fixtures make rune and
// byte counts identical, so the ADR's "Thinking/Data lengths" wording and the
// rune-based precedent agree).
func TestEstimateMessageTokens_CountsThinkingBlocks(t *testing.T) {
	const content = "Answer."

	withRC := providers.Message{Role: "assistant", Content: content, ReasoningContent: strings.Repeat("r", 1000)}
	withBlocks := providers.Message{
		Role:    "assistant",
		Content: content,
		ThinkingBlocks: []providers.ThinkingBlock{
			{Type: "thinking", Thinking: strings.Repeat("t", 600)},
			{Type: "redacted_thinking", Data: strings.Repeat("d", 400)},
		},
	}
	plain := providers.Message{Role: "assistant", Content: content}

	base := estimateMessageTokens(plain)
	wantDelta := estimateMessageTokens(withRC) - base
	gotDelta := estimateMessageTokens(withBlocks) - base

	if wantDelta <= 0 {
		t.Fatalf("derived wantDelta = %d, want > 0 — the ReasoningContent precedent must count in this build", wantDelta)
	}
	if gotDelta != wantDelta {
		t.Errorf("estimateMessageTokens delta for 1000 runes of ThinkingBlocks = %d, want %d — blocks must count exactly like ReasoningContent counts (ADR-095 D8)", gotDelta, wantDelta)
	}
}

// The zero case: nil and empty block lists must estimate identically — no
// phantom overhead for carrying no blocks, whichever spelling the caller uses.
func TestEstimateMessageTokens_NoBlocks_NoPhantomOverhead(t *testing.T) {
	m := providers.Message{
		Role:    "assistant",
		Content: "Answer.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "SF"}},
		},
	}
	withEmpty := m
	withEmpty.ThinkingBlocks = []providers.ThinkingBlock{}

	if got, want := estimateMessageTokens(m), estimateMessageTokens(withEmpty); got != want {
		t.Errorf("estimateMessageTokens = %d with nil blocks vs %d with an empty slice — the two spellings must agree (no phantom overhead)", got, want)
	}
}
