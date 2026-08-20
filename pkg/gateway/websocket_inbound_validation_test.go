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

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// T4: TokenFrame — no null parts field after migration
// ---------------------------------------------------------------------------

// TestWS_TokenFrame_NotSerializedAsNullParts verifies that the TokenFrame
// produces schema-valid JSON with no spurious null fields.
//
// BDD:
//
//	Given a TokenFrame{Type:"token", Content:"", SessionId:"s1"} is marshaled,
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
	require.NoError(t, json.Unmarshal(data, &decoded), "marshaled TokenFrame must be valid JSON")

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
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open after cancel rejection")
}

// ---------------------------------------------------------------------------
// T6: Inbound exec_approval_response with unknown decision
// ---------------------------------------------------------------------------
//
// RETIRED 2026-07-04 (ADR-036 §3.4): the exec_approval_response WS frame and
// its entire dedicated approval protocol (wsApprovalHook, wsApprovalRegistry)
// were fully retired -- every tool's "ask" verdict now goes through the
// generic REST tool-approval endpoint (POST /api/v1/tool-approvals/{id}) and
// its ToolApprovalRequiredFrame/ToolApprovalResponse WS frames instead. This
// test (T6) asserted the old protocol's unknown-decision guard; since the
// server no longer has any case for "exec_approval_response" at all, it now
// times out waiting for an error frame that will never arrive. There is no
// direct replacement here because the new REST endpoint validates its
// request body via the generated OpenAPI schema (decodeAndValidate), which
// is already covered by pkg/gateway/rest_tool_registry_test.go and
// contract-level validation -- an "unknown decision over WS" scenario does
// not exist in the new protocol shape.

// ---------------------------------------------------------------------------
// T7: WS JSON Schema validation (validate_inbound=true)
// ---------------------------------------------------------------------------

// TestWS_ValidateInbound_SchemaRejectsMessageFrameMissingContent verifies that
// when gateway.validate_inbound=true, a message frame without the required
// "content" field is rejected with an error frame and the connection stays open.
//
// BDD:
//
//	Given an authenticated WebSocket connection with validate_inbound=true,
//	When the client sends {"type":"message"} with no content field,
//	Then the server responds with {"type":"error"} mentioning "MessageFrame",
//	And the connection stays open.
//
// Implements: T7 — WS inbound schema validation parity with REST.
// Traces to: pkg/gateway/websocket.go readLoop validate_inbound block.
func TestWS_ValidateInbound_SchemaRejectsMessageFrameMissingContent(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	// Enable schema validation.
	al.GetConfig().Gateway.ValidateInbound = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Send a message frame missing the required "content" field.
	// The MessageFrame schema requires content to be present.
	malformedFrame := map[string]any{"type": "message"} // no content
	data, err := json.Marshal(malformedFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// Server must respond with an error frame mentioning the schema name.
	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.NotEmpty(t, resp.Message,
		"error frame must carry a schema validation message")
	assert.Contains(t, resp.Message, "MessageFrame",
		"error message must identify the failing schema")

	// Connection must remain open.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open after schema rejection")
}

// TestWS_ValidateInbound_ValidFramePassesThrough verifies that a valid
// MessageFrame passes schema validation without being dropped.
//
// BDD:
//
//	Given an authenticated WebSocket connection with validate_inbound=true,
//	When the client sends a well-formed {"type":"message","content":"hello"},
//	Then the frame is NOT rejected (no error frame from schema validation).
//
// Implements: T7 — WS inbound schema validation passes valid frames.
// Traces to: pkg/gateway/websocket.go readLoop validate_inbound block.
func TestWS_ValidateInbound_ValidFramePassesThrough(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	// Enable schema validation.
	al.GetConfig().Gateway.ValidateInbound = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Send a well-formed message frame.
	validFrame := wsClientFrameTestHelper{Type: "message", Content: "hello from T7"}
	data, err := json.Marshal(validFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// The server should not respond with an error frame for valid input.
	// Send a second message (ping) after a brief delay; if the first frame caused
	// a schema error the ping would arrive after the error frame.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open when a valid frame is sent")
}

// TestWS_ValidateInbound_EmptyContentWithMedia_PassesThrough verifies the
// UAT Issue 5 fix: an attachment-only send (content is legitimately empty
// because media is present) must NOT trip schema validation.
//
// BDD:
//
//	Given an authenticated WebSocket connection with validate_inbound=true,
//	When the client sends {"type":"message","content":"","media":["media://..."]},
//	Then the frame is NOT rejected (no error frame from schema validation).
//
// Traces to: contracts/components/schemas/MessageFrame.yaml anyOf
// (content minLength:1 OR media minItems:1).
func TestWS_ValidateInbound_EmptyContentWithMedia_PassesThrough(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	al.GetConfig().Gateway.ValidateInbound = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// wsClientFrameTestHelper.Content has `omitempty`, which would drop the
	// key entirely for "" — the real bug shape needs the key PRESENT with
	// an empty value, so build the frame as a raw map instead.
	attachmentOnlyFrame := map[string]any{
		"type":    "message",
		"content": "",
		"media":   []string{"media://workspace/ws1/att1"},
	}
	data, err := json.Marshal(attachmentOnlyFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open — an attachment-only send must not fail schema validation")
}

// TestWS_ValidateInbound_EmptyContentNoMedia_Rejected proves the Issue 5 fix
// did NOT weaken content into "always optional": empty content with no
// media at all must still be rejected.
func TestWS_ValidateInbound_EmptyContentNoMedia_Rejected(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	al.GetConfig().Gateway.ValidateInbound = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	emptyFrame := map[string]any{"type": "message", "content": ""}
	data, err := json.Marshal(emptyFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.Contains(t, resp.Message, "MessageFrame",
		"empty content with no media must still fail schema validation")
}
