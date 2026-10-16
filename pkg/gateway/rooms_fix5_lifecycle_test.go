package gateway

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Architect R3-N2: the actual async producer shape has no SessionID. A
// successful system turn still owes the consuming main exactly one reminder.
func TestRoomsFix5_SystemWakeNoAnswerSettlesAfterOneReminder(t *testing.T) {
	h := wu15NewHarness(t)
	h.stopMain("mia")
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "held", "@Mia SYSTEM_NO_ANSWER")
	d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, d.RequestId, "held")
	// Saved-state fixture: the preceding turn has finished and the main is
	// runnable. This does not grant a system wake authority to lift Stop.
	require.NoError(t, h.al.GetSessionLifecycleStore().Mutate(h.main("mia"), func(r *session.LifecycleRecord) error {
		r.State, r.Stop, r.StopNote = session.LifecycleCompleted, nil, nil
		return nil
	}))
	p := &wu15ScriptProvider{answer: "A plain main-chat response, not a group answer."}
	h.provider("mia", p)
	var reminders atomic.Int64
	h.al.SetEventSyncTap(func(e agent.Event) {
		if start, ok := e.Payload.(agent.TurnStartPayload); e.Kind == agent.EventKindTurnStart && ok && start.ChatID == "request-reminder:"+h.main("mia") {
			reminders.Add(1)
		}
	})
	rec, err := h.al.GetSessionLifecycleStore().Load(h.main("mia"))
	require.NoError(t, err)
	require.NoError(t, h.bus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "system", Content: "helper finished", AsyncOriginAgentID: "mia",
		AsyncTranscriptSessionID: h.main("mia"),
		Metadata:                 map[string]string{"steer_message_id": "fix5-helper-final", "steer_generation": strconv.Itoa(rec.Generation)},
	}))
	snap := h.waitState(room.Id, d.RequestId, "failed")
	final := wu15Delivery(t, wu15FindEntry(t, snap, "held"), "mia")
	require.NotNil(t, final.Reason)
	require.Equal(t, "no_answer", *final.Reason)
	fix4WaitTurnSettled(t, h, "mia")
	require.Equal(t, int64(1), reminders.Load(), "one reminder, without a further human message")
	require.Len(t, p.inputs(), 2, "one successful consuming turn and its one successful reminder")
	wu15AssertProviderSaw(t, p, "SYSTEM_NO_ANSWER")
	require.Len(t, snap.Entries, 1, "plain main replies must not masquerade as room answers")
}

// Architect R3-N3: the room wake IS the starting input, not an additional held
// input. The paid provider waits for forced cancellation so this reaches Aborted.
func TestRoomsFix5_HardStopInterruptsInitiatingRequestWithoutReplay(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	p := &wu15ScriptProvider{answer: "later ordinary response", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("mia", p)
	var aborted atomic.Int64
	h.al.SetEventSyncTap(func(e agent.Event) {
		if end, ok := e.Payload.(agent.TurnEndPayload); e.Kind == agent.EventKindTurnEnd && ok && end.SessionID == h.main("mia") && end.Status == agent.TurnEndStatusAborted {
			aborted.Add(1)
		}
	})
	h.post(room.Id, "root", "@Mia SOLE_ROOM_START")
	d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "root"), "mia")
	h.startLoop()
	select {
	case <-p.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("instrument: sole room wake never entered the model")
	}
	h.waitState(room.Id, d.RequestId, "working")
	result, err := h.al.StopSession(context.Background(), agent.StopRequest{SessionID: h.main("mia"), By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "wu15-tester"}, Channel: "webchat"})
	require.NoError(t, err)
	require.True(t, result.Durable)
	require.NoError(t, result.RootErr)
	require.Empty(t, result.Report.Unreachable)
	fix4WaitTurnSettled(t, h, "mia")
	require.Equal(t, int64(1), aborted.Load(), "instrument: actual forced hard-abort branch, not a provider-error substitute")
	final := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "root"), "mia")
	require.Equal(t, "interrupted", final.State, "Stop must settle the initiating request even when its public turn result has nil error")
	require.Nil(t, final.Reason, "a user Stop is neither no_answer nor restart")
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), d.RequestId)
	require.NoError(t, err)
	require.True(t, found && capture.Discarded, "interrupted work is not eligible for a reminder or replay")
	h.post(room.Id, "root", "@Mia SOLE_ROOM_START")
	require.Len(t, p.inputs(), 1, "same-message Retry must not replay consumed work")
	close(p.release)
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.sendMain(conn, "mia", "A new ordinary task, not an ask-again for the group.")
	wu15ReadFrame(t, conn, "done")
	fix4WaitTurnSettled(t, h, "mia")
	final = wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "root"), "mia")
	require.Equal(t, "interrupted", final.State, "a later turn must not downgrade the Stop to no_answer")
	require.Len(t, p.inputs(), 2, "only the original turn and the explicitly new human turn")
}
