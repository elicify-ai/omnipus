package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestWU15Rooms_PauseCancelsQueuedHeldAndAwaitingApproval(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim", "researcher2")
	h.stopMain("mia")
	h.receiverPolicy("jim", "ask")
	// Q-A starts the real loop to prime Mia. Keep Researcher2 genuinely busy
	// so this case still exercises an unconsumed queued request, not an idle
	// member that is free to finish before the pause reaches the server.
	busy := &wu15ScriptProvider{answer: "ordinary busy main finished", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("researcher2", busy)
	mainConn := h.connect()
	h.sendMain(mainConn, "researcher2", "hold this main while the room request queues")
	defer close(busy.release)
	select {
	case <-busy.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: the queued receiver's real busy turn never reached its model edge")
	}
	h.post(room.Id, "pending", "status?")
	entry := wu15FindEntry(t, h.snapshot(room.Id), "pending")
	before := h.room(room.Id)
	h.waitState(room.Id, wu15Delivery(t, entry, "mia").RequestId, "held")
	h.waitState(room.Id, wu15Delivery(t, entry, "jim").RequestId, "awaiting_approval")
	h.waitState(room.Id, wu15Delivery(t, entry, "researcher2").RequestId, "admitted")
	pending := h.api.approvalReg.pendingApprovals()
	require.Len(t, pending, 1)
	h.post(room.Id, "pause", "/pause")
	after := h.room(room.Id)
	require.True(t, after.Paused)
	require.Equal(t, before.Generation+1, after.Generation, "FR-217: one durable generation bump")
	for _, id := range room.Members {
		delivery := wu15Delivery(t, entry, id)
		h.waitState(room.Id, delivery.RequestId, "cancelled")
		capture, found, err := h.al.RequestLedger().Get(h.main(id), delivery.RequestId)
		require.NoError(t, err)
		if found {
			require.True(t, capture.Discarded, "FR-217: ledger discards admitted-but-unconsumed input")
		}
	}
	require.Empty(t, h.api.approvalReg.pendingApprovals(), "FR-251/217: pause cancels pre-admission approval")
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+pending[0].ApprovalID, gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.GreaterOrEqual(t, status, 400, "FR-217: late approval must be visibly stale: %s", body)
	_, found, err := h.al.RequestLedger().Get(h.main("jim"), wu15Delivery(t, entry, "jim").RequestId)
	require.NoError(t, err)
	require.False(t, found, "FR-217: late approval never captures/wakes")
	h = h.restart()
	require.Equal(t, after.Generation, h.room(room.Id).Generation, "FR-217: fence survives restart")
}

func TestWU15Rooms_ResumeSendsNewMessagesOnlyNeverPausedOrCancelledInputs(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	h.stopMain("jim")
	h.post(room.Id, "old", "old pending input")
	old := wu15FindEntry(t, h.snapshot(room.Id), "old")
	h.post(room.Id, "pause", "/pause")
	h.post(room.Id, "while-paused", "do not replay this")
	paused := wu15FindEntry(t, h.snapshot(room.Id), "while-paused")
	require.Empty(t, paused.Deliveries, "FR-170: paused messages retained without admission")
	h.post(room.Id, "resume", "/resume")
	require.False(t, h.room(room.Id).Paused)
	h.post(room.Id, "new", "new input")
	snap := h.snapshot(room.Id)
	require.Equal(t, paused, wu15FindEntry(t, snap, "while-paused"), "FR-170: resume does not redeliver old room text")
	for _, id := range room.Members {
		delivery := wu15Delivery(t, old, id)
		h.waitState(room.Id, delivery.RequestId, "cancelled")
		capture, found, err := h.al.RequestLedger().Get(h.main(id), delivery.RequestId)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, capture.Discarded, "FR-217: old generation never comes back")
		newDelivery := wu15Delivery(t, wu15FindEntry(t, snap, "new"), id)
		require.NotEqual(t, delivery.RequestId, newDelivery.RequestId)
		h.waitState(room.Id, newDelivery.RequestId, "held")
	}
}

func TestWU15Rooms_RunningMainFinishesAfterPauseAndResumeWithoutMentionFanout(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	roomConn := h.connect()
	h.attach(roomConn, room.Id, 0)
	h.post(room.Id, "running", "@Jim do the work")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "running"), "jim")
	p := &wu15ScriptProvider{requestID: request.RequestId, answer: "@Mia answer after pause", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("jim", p)
	h.startLoop()
	select {
	case <-p.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: real receiver provider never started")
	}
	h.waitState(room.Id, request.RequestId, "working")
	h.post(room.Id, "pause", "/pause")
	h.post(room.Id, "resume", "/resume")
	close(p.release)
	snap := h.waitState(room.Id, request.RequestId, "answered")
	var answers []gen.RoomEntry
	for _, entry := range snap.Entries {
		if entry.Author.Kind == "agent" && entry.Text == p.answer {
			answers = append(answers, entry)
		}
	}
	require.Len(t, answers, 1, "FR-170: running main work finishes, not interrupted by room pause")
	require.True(t, answers[0].AfterPause, "FR-217/BDD-17.29: sticky old-generation after-pause mark")
	require.Empty(t, answers[0].Deliveries, "FR-170/217: old-generation @Mia never forwarded")
	for _, message := range h.messages("mia") {
		if message.RoomRef != nil {
			require.NotEqual(t, room.Id, message.RoomRef.RoomId, "FR-170: no request from after-pause mentions")
		}
	}
	h = h.restart()
	reloaded := h.snapshot(room.Id)
	var persisted []gen.RoomEntry
	for _, entry := range reloaded.Entries {
		if entry.Id == answers[0].Id {
			persisted = append(persisted, entry)
		}
	}
	require.Equal(t, answers, persisted, "FR-217: after-pause answer retained after resume/reload")
}

func TestWU15Rooms_RemovalAndDeletionCancelPendingAdmission(t *testing.T) {
	for _, action := range []string{"remove", "delete"} {
		t.Run(action, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim", "researcher2")
			h.receiverPolicy("jim", "ask")
			h.post(room.Id, "pending", "@Jim ask before capture")
			request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "pending"), "jim")
			h.waitState(room.Id, request.RequestId, "awaiting_approval")
			pending := h.api.approvalReg.pendingApprovals()
			require.Len(t, pending, 1)
			if action == "remove" {
				status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionRemove, AgentIds: []string{"jim"}})
				require.Equal(t, http.StatusOK, status, "FR-217: removal: %s", body)
				h.waitState(room.Id, request.RequestId, "cancelled")
			} else {
				status, body := h.http(http.MethodDelete, "/api/v1/rooms/"+room.Id, nil)
				require.Equal(t, http.StatusNoContent, status, "FR-217: deletion: %s", body)
			}
			require.Empty(t, h.api.approvalReg.pendingApprovals(), "FR-251: removal/deletion cancels pending card")
			_, data := h.http(http.MethodPost, "/api/v1/tool-approvals/"+pending[0].ApprovalID, gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
			var refusal gen.ErrorResponse
			require.NoError(t, json.Unmarshal(data, &refusal))
			require.NotEmpty(t, refusal.Error)
			_, found, err := h.al.RequestLedger().Get(h.main("jim"), request.RequestId)
			require.NoError(t, err)
			require.False(t, found, "FR-217: no late capture of removed member/deleted room")
		})
	}
}
