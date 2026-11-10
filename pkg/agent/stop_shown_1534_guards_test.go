package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1534 architect decision §2 and CHECK K10: exercise the guard itself, not
// claimActiveTurn's administrative adapter, which already omits Shown.
func Test1534Shown_AdministrativeClaimDoesNotStoreCheckpoint(t *testing.T) {
	ts := &turnState{turnID: "turn-admin-direct", finishedChan: make(chan struct{})}
	id := ts.nextRoundMessageID()
	shown := &StopShown{TurnID: ts.turnID, MessageID: id, CodePoints: 1}

	require.True(t, ts.claimCancelShown(false, shown), "the first administrative claim must win")
	assert.Nil(t, ts.stopCheckpoint, "administrative cancellation must store no screen checkpoint")
	assert.False(t, ts.stopRequested.Load(), "administrative cancellation is not a client Stop")
	_, limited := ts.stopKeepFor(id)
	assert.False(t, limited, "an administrative cancel must not cut the answer to a client screen")
}

// Oracle: #1534 architect decision §§2,4; CHECK S3f. A😀éZ consists of
// A, emoji, e, combining acute, Z: five code points, not five bytes/UTF-16 units.
func Test1534Shown_CutToCodePointsReturnsAheadOnlyPastEnd(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		keep             int
		ahead            bool
	}{
		{"negative", "A😀éZ", "", -1, false},
		{"zero", "A😀éZ", "", 0, false},
		{"one", "A😀éZ", "A", 1, false},
		{"one_before_end", "A😀éZ", "A😀é", 4, false},
		{"exact_end", "A😀éZ", "A😀éZ", 5, false},
		{"one_past_end", "A😀éZ", "A😀éZ", 6, true},
		{"empty_at_zero", "", "", 0, false},
		{"empty_one_past_end", "", "", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cut, ahead := CutToCodePoints(tc.text, tc.keep)
			assert.Equal(t, tc.want, cut, "cut must preserve exactly the specified code-point prefix")
			assert.Equal(t, tc.ahead, ahead, "only a checkpoint strictly beyond the text is ahead")
		})
	}
}

// Reuse cancel_transcript_test.go's native-turn fixture with the real loop,
// RequestCancel and transcript store. No persistence callback is replaced.
func checkpoint1534NativeCancelFixture(t *testing.T) (*AgentLoop, *turnState, string) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al := newCancelTestAgentLoop(t)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	ts := &turnState{
		turnID:              "turn-checkpoint-native",
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(meta.ID, ts)
	t.Cleanup(func() {
		ts.Finish(ts.hardAbortRequested())
		al.activeTurnStates.Delete(meta.ID)
	})
	return al, ts, meta.ID
}

// CHECK D4 / rider #1524: the before-done write must describe a requested
// hard abort as hard, before Finish's separate callback could supply the label.
func Test1534Shown_PersistCancelBeforeDoneStoresMethod(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		hard         bool
	}{
		{"plain_stop_is_graceful", "graceful", false},
		{"hard_abort_is_hard", "hard", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, ts, sid := checkpoint1534NativeCancelFixture(t)
			outcome, err := al.RequestCancel(context.Background(),
				CancelScope{SessionID: sid, TurnOnly: true},
				CancelCanceller{UserID: "checkpoint-owner", Channel: "web"}, CancelHooks{})
			require.NoError(t, err)
			require.True(t, outcome.Fired, "the real Stop must register native cancel persistence")
			if tc.hard {
				require.True(t, ts.requestHardAbort(), "a hard-abort request must precede persistence")
			}
			require.False(t, ts.isFinished.Load(), "the record must be checked before Finish")

			ts.persistCancelBeforeDone()
			entries, err := al.GetSessionStore().ReadTranscript(sid)
			require.NoError(t, err)
			require.Len(t, entries, 1, "before-done persistence must write exactly one real cancel entry")
			assert.Equal(t, session.EntryTypeTurnCancelled, entries[0].Type)
			assert.Equal(t, ts.turnID, entries[0].TurnID)
			assert.Equal(t, "checkpoint-owner", entries[0].CancelledByUser)
			assert.Equal(t, "web", entries[0].CancelledByChannel)
			assert.Equal(t, tc.method, entries[0].CancelMethod, "before-done record must preserve hard versus graceful")

			ts.persistCancelBeforeDone()
			ts.Finish(tc.hard)
			after, err := al.GetSessionStore().ReadTranscript(sid)
			require.NoError(t, err)
			assert.Equal(t, entries, after, "repeat before-done and Finish must neither duplicate nor relabel the record")
		})
	}
}
