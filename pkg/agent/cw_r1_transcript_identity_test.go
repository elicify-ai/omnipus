package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scenario 1. The dated ADR-066 §12 correction makes the archive occurrence,
// not the last matching provider ID, the projection's address.
func TestCWIdentity_ReusedIDProjectsOlderTranscriptRow(t *testing.T) {
	for _, tc := range []struct{ name, newerID string }{
		{"reused_id", "call_0"},
		{"unique_id_control", "call_1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCWIdentityHarness(t)
			h.prefix(t)
			// 513 = the fixture's 512-rune cap + 1; both results are capped.
			older := h.record(t, h.turn("older"), cwIdentityCall("call_0", "older"), strings.Repeat("o", 513), nil, nil)
			h.record(t, h.turn("newer"), cwIdentityCall(tc.newerID, "newer"), strings.Repeat("n", 513), nil, nil)
			before, archived := h.transcript(t), h.archive(t)

			mark := h.projectOK(t, h.turn("projection"), older)

			require.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark), h.transcript(t),
				"the older composite key must change only the older row; the newest same-ID row must remain intact")
			assert.Equal(t, archived, h.archive(t), "projection must never rewrite the full archive")
		})
	}
}

// Scenario 2. Full results need an address before any projection exists, and
// that address must survive persistence and later capped/emptied updates.
func TestCWIdentity_FullResultRemainsAddressableForLaterProjection(t *testing.T) {
	for _, mode := range []string{"emptied", "capped_then_emptied", "persisted_while_full"} {
		t.Run(mode, func(t *testing.T) {
			h := newCWIdentityHarness(t)
			h.prefix(t)
			// fullResultContent (400 runes) stays under the fixture's 512-rune
			// admission cap (so Capped stays false, as this scenario requires)
			// while staying comfortably over the real recall mark's fixed
			// ~285-rune size (buildRecallMark, measured directly) — long enough
			// that the real D5 pass this scenario later drives (h.projectOK)
			// genuinely shrinks the window instead of growing it. The literal
			// "full older bytes"/"full newer bytes" strings the dead-path test
			// used (17 runes) could never do that: no budget could ever make
			// a real empty of 17 runes relieve real pressure.
			const fullResultContent = 400
			older := h.record(t, h.turn("full-older"), cwIdentityCall("call_0", "full older"), strings.Repeat("o", fullResultContent), nil, nil)
			newer := h.record(t, h.turn("full-newer"), cwIdentityCall("call_0", "full newer"), strings.Repeat("n", fullResultContent), nil, nil)
			require.False(t, older.admitted.Capped, "this case must start with an unprojected result")
			require.False(t, newer.admitted.Capped, "the newer duplicate also starts full")
			assert.Equal(t, older.key.ArchiveLine, older.admitted.ArchiveLine,
				"a full admitted result needs its real archive line, not an unknown -1 address")
			assert.Equal(t, newer.key.ArchiveLine, newer.admitted.ArchiveLine,
				"every occurrence, not just capped results, needs an archive identity")
			before, archived := h.transcript(t), h.archive(t)

			if mode == "persisted_while_full" {
				h.assertLines(t, older, newer)
				h.reopen(t)
				h.assertLines(t, older, newer)
				require.Equal(t, before, h.transcript(t), "persisting identity must not project full content")
				require.Equal(t, archived, h.archive(t), "reopening must preserve full archive bytes")
			}
			if mode == "capped_then_emptied" {
				update := cwIdentityAddressedUpdate(t, session.ToolCallID(older.key.ToolCallID), older.line,
					"capped", map[string]any{"text": "older occurrence capped"})
				previous, err := h.store.UpdateToolCallProjections(h.sessionID, []session.ToolCallProjectionUpdate{update})
				require.NoError(t, err)
				require.Equal(t, cwIdentityProjectedTranscript(before, older.line, "capped", "older occurrence capped"), h.transcript(t),
					"later capping must address the formerly full occurrence")
				cwIdentityAssertUndoAddress(t, previous, older.line)
			}

			mark := h.projectOK(t, h.turn("late-projection"), older)
			require.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark), h.transcript(t),
				"later emptying must still address the same formerly full occurrence")
			require.Equal(t, archived, h.archive(t), "both projection kinds leave the full archive unchanged")
			h.assertLines(t, older, newer)
			h.reopen(t)
			h.assertLines(t, older, newer)
			require.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark), h.transcript(t),
				"reopening must preserve the addressed projection")
		})
	}
}

