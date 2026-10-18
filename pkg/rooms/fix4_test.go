package rooms

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Round-4 completion/undo expectations come from the chief's R3-N1,
// NEW-SF-1 and R2-N1 residual brief, plus the existing no-replay ruling.
func TestRoomsFix4_FailedTerminalWriteKeepsRepairOwnership(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name, target, reason, turnErr := "failed", StateFailed, ReasonTurnFailed, errors.New("provider failed")
		if cancelled {
			name, target, reason, turnErr = "cancelled", StateInterrupted, "", context.Canceled
		}
		t.Run(name, func(t *testing.T) {
			f, j := newFaultFixture(t)
			room := f.squad("mia", "jim")
			require.NoError(t, f.post(room.Id, "c1", "@Jim work"))
			d := f.deliveryOf(room.Id, "jim")
			require.True(t, f.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
			j.set(func(j *faultJournal) { j.append = failState(target) })
			discard, completionErr := f.svc.ConsumeEnded(room.Id, d.RequestID, turnErr)
			require.Error(t, completionErr)
			require.False(t, discard, "an unrecorded completion cannot authorize capture disposal")
			require.Positive(t, j.injected(), "instrument: the terminal append failed")
			f.svc.cardMu.Lock()
			owner := f.svc.working["main-jim"][d.RequestID]
			f.svc.cardMu.Unlock()
			require.Equal(t, room.Id, owner, "a failed terminal write must not forget its repair owner")
			_, err := f.svc.Get(room.Id)
			require.Error(t, err, "reads must not serve a failed turn as healthy working")
			require.Equal(t, 500, StatusOf(err))
			j.set(func(j *faultJournal) { j.append = nil })
			// Restart before a successful repair read: durable completion intent
			// must preserve the actual failure instead of replaying model work.
			fresh := f.reopen()
			_, err = fresh.svc.Get(room.Id)
			require.NoError(t, err)
			require.Equal(t, target+"/"+reason, fresh.stateOf(room.Id, d.RequestID))
			require.False(t, fresh.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
		})
	}
}

// R3-N1 chief Q1 A: a late old wake is not cancellation authority over an
// already-posted answer's unfinished mirror. Defer preserves repair, not work.
func TestRoomsFix4_DuplicateWakePreservesUnmirroredAnswer(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim", "r2")
	require.NoError(t, f.post(room.Id, "c1", "@Jim task"))
	d := f.deliveryOf(room.Id, "jim")
	require.True(t, f.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
	f.deps.mirrorErr = errFault
	require.ErrorIs(t, f.answer(room.Id, d.RequestID, "jim", "answer @Mia"), ErrMirrorFailed)
	discard, err := f.svc.ConsumeEnded(room.Id, d.RequestID, errors.New("later provider failure"))
	require.NoError(t, err)
	require.False(t, discard)
	require.Equal(t, ConsumeDefer, f.svc.DecideConsume(room.Id, d.RequestID, "main-jim"), "late wake must neither execute nor invalidate the live repair route")
	require.Equal(t, "answered/", f.stateOf(room.Id, d.RequestID))
	f.deps.mirrorErr = nil
	require.NoError(t, f.answer(room.Id, d.RequestID, "jim", "answer @Mia"))
	require.Len(t, f.entriesOf(room.Id), 2)
	require.Equal(t, []string{"jim", "mia"}, f.deps.admittedTo())
	require.Equal(t, ConsumeCancel, f.svc.DecideConsume(room.Id, d.RequestID, "main-jim"), "fully mirrored terminal work is still not executed again")
}

func TestRoomsFix4_CompletionReadFailureIsRepairable(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	require.NoError(t, f.post(room.Id, "c1", "@Jim work"))
	d := f.deliveryOf(room.Id, "jim")
	require.True(t, f.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
	j := &fix2RecordReadFault{FileJournal: NewFileJournal(f.dir), fail: true}
	svc := New(j, DepsFrom(f.deps))
	t.Cleanup(svc.Close)
	discard, completionErr := svc.ConsumeEnded(room.Id, d.RequestID, errors.New("provider failed"))
	require.Error(t, completionErr)
	require.False(t, discard)
	require.Positive(t, j.hits, "instrument: completion could not load its room")
	j.fail = false
	_, err := svc.Get(room.Id)
	require.NoError(t, err)
	fresh := &fixture{t: t, dir: f.dir, deps: f.deps, svc: svc}
	require.Equal(t, "failed/turn_failed", fresh.stateOf(room.Id, d.RequestID), "repair keeps the known outcome, not ghost working or replay")
}

func TestRoomsFix4_ReminderCannotFailPendingHandoffUndo(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	require.NoError(t, f.post(room.Id, "older", "@Jim old task"))
	a := f.deliveryOf(room.Id, "jim")
	require.True(t, f.svc.BeginConsume(room.Id, a.RequestID, "main-jim"))
	require.NoError(t, f.post(room.Id, "unstarted", "@Jim new task"))
	b := f.deliveryOf(room.Id, "jim")
	require.NotEqual(t, a.RequestID, b.RequestID)
	require.True(t, f.svc.BeginConsume(room.Id, b.RequestID, "main-jim"))
	j.set(func(j *faultJournal) { j.append = failState(StateAdmitted) })
	require.Error(t, f.svc.ReleaseConsume(room.Id, b.RequestID))
	require.Positive(t, j.injected(), "instrument: handoff undo failed")
	j.set(func(j *faultJournal) { j.append = nil })
	// Storage recovered, but no room open/read repaired B before A's genuine
	// reminder ended. B was never handed to that turn or marked delivered.
	require.NoError(t, f.svc.ReminderTurnEnded("main-jim"))
	_, err := f.svc.Get(room.Id)
	require.NoError(t, err)
	require.Equal(t, "failed/no_answer", f.stateOf(room.Id, a.RequestID), "the real reminder still settles A")
	require.Equal(t, "admitted/", f.stateOf(room.Id, b.RequestID), "unstarted B remains runnable after undo repair")
	require.NoError(t, f.post(room.Id, "unstarted", "@Jim new task"))
	require.Len(t, f.admittedIDs(), 2, "Retry must reuse the existing capture")
	require.True(t, f.svc.BeginConsume(room.Id, b.RequestID, "main-jim"), "original-message Retry can run B once")
	require.False(t, f.svc.BeginConsume(room.Id, b.RequestID, "main-jim"), "no duplicate execution")
}

func TestRoomsFix4_ReleasedInputStillHonoursPause(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	require.NoError(t, f.post(room.Id, "held", "@Jim task"))
	d := f.deliveryOf(room.Id, "jim")
	require.True(t, f.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
	j.set(func(j *faultJournal) { j.append = failState(StateAdmitted) })
	require.Error(t, f.svc.ReleaseConsume(room.Id, d.RequestID))
	j.set(func(j *faultJournal) { j.append = nil })
	require.NoError(t, f.post(room.Id, "pause", "/pause"))
	require.Equal(t, "cancelled/paused", f.stateOf(room.Id, d.RequestID))
	require.False(t, f.svc.BeginConsume(room.Id, d.RequestID, "main-jim"), "undo repair cannot bypass the real pause fence")
}

// Documentation N1: neither write reaching disk is an explicitly limited
// durability case, not permission to replay interrupted work after restart.
func TestRoomsFix4_UnsavedUndoNeverPromisesRestartReplay(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	require.NoError(t, f.post(room.Id, "held", "@Jim task"))
	d := f.deliveryOf(room.Id, "jim")
	require.True(t, f.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
	j.set(func(j *faultJournal) {
		j.append = failState(StateAdmitted)
		j.writeRecord = func() error { return errFault }
	})
	require.Error(t, f.svc.ReleaseConsume(room.Id, d.RequestID))
	require.GreaterOrEqual(t, j.injected(), 2, "instrument: neither reset nor undo intent was saved")
	fresh := f.reopen()
	_, err := fresh.svc.Get(room.Id)
	require.NoError(t, err)
	require.Equal(t, "interrupted/restart", fresh.stateOf(room.Id, d.RequestID))
	require.False(t, fresh.svc.BeginConsume(room.Id, d.RequestID, "main-jim"))
}
