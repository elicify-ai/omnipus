// approval_transcript_adr057_test.go — ADR-057 U22 (W3b, FR-099, BDD-109,
// test #113): the transcript SETTLE path's silent-failure fix, ported to the
// append-only effect design (session-core U2 effects design D5; ARCHITECT-
// ANSWER-U2-EFFECTS.md section 3, "keep the FR-099 miss counter, fed by
// SettleToolCall errors").
//
// The rewrite-in-place helper mutateToolCallInTranscript was deleted with the
// in-place rewrite surface (DEL-12). Its role is now mutateToolCall, which
// exploits the turn's own remembered call record (an ArchiveAddress) to append
// a settle effect. FR-099's requirement is unchanged: each of the two failure
// shapes — "session not found" and "entry not found" — must surface as a
// counter increment (transcriptMutateMissed / TranscriptMutateMissed()) plus a
// WARN naming the session id and call id, distinguishable between the two.
//
// Per binding rule 6, the one package-level helper this file adds is prefixed
// u22.

package agent

import (
	"log/slog"
	"path/filepath"
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

// u22SettleHarness is a minimal turn state over a real store the settle path
// can be driven through.
func u22SettleHarness(t *testing.T, store *session.UnifiedStore, sessionID string) *turnState {
	t.Helper()
	agent := &AgentInstance{ID: "u22", Sessions: store}
	return newTurnState(agent, processOptions{
		SessionKey: "agent:u22:session:" + sessionID, TranscriptSessionID: sessionID, TranscriptStore: store,
	}, turnEventScope{turnID: "u22"})
}

// TestTranscriptMutate_MissingSessionOrTargetIsLoggedAndCounted is TDD plan
// test #113, tracing to BDD-109. It covers FR-099's two named failure shapes
// in one run, plus a positive-control third case (binding rule 4's lower
// bound) proving the counter is not simply free-running.
func TestTranscriptMutate_MissingSessionOrTargetIsLoggedAndCounted(t *testing.T) {
	store, err := session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	noopMutate := func(tc *session.ToolCall) { tc.Status = "settled" }

	t.Run("session not found — the remembered target's session does not exist", func(t *testing.T) {
		const missingSessionID = "sess_never_created_u22"
		const callID = session.ToolCallID("call_u22_missing_session")
		ts := u22SettleHarness(t, store, missingSessionID)
		// The turn remembers a record whose address names a session that never
		// existed, so the settle's own read fails and the stat distinguishes it.
		ts.rememberCallRecord(session.ToolCall{ID: callID, Status: "pending"},
			session.ArchiveAddress{PartitionKey: "2026-01-01", ByteOffset: 0, EntryID: "missing-record"})

		before := TranscriptMutateMissed()
		logs := u22CaptureLogs(t)

		got := mutateToolCall(ts, callID, "pending", noopMutate)

		assert.False(t, got, "settling a call whose session is gone must report no-op via false")

		after := TranscriptMutateMissed()
		assert.Equal(t, before+1, after,
			"BDD-109: TranscriptMutateMissed() must increase by exactly 1 for the missing-session case, never a silent bare false")

		out := logs.String()
		for _, want := range []string{missingSessionID, string(callID), "session_not_found"} {
			assert.Contains(t, out, want,
				"the WARN record must name the session id, the call id, and be distinguishable as the session_not_found reason; got:\n%s", out)
		}
	})

	t.Run("entry not found — the turn remembers no record for the call", func(t *testing.T) {
		meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
		require.NoError(t, err)
		ts := u22SettleHarness(t, store, meta.ID)

		before := TranscriptMutateMissed()
		logs := u22CaptureLogs(t)

		const missingCallID = session.ToolCallID("call_u22_does_not_exist")
		got := mutateToolCall(ts, missingCallID, "pending", noopMutate)

		assert.False(t, got, "settling a call the turn never recorded must report no-op via false")

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
		addr, err := store.AppendTranscriptAddressed(meta.ID, session.TranscriptEntry{
			Type: session.EntryTypeToolCall,
			ToolCalls: []session.ToolCall{
				{ID: callID, Status: "pending"},
			},
		})
		require.NoError(t, err)

		ts := u22SettleHarness(t, store, meta.ID)
		ts.rememberCallRecord(session.ToolCall{ID: callID, Status: "pending"}, addr)

		before := TranscriptMutateMissed()

		got := mutateToolCall(ts, callID, "pending", noopMutate)

		assert.True(t, got, "binding rule 4: a real match must succeed (true) — proves the false "+
			"assertions above are meaningful failures, not an artifact of mutateToolCall always returning false")
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
		assert.Equal(t, "settled", settledStatus, "the real match must actually be settled in the merged transcript")
	})
}
