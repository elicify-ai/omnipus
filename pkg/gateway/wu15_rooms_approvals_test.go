package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestWU15Rooms_PreAdmissionCardBelongsToReceiverMainAndDeliveryID(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.receiverPolicy("mia", "ask")
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.post(room.Id, "ask", "@Mia ask before receiving")
	var card gen.ToolApprovalRequiredFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "tool_approval_required"), &card))
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "ask"), "mia")
	require.Equal(t, h.main("mia"), card.SessionId, "FR-251: receiver main, no sender session and no room-as-session")
	require.NotNil(t, card.RoomRef)
	require.Equal(t, gen.RoomRef{RoomId: room.Id, RoomName: room.Name, RequestId: request.RequestId, Role: "request"}, *card.RoomRef)
	require.NotNil(t, card.OriginLabel, "FR-251: room origin label is required")
	require.Equal(t, "for group Launch", *card.OriginLabel)
	if card.RootSessionId != nil {
		require.Equal(t, h.main("mia"), *card.RootSessionId, "R2: receiver main is interactive root")
	}
	require.True(t, h.room(room.Id).NeedsAttention, "K23: pre-admission decision causes attention")
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+card.ApprovalId, gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "FR-251: actual user approval: %s", body)
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, request.RequestId, capture.RequestID, "FR-251/216: approval preserves caller delivery id")
	require.False(t, h.room(room.Id).NeedsAttention, "K23: settled pre-admission card clears attention")
}

func TestWU15Rooms_TurnToolApprovalWaitsInReceiverMainNotRoom(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	probe := &askProbeTool{}
	h.al.RegisterTool(probe)
	inst, ok := h.al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	policy := *inst.LoadToolPolicy()
	policy.Policies = map[string]config.ToolPolicy{askProbeToolName: "ask"}
	inst.StoreToolPolicy(&policy)
	h.provider("mia", &wu15ScriptProvider{tool: askProbeToolName, toolArgs: map[string]any{}})
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.startLoop()
	h.post(room.Id, "tool-ask", "@Mia write the report")
	var card gen.ToolApprovalRequiredFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "tool_approval_required"), &card))
	require.Equal(t, h.main("mia"), card.SessionId, "FR-169/R2: interactive receiver main owns approval")
	require.NotEqual(t, room.Id, card.SessionId, "FR-169: card never lives in room")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "tool-ask"), "mia")
	h.waitState(room.Id, request.RequestId, "waiting_on_user")
	require.Equal(t, 0, probe.runs(), "genuine Ask: tool not executed before user action")
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+card.ApprovalId, gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "FR-169: owning main approval resolves over real REST: %s", body)
	require.Eventually(t, func() bool { return probe.runs() == 1 }, 30*time.Second, 10*time.Millisecond, "approved receiver tool executes once")
}

func TestWU15Rooms_QuestionAppearsOnlyInReceiverMainAndAgentCannotAnswerIt(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.provider("mia", &wu15ScriptProvider{tool: tools.AskUserQuestionToolName, toolArgs: map[string]any{"questions": []any{map[string]any{
		"header": "Launch", "question": "Which launch day?", "options": []any{map[string]any{"label": "Friday"}, map[string]any{"label": "Monday"}},
	}}}})
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.startLoop()
	h.post(room.Id, "question", "@Mia ask the person")
	var card gen.AskUserQuestionFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "ask_user_question"), &card))
	require.Equal(t, h.main("mia"), card.Card.SessionId, "FR-169: question belongs only to receiver main")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "question"), "mia")
	h.waitState(room.Id, request.RequestId, "waiting_on_user")
	h.post(room.Id, "jim-text", "@Jim answer Friday")
	jim := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "jim-text"), "jim")
	_, err := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{ReplyTo: jim.RequestId, Content: "Friday", ResponderSessionID: h.main("jim"), Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "jim"}})
	require.NoError(t, err)
	h.waitState(room.Id, request.RequestId, "waiting_on_user")
	reloaded := h.connect()
	require.NoError(t, reloaded.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	var state gen.SessionStateFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, reloaded, "session_state"), &state))
	var same []gen.AskUserQuestionCard
	for _, pending := range state.PendingAsks {
		if pending.CardId == card.Card.CardId {
			same = append(same, pending)
		}
	}
	require.Len(t, same, 1, "FR-168: agent room answer did not resolve the human question")
	require.Equal(t, card.Card.SessionId, same[0].SessionId)
}
