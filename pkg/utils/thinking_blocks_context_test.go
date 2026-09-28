// Omnipus — WP-F RED pack, stage B: pkg/utils/context.go's half of ADR-095
// D8's adjacent-consumer sweep (F12): MeasureContextRunes and
// TruncateContextSmart count ReasoningContent today — they must count
// ThinkingBlocks (Thinking/Data lengths) or thinking is under-counted against
// the context budget, and a trim can drop the very assistant message whose
// blocks the next thinking request needs (D8.5's guard would then silently
// omit thinking for the rest of the session).
package utils

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

const (
	budgetContent   = "y" // 1 rune
	blockRuneBudget = 600 // the block's rune load under test
	boundaryKeep    = 681 // maxRunes: remaining = 681-80 = 601 = block msg's load → kept
	boundaryDrop    = 679 // maxRunes: remaining = 679-80 = 599 < 601 → dropped
)

// blockedAsstFixture builds the under-test assistant: 1 rune of content plus a
// single 600-rune thinking block (ASCII, so rune == byte).
func blockedAsstFixture() providers.Message {
	return providers.Message{
		Role:    "assistant",
		Content: budgetContent,
		ThinkingBlocks: []providers.ThinkingBlock{
			{Type: "thinking", Thinking: strings.Repeat("t", blockRuneBudget)},
		},
	}
}

// D8: MeasureContextRunes counts ReasoningContent — it must count blocks the
// same way. Derived oracle: the measurement with blocks must equal the
// measurement without plus exactly the blocks' rune count (600).
func TestMeasureContextRunes_CountsThinkingBlocks(t *testing.T) {
	plain := []providers.Message{{Role: "assistant", Content: budgetContent}}
	withBlocks := []providers.Message{blockedAsstFixture()}

	base := MeasureContextRunes(plain)
	want := base + blockRuneBudget
	if got := MeasureContextRunes(withBlocks); got != want {
		t.Errorf("MeasureContextRunes = %d, want %d (base %d + %d block runes) — blocks must count toward the budget (ADR-095 D8)", got, want, base, blockRuneBudget)
	}

	// Both block kinds count: a redacted_thinking block's Data is budget
	// material too.
	both := []providers.Message{{
		Role:    "assistant",
		Content: budgetContent,
		ThinkingBlocks: []providers.ThinkingBlock{
			{Type: "thinking", Thinking: strings.Repeat("t", 500)},
			{Type: "redacted_thinking", Data: strings.Repeat("d", 700)},
		},
	}}
	if got := MeasureContextRunes(both); got != base+500+700 {
		t.Errorf("MeasureContextRunes = %d, want %d — Thinking AND Data lengths both count", got, base+500+700)
	}
}

// The zero case: no blocks → the measurement is exactly the pre-carrier value.
func TestMeasureContextRunes_NoBlocks_NoChange(t *testing.T) {
	m := providers.Message{Role: "assistant", Content: budgetContent}
	withEmpty := m
	withEmpty.ThinkingBlocks = []providers.ThinkingBlock{}

	got := MeasureContextRunes([]providers.Message{m})
	want := MeasureContextRunes([]providers.Message{withEmpty})
	if got != want {
		t.Errorf("MeasureContextRunes = %d with nil blocks vs %d with an empty slice — the spellings must agree", got, want)
	}
}

