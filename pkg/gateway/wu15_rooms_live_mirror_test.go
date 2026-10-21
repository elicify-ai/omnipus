package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func (h *wu15RoomHarness) renameAgent(id, name string) {
	h.t.Helper()
	status, data := h.http(http.MethodGet, "/api/v1/agents/"+id, nil)
	require.Equal(h.t, http.StatusOK, status)
	var before gen.Agent
	require.NoError(h.t, json.Unmarshal(data, &before))
	status, data = h.http(http.MethodPut, "/api/v1/agents/"+id, gen.AgentUpdateRequest{Revision: before.Revision, Name: &name})
	require.Equal(h.t, http.StatusOK, status, "SETUP: real agent name change: %s", data)
	var after gen.Agent
	require.NoError(h.t, json.Unmarshal(data, &after))
	require.Equal(h.t, name, after.Name, "SETUP: authoritative current name")
}

func TestWU15Rooms_LiveAnswerTokenAndFinalCarryExactPersistedRoomReference(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "mirror", "@Mia return a mirrored answer")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "mirror"), "mia")
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	answer := "exact mirrored room answer"
	_, err := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{ReplyTo: request.RequestId, Content: answer, ResponderSessionID: h.main("mia"), Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "mia"}})
	require.NoError(t, err)
	var token gen.TokenFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "token"), &token))
	want := gen.RoomRef{RoomId: room.Id, RoomName: room.Name, RequestId: request.RequestId, Role: "answer"}
	require.Equal(t, h.main("mia"), token.SessionId)
	require.Equal(t, answer, token.Content)
	require.Equal(t, &want, token.RoomRef, "K33/#1281: live token has the actual answer reference")
	var done gen.DoneFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "done"), &done))
	require.Equal(t, token.MessageId, done.MessageId, "same one committed main mirror")
	require.Equal(t, &want, done.RoomRef, "K33: final carrier preserves answer reference")
	wu15RequireMirrors(t, h.messages("mia"), room, request.RequestId, answer)
}
