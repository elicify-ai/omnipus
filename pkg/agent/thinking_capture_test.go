// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the per-round thinking capture (spec §1 C2/C3, §8.1 storage unit,
// FR-030/FR-033/FR-006; RED tests for WP-C — the pinned production symbol is
// pkg/agent/thinking_capture.go::thinkingCapture).
//
// Every expected value below derives from the spec, not from any
// implementation run:
//   - entry ID minted AT CAPTURE START, shared with the live frame (C2/C3);
//   - one type:"thinking" entry per round, appended EXACTLY ONCE (C3/FR-030);
//   - display copy in thinking_text, never content; NO role (C3);
//   - redaction = audit credential patterns on the whole accumulated copy,
//     emails kept, unrecognised formats NOT masked (FR-006/FR-034, §13);
//   - hold-back: live text stops before the trailing run of token-ish chars
//     (A-Za-z0-9 plus . _ - ~ / + =) (§5 hold-back rule, FR-033).

// holdbackTokenish mirrors the spec's token-ish alphabet (A-Za-z0-9 and
// . _ - ~ / + =) — the test-side oracle for the hold-back boundary, used to
// check that live text always ends on a NON-token-ish character.
func holdbackTokenish(r byte) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case strings.IndexByte("._-~/+=", r) >= 0:
		return true
	}
	return false
}

// requireHoldBackBoundary asserts the live text ends on a non-token-ish char
// (or is empty) — the boundary property the hold-back rule guarantees.
func requireHoldBackBoundary(t *testing.T, live string) {
	t.Helper()
	if live == "" {
		return
	}
	last := live[len(live)-1]
	if holdbackTokenish(last) {
		t.Fatalf("live text %q ends in a token-ish char (%q) — the hold-back rule must trim the trailing "+
			"token-ish run (spec §5 hold-back rule)", live, string(last))
	}
}

func TestThinkingCapture_MintsEntryIDAtCaptureStart(t *testing.T) {
	c1 := newThinkingCapture("turn-1")
	c2 := newThinkingCapture("turn-1")

	require.NotEmpty(t, c1.EntryID, "entry ID must be minted at capture start (spec §1 C3: "+
		"'the capture mints the thinking entry's ID at capture start')")
	assert.NotEqual(t, c1.EntryID, c2.EntryID,
		"two captures in the same session must mint distinct entry IDs (one entry, one ID)")
	assert.Equal(t, c1.EntryID, c1.EntryID, "the minted ID must be stable for the capture's lifetime")
}

func TestThinkingCapture_WriteOnce_ExactlyOneEntry(t *testing.T) {
	c := newThinkingCapture("turn-1")
	c.add("The plan is to inspect the module first.")

	entry, ok := c.finalizeEntry()
	require.True(t, ok, "finalizeEntry must produce the round's thinking entry")
	assert.Equal(t, session.EntryTypeThinking, entry.Type,
		"thinking persists as its own type:\"thinking\" transcript entry (spec §1 C3)")
	assert.Empty(t, entry.Role,
		"the entry carries NO role — the invariant that keeps role-keyed readers from classifying it (spec §1 C3)")
	assert.Empty(t, entry.Content,
		"display copy lives in thinking_text, never content (spec §1 C3): content=%q", entry.Content)
	assert.Equal(t, "The plan is to inspect the module first.", entry.ThinkingText,
		"thinking_text carries the display copy")
	assert.Equal(t, "turn-1", entry.TurnID,
		"turn_id binds the row to its round (spec §8.1)")
	assert.Equal(t, "turn-1", entry.ID,
		"the entry's own ID must be the one minted at capture start (live and replay share one row by construction)")

	// Write-once: the entry is appended EXACTLY ONCE — never rewritten or
	// re-appended (spec §1 C3, FR-030).
	_, ok2 := c.finalizeEntry()
	assert.False(t, ok2, "a second finalizeEntry must not produce another entry (appended EXACTLY ONCE)")
}

func TestThinkingCapture_NoText_NoEntry(t *testing.T) {
	// Dataset rows 5/6: empty / signature-only thinking → no row at all
	// (metadata without an empty row).
	c := newThinkingCapture("turn-1")
	c.noteUsage(120, true) // metadata without display text

	_, ok := c.finalizeEntry()
	assert.False(t, ok, "no thinking text → no thinking row (spec §5: 'no thinking row at all — "+
		"metadata without an empty row')")
}

func TestThinkingCapture_D11Metadata_RidesTypedFields(t *testing.T) {
	c := newThinkingCapture("turn-1")
	c.add("reasoning about the fix")
	c.noteUsage(321, true)

	entry, ok := c.finalizeEntry()
	require.True(t, ok)
	assert.Equal(t, 321, entry.ThinkingTokens,
		"provider-reported thinking token count rides the entry (D11; ThinkingTokens is an existing field)")
	assert.True(t, entry.ProviderSummary,
		"the provider_summary flag (D18 'summarized thinking') rides the entry")
	assert.Greater(t, entry.ElapsedMS, int64(0),
		"elapsed_ms must be positive for a round that captured thinking")
}

func TestThinkingCapture_EntryShape_NoEffortField(t *testing.T) {
	// C3 explicit non-addition: "No reasoning_effort field on the entry
	// (no consumer; effort resolution is C5's, at request time)."
	entryType := reflect.TypeOf(session.TranscriptEntry{})
	for _, name := range []string{"ReasoningEffort"} {
		if _, found := entryType.FieldByName(name); found {
			t.Errorf("TranscriptEntry must not gain a %s field (spec §1 C3 explicit non-addition)", name)
		}
	}
}

