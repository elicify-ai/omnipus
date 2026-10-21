package gateway

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func (h *wu15RoomHarness) listRooms(workspaceID string) []gen.Room {
	h.t.Helper()
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/rooms", nil)
	if status == http.StatusNotFound {
		h.t.Fatalf("BLOCKED: GET /api/v1/workspaces/{id}/rooms not implemented — required by K23/FR-160 (want 200, got %d: %s)", status, body)
	}
	require.Equal(h.t, http.StatusOK, status, "K23: room list: %s", body)
	var rooms []gen.Room
	require.NoError(h.t, json.Unmarshal(body, &rooms))
	require.NotNil(h.t, rooms, "K23: successful list is never null")
	return rooms
}

func wu15TeamRoom(t *testing.T, rooms []gen.Room) gen.Room {
	t.Helper()
	var teams []gen.Room
	for _, room := range rooms {
		if room.Kind == gen.RoomKindTeam {
			teams = append(teams, room)
		}
	}
	require.Len(t, teams, 1, "FR-160: exactly one standing team room, even when hidden")
	return teams[0]
}

func (h *wu15RoomHarness) team(ids ...string) {
	h.t.Helper()
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/sales", nil)
	require.Equal(h.t, http.StatusOK, status)
	var current gen.Workspace
	require.NoError(h.t, json.Unmarshal(body, &current))
	status, body = h.http(http.MethodPut, "/api/v1/workspaces/sales", gen.WorkspaceUpdateRequest{Revision: current.Revision, CoreTeam: &ids})
	require.Equal(h.t, http.StatusOK, status, "FR-162: workspace membership write: %s", body)
}

func TestWU15Rooms_TeamIsUniqueAndExcludesWorkers(t *testing.T) {
	h := wu15NewHarness(t)
	team := wu15TeamRoom(t, h.listRooms(wu15Workspace))
	require.ElementsMatch(t, []string{"mia", "jim", "researcher2"}, team.Members, "BDD-17.1: workers/Omnipus are not room participants")
	require.False(t, team.Hidden)
	require.Nil(t, team.ReadOnlyReason)
	require.Equal(t, team, wu15TeamRoom(t, h.listRooms(wu15Workspace)), "FR-160: GET never creates another team room")
	for _, action := range []gen.RoomMembersUpdateRequestAction{gen.RoomMembersUpdateRequestActionAdd, gen.RoomMembersUpdateRequestActionRemove} {
		status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+team.Id+"/members", gen.RoomMembersUpdateRequest{Action: action, AgentIds: []string{"jim"}})
		wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "Team room members are the workspace's agents — add the agent to the workspace")
	}
	status, body := h.http(http.MethodDelete, "/api/v1/rooms/"+team.Id, nil)
	wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "")
	require.Equal(t, team, h.room(team.Id), "K24: team membership and identity unchanged by refused writes")
}

func TestWU15Rooms_TeamHidesBelowTwoAndReappearsWithHistory(t *testing.T) {
	h := wu15NewHarness(t)
	team := wu15TeamRoom(t, h.listRooms(wu15Workspace))
	h.post(team.Id, "mute-jim", "/mute @Jim")
	h.post(team.Id, "pause", "/pause")
	h.post(team.Id, "history", "remember this team history")
	before := h.snapshot(team.Id)
	h.team("mia")
	hidden := h.room(team.Id)
	require.True(t, hidden.Hidden, "FR-160: hidden below two")
	require.NotNil(t, hidden.ReadOnlyReason, "FR-160: read-only below two")
	require.Equal(t, before.AllocatedCostMicroUsd, hidden.AllocatedCostMicroUsd)
	require.ElementsMatch(t, before.Muted, hidden.Muted, "FR-160: mute preferences retained while hidden")
	h.team("mia", "jim", "researcher2")
	rejoined := wu15TeamRoom(t, h.listRooms(wu15Workspace))
	require.Equal(t, team.Id, rejoined.Id, "FR-160: same standing room after rejoin")
	require.False(t, rejoined.Hidden)
	require.ElementsMatch(t, []string{"mia", "jim", "researcher2"}, rejoined.Members)
	restored := h.snapshot(team.Id)
	require.Equal(t, before.Entries, restored.Entries, "FR-160: transcript retained exactly")
	require.Equal(t, before.AllocatedCostMicroUsd, restored.AllocatedCostMicroUsd)
	require.ElementsMatch(t, before.Muted, restored.Muted)
}

func TestWU15Rooms_WorkspaceRemovalCascadesAndSquadBecomesReadOnly(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.team("mia")
	updated := h.room(room.Id)
	require.Equal(t, []string{"mia"}, updated.Members, "FR-162: workspace removal removes every room membership")
	require.NotNil(t, updated.ReadOnlyReason, "FR-204: workspace-side reduction below two is read-only, not deletion")
	status, data := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "read-only", Text: "do not deliver", Mentions: []string{}})
	wu15RequireError(t, status, data, http.StatusUnprocessableEntity, "")
	h.team("mia", "researcher2")
	status, data = h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionAdd, AgentIds: []string{"researcher2"}})
	require.Equal(t, http.StatusOK, status, "FR-204: replacement member restores writeability: %s", data)
	require.Nil(t, h.room(room.Id).ReadOnlyReason)
}

