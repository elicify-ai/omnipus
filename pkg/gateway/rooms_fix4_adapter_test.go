package gateway

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/rooms"
)

// Chief Q2 B: supplementary one-row CHECK evidence. The adapter and the room
// service are real; only the delivery-state append is faulted at its journal.
func TestRoomsFix4_RealCompletionAdapterReturnsStorageFailure(t *testing.T) {
	h := wu15NewHarness(t)
	j := &fix4RoomJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home)), state: "failed"}
	hub := fix4InstallJournal(t, h, j)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "queued", "@Mia task")
	d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "queued"), "mia")
	h.waitState(room.Id, d.RequestId, "admitted")
	require.True(t, hub.svc.BeginConsume(room.Id, d.RequestId, h.main("mia")))
	runtime := agentRoomRuntime{svc: hub.svc}
	j.fail.Store(true)
	discard, err := runtime.RoomConsumeEnded(room.Id, d.RequestId, errors.New("provider failed"))
	require.Positive(t, j.hits.Load(), "instrument: the real service completion write failed")
	require.Error(t, err, "the real completion adapter must return, not swallow, the service error")
	require.Equal(t, 500, rooms.StatusOf(err))
	require.False(t, discard, "storage failure is not disposal permission")
	capture, found, readErr := h.al.RequestLedger().Get(h.main("mia"), d.RequestId)
	require.NoError(t, readErr)
	require.True(t, found && !capture.Discarded)
	j.fail.Store(false)
	require.Equal(t, room.Id, h.room(room.Id).Id, "healthy read repairs only bookkeeping")
	discard, err = runtime.RoomConsumeEnded(room.Id, d.RequestId, errors.New("provider failed"))
	require.NoError(t, err, "healthy confirmed completion is not falsely reported as failed")
	require.True(t, discard)
}
