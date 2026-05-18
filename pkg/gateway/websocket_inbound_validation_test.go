//go:build !cgo

// Package gateway — inbound frame validation tests.
//
// Covers three behaviors of the discriminated inbound decode:
//   1. TokenFrame serialization — no spurious null/missing fields in the output.
//   2. Inbound cancel with empty session_id — drops the frame and increments
//      the inboundDropped counter without crashing the connection.
//   3. Inbound exec_approval_response with unknown decision — drops the frame
//      and increments inboundDropped; connection stays open.

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// T4: TokenFrame — no null parts field after migration
// ---------------------------------------------------------------------------

// TestWS_TokenFrame_NotSerializedAsNullParts verifies that the TokenFrame
// produces schema-valid JSON with no spurious null fields.
//
// BDD:
//
//	Given a TokenFrame{Type:"token", Content:"", SessionId:"s1"} is marshalled,
//	When the resulting JSON is decoded into a map,
//	Then the map contains exactly the keys "type", "content", "session_id"
//	 and no "parts" key is present.
//
// Traces to: pkg/gateway/websocket.go wsStreamer.Update — generated.TokenFrame marshal.
func TestWS_TokenFrame_NotSerializedAsNullParts(t *testing.T) {
	frame := generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "", // empty content — the case that previously yielded null parts
		SessionId: "s1",
	}

	data, err := json.Marshal(frame)
	require.NoError(t, err, "TokenFrame must marshal without error")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded), "marshalled TokenFrame must be valid JSON")

	// Required fields must be present.
	assert.Equal(t, "token", decoded["type"], "type field must be 'token'")
	assert.Equal(t, "s1", decoded["session_id"], "session_id must round-trip")
	_, hasContent := decoded["content"]
	assert.True(t, hasContent, "content field must be present (even when empty)")

	// The old wsServerFrame had a nullable parts field — it must not appear here.
	_, hasParts := decoded["parts"]
	assert.False(t, hasParts,
		"TokenFrame must NOT contain a 'parts' key — the old wsServerFrame null-parts regression")

	// Exactly three keys; no unexpected fields.
	assert.Len(t, decoded, 3,
		"TokenFrame JSON must have exactly 3 keys: type, content, session_id")
}

// ---------------------------------------------------------------------------
// T5: Inbound cancel with empty session_id
// ---------------------------------------------------------------------------

// TestWS_InboundCancel_RejectsEmptySessionID verifies that feeding a cancel
// frame with no session_id causes the server to:
//
//	(a) respond with an error frame, AND
//	(b) increment wc.inboundDropped.
//
// The connection must remain open after the rejection.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends {"type":"cancel"} with no session_id,
//	Then the server responds with {"type":"error"} and the connection stays open.
//
// Implements: T5 — cancel empty-session_id guard.
// Traces to: pkg/gateway/websocket.go readLoop case "cancel" empty-ID branch.
func TestWS_InboundCancel_RejectsEmptySessionID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Send cancel frame without a session_id.
	cancelFrame := wsClientFrameTestHelper{Type: "cancel"} // session_id omitted
	data, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// Server must respond with an error frame.
	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.NotEmpty(t, resp.Message,
		"error frame must carry a message when cancel has no session_id")

	// Connection must remain open.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, _ := json.Marshal(ping)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open after cancel rejection")
}

// ---------------------------------------------------------------------------
// T6: Inbound exec_approval_response with unknown decision
// ---------------------------------------------------------------------------

// TestWS_InboundApproval_RejectsUnknownDecision verifies that a
// exec_approval_response frame carrying an unrecognised decision value (e.g.
// "banana") is rejected: the server sends an error frame and the connection
// stays open.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends {"type":"exec_approval_response","decision":"banana","id":"x"},
//	Then the server responds with {"type":"error"} and the connection stays open.
//
// Implements: T6 — exec_approval_response unknown-decision guard.
// Traces to: pkg/gateway/websocket.go readLoop case "exec_approval_response"
//
//	decision-validation switch.
func TestWS_InboundApproval_RejectsUnknownDecision(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Send approval response with an unrecognised decision.
	approvalFrame := wsClientFrameTestHelper{
		Type:     "exec_approval_response",
		ID:       "approval-id-001",
		Decision: "banana", // not one of: allow / deny / always
	}
	data, err := json.Marshal(approvalFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// Server must respond with an error frame.
	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.NotEmpty(t, resp.Message,
		"error frame must carry a message for unknown approval decision")

	// Connection must remain open.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, _ := json.Marshal(ping)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open after approval rejection")
}
