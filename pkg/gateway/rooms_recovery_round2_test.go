package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
)

type fix2StateWriteJournal struct {
	rooms.Journal
	state   string
	fail    atomic.Bool
	hits    atomic.Int64
	working atomic.Int64
}

func (j *fix2StateWriteJournal) Append(id, stream string, value any) error {
	if stream == rooms.StreamDeliveries {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var record map[string]any
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record["state"] == j.state && j.fail.Load() {
			j.hits.Add(1)
			return errors.New("injected " + j.state + " append failure")
		}
		if record["state"] == "working" {
			j.working.Add(1)
		}
	}
	return j.Journal.Append(id, stream, value)
}

func fix2InstallStateJournal(t *testing.T, h *wu15RoomHarness, state string) *fix2StateWriteJournal {
	t.Helper()
	channel, found := h.rs.ChannelManager.GetChannel("webchat")
	require.True(t, found)
	web, ok := channel.(*webchatChannel)
	require.True(t, ok)
	hub := web.wsHandler.rooms
	hub.svc.Close()
	j := &fix2StateWriteJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home)), state: state}
	hub.svc = rooms.New(j, rooms.DepsFrom(&gatewayRoomDeps{h: web.wsHandler, home: h.home, hub: hub}))
	h.al.SetRoomRuntime(agentRoomRuntime{svc: hub.svc})
	return j
}

func TestRoomsFix2_RealFailedHandoffAndUndoRecoverOnRetry(t *testing.T) {
	h := wu15NewHarness(t)
	// A real aggregate-cap refusal, not a replaced handoff implementation.
	h.cfg.SessionMessaging.SteerAggregateBody = 1
	h = h.restart()
	h.stopMain("mia")
	j := fix2InstallStateJournal(t, h, "admitted")
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "held", "@Mia HANDOFF_TASK")
	d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, d.RequestId, "held")
	j.fail.Store(true)
	h.provider("mia", &wu15ScriptProvider{answer: "unrelated web answer"})
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.sendMain(conn, "mia", "REVIVE")
	wu15ReadTurnDone(t, conn)
	require.Equal(t, int64(1), j.working.Load(), "instrument: the real fence committed working before the handoff failed")
	require.Positive(t, j.hits.Load(), "instrument: failed handoff attempted the real undo, whose write failed too")
	status, body := h.http(http.MethodGet, "/api/v1/rooms/"+room.Id, nil)
	require.Equal(t, http.StatusInternalServerError, status, "pending undo is not a successful queued/working label: %s", body)
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), d.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, capture.Delivered)
	require.False(t, capture.Discarded)
	j.fail.Store(false)
	h.provider("mia", &wu15ScriptProvider{requestID: d.RequestId, answer: "room answer"})
	h.post(room.Id, "held", "@Mia HANDOFF_TASK")
	h.waitState(room.Id, d.RequestId, "answered")
	entries, err := h.al.GetSessionStore().ReadTranscript(h.main("mia"))
	require.NoError(t, err)
	requests := 0
	for _, entry := range entries {
		if entry.ID == d.RequestId {
			requests++
		}
	}
	require.Equal(t, 1, requests, "recovery reuses the captured request, never a second append")
	require.Equal(t, int64(2), j.working.Load(), "only the refused handoff and the one actual retry consume")
}

func TestRoomsFix2_RealDeferredHeldRetryRunsWithoutAnotherWebTurn(t *testing.T) {
	h := wu15NewHarness(t)
	h.stopMain("mia")
	j := fix2InstallStateJournal(t, h, "working")
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "held", "@Mia DEFERRED_TASK")
	d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, d.RequestId, "held")
	j.fail.Store(true)
	h.provider("mia", &wu15ScriptProvider{answer: "unrelated web answer"})
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.sendMain(conn, "mia", "REVIVE")
	wu15ReadTurnDone(t, conn)
	require.Positive(t, j.hits.Load(), "instrument: real held-to-working fence write failed")
	h.waitState(room.Id, d.RequestId, "held")
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), d.RequestId)
	require.NoError(t, err)
	require.True(t, found && !capture.Delivered && !capture.Discarded)
	j.fail.Store(false)
	h.provider("mia", &wu15ScriptProvider{requestID: d.RequestId, answer: "retried answer"})
	h.post(room.Id, "held", "@Mia DEFERRED_TASK")
	h.waitState(room.Id, d.RequestId, "answered")
	require.Equal(t, int64(1), j.working.Load(), "the request actually executes once, only after Retry")
	entries, err := h.al.GetSessionStore().ReadTranscript(h.main("mia"))
	require.NoError(t, err)
	requests := 0
	for _, entry := range entries {
		if entry.ID == d.RequestId {
			requests++
		}
	}
	require.Equal(t, 1, requests)
}