// Scenario 3. No ArchiveLine exists when approval writes the placeholder.
// Completion must bind its later archive identity to that SAME physical row.
func TestCWIdentity_PendingReplacementKeepsActualTranscriptIndex(t *testing.T) {
	t.Run("replacement_in_place_control", func(t *testing.T) {
		h, older, newer := cwIdentityPendingFixture(t)
		require.Equal(t, 1, older.line, "the placeholder follows one anchor row")
		require.Equal(t, 3, newer.line, "the unrelated intervening row and replacement must not append a duplicate")
		require.Len(t, h.transcript(t), 4, "completion replaces the pending row rather than adding a fifth row")
	})
	t.Run("identity_uses_replaced_index", func(t *testing.T) {
		h, older, newer := cwIdentityPendingFixture(t)
		h.assertLines(t, older, newer)
		h.reopen(t)
		h.assertLines(t, older, newer)
	})
	t.Run("later_projection_reaches_replaced_row", func(t *testing.T) {
		h, older, _ := cwIdentityPendingFixture(t)
		before, archived := h.transcript(t), h.archive(t)
		mark := h.projectOK(t, h.turn("after-approval"), older)
		require.Equal(t, cwIdentityProjectedTranscript(before, 1, "emptied", mark), h.transcript(t),
			"the original pending row, not an appended index or the newest duplicate, owns the completed result")
		assert.Equal(t, archived, h.archive(t), "approval projection leaves the admitted full result recoverable")
	})
}

func cwIdentityPendingFixture(t *testing.T) (*cwIdentityHarness, cwIdentityRecorded, cwIdentityRecorded) {
	t.Helper()
	h := newCWIdentityHarness(t)
	h.prefix(t)
	ts := h.turn("approval")
	pendingLine := h.transcriptLines
	args := map[string]any{"occurrence": "approved older"}
	recordAskPendingToolCall(ts, "call_0", "identity_tool", args)
	h.transcriptLines++
	require.Equal(t, toolCallStatusPending, h.transcript(t)[pendingLine].ToolCalls[0].Status)
	// This transcript-only row deliberately breaks archive/transcript ordinal
	// parity and makes the hypothetical appended completion index incorrect.
	h.addTranscript(t, session.TranscriptEntry{ID: "approval-wait", Role: "user", Content: "still waiting", TurnID: "approval"})
	older := h.record(t, ts, cwIdentityCall("call_0", "approved older"), strings.Repeat("a", 513), nil, &pendingLine)
	newer := h.record(t, h.turn("after-approval-newer"), cwIdentityCall("call_0", "newer"), strings.Repeat("n", 513), nil, nil)
	return h, older, newer
}

