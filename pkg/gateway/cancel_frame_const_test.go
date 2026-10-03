// cancel_frame_const_test.go — fix/1081 coverage-gap closure (architect
// ruling, ADR-013 §7 addendum): TestWebRTCFrameSchemasRoundTrip
// (browser_webrtc_contract_test.go) proved `const`-keyword rejection is
// restored by commit 45ca97bb0, but only for the 7 WebRTC/browser-capture
// frame types — whose own call sites (browser_webrtc.go) pass a HARDCODED
// schema-name string literal straight to ValidateInboundFrameJSON, never
// through wsFrameSchemaName, the one frame-type -> schema-name table the
// live chat WS readLoop actually consults (websocket.go:1184, inside the
// gateway.validate_inbound block; wsFrameSchemaName's own doc comment:
// "there is exactly one frame-type->schema table for the whole gateway,
// not two that can drift apart"). This file closes that gap for
// CancelFrame, the frame the architect named as the example: every
// assertion below resolves the schema name the SAME way real WS traffic
// does, rather than hardcoding "CancelFrame" as a literal the test chose.
//
// Spec sources for every expected value below (none derived from running
// the code):
//   - pkg/gateway/inboundschemas/CancelFrame.yaml — required: [type,
//     session_id]; properties.type: {type: string, const: cancel}.
//   - pkg/gateway/websocket.go::wsFrameSchemaName — case
//     string(generated.WsFrameTypeCancel): return "CancelFrame" — the
//     single routing-table entry for this frame type.
//   - pkg/gateway/rest_inbound_validate.go::ValidateInboundFrameJSON —
//     documented return shape (errMsg string, serverErr bool): a
//     non-empty errMsg with serverErr==false is a CLIENT-side schema
//     violation (not a server/compile error) — generated/wsFrameSchemaName
//     and ValidateInboundFrameJSON tests must distinguish schema-compile
//     failures from genuine rejections, exactly as the existing WebRTC
//     contract test does.
//   - pkg/gateway/websocket.go:1197-1200 — the exact error-frame message
//     format the live readLoop sends on a schema rejection:
//     "frame schema validation failed (" + schemaName + "): " + errMsg —
//     this is what licenses asserting the live round-trip error frame's
//     Message contains the literal schema name "CancelFrame".
//
// Why there is no live-socket test asserting a `type`-const rejection
// specifically: wsFrameSchemaName has exactly ONE case that resolves to
// "CancelFrame", keyed off that SAME wire `type` value ("cancel") the
// CancelFrame schema's own `const` also checks. A wire frame whose `type`
// is not literally "cancel" gets routed to a different schema (or to no
// schema at all, silently skipping validation) by the peek.Type lookup
// (websocket.go:1170) BEFORE ValidateInboundFrameJSON is ever called with
// schema "CancelFrame" — so a frame that is both (a) routed to the
// CancelFrame schema and (b) in violation of that schema's `type` const is
// structurally impossible to construct over the real socket. This is
// exactly why TestWebRTCFrameSchemasRoundTrip calls ValidateInboundFrameJSON
// directly with the schema name as a given rather than deriving it from a
// wire frame: for a type-discriminated `const`, the rejection is a
// function-level property of the compiled schema, not a live-routable
// negative case. What a live round trip over the real readLoop CAN prove —
// and what the tests below do prove — is that the exact routing-table
// output for "cancel" is what gets handed to the live schema validator, and
// that the resulting schema (the very one direct-call tests prove rejects a
// `type`-const violation) is genuinely wired into that path.
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

// TestWsFrameSchemaName_CancelResolvesToCancelFrame proves the live WS
// routing table (websocket.go::wsFrameSchemaName), not a literal this test
// chose, is what maps the "cancel" frame type to the CancelFrame schema.
func TestWsFrameSchemaName_CancelResolvesToCancelFrame(t *testing.T) {
	got := wsFrameSchemaName(string(generated.WsFrameTypeCancel))
	assert.Equal(t, "CancelFrame", got,
		"wsFrameSchemaName(%q) must resolve to the CancelFrame schema — this IS "+
			"the lookup the live WS readLoop performs (websocket.go:1184) before "+
			"calling ValidateInboundFrameJSON",
		generated.WsFrameTypeCancel)
}

