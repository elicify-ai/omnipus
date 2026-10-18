package gateway

import (
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// Gate round 1, code-reviewer F06 / architect F2 (spec §6.18 "origin is the
// set of inputs consumed"; chief ruling 2026-10-18: channel > room > peer >
// web, never downgraded, first of a class keeps it). Mia is stopped when a
// group request arrives, so it is held. A person resumes Mia from her main
// web chat; that turn consumes the held group request among its first inputs
// and then asks for approval. The card must carry the group origin — "for
// group <name>" and room_ref — although the turn was started by the web.
func TestTrain3Fix1_HeldGroupRequestJoiningAWebTurnLabelsItsApprovals(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	h.stopMain("mia")
	h.post(room.Id, "held-ask", "@Mia write the report")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held-ask"), "mia")
	h.waitState(room.Id, request.RequestId, "held")

	h.provider("mia", &wu15ScriptProvider{tool: askProbeToolName, toolArgs: map[string]any{}})
	h.startLoop()
	conn := h.connect()
	h.sendMain(conn, "mia", "run again")

	frame := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.Equal(t, h.main("mia"), frame.SessionId, "the member's own turn asks in her main chat")
	require.NotNil(t, frame.OriginLabel, "a turn that consumed a group request is labelled for its group")
	require.Equal(t, "for group "+room.Name, *frame.OriginLabel)
	require.NotNil(t, frame.RoomRef, "the consumed group request's room reference rides on the card")
	require.Equal(t, room.Id, frame.RoomRef.RoomId)
	require.Equal(t, request.RequestId, frame.RoomRef.RequestId)
}
