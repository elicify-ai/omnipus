package askuser

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// The founder's 2026-10-21 rule applies even when a question carries the old
// opt-in. Decode that fixture rather than referring to a field being deleted,
// so the SAME behavioral assertion runs before and after removal.
func noAutoAnswerQuestion(t *testing.T) Question {
	t.Helper()
	var q Question
	require.NoError(t, json.Unmarshal([]byte(`{"header":"Scope","question":"Which scope?","options":[{"label":"Backend"},{"label":"Full stack"}],"recommended":"Backend","default_safe":true}`), &q))
	return q
}

func TestQuestionWaitsForUserBeyondFormerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resume, sink := &fakeResume{}, &fakeSink{}
		reg := NewRegistry(nil, resume, Options{Sink: sink})
		set := testSet("session-wait", noAutoAnswerQuestion(t))
		require.NoError(t, reg.CreatePending(set))

		// The removed opt-in fired at 30 minutes. The fake clock advances
		// beyond that deadline without waiting in real time.
		time.Sleep(31 * time.Minute)
		synctest.Wait()
		pending, ok := reg.Get(set.CardID)
		require.True(t, ok, "an unanswered question must remain registered after the former deadline")
		require.Equal(t, StatusPending, pending.Status)
		require.Empty(t, pending.Answers)
		require.Equal(t, 0, resume.count(), "time alone must never dispatch an answer")
		require.Equal(t, 1, sink.count(), "time alone must not emit a changed card")

		// A later, explicit answer remains reachable after an entire day.
		time.Sleep(24 * time.Hour)
		require.NoError(t, reg.Submit(set.CardID, set.TranscriptSessionID, "", []SubmittedAnswer{
			{Header: "Scope", Selected: []string{"Full stack"}},
		}))
		require.Equal(t, 1, resume.count())
		require.Equal(t, []string{"Full stack"}, resume.last(t).Set.Answers[0].Selected)
	})
}

func TestQuestionRestartWaitsForUserBeyondFormerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(t)
		sid := newOwnerSession(t, store)
		set := testSet(sid, noAutoAnswerQuestion(t))
		set.Status = StatusPending
		set.CreatedAt = time.Now().Add(-31 * time.Minute)
		data, err := json.Marshal(set)
		require.NoError(t, err)
		persisted := string(data)
		require.NoError(t, store.SetMeta(sid, session.MetaPatch{PendingAskJSON: &persisted}))

		resume := &fakeResume{}
		reg := NewRegistry(store, resume, Options{})
		require.NoError(t, reg.RearmSession(sid))
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		pending, ok := reg.PendingForSession(sid)
		require.True(t, ok, "restart must not answer an overdue question")
		require.Equal(t, StatusPending, pending.Status)
		require.Empty(t, pending.Answers)
		require.Equal(t, 0, resume.count())

		meta, err := store.GetMeta(sid)
		require.NoError(t, err)
		var durable PendingSet
		require.NoError(t, json.Unmarshal([]byte(meta.PendingAskJSON), &durable))
		require.Equal(t, StatusPending, durable.Status)
		require.Empty(t, durable.Answers)
		require.True(t, reg.CancelOnSessionStop(sid), "existing Stop must still cancel a parked question")
		require.Equal(t, 0, reg.PendingCount())
		require.Equal(t, 0, resume.count(), "Stop must not resume the turn")
	})
}