// D8's trim-boundary pair: the block load must move TruncateContextSmart's
// keep/drop boundary. The same history is trimmed at two budgets two runes
// apart around the block-inclusive size; the block message flips sides. A
// build that stops counting blocks (today's behavior) fails BOTH cases: the
// keep case would also keep the older message, the drop case would keep the
// block message it must drop.
//
// Arithmetic (ASCII fixtures, rune == byte; no system messages; the notice
// estimate is the source's constant 80):
//   - block message load = 1 (content) + 600 (block) = 601
//   - older user message load = 1 rune
//   - keep case: maxRunes 681 → remaining 601 → newest kept exactly, the
//     older message no longer fits → truncation notice for 1 dropped message.
//   - drop case: maxRunes 679 → remaining 599 → the block message itself is
//     dropped → truncation notice for 2 dropped messages.
func TestTruncateContextSmart_ThinkingBlocksCountTowardBudget(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "x"},
		blockedAsstFixture(),
	}

	t.Run("keep case: block message fits exactly, older message drops", func(t *testing.T) {
		out := TruncateContextSmart(history, boundaryKeep)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2 (notice + kept assistant); got: %+v", len(out), out)
		}
		if !strings.Contains(out[0].Content, "[Context truncated: 1 earlier messages omitted") {
			t.Errorf("out[0].Content = %q, want the truncation notice for the 1 dropped older message — the block load must have consumed the budget", out[0].Content)
		}
		got := out[1]
		if got.Role != "assistant" {
			t.Fatalf("out[1].Role = %q, want the kept assistant", got.Role)
		}
		if got.Content != budgetContent {
			t.Errorf("kept Content = %q, want %q", got.Content, budgetContent)
		}
		// And the kept message still carries its blocks byte-exact — a trim
		// must never strip them (D6: round-trip echoes every block).
		if len(got.ThinkingBlocks) != 1 || got.ThinkingBlocks[0].Thinking != strings.Repeat("t", blockRuneBudget) {
			t.Errorf("kept ThinkingBlocks = %+v, want the 600-rune block byte-exact", got.ThinkingBlocks)
		}
	})

	t.Run("drop case: block message itself drops two runes earlier", func(t *testing.T) {
		out := TruncateContextSmart(history, boundaryDrop)
		if len(out) != 1 {
			t.Fatalf("len = %d, want 1 (notice only); got: %+v", len(out), out)
		}
		if !strings.Contains(out[0].Content, "[Context truncated: 2 earlier messages omitted") {
			t.Errorf("out[0].Content = %q, want the notice for BOTH dropped messages — the block message must have counted its 600 runes", out[0].Content)
		}
	})
}

// TruncateContextSmart's system-message accounting must count system-side
// blocks too — same D8 rule, different branch of the function.
//
// Arithmetic (ASCII, no notice space subtleties): systemRunes = 3 (content) +
// 300 (block) = 303 with the fix, 3 without. remaining = maxRunes - systemRunes
// - 80 (the source's own notice estimate).
//   - keep side, maxRunes 384: remaining = 1 with the fix → the 1-rune user
//     message fits exactly → [system, user], no notice.
//   - drop side, maxRunes 383: remaining = 0 with the fix → the user message
//     no longer fits → [system, notice-for-1]. Without the count (today's
//     behavior) remaining would be 300 and the user message would keep its
//     slot — this case is what kills the uncounted mutant.
func TestTruncateContextSmart_SystemMessageBlocksCountTowardBudget(t *testing.T) {
	sys := providers.Message{
		Role:    "system",
		Content: "sys",
		ThinkingBlocks: []providers.ThinkingBlock{
			{Type: "thinking", Thinking: strings.Repeat("s", 300)},
		},
	}
	history := []providers.Message{sys, {Role: "user", Content: "x"}}

	t.Run("keep side: user message fits exactly below the block load", func(t *testing.T) {
		out := TruncateContextSmart(history, 384)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2 (system + user, nothing dropped); got: %+v", len(out), out)
		}
		if out[1].Role != "user" {
			t.Errorf("out[1].Role = %q, want the kept user message", out[1].Role)
		}
		if len(out[0].ThinkingBlocks) != 1 {
			t.Errorf("system ThinkingBlocks len = %d, want 1 — a trim never strips blocks", len(out[0].ThinkingBlocks))
		}
	})

	t.Run("drop side: user message loses its slot to the system block", func(t *testing.T) {
		out := TruncateContextSmart(history, 383)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2 (system + notice); got: %+v", len(out), out)
		}
		if out[1].Role != "system" || !strings.Contains(out[1].Content, "[Context truncated: 1 earlier messages omitted") {
			t.Errorf("out[1] = %+v, want the truncation notice — the system block's 300 runes must consume the budget", out[1])
		}
	})
}