// Scenario 5. Use the real newTurnState -> D5 transcript update ->
// restoreSession pattern from TestRunTurn_AbortRestoresTurnStartTriple.
// Intermediate writes are checked too: an end-state-only rollback test could
// pass after corrupting, then restoring, the wrong duplicate throughout.
func TestCWIdentity_RepeatedUpdatesAbortToOriginalAndKeepAddress(t *testing.T) {
	for _, tc := range []struct{ name, newerID string }{
		{"reused_id", "call_0"},
		{"unique_id_control", "call_1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCWIdentityHarness(t)
			h.prefix(t)
			older := h.record(t, h.turn("older"), cwIdentityCall("call_0", "older"), strings.Repeat("o", 513), nil, nil)
			newer := h.record(t, h.turn("newer"), cwIdentityCall(tc.newerID, "newer"), strings.Repeat("n", 513), nil, nil)
			before, archived := h.transcript(t), h.archive(t)
			initialSet := h.store.Projection(h.key).Entries.Clone()
			initialSkip := len(archived) - len(h.store.GetHistory(h.key))
			ts := h.turn("abort")
			require.Equal(t, h.archiveLines, ts.initialArchiveLen, "rollback snapshots the actual turn-start archive")
			require.Equal(t, initialSet, ts.initialEmptiedSet, "rollback snapshots the actual turn-start projection set")

			// First real pass: genuine D5 budget pressure empties `older` in
			// place. The dead path's "first projection"/"second projection"
			// synthetic marks (two arbitrary strings the caller supplied for
			// the SAME key) have no real-mechanism equivalent: the real mark
			// is a pure function of the archived content/line/tool/id/turn
			// (pkg/agent/recall_mark.go::buildRecallMark), and
			// retainedSourceRunes short-circuits an already-Emptied key to 0
			// (projection_checked.go) — a second real pass on the SAME key
			// can only ever be a genuine no-op, never a second distinct
			// write. That idempotency is itself a real, previously-
			// unverifiable claim the dead path had no mechanism to prove (it
			// wrote the transcript directly, with no concept of "already
			// projected"); this test proves it instead of fabricating a
			// second distinct mark.
			mark, ok := h.project(t, ts, older)
			require.True(t, ok, "the first real D5 pressure check must actually commit a projection change")
			assert.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark), h.transcript(t),
				"the first real write must reach the addressed older occurrence")
			require.Len(t, ts.emptiedTranscriptPrev, 1, "the first real write must retain its undo record")

			mark2, ok2 := h.project(t, ts, older)
			require.False(t, ok2, "a second real pressure check on an already-emptied key must be a genuine no-op")
			assert.Equal(t, mark, mark2, "the mark is a pure function of the archived content/line/tool/id/turn — identical on repeat")
			assert.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark), h.transcript(t),
				"the no-op second check must leave that same row exactly as the first write left it")
			require.Len(t, ts.emptiedTranscriptPrev, 1, "a no-op pressure check retains no further undo record")

			undo := append([]session.ToolCallProjectionUpdate(nil), ts.emptiedTranscriptPrev...)
			h.addArchive(t, providers.Message{Role: "user", Content: "discard this aborted tail"})

			require.NoError(t, ts.restoreSession(h.agent))
			h.archiveLines-- // exactly the one explicit tail write above is rolled back
			require.Equal(t, archived, h.archive(t), "abort restores the original full archive, not a projected version")
			require.Equal(t, initialSkip, len(h.archive(t))-len(h.store.GetHistory(h.key)), "abort restores turn-start Skip")
			require.Equal(t, initialSet, h.store.Projection(h.key).Entries, "abort restores the original projection set")
			require.Equal(t, before, h.transcript(t), "abort must restore original text and content_state, not the intermediate first write")
			require.Empty(t, ts.emptiedTranscriptPrev, "undo is consumed exactly once")
			require.NoError(t, ts.restoreSession(h.agent), "a repeated restore must leave the original state intact")
			require.Equal(t, before, h.transcript(t))

			mark3 := h.projectOK(t, h.turn("after-abort"), older)
			require.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark3), h.transcript(t),
				"restoring projections must not erase the surviving occurrence's transcript address")
			if tc.name == "reused_id" {
				for _, record := range undo {
					cwIdentityAssertUndoAddress(t, []session.ToolCallProjectionUpdate{record}, older.line)
				}
				h.assertLines(t, older, newer)
				h.reopen(t)
				h.assertLines(t, older, newer)
			}
		})
	}
}

// Scenario 6. Media and a genuine failed admission must not fall back to the
// latest success-text duplicate. Full-result parity lives in Scenario 2.
func TestCWIdentity_MediaAndFailureUseSameCompositeAddress(t *testing.T) {
	for _, kind := range []string{"media", "failure"} {
		for _, occurrence := range []string{"reused_id", "unique_id_control"} {
			t.Run(kind+"/"+occurrence, func(t *testing.T) {
				h := newCWIdentityHarness(t)
				h.prefix(t)
				call := cwIdentityCall("call_0", "older "+kind)
				var media []string
				if kind == "media" {
					media = []string{"data:image/png;base64,iVBORw0KGgo="}
					call.Result = map[string]any{"media": []any{map[string]any{"mime_type": "image/png", "url": media[0]}}}
				} else {
					call.Status = "error"
					call.Error = "permission denied opening report"
				}
				older := h.record(t, h.turn("older-"+kind), call, strings.Repeat("x", 513), media, nil)
				newerID := "call_0"
				if occurrence == "unique_id_control" {
					newerID = "call_1"
				}
				h.record(t, h.turn("newer-success"), cwIdentityCall(newerID, "newer plain success"), strings.Repeat("n", 513), nil, nil)
				before, archived := h.transcript(t), h.archive(t)
				require.True(t, older.admitted.Capped, "parity is exercised on actually capped admissions")
				if kind == "failure" {
					require.Equal(t, memory.ProjectionCappedFailure, h.store.Projection(h.key).Entries[older.key],
						"this is the real failure-admission class, not success text labelled failure")
					require.Equal(t, "error", before[older.line].ToolCalls[0].Status)
					require.Equal(t, "permission denied opening report", before[older.line].ToolCalls[0].Error)
				} else {
					require.Equal(t, media, archived[older.key.ArchiveLine].Media)
					require.Equal(t, call.Result, before[older.line].ToolCalls[0].Result, "the recorded result really carries media")
				}

				mark := h.projectOK(t, h.turn("project-"+kind), older)
				require.Equal(t, cwIdentityProjectedTranscript(before, older.line, "emptied", mark), h.transcript(t),
					"media/failure addressing must change only the older text, preserving its shape, non-text metadata, status, parameters and the newer row")
				assert.Equal(t, archived, h.archive(t), "media and failure source content must stay recoverable")
			})
		}
	}
}
