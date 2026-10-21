package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// US1.6 / D7a / D15 / FR-023: one real ordinary admission, real builtin Ask,
// current approval restored beside the same history failure, real approval
// POST, then engine-observed web Stop. This is not a DOM composer assertion.
func TestUnavailableHistory_US16_CurrentApprovalSendAndEngineStop(t *testing.T) {
	h := newHistoryUS16Harness(t)
	peer := h.connect(t)
	h.assertHistoryRefuses(t)
	historyUS16LiveOnly(t, h.attach(t, peer), h.sid, "", "")
	t.Log("US16_INITIAL_UNAVAILABLE_LIVE_ONLY=PASS fixed history error and completed controls before send")

	agentID, clientID := "mia", historyUS16ClientID
	peer.send(t, generated.MessageFrame{Type: "message", SessionId: &h.sid, AgentId: &agentID, ClientMessageId: &clientID, Content: historyUS16Instruction, Metadata: map[string]any{"workspace_id": testHarnessWorkspaceMembershipID}})
	admitted := peer.until(t, "tool_approval_required")
	var approval generated.ToolApprovalRequiredFrame
	require.NoError(t, json.Unmarshal(admitted[len(admitted)-1], &approval))
	require.Equal(t, h.sid, approval.SessionId)
	require.Equal(t, "mia", approval.AgentId)
	require.Equal(t, "write_file", approval.ToolName)
	require.Equal(t, historyUS16CallID, approval.ToolCallId)
	require.Equal(t, map[string]any{"path": historyUS16Filename, "content": historyUS16Proof}, approval.Args)
	require.NotEmpty(t, approval.ApprovalId)
	require.NotEmpty(t, approval.TurnId)
	require.Positive(t, approval.ExpiresInMs)
	received := 0
	for _, raw := range admitted {
		var envelope wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &envelope))
		if envelope.Type != "message_status" {
			continue
		}
		var status generated.MessageStatusFrame
		require.NoError(t, json.Unmarshal(raw, &status))
		if status.State == "received" {
			received++
			require.Equal(t, h.sid, status.SessionId)
			require.Equal(t, clientID, status.ClientMessageId)
		}
	}
	require.Equal(t, 1, received, "one genuine durable-save receipt, not an assumed successful send")
	h.assertSavedOnce(t)
	require.EqualValues(t, 1, h.provider.calls.Load(), "pending approval parks the actual first provider call")
	definitions, _ := h.provider.snapshot()
	visible := 0
	for _, definition := range definitions {
		if definition.Function.Name == "write_file" {
			visible++
		}
	}
	require.Equal(t, 1, visible, "reachability control: the model was actually offered the genuine builtin")
	_, statErr := os.Stat(filepath.Join(h.workDir, historyUS16Filename))
	require.ErrorIs(t, statErr, os.ErrNotExist, "Ask must not execute before the human approval")
	h.assertPendingRegistry(t, approval)
	t.Log("US16_NORMAL_ADMISSION_AND_APPROVAL=PASS actual saved instruction and genuine pending builtin approval")

	// Retry and an actual authenticated reconnect both re-offer the SAME current
	// approval after the SAME fixed history error, without sending the instruction.
	historyUS16LiveOnly(t, h.attach(t, peer), h.sid, approval.ApprovalId, approval.TurnId)
	h.assertPendingRegistry(t, approval)
	h.assertSavedOnce(t)
	peer.close(t)
	reconnected := h.connect(t)
	historyUS16LiveOnly(t, h.attach(t, reconnected), h.sid, approval.ApprovalId, approval.TurnId)
	h.assertPendingRegistry(t, approval)
	require.EqualValues(t, 1, h.provider.calls.Load())
	h.assertSavedOnce(t)
	h.assertHistoryRefuses(t)

	body, marshalErr := json.Marshal(generated.ToolApprovalActionRequest{Action: generated.ToolApprovalActionRequestActionAllowOnce})
	require.NoError(t, marshalErr)
	request, requestErr := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/tool-approvals/"+approval.ApprovalId, bytes.NewReader(body))
	require.NoError(t, requestErr)
	request.Header.Set("Authorization", "Bearer "+historyUS16Token)
	request.Header.Set("Content-Type", "application/json")
	response, actionErr := h.server.Client().Do(request)
	require.NoError(t, actionErr)
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.Equal(t, http.StatusOK, response.StatusCode, "%s", responseBody)
	var applied generated.ToolApprovalResponse
	require.NoError(t, json.Unmarshal(responseBody, &applied))
	require.Equal(t, approval.ApprovalId, applied.ApprovalId)
	require.Equal(t, generated.ToolApprovalResponseActionAllowOnce, applied.Action)
	require.Equal(t, generated.ToolApprovalResponseStatusOk, applied.Status)
	historyUS16Await(t, h.provider.resumed, "real tool result resumed model execution")
	_, toolMessages := h.provider.snapshot()
	require.Len(t, toolMessages, 1)
	require.Equal(t, historyUS16CallID, toolMessages[0].ToolCallID)
	proof, proofErr := os.ReadFile(filepath.Join(h.workDir, historyUS16Filename))
	require.NoError(t, proofErr)
	require.Equal(t, historyUS16Proof, string(proof), "actual approved builtin wrote exactly the requested file bytes")
	historyUS16LiveOnly(t, h.attach(t, reconnected), h.sid, "", approval.TurnId)
	select {
	case <-h.provider.cancelled:
		t.Fatal("history Retry/reconnect cancelled the actual resumed turn before Stop")
	default:
	}
	reconnected.send(t, generated.CancelFrame{Type: "cancel", SessionId: h.sid})
	historyUS16Await(t, h.provider.cancelled, "web Stop reached the actual in-flight model context")
	h.awaitTurnEnd(t, approval.TurnId) // Engine completion BEFORE harness teardown.
	h.stop(t)
	require.EqualValues(t, 2, h.provider.calls.Load(), "one turn, one resumed call; history Retry/reconnect never relaunch")
	h.assertOneRealDispatch(t, approval.TurnId)
	h.assertSavedOnce(t)
	h.assertHistoryRefuses(t)
	t.Log("US16_APPROVAL_SEND_AND_ENGINE_STOP=PASS real approval action and independently observed engine Stop; history remains unavailable")
}
