package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Entity cases pin US-17 AS-1/2/8/13, K23/K24 and §0.10. HTTP is the
// boundary even when the base has no room implementation.
func TestWU15Rooms_UserCreatesSquadAndAdhocWithOwnTranscript(t *testing.T) {
	for _, kind := range []string{"squad", "adhoc"} {
		t.Run(kind, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create(kind, "Launch", "mia", "jim")
			require.Equal(t, "Launch", room.Name)
			require.False(t, room.Paused)
			require.False(t, room.Hidden)
			require.Nil(t, room.ReadOnlyReason)
			require.Equal(t, int64(0), room.AllocatedCostMicroUsd)
			snap := h.snapshot(room.Id)
			require.Empty(t, snap.Entries, "FR-199: a new room owns an empty transcript")
			require.ElementsMatch(t, []string{"mia", "jim"}, snap.Members)
			require.Equal(t, room, h.room(room.Id), "K23: complete metadata round trip")
		})
	}
}

func TestWU15Rooms_TwoAgentMinimumAndDistinctMembers(t *testing.T) {
	for _, ids := range [][]string{{}, {"mia"}, {"mia", "jim"}, {"mia", "jim", "researcher2"}, {"mia", "mia"}} {
		t.Run(fmt.Sprintf("members_%v", ids), func(t *testing.T) {
			h := wu15NewHarness(t)
			// Positive route control makes a 404 an absent feature, not a
			// false-negative proof of semantic membership rejection.
			h.create("squad", "Control", "mia", "jim")
			name := "Launch"
			before := h.listRooms(wu15Workspace)
			status, body := h.http(http.MethodPost, "/api/v1/workspaces/sales/rooms", gen.RoomCreateRequest{
				Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: ids,
			})
			if len(ids) < 2 || (len(ids) == 2 && ids[0] == ids[1]) {
				wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "at least two agents")
				require.ElementsMatch(t, before, h.listRooms(wu15Workspace), "FR-204: rejected room creation saved nothing")
			} else {
				require.Equal(t, http.StatusCreated, status, "FR-204: two and three distinct agents are valid: %s", body)
				var room gen.Room
				require.NoError(t, json.Unmarshal(body, &room))
				require.ElementsMatch(t, ids, room.Members)
			}
		})
	}
}

func TestWU15Rooms_SquadNameBoundaries(t *testing.T) {
	// K24: name length 1..80; all six boundary values are independent oracles.
	for _, length := range []int{0, 1, 2, 79, 80, 81} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			h := wu15NewHarness(t)
			h.create("squad", "Control", "mia", "jim")
			name := strings.Repeat("N", length)
			before := h.listRooms(wu15Workspace)
			status, body := h.http(http.MethodPost, "/api/v1/workspaces/sales/rooms", gen.RoomCreateRequest{
				Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: []string{"mia", "jim"},
			})
			if length == 0 || length == 81 {
				wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "")
				require.ElementsMatch(t, before, h.listRooms(wu15Workspace), "K24: rejected name saved nothing")
			} else {
				require.Equal(t, http.StatusCreated, status, "K24: valid name: %s", body)
				var room gen.Room
				require.NoError(t, json.Unmarshal(body, &room))
				require.Equal(t, name, room.Name, "K24: no truncation or generated replacement")
			}
		})
	}
}

func TestWU15Rooms_IneligibleAgentsAndTeamCreationAreRefused(t *testing.T) {
	for _, id := range []string{"ray", "gp", "omnipus", "missing-agent"} {
		t.Run(id, func(t *testing.T) {
			h := wu15NewHarness(t)
			h.create("squad", "Control", "mia", "jim")
			name := "Forbidden"
			before := h.listRooms(wu15Workspace)
			status, body := h.http(http.MethodPost, "/api/v1/workspaces/sales/rooms", gen.RoomCreateRequest{
				Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: []string{"mia", id},
			})
			wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "")
			require.ElementsMatch(t, before, h.listRooms(wu15Workspace), "FR-162: invalid participant saves nothing")
			if id == "ray" {
				var refusal gen.ErrorResponse
				require.NoError(t, json.Unmarshal(body, &refusal))
				require.Contains(t, refusal.Error, "Ray", "BDD-17.9: refusal names the agent")
				require.Contains(t, strings.ToLower(refusal.Error), "sales", "BDD-17.9: refusal names the workspace")
			}
		})
	}
	t.Run("team_is_server_owned", func(t *testing.T) {
		h := wu15NewHarness(t)
		h.create("squad", "Control", "mia", "jim")
		before := h.listRooms(wu15Workspace)
		status, body := h.rawHTTP(http.MethodPost, "/api/v1/workspaces/sales/rooms", []byte(`{"kind":"team","agent_ids":["mia","jim"]}`))
		wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "")
		require.ElementsMatch(t, before, h.listRooms(wu15Workspace), "K24: rejected team creation saves nothing")
	})
}

func TestWU15Rooms_MembershipMinimumMuteAndForeignAgentRefusal(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionRemove, AgentIds: []string{"jim"}})
	wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "a room needs at least two agents — delete the room instead")
	require.Equal(t, room, h.room(room.Id), "FR-204: rejected removal changes nothing")
	status, body = h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionAdd, AgentIds: []string{"ray"}})
	wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "")
	require.Equal(t, room, h.room(room.Id), "FR-162: cross-workspace add changes nothing")
	for _, action := range []gen.RoomMembersUpdateRequestAction{gen.RoomMembersUpdateRequestActionMute, gen.RoomMembersUpdateRequestActionUnmute, gen.RoomMembersUpdateRequestActionAdd, gen.RoomMembersUpdateRequestActionRemove} {
		id := "jim"
		if action == gen.RoomMembersUpdateRequestActionAdd || action == gen.RoomMembersUpdateRequestActionRemove {
			id = "researcher2"
		}
		status, body = h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: action, AgentIds: []string{id}})
		require.Equal(t, http.StatusOK, status, "K24: %s: %s", action, body)
		var updated gen.Room
		require.NoError(t, json.Unmarshal(body, &updated))
		switch action {
		case gen.RoomMembersUpdateRequestActionMute:
			require.Equal(t, []string{"jim"}, updated.Muted)
			require.ElementsMatch(t, []string{"mia", "jim"}, updated.Members, "FR-158: muted agents remain members and count toward minimum")
		case gen.RoomMembersUpdateRequestActionUnmute:
			require.Empty(t, updated.Muted)
		case gen.RoomMembersUpdateRequestActionAdd:
			require.ElementsMatch(t, []string{"mia", "jim", "researcher2"}, updated.Members)
		default:
			require.ElementsMatch(t, []string{"mia", "jim"}, updated.Members)
		}
	}
}
