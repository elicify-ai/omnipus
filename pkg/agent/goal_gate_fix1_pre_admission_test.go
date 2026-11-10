package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// The accepted wake must not retain an unadmitted check-in after a real receipt
// read failure. No publication-false fault, human reset, or rollback helper call.
func TestGateFix1BusyCheckInReceiptFaultReleasesReservation(t *testing.T) {
	h, process := b2DraftHarness(t, false)
	first := time.Now().UTC().Add(-31 * time.Minute)
	b2DraftTick(t, h, first)
	grNoPublishedReminder(t, h.al)
	wake := b2DraftTakeDue(t, h, process, first.Add(b2SpecStall))
	prior := h.load(t)
	require.Equal(t, session.LifecycleCompleted, prior.State)
	require.NotNil(t, prior.ExecutionID)
	require.Nil(t, h.al.ownLiveTurn(h.id))
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	reservation, sent := work.checkIn, work.checkInSent
	work.mu.Unlock()
	require.NotNil(t, reservation, "BLOCKED: actual keeper must have reserved the bus-accepted check-in")
	require.True(t, sent)
	require.Empty(t, reservation.execution.RunID, "BLOCKED: the matching wake must still be unadmitted")
	store := h.al.GetSessionStore()
	entries, err := store.ReadTranscript(h.id)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	// This is the actual current partition path of the real UnifiedStore.
	partition := filepath.Join(store.BaseDir(), h.id, "transcript.jsonl")
	st, err := os.Stat(partition)
	require.NoError(t, err, "BLOCKED: real canonical current partition must exist")
	require.True(t, st.Mode().IsRegular())
	backup := partition + ".gate1-owned-backup"
	require.NoError(t, os.Rename(partition, backup))
	restored := false
	restore := func() {
		if restored {
			return
		}
		require.NoError(t, os.Remove(partition))
		require.NoError(t, os.Rename(backup, partition))
		restored = true
	}
	require.NoError(t, os.Mkdir(partition, 0700))
	t.Cleanup(restore)
	_, readErr := store.ReadTranscript(h.id)
	require.Error(t, readErr, "BLOCKED: owned filesystem fault must really fail canonical transcript read")
	run := h.system(wake)
	failed := grJoin(t, run)
	require.Error(t, failed.err)
	require.True(t, strings.Contains(failed.err.Error(), "goal reminder: read acceptance receipt:"), "BLOCKED: must reach the actual early ordinary receipt-read failure")
	restore()
	_, err = store.ReadTranscript(h.id)
	require.NoError(t, err, "owned canonical partition must be restored before the cleanup/liveness assertion")
	after := h.load(t)
	require.Equal(t, prior.Generation, after.Generation)
	require.Equal(t, prior.ExecutionID.RunID, after.ExecutionID.RunID)
	require.Equal(t, session.LifecycleCompleted, after.State)
	require.Nil(t, h.al.ownLiveTurn(h.id))
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	work.mu.Lock()
	cleared, pending := work.checkIn == nil, work.pending
	work.mu.Unlock()
	require.Nil(t, pending)
	// Parent's B2 ownership requirement: an exact matching unadmitted wake
	// cannot permanently reserve the only busy check-in after its receiver fails.
	require.True(t, cleared, "accepted but unadmitted receipt failure must release its matching check-in reservation")
	retry := b2DraftTakeDue(t, h, process, first.Add(b2SpecStall+time.Second))
	later := h.system(retry)
	call := grAwaitCall(t, h.root, later)
	h.assertAdmission(t, call, prior)
	b2DraftTick(t, h, first.Add(b2SpecStall+2*time.Second))
	grNoPublishedReminder(t, h.al)
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	h.finish(t, later, call)
	grAwaitOwner(t, call)
}
