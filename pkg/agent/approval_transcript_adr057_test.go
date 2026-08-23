// approval_transcript_adr057_test.go — ADR-057 U22 (W3b, FR-099, BDD-109,
// test #113): the transcript MUTATE path's silent-failure fix.
//
// mutateToolCallInTranscript (approval_transcript.go) used to return a bare
// `false` for two distinct failure shapes — "session not found" (no
// transcript.jsonl on disk) and "entry not found" (the session exists but no
// tool_call entry matches callID/expectStatus) — with zero operator-visible
// signal for either. This is the read-modify-write twin of AC-1's silent
// APPEND fix (U2/U5's AppendTranscriptStrict), on the MUTATE side. FR-099
// requires each case to surface as a counter increment (transcriptMutateMissed
// / TranscriptMutateMissed()) plus a WARN naming the session id and call id,
// distinguishable between the two cases.
//
// Per binding rule 5, this is a NEW file (mutateToolCallInTranscript's other
// tests live in the pre-existing approval_transcript_test.go, which this
// file does not touch). Per binding rule 6, the one new package-level helper
// this file adds is prefixed u22.

package agent

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// u22CaptureLogs installs a Warn-level text handler as the default slog
// logger for the duration of the test and returns the buffer it writes
// into. Same technique this package already uses for exactly this class of
// requirement — see wave3_fix5b_test.go's inline slog.SetDefault capture —
// asserting on real emitted bytes rather than a recording spy.
func u22CaptureLogs(t *testing.T) *raceFreeLogBuffer {
	t.Helper()
	return captureDefaultSlog(t, slog.LevelWarn)
}

// TestTranscriptMutate_MissingSessionIsLoggedAndCounted is TDD plan test
// #113, tracing to BDD-109. It covers FR-099's two named failure shapes in
// one run, plus a positive-control third case (binding rule 4's lower
// bound) proving the counter is not simply free-running.
func TestTranscriptMutate_MissingSessionIsLoggedAndCounted(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	noopMutate := func(tc *session.ToolCall) { tc.Status = "settled" }

	t.Run("session not found — no meta.json, no transcript.jsonl", func(t *testing.T) {
		before := TranscriptMutateMissed()
		logs := u22CaptureLogs(t)

		const missingSessionID = "sess_never_created_u22"
		const callID = session.ToolCallID("call_u22_missing_session")

		got := mutateToolCallInTranscript(store, missingSessionID, callID, "pending", noopMutate)

		assert.False(t, got, "mutating a session that was never created must still report no-op via false")

		after := TranscriptMutateMissed()
		assert.Equal(t, before+1, after,
			"BDD-109: TranscriptMutateMissed() must increase by exactly 1 for the missing-session case, never a silent bare false")

		out := logs.String()
		for _, want := range []string{missingSessionID, string(callID), "session_not_found"} {
			assert.Contains(t, out, want,
				"the WARN record must name the session id, the call id, and be distinguishable as the session_not_found reason; got:\n%s", out)
		}
	})

	t.Run("entry not found — session exists, no matching tool_call entry", func(t *testing.T) {
		meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
		require.NoError(t, err)

		// Seed the transcript with an UNRELATED tool_call entry so the file
		// genuinely exists and is genuinely searched — proving this is the
		// "entry not found" branch, not an accidental re-hit of "session
		// not found".
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
			Type: session.EntryTypeToolCall,
			ToolCalls: []session.ToolCall{
				{ID: "call_u22_unrelated", Status: "pending"},
			},
		}))

		before := TranscriptMutateMissed()
		logs := u22CaptureLogs(t)

		const missingCallID = session.ToolCallID("call_u22_does_not_exist")
		got := mutateToolCallInTranscript(store, meta.ID, missingCallID, "pending", noopMutate)

		assert.False(t, got, "mutating a callID with no matching entry must still report no-op via false")

		after := TranscriptMutateMissed()
		assert.Equal(t, before+1, after,
			"BDD-109: TranscriptMutateMissed() must increase by exactly 1 for the missing-entry case, never a silent bare false")

		out := logs.String()
		for _, want := range []string{meta.ID, string(missingCallID), "entry_not_found"} {
			assert.Contains(t, out, want,
				"the WARN record must name the session id, the call id, and be distinguishable as the entry_not_found reason; got:\n%s", out)
		}
		assert.NotContains(t, out, "session_not_found",
			"BDD-109: the two failure shapes must be distinguishable — an existing-session miss must not log the session_not_found reason")
	})

	t.Run("positive control — a real match neither counts nor bare-false's", func(t *testing.T) {
		meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
		require.NoError(t, err)
		const callID = session.ToolCallID("call_u22_real_match")
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
			Type: session.EntryTypeToolCall,
			ToolCalls: []session.ToolCall{
				{ID: callID, Status: "pending"},
			},
		}))

		before := TranscriptMutateMissed()

		got := mutateToolCallInTranscript(store, meta.ID, callID, "pending", noopMutate)

		assert.True(t, got, "binding rule 4: a real match must succeed (true) — proves the false "+
			"assertions above are meaningful failures, not an artifact of mutateToolCallInTranscript always returning false")
		assert.Equal(t, before, TranscriptMutateMissed(),
			"a successful mutate must NOT increment the missed-counter")

		entries, err := store.ReadTranscript(meta.ID)
		require.NoError(t, err)
		var settledStatus string
		for _, e := range entries {
			for _, tc := range e.ToolCalls {
				if tc.ID == callID {
					settledStatus = tc.Status
				}
			}
		}
		assert.Equal(t, "settled", settledStatus, "the real match must actually be mutated in place")
	})
}