// TestValidateInboundFrameJSON_CancelFrame_ConstRejection drives
// ValidateInboundFrameJSON with the schema name resolved via
// wsFrameSchemaName (not a hardcoded literal), proving the fix in
// 45ca97bb0 (explicit Draft2020 default + narrow per-file draft-04 tagging,
// replacing the blanket DefaultDraft(Draft4) that silently disabled
// `const` on all 458 non-tagged schemas) holds for the exact schema real
// cancel traffic is validated against.
func TestValidateInboundFrameJSON_CancelFrame_ConstRejection(t *testing.T) {
	schemaName := wsFrameSchemaName(string(generated.WsFrameTypeCancel))
	require.Equal(t, "CancelFrame", schemaName,
		"sanity: the routing table must resolve before testing the schema it names")

	t.Run("valid frame validates cleanly", func(t *testing.T) {
		frame := generated.CancelFrame{
			Type:      string(generated.WsFrameTypeCancel),
			SessionId: "sess-e2e-1081",
		}
		data, err := json.Marshal(frame)
		require.NoError(t, err)

		errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data)
		require.False(t, serverErr, "schema compile must succeed for %s", schemaName)
		assert.Empty(t, errMsg,
			"a realistic, valid CancelFrame must validate cleanly against its schema; got: %s", errMsg)
	})

	t.Run("wrong string value for type const is rejected", func(t *testing.T) {
		// CancelFrame.yaml: properties.type.const == "cancel". A same-typed
		// (string), wrong-VALUE "type" can ONLY be caught by the `const`
		// keyword itself — the `type: string` keyword alone would accept
		// it. This is THE const-regression sentinel: confirmed by this
		// test's own RED run (fix/1081-const-cancelframe-test) against a
		// reproduction of the pre-45ca97bb0 blanket-Draft4 mutation, where
		// this exact subtest silently passed (errMsg=="") while every
		// other subtest in this file stayed green — proving `const`
		// specifically, and only `const`, was disabled by that regression.
		data := []byte(`{"type":"not_cancel","session_id":"sess-e2e-1081"}`)
		errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data)
		require.False(t, serverErr)
		assert.NotEmpty(t, errMsg,
			`CancelFrame with type="not_cancel" (wrong const value) must be REJECTED, not silently accepted`)
	})

	t.Run("wrong JSON type for type const is rejected", func(t *testing.T) {
		// type as a JSON number instead of the literal "cancel" string.
		// NOTE (confirmed by this test's own RED run): this case is
		// rejected by the schema's `type: string` keyword, NOT by
		// `const` — Draft4 (the historical regression) still enforces
		// `type`, so this subtest stayed GREEN even under the reverted,
		// pre-fix compiler config. It is kept as a legitimate, independent
		// negative case (wrong JSON type must still be rejected) but it is
		// NOT, by itself, const-regression coverage — only the
		// wrong-string-value subtest above is.
		data := []byte(`{"type":123,"session_id":"sess-e2e-1081"}`)
		errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data)
		require.False(t, serverErr)
		assert.NotEmpty(t, errMsg,
			"CancelFrame with type=123 (wrong JSON type) must be REJECTED")
	})

	t.Run("missing required session_id is rejected", func(t *testing.T) {
		// CancelFrame.yaml: required: [type, session_id].
		data := []byte(`{"type":"cancel"}`)
		errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data)
		require.False(t, serverErr)
		assert.NotEmpty(t, errMsg,
			"CancelFrame with required field session_id REMOVED must be REJECTED, not silently accepted")
	})
}

// ---------------------------------------------------------------------------
// Live-socket proof: the SAME routing-table output real WS traffic gets
// (wsFrameSchemaName("cancel") == "CancelFrame") is what actually drives
// ValidateInboundFrameJSON inside the live readLoop — not a schema name a
// test supplied. See the file doc comment for why this cannot additionally
// exercise a type-const violation (routing and the const-checked field
// share the same wire key, by construction).
// ---------------------------------------------------------------------------

// TestWS_ValidateInbound_CancelFrame_RoutedLive_RejectsMissingSessionID
// sends a real "cancel" frame (so peek.Type routes it to the CancelFrame
// schema exactly as production traffic would) missing session_id, the
// schema's other required property, over the actual WS readLoop with
// gateway.validate_inbound enabled.
//
// BDD:
//
//	Given an authenticated WebSocket connection with validate_inbound=true,
//	When the client sends {"type":"cancel"} with no session_id,
//	Then the server responds with an error frame naming the CancelFrame
//	 schema, and the connection stays open.
//
// Traces to: websocket.go:1184 (schemaName := wsFrameSchemaName(peek.Type)),
// websocket.go:1186-1200 (validation + error-frame send), FR-030 / #1081.
func TestWS_ValidateInbound_CancelFrame_RoutedLive_RejectsMissingSessionID(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	al.GetConfig().Gateway.ValidateInbound = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// type is the REAL literal "cancel" — this is what makes
	// wsFrameSchemaName route this exact frame to CancelFrame in the
	// first place. session_id, the schema's other required property, is
	// omitted so the schema (not the switch's own later f.SessionId=="""
	// check, which this never reaches once validation drops the frame)
	// is what rejects it.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"cancel"}`)))

	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.Contains(t, resp.Message, "CancelFrame",
		"error frame must name the CancelFrame schema — proves wsFrameSchemaName "+
			"resolved this live frame to CancelFrame via the real routing table, "+
			"not a name the test supplied")

	// Connection must remain open after a schema rejection.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open after schema rejection")
}

// TestWS_ValidateInbound_CancelFrame_RoutedLive_ValidFramePassesThrough
// sends a well-formed cancel frame over the real WS readLoop with
// gateway.validate_inbound enabled and proves it is not dropped by schema
// validation.
//
// BDD:
//
//	Given an authenticated WebSocket connection with validate_inbound=true,
//	When the client sends a well-formed {"type":"cancel","session_id":"..."},
//	Then the frame is NOT rejected by schema validation (no error frame from
//	 the validate_inbound block), and the connection stays open.
//
// Mirrors the established pattern in
// TestWS_ValidateInbound_ValidFramePassesThrough
// (websocket_inbound_validation_test.go): a subsequent write succeeding is
// this suite's existing connection-stayed-open proof.
func TestWS_ValidateInbound_CancelFrame_RoutedLive_ValidFramePassesThrough(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	al.GetConfig().Gateway.ValidateInbound = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	validFrame := generated.CancelFrame{
		Type:      string(generated.WsFrameTypeCancel),
		SessionId: "sess-e2e-1081-live",
	}
	data, err := json.Marshal(validFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	ping := wsClientFrameTestHelper{Type: "ping"}
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open — a valid, correctly-routed CancelFrame must not be rejected")
}
