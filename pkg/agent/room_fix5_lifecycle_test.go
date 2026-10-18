package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Architect R3-N2: actual direct durable replay, not worker dispatch. Room
// state, request admission/ledger and both successful native turns stay real.
func TestRoomsFix5_DirectWakeNoAnswerOwnsOneReminder(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(map[bool]string{false: "runnable", true: "stopped_control"}[stopped], func(t *testing.T) {
			h, bridge, room, requestID := newFix5RoomMain(t, true)
			h.provider.id = h.id
			require.Equal(t, rooms.StateHeld, fix5RoomSnapshot(t, bridge, room.Id).MemberStates[0].State)
			if !stopped {
				require.NoError(t, h.al.GetSessionLifecycleStore().Mutate(h.id, func(r *session.LifecycleRecord) error {
					r.State, r.Stop, r.StopNote = session.LifecycleCompleted, nil, nil
					return nil
				}))
			}
			const messageID = "fix5-direct-final"
			appended, err := h.al.GetMessageInboxStore().Append(h.id, pauseHandbackMessage(t, "child-fix5", messageID))
			require.NoError(t, err)
			require.True(t, appended.Accepted)
			err = h.al.replayPostFinishWake(h.id, messageID)
			if stopped {
				require.ErrorContains(t, err, "was stopped; entry remains unacknowledged")
				require.Empty(t, h.provider.calls(), "a system wake cannot lift Stop")
				acked, ackErr := deliverEntryIsAcked(h.al.GetMessageInboxStore(), h.id, "", messageID)
				require.NoError(t, ackErr)
				require.False(t, acked)
				require.Equal(t, rooms.StateHeld, fix5RoomSnapshot(t, bridge, room.Id).MemberStates[0].State)
				require.Equal(t, session.LifecycleStopped, h.load(t).State)
				require.Empty(t, h.al.bus.InboundChan(), "stopped, unconsumed work owes no reminder")
				return
			}
			require.NoError(t, err)
			require.Len(t, h.provider.calls(), 1, "instrument: actual native replay consumed the room request")
			capture, found, err := h.al.RequestLedger().Get(h.id, requestID)
			require.NoError(t, err)
			require.True(t, found && capture.Delivered && !capture.Discarded)
			require.Equal(t, rooms.StateWorking, fix5RoomSnapshot(t, bridge, room.Id).MemberStates[0].State)
			reminder, ok := addrDrainInbound(t, h.al.bus)
			require.True(t, ok, "direct replay must publish the one reminder without another web message")
			require.Equal(t, h.id, reminder.SessionID, "reminder belongs to the actual consuming main")
			require.Equal(t, "1", reminder.Metadata[requestReminderMetadataKey])
			require.Equal(t, "1", reminder.Metadata[ownerWakeMetadataKey])
			require.Contains(t, reminder.Content, requestID)
			require.False(t, reminder.UserInitiated || reminder.OperatorPrompt, "reminder cannot acquire human authority")
			worker := newSessionWorker("agent:"+testDefaultAgentID+":session:"+h.id, h.al, func() {})
			worker.processTurn(context.Background(), reminder)
			require.Len(t, h.provider.calls(), 2, "one successful consuming turn plus one successful reminder")
			final := fix5RoomSnapshot(t, bridge, room.Id)
			require.Len(t, final.MemberStates, 1)
			require.Equal(t, rooms.StateFailed, final.MemberStates[0].State)
			require.NotNil(t, final.MemberStates[0].Reason)
			require.Equal(t, rooms.ReasonNoAnswer, *final.MemberStates[0].Reason)
			require.Len(t, final.Entries, 1, "neither plain response is a group answer")
			require.Empty(t, h.al.bus.InboundChan(), "one reminder only, never an automatic loop")
		})
	}
}

// Architect R3-N3's nil-error hard-abort path. Stop lands after a successful
// provider response, so abortTurn (not the typed provider-error exit) unwinds.
func TestRoomsFix5_InitiatingPostResponseHardAbortRetainsSilentReturn(t *testing.T) {
	h, bridge, room, requestID := newFix5RoomMain(t, false)
	wake, found := addrDrainInbound(t, h.al.bus)
	require.True(t, found)
	require.Equal(t, requestID, wake.TranscriptEntryID)
	h.al.SetEventSyncTap(func(e Event) {
		if e.Kind == EventKindLLMResponse {
			turns, err := h.al.InterruptSessionHard("agent:"+testDefaultAgentID+":session:"+h.id, ScopeSelfOnly, "")
			require.NoError(t, err)
			require.Len(t, turns, 1, "instrument: actual hard abort after the successful provider response")
		}
	})
	_, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), wake)
	require.NoError(t, err, "an intentional successful hard Stop keeps its public silent return")
	require.Len(t, h.provider.calls(), 1, "the abort cut follows one successful provider response")
	final := fix5RoomSnapshot(t, bridge, room.Id)
	require.Equal(t, rooms.StateInterrupted, final.MemberStates[0].State)
	require.Nil(t, final.MemberStates[0].Reason)
	capture, found, err := h.al.RequestLedger().Get(h.id, requestID)
	require.NoError(t, err)
	require.True(t, found && capture.Discarded)
}
