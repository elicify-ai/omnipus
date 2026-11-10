package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

type fix4CompletionRuntime struct {
	fakeRoomRuntime
	completionErr error
	discard       bool
}

func (r *fix4CompletionRuntime) RoomConsumeEnded(_, requestID string, _ error) (bool, error) {
	r.ended = append(r.ended, requestID)
	return r.discard, r.completionErr
}

// NEW-SF-1: an uncompleted finalizer must remain retryable, while confirmed
// work is not completed or discarded twice by the outer admission defer.
func TestRoomsFix4_FinalizerFinishesOnlyAfterCheckedCompletion(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fix4CompletionRuntime{fakeRoomRuntime: fakeRoomRuntime{allow: true}, completionErr: errors.New("terminal write failed")}
	f.al.SetRoomRuntime(rt)
	_, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	require.NoError(t, err)
	wake, found := addrDrainInbound(t, f.bus)
	require.True(t, found)
	f.al.markRequestDelivered(wake.SessionID, roomTestDelivery)
	m := f.al.newTurnRoomMeter(bus.InboundMessage{SessionID: wake.SessionID})
	m.ownInput(roomTestDelivery, "room-1")
	err = f.al.finishRoomTurn(m, errors.New("provider failed"))
	require.ErrorIs(t, err, rt.completionErr)
	require.False(t, m.finished, "a checked completion failure is not a finished settlement")
	rt.completionErr, rt.discard = nil, true
	require.NoError(t, f.al.finishRoomTurn(m, errors.New("provider failed")))
	require.True(t, m.finished)
	capture, found, err := f.al.RequestLedger().Get(wake.SessionID, roomTestDelivery)
	require.NoError(t, err)
	require.True(t, found && capture.Discarded)
	require.Equal(t, []string{roomTestDelivery, roomTestDelivery}, rt.ended, "only the failed completion and one successful retry")
	require.NoError(t, f.al.finishRoomTurn(m, errors.New("provider failed")))
	require.Len(t, rt.ended, 2, "already-confirmed finalization is idempotent")
}

// Keeping a failed completion's capture for bookkeeping is not permission to
// schedule another model turn or an unanswered reminder for the failed input.
func TestRoomsFix4_CompletionFailureKeepsCaptureWithoutReminderReplay(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fix4CompletionRuntime{fakeRoomRuntime: fakeRoomRuntime{allow: true}, completionErr: errors.New("terminal write failed")}
	f.al.SetRoomRuntime(rt)
	_, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	require.NoError(t, err)
	wake, found := addrDrainInbound(t, f.bus)
	require.True(t, found)
	f.al.markRequestDelivered(wake.SessionID, roomTestDelivery)
	m := f.al.newTurnRoomMeter(bus.InboundMessage{SessionID: wake.SessionID})
	m.ownInput(roomTestDelivery, "room-1")
	err = f.al.settleRoomInputs(m, errors.New("provider failed"))
	require.ErrorIs(t, err, rt.completionErr, "completion failure is returned, not swallowed")
	capture, found, err := f.al.RequestLedger().Get(wake.SessionID, roomTestDelivery)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, capture.Discarded, "keep the live bookkeeping/reply capability")
	require.True(t, capture.Delivered, "retention must not make old work undelivered")
	_, pending := f.al.pendingRequests.Load(roomTestDelivery)
	require.False(t, pending, "failed work is not owed another automatic model reminder")
	f.al.remindUnansweredRequests(wake.SessionID)
	if _, published := addrDrainInbound(t, f.bus); published {
		t.Fatal("failed task was automatically replayed through a reminder")
	}
}
