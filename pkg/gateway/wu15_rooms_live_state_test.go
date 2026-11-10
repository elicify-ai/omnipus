package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestWU15Rooms_LiveMemberStateAndMetaAgreeWithCatchupSnapshot(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	conn := h.connect()
	first := h.attach(conn, room.Id, 0)
	h.post(room.Id, "live-held", "@Mia wait")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "live-held"), "mia")
	var state gen.RoomMemberStateFrame
	for state.Delivery.State != "held" {
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "room_member_state"), &state))
		require.Equal(t, room.Id, state.RoomId, "K27/K45: correct room scope")
	}
	require.Equal(t, request.RequestId, state.Delivery.RequestId)
	require.Equal(t, "mia", state.Delivery.AgentId)
	require.Greater(t, state.Seq, first.Seq, "K45: live frame advances stream cursor")
	fresh := h.snapshot(room.Id)
	var same []gen.RoomDeliveryState
	for _, delivery := range fresh.MemberStates {
		if delivery.RequestId == request.RequestId {
			same = append(same, delivery)
		}
	}
	require.Equal(t, []gen.RoomDeliveryState{state.Delivery}, same, "K27: reload equals real live state, not fixture state")
	status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionMute, AgentIds: []string{"jim"}})
	require.Equal(t, http.StatusOK, status, "FR-158: %s", body)
	var meta gen.RoomMetaFrame
	for len(meta.Muted) == 0 {
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "room_meta"), &meta))
	}
	require.Equal(t, room.Id, meta.RoomId)
	require.Equal(t, []string{"jim"}, meta.Muted)
	require.ElementsMatch(t, room.Members, meta.Members)
	require.Greater(t, meta.Seq, state.Seq)
	fresh = h.snapshot(room.Id)
	require.Equal(t, meta.Muted, fresh.Muted)
	require.Equal(t, meta.Paused, fresh.Paused)
	require.Equal(t, meta.Generation, fresh.Generation)
	require.Equal(t, meta.AllocatedCostMicroUsd, fresh.AllocatedCostMicroUsd)
	require.Equal(t, meta.AllocatedCostUnpriced, fresh.AllocatedCostUnpriced)
}

func TestWU15Rooms_OutstandingRequestsKeepDistinctStatesAndRoomIsolation(t *testing.T) {
	h := wu15NewHarness(t)
	roomA := h.create("squad", "A", "mia", "jim")
	roomB := h.create("squad", "B", "mia", "jim")
	h.stopMain("mia")
	h.post(roomA.Id, "a1", "@Mia first")
	h.post(roomA.Id, "a2", "@Mia second")
	h.post(roomB.Id, "b1", "@Mia other room")
	a, b := h.snapshot(roomA.Id), h.snapshot(roomB.Id)
	a1 := wu15Delivery(t, wu15FindEntry(t, a, "a1"), "mia")
	a2 := wu15Delivery(t, wu15FindEntry(t, a, "a2"), "mia")
	b1 := wu15Delivery(t, wu15FindEntry(t, b, "b1"), "mia")
	require.NotEqual(t, a1.RequestId, a2.RequestId, "§6.11: each outstanding request has its own state")
	require.NotEqual(t, a2.RequestId, b1.RequestId)
	var aStates, bStates []string
	for _, state := range a.MemberStates {
		if state.AgentId == "mia" {
			aStates = append(aStates, state.RequestId)
			require.Equal(t, "held", state.State)
		}
	}
	for _, state := range b.MemberStates {
		if state.AgentId == "mia" {
			bStates = append(bStates, state.RequestId)
			require.Equal(t, "held", state.State)
		}
	}
	require.ElementsMatch(t, []string{a1.RequestId, a2.RequestId}, aStates, "§6.11: no overwrite by latest member state")
	require.Equal(t, []string{b1.RequestId}, bStates, "K27: another room's request never contaminates this room")
	require.Greater(t, wu15FindEntry(t, a, "a2").Seq, wu15FindEntry(t, a, "a1").Seq, "K26: most recent request identifiable by ordered entry")
}