func TestWU15Rooms_RoomTranscriptAndMetadataSurviveRestartAndStayInWorkspace(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "pause", "/pause")
	h.post(room.Id, "saved", "persist this exact text")
	before := h.snapshot(room.Id)
	metadata := h.room(room.Id)
	h = h.restart()
	require.Equal(t, metadata, h.room(room.Id), "FR-199: durable room metadata, not cache-only")
	after := h.snapshot(room.Id)
	require.Equal(t, before.Entries, after.Entries, "FR-199: transcript persists exactly once")
	for _, other := range h.listRooms("marketing") {
		require.NotEqual(t, room.Id, other.Id, "FR-162: workspace list isolation")
	}
	status, data := h.http(http.MethodDelete, "/api/v1/rooms/"+room.Id, nil)
	require.Equal(t, http.StatusNoContent, status, "K24: user can delete squad: %s", data)
	status, data = h.http(http.MethodGet, "/api/v1/rooms/"+room.Id, nil)
	wu15RequireError(t, status, data, http.StatusNotFound, "")
}

func TestWU15Rooms_AddParticipantStartsEmptyAndLeavesOriginMainUnchanged(t *testing.T) {
	h := wu15NewHarness(t)
	h.create("squad", "Control", "mia", "jim")
	before := h.messages("mia")
	sid := h.main("mia")
	status, body := h.http(http.MethodPost, "/api/v1/workspaces/sales/rooms", gen.RoomCreateRequest{
		Kind: gen.RoomCreateRequestKindAdhoc, AgentIds: []string{"mia", "jim"}, OriginSessionId: &sid,
	})
	require.Equal(t, http.StatusCreated, status, "FR-207: participant creates a different conversation: %s", body)
	var room gen.Room
	require.NoError(t, json.Unmarshal(body, &room))
	require.Equal(t, &sid, room.OriginSessionId)
	require.Empty(t, h.snapshot(room.Id).Entries)
	require.Equal(t, before, h.messages("mia"), "FR-207: no copied/changed origin transcript")
	meta, err := h.al.GetSessionStore().GetMeta(sid)
	require.NoError(t, err)
	require.Equal(t, "mia", meta.AgentID, "origin stays one-to-one")
}

// Oracle correction authorized by Chief Q5 A: FR-172 requires a new receipt
// for each mutation, not a global ban on extra legitimate audit events.
func TestWU15Rooms_CreateMembershipMuteDeleteAreAudited(t *testing.T) {
	h := wu15NewHarness(t)
	before := wu15ReadAuditLines(t, h.home)
	room := h.create("squad", "Audit sentinel", "mia", "jim")
	wu15RequireMutationAudit(t, before, wu15ReadAuditLines(t, h.home), room.Id, []string{"mia", "jim"}, "create")
	for _, step := range []struct{ action, id string }{{"add", "researcher2"}, {"remove", "researcher2"}, {"mute", "jim"}, {"unmute", "jim"}} {
		before = wu15ReadAuditLines(t, h.home)
		status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestAction(step.action), AgentIds: []string{step.id}})
		require.Equal(t, http.StatusOK, status, "FR-172: %s: %s", step.action, body)
		wu15RequireMutationAudit(t, before, wu15ReadAuditLines(t, h.home), room.Id, []string{step.id}, step.action)
	}
	before = wu15ReadAuditLines(t, h.home)
	status, body := h.http(http.MethodDelete, "/api/v1/rooms/"+room.Id, nil)
	require.Equal(t, http.StatusNoContent, status, "FR-172: delete: %s", body)
	wu15RequireMutationAudit(t, before, wu15ReadAuditLines(t, h.home), room.Id, []string{"mia", "jim"}, "delete")
}

func wu15ReadAuditLines(t *testing.T, home string) [][]byte {
	t.Helper()
	var rows [][]byte
	err := filepath.WalkDir(home, func(path string, info fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasPrefix(info.Name(), "audit") || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) != 0 {
				rows = append(rows, append([]byte(nil), line...))
			}
		}
		return nil
	})
	require.NoError(t, err)
	return rows
}

func wu15RequireMutationAudit(t *testing.T, before, after [][]byte, roomID string, agents []string, action string) {
	t.Helper()
	exactAction := "members_" + action
	switch action {
	case "create":
		exactAction = "created"
	case "delete":
		exactAction = "deleted"
	}
	requireRoomAudit(t, wu15NewAuditRows(t, before, after), roomID, exactAction, "wu15-tester", agents...)
}
