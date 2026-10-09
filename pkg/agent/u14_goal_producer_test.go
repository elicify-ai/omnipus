package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Oracle: session-core spec FR-039 / C-GOAL —
//
//	"Capture actual dispatch/activation goal_id into producing run/turn;
//	 ordinary work without proven goal is unknown."
//
// The producing turn captures the session's active goal id ONCE at turn start
// (turnState.goalID, newTurnState) and every entry it writes carries the SAME
// association. A session with no active goal stays UNKNOWN (empty), never a
// guessed/latest value — and the empty association is omitted on the wire
// (omitempty), so the SPA can tell "unknown" from a specific goal.
//
// Oracle provenance is the spec clause, not the implementation: the expected
// goal id is the one seedActiveGoalRecord returned, independent of how the
// producer resolves it.
func TestU14_ProducerCapturesAndStampsGoalID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	base := filepath.Join(home, "sessions")
	store, err := session.NewUnifiedStoreWithHome(base, home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// A session WITH an active goal bound to it.
	withGoal, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	rec := seedActiveGoalRecord(t, withGoal.ID, "ship the release notes", nil, nil)
	require.NotEmpty(t, rec.GoalID)

	// A session with NO active goal (control: unknown association).
	noGoal, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)

	// stamp runs a bare producing turn for the session and returns the turn
	// state plus the single assistant entry it persisted.
	stamp := func(sid, turnID string) (*turnState, session.TranscriptEntry) {
		ts := newTurnState(&AgentInstance{ID: "mia"}, processOptions{
			SessionKey:          "agent:mia:session:" + sid,
			TranscriptSessionID: sid,
			TranscriptStore:     store,
			NoHistory:           true,
		}, turnEventScope{turnID: turnID})
		ts.appendAssistantTranscript("hello from " + sid)
		entries, rerr := store.ReadTranscript(sid)
		require.NoError(t, rerr)
		require.Len(t, entries, 1, "exactly one assistant entry must be written")
		return ts, entries[0]
	}

	ts, entry := stamp(withGoal.ID, "turn-u14-with-goal")
	require.Equal(t, rec.GoalID, ts.goalID,
		"turn start must capture the session's active goal id")
	require.Equal(t, rec.GoalID, entry.GoalID,
		"the persisted assistant entry must carry the producing turn's captured goal_id")

	raw, merr := json.Marshal(entry)
	require.NoError(t, merr)
	require.Contains(t, string(raw), `"goal_id":"`+rec.GoalID+`"`,
		"a captured goal_id must reach the wire (REST/replay share this struct)")

	// Negative control: a session with no active goal is UNKNOWN, not a guess.
	ts2, entry2 := stamp(noGoal.ID, "turn-u14-no-goal")
	require.Empty(t, ts2.goalID, "a turn with no proven goal must be unknown, not guessed")
	require.Empty(t, entry2.GoalID, "an unknown association must stay empty on the entry")

	raw2, merr2 := json.Marshal(entry2)
	require.NoError(t, merr2)
	require.NotContains(t, string(raw2), "goal_id",
		"an unknown association must be omitted on the wire (never a fabricated goal)")
}
