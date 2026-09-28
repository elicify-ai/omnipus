// Omnipus — WP-F, squad-lead ruling 3: Rule A must NOT drop an assistant
// entry that carries non-empty ThinkingBlocks even when Content is blank and
// there are no tool calls — a signature-only (redacted_thinking) or
// thinking-only round must survive merge/normalize (ADR-095 D6/D8:
// normalization is never a strip step).
package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

var (
	// ruleAThinkingBlocks / ruleARedactedBlocks are this file's own fixtures
	// (distinct from qa-lead's), so which list survived is observable
	// byte-exact.
	ruleAThinkingBlocks = []providers.ThinkingBlock{
		{Type: "thinking", Thinking: "rule A: silent plan → 回答の前に", Signature: "sig-ruleA=="},
	}
	ruleARedactedBlocks = []providers.ThinkingBlock{
		{Type: "redacted_thinking", Data: "opaque-rule-A"},
	}
)

// A signature-only round (empty display text, signed thinking block, no tool
// calls) must survive normalization: dropped here, its blocks could never
// round-trip and the next thinking-enabled request would fail or silently
// degrade (D8.5's guard would then omit thinking for the rest of the session).
func TestNormalize_RuleA_SignatureOnlyRoundSurvives(t *testing.T) {
	in := []providers.Message{
		{Role: "user", Content: "question"},
		{
			Role:           "assistant",
			Content:        "",
			ThinkingBlocks: ruleAThinkingBlocks,
		},
	}

	out := normalizeMessagesForProvider(in)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 — the blocks-carrying assistant must NOT be dropped (ruling 3 / ADR-095 D8)", len(out))
	}
	got := out[1]
	if got.Role != "assistant" {
		t.Fatalf("out[1].Role = %q, want assistant", got.Role)
	}
	if got.Content != "" {
		t.Errorf("out[1].Content = %q, want the empty string preserved verbatim", got.Content)
	}
	if len(got.ThinkingBlocks) != len(ruleAThinkingBlocks) {
		t.Fatalf("out[1].ThinkingBlocks len = %d, want %d — the signature-only round's blocks must survive", len(got.ThinkingBlocks), len(ruleAThinkingBlocks))
	}
	for i := range ruleAThinkingBlocks {
		if got.ThinkingBlocks[i] != ruleAThinkingBlocks[i] {
			t.Errorf("out[1].ThinkingBlocks[%d] = %+v, want %+v (byte-exact, order-stable)", i, got.ThinkingBlocks[i], ruleAThinkingBlocks[i])
		}
	}
}

// Same survival, redacted_thinking spelling: the opaque-data-only round.
func TestNormalize_RuleA_RedactedOnlyRoundSurvives(t *testing.T) {
	in := []providers.Message{
		{Role: "user", Content: "question"},
		{
			Role:           "assistant",
			Content:        "",
			ThinkingBlocks: ruleARedactedBlocks,
		},
	}

	out := normalizeMessagesForProvider(in)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 — the redacted-only assistant must NOT be dropped", len(out))
	}
	got := out[1]
	if len(got.ThinkingBlocks) != 1 || got.ThinkingBlocks[0] != ruleARedactedBlocks[0] {
		t.Errorf("out[1].ThinkingBlocks = %+v, want the redacted block byte-exact", got.ThinkingBlocks)
	}
}