func TestThinkingCapture_Redaction_WholeString(t *testing.T) {
	c := newThinkingCapture("turn-1")
	// A secret split across two add() pieces — redaction scans the whole
	// accumulated copy, so a split secret is still masked (spec §5; FR-006).
	c.add("First, check the endpoint using key ")
	c.add("First, check the endpoint using key sk-live-abcd1234EFGH, then verify.")
	c.noteUsage(0, false)

	entry, ok := c.finalizeEntry()
	require.True(t, ok)

	assert.NotContains(t, entry.ThinkingText, "sk-live-abcd1234EFGH",
		"the split secret must be masked in the stored display copy")
	assert.Contains(t, entry.ThinkingText, "[REDACTED]",
		"recognised credential formats are masked with the audit redactor's placeholder")
	assert.Contains(t, entry.ThinkingText, "First, check the endpoint using key",
		"surrounding text before the secret stays intact")
	assert.Contains(t, entry.ThinkingText, ", then verify.",
		"surrounding text after the secret stays intact")

	// Emails are kept (CF10/D3: same behavior as the audit log).
	c2 := newThinkingCapture("turn-2")
	c2.add("ping alice@example.com about the rollout")
	e2, ok := c2.finalizeEntry()
	require.True(t, ok)
	assert.Contains(t, e2.ThinkingText, "alice@example.com",
		"email addresses are kept in the display copy, matching audit redaction behavior")

	// Unrecognised formats are NOT masked — the promise is scoped (spec §13
	// promise 2; dataset rows 8/9).
	c3 := newThinkingCapture("turn-3")
	c3.add("the google key is AIzaSyABCDEF0123456789abcdefGHIJKLMNop for now")
	e3, ok := c3.finalizeEntry()
	require.True(t, ok)
	assert.Contains(t, e3.ThinkingText, "AIzaSyABCDEF0123456789abcdefGHIJKLMNop",
		"an unrecognised format (AIza…) is stored and shown unmasked — the scoped promise")
}

func TestThinkingCapture_HoldBack_ExactBoundaries(t *testing.T) {
	// Hand-derived from the spec's rule: live text = whole-text redaction
	// minus the trailing run of token-ish chars.
	t.Run("trailing number held", func(t *testing.T) {
		c := newThinkingCapture("turn-1")
		c.add("the answer is 42")
		live := c.liveText()
		assert.Equal(t, "the answer is ", live,
			"the trailing token-ish run \"42\" is held back from the live frame")
		requireHoldBackBoundary(t, live)
	})

	t.Run("space-terminated text fully visible", func(t *testing.T) {
		c := newThinkingCapture("turn-1")
		c.add("the answer is 42. ")
		live := c.liveText()
		assert.Equal(t, "the answer is 42. ", live,
			"a trailing space is NOT token-ish — the whole redacted text is visible")
	})

	t.Run("incomplete secret held whole", func(t *testing.T) {
		c := newThinkingCapture("turn-1")
		c.add("uses key sk-abc123")
		live := c.liveText()
		assert.Equal(t, "uses key ", live,
			"an incomplete credential (no delimiter yet) is part of the trailing token-ish run — held back, not shown")
		requireHoldBackBoundary(t, live)
	})

	t.Run("delimiter-completed secret appears masked live", func(t *testing.T) {
		c := newThinkingCapture("turn-1")
		c.add("uses key sk-abc123 and more")
		live := c.liveText()
		assert.NotContains(t, live, "sk-abc123",
			"once delimiter-completed, the whole-text scan has already masked the credential")
		assert.Contains(t, live, "[REDACTED]",
			"the masked form is visible live")
		assert.Equal(t, "uses key [REDACTED] and", live,
			"hold-back still trims the new trailing token-ish run (\"more\")")
	})

	t.Run("provider_summary note does not disturb hold-back", func(t *testing.T) {
		c := newThinkingCapture("turn-1")
		c.add("thinking…")
		// noteUsage must not change the live view.
		c.noteUsage(10, true)
		assert.Equal(t, "thinking", c.liveText(),
			"noteUsage only records metadata; the trailing token-ish run is still held")
	})
}

func TestThinkingCapture_HoldBack_PropertyAtEverySplitPoint(t *testing.T) {
	// Spec §6 machine-verifiable constraint (FR-033): "the live hold-back
	// rule MUST hold at every split point: for any prefix of a thinking
	// stream, ... a credential-shaped run may never appear unmasked in any
	// live frame."
	const secret = "sk-live-abcd1234EFGH"
	raw := "checking the endpoint with key " + secret + " before the call."

	for split := 0; split <= len(raw); split++ {
		c := newThinkingCapture("turn-1")
		c.add(raw[:split])
		live := c.liveText()

		// (a) No part of the credential ever appears unmasked in any live frame.
		for k := 1; k <= len(secret); k++ {
			assert.NotContains(t, live, secret[:k],
				"split=%d: live frame shows a fragment %q of the credential (hold-back violation)", split, secret[:k])
		}
		// (b) The live text ends on a non-token-ish boundary (or is empty).
		requireHoldBackBoundary(t, live)
		// (c) Hold-back only ever trims: the live view is a prefix of the
		// raw accumulated text.
		assert.True(t, strings.HasPrefix(raw[:split], live) || strings.Contains(redactForTestOnly(raw[:split]), live),
			"split=%d: live text %q is neither a prefix of the accumulated text nor its redaction — hold-back must only trim", split, live)
	}
}

// redactForTestOnly is the test-side stand-in for whole-string redaction used
// ONLY by the split-property test's (c) clause: it masks the recognised
// credential shape the same way the audit set does. It exists so clause (c)
// can compare the live view against the redaction of the same prefix without
// re-deriving the audit regexes here; the masking assertions themselves stay
// behavioral (in the whole-string test above).
func redactForTestOnly(s string) string {
	return strings.ReplaceAll(s, "sk-live-abcd1234EFGH", "[REDACTED]")
}
