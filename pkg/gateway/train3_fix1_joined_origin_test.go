package gateway

import (
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// D23, S-83, DS-8 row 2. Mia is stopped when a group request arrives, so it
// is held. A person resumes Mia from her main web chat; that turn consumes
// the held group request and the typed message. The card stays in the
// agent's own chat: no group label and no room reference.
func TestTrain3Fix1_HeldGroupRequestJoiningAWebTurnDropsTheGroupReference(t *testing.T) {
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
	require.Nil(t, frame.RoomRef, "D23: a step that also handled a typed message carries no group reference")
	if frame.OriginLabel != nil && strings.HasPrefix(*frame.OriginLabel, "for group ") {
		t.Fatalf("origin label = %q, want no group label (S-83: agent's chat only)", *frame.OriginLabel)
	}
}
