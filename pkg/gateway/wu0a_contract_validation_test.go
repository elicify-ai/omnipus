// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WU-0a contract validation tests (spec omnipus-system-agent §7.2; binding
// amendment L1-AMENDMENTS-20261013 §5.1/§5.2).
//
// Every NEW inbound shape that WU-0a adds is driven through the gateway's
// real inbound validation:
//
//   - REST bodies (SecretSubmitRequest, SecretOptionsUpdateRequest,
//     RoomCreateRequest, RoomMembersUpdateRequest, RoomMessageRequest) through
//     decodeAndValidate — the function every REST handler calls — with
//     validation on and off. Their routes have no handler yet (WU-4/5/15), so
//     the semantic rules §7.2 requires to hold with validation OFF (field-key
//     match, byte limits, NUL, two-agent minimum, …) are owned by those
//     handlers' own tests; here the OFF mode proves only what the typed
//     decode itself enforces.
//   - WS frames (attach_room; message.workflow_start;
//     whatsapp_pairing_subscribe.request_id/session_id) through the real chat
//     WebSocket read loop with gateway.validate_inbound on and off.
//
// Every bad case is paired with a passing control in the same mode, so a
// green run cannot come from a validator that rejects everything.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// wu0aRESTCase is one body sent to decodeAndValidate for one schema.
type wu0aRESTCase struct {
	name string
	body string
}

// wu0aRESTShape is one new REST inbound schema with its passing control and
// the bad bodies that validation ON must refuse.
type wu0aRESTShape struct {
	schema  string
	control string
	newDst  func() any
	bad     []wu0aRESTCase
}

func wu0aRESTShapes() []wu0aRESTShape {
	return []wu0aRESTShape{
		{
			schema:  "SecretSubmitRequest",
			control: `{"values":{"api_key":"sk-test-value"}}`,
			newDst:  func() any { return &generated.SecretSubmitRequest{} },
			bad: []wu0aRESTCase{
				{"both values and cancel", `{"values":{"api_key":"sk-test-value"},"cancel":true}`},
				{"neither values nor cancel", `{}`},
				{"extra field", `{"values":{"api_key":"sk-test-value"},"note":"x"}`},
				{"null body", `null`},
				{"values null", `{"values":null}`},
				{"cancel false", `{"cancel":false}`},
				{"empty value", `{"values":{"api_key":""}}`},
				{"non-string value", `{"values":{"api_key":42}}`},
				{"malformed json", `{"values":`},
			},
		},
		{
			schema:  "SecretOptionsUpdateRequest",
			control: `{"available_in_channel_turns":true}`,
			newDst:  func() any { return &generated.SecretOptionsUpdateRequest{} },
			bad: []wu0aRESTCase{
				{"missing field", `{}`},
				{"extra field", `{"available_in_channel_turns":true,"reference":"x"}`},
				{"null field", `{"available_in_channel_turns":null}`},
				{"wrong type", `{"available_in_channel_turns":"yes"}`},
				{"null body", `null`},
			},
		},
		{
			schema:  "RoomCreateRequest",
			control: `{"kind":"squad","name":"Launch","agent_ids":["mia","jim"],"origin_session_id":"s1"}`,
			newDst:  func() any { return &generated.RoomCreateRequest{} },
			bad: []wu0aRESTCase{
				{"one agent", `{"kind":"adhoc","agent_ids":["mia"]}`},
				{"team kind not creatable", `{"kind":"team","agent_ids":["mia","jim"]}`},
				{"missing kind", `{"agent_ids":["mia","jim"]}`},
				{"agent_ids null", `{"kind":"adhoc","agent_ids":null}`},
				{"extra field", `{"kind":"adhoc","agent_ids":["mia","jim"],"hidden":true}`},
				{"name too long", `{"kind":"squad","name":"` + strings.Repeat("n", 81) + `","agent_ids":["mia","jim"]}`},
				{"null body", `null`},
			},
		},
		{
			schema:  "RoomMembersUpdateRequest",
			control: `{"action":"mute","agent_ids":["mia"]}`,
			newDst:  func() any { return &generated.RoomMembersUpdateRequest{} },
			bad: []wu0aRESTCase{
				{"empty agent_ids", `{"action":"add","agent_ids":[]}`},
				{"unknown action", `{"action":"kick","agent_ids":["mia"]}`},
				{"missing action", `{"agent_ids":["mia"]}`},
				{"extra field", `{"action":"mute","agent_ids":["mia"],"room_id":"r1"}`},
				{"null body", `null`},
			},
		},
		{
			schema:  "RoomMessageRequest",
			control: `{"client_message_id":"c1","text":"hello @Mia","mentions":["mia","all"]}`,
			newDst:  func() any { return &generated.RoomMessageRequest{} },
			bad: []wu0aRESTCase{
				{"missing client_message_id", `{"text":"hi","mentions":[]}`},
				{"empty text", `{"client_message_id":"c1","text":"","mentions":[]}`},
				{"mentions null", `{"client_message_id":"c1","text":"hi","mentions":null}`},
				{"extra field", `{"client_message_id":"c1","text":"hi","mentions":[],"author":"mia"}`},
				{"null body", `null`},
			},
		},
	}
}

// wu0aDecode runs the real decodeAndValidate over body and returns its
// verdict plus the HTTP status it wrote (200 when it wrote nothing).
func wu0aDecode(t *testing.T, schema, body string, dst any, validate bool) (bool, int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wu0a-probe", strings.NewReader(body))
	rec := httptest.NewRecorder()
	ok := decodeAndValidate(rec, req, schema, dst, validate)
	return ok, rec.Code, rec.Body.String()
}

// TestWU0aContracts_RESTInbound_ValidationOn proves every new REST inbound
// schema compiles from the embedded inboundschemas mirror, accepts its
// control, and refuses malformed / missing / extra / null bodies with 400.
func TestWU0aContracts_RESTInbound_ValidationOn(t *testing.T) {
	for _, shape := range wu0aRESTShapes() {
		t.Run(shape.schema+"/control", func(t *testing.T) {
			ok, code, body := wu0aDecode(t, shape.schema, shape.control, shape.newDst(), true)
			require.True(t, ok, "control must pass %s validation; got %d %s", shape.schema, code, body)
			assert.Equal(t, http.StatusOK, code, "control must not write an error status")
		})
		for _, bc := range shape.bad {
			t.Run(shape.schema+"/"+bc.name, func(t *testing.T) {
				ok, code, body := wu0aDecode(t, shape.schema, bc.body, shape.newDst(), true)
				assert.False(t, ok, "%s must be refused with validation on", bc.name)
				assert.Equal(t, http.StatusBadRequest, code, "%s must be a 400; body %s", bc.name, body)
				assert.NotContains(t, body, "sk-test-value", "an error must never echo a secret value")
			})
		}
	}
}

// TestWU0aContracts_RESTInbound_ValidationOff proves the new shapes decode
// into their generated types with validation off (the decode the future
// handlers receive), and that the typed decode alone still refuses syntax
// and type errors.
func TestWU0aContracts_RESTInbound_ValidationOff(t *testing.T) {
	var submit generated.SecretSubmitRequest
	ok, code, body := wu0aDecode(t, "SecretSubmitRequest", `{"values":{"api_key":"sk-test-value"}}`, &submit, false)
	require.True(t, ok, "control must decode with validation off; got %d %s", code, body)
	require.NotNil(t, submit.Values, "values must decode into the generated type")
	assert.Equal(t, "sk-test-value", (*submit.Values)["api_key"])
	assert.Nil(t, submit.Cancel)

	var cancel generated.SecretSubmitRequest
	ok, _, _ = wu0aDecode(t, "SecretSubmitRequest", `{"cancel":true}`, &cancel, false)
	require.True(t, ok)
	require.NotNil(t, cancel.Cancel)
	assert.Equal(t, generated.SecretSubmitRequestCancelTrue, *cancel.Cancel)

	var room generated.RoomCreateRequest
	ok, _, _ = wu0aDecode(t, "RoomCreateRequest", `{"kind":"squad","name":"Launch","agent_ids":["mia","jim"]}`, &room, false)
	require.True(t, ok)
	assert.Equal(t, generated.RoomCreateRequestKindSquad, room.Kind)
	assert.Equal(t, []string{"mia", "jim"}, room.AgentIds)

	var opts generated.SecretOptionsUpdateRequest
	ok, code, _ = wu0aDecode(t, "SecretOptionsUpdateRequest", `{"available_in_channel_turns":"yes"}`, &opts, false)
	assert.False(t, ok, "a wrong JSON type must still fail the typed decode with validation off")
	assert.Equal(t, http.StatusBadRequest, code)

	var msg generated.RoomMessageRequest
	ok, code, _ = wu0aDecode(t, "RoomMessageRequest", `{"client_message_id":`, &msg, false)
	assert.False(t, ok, "malformed JSON must fail with validation off")
	assert.Equal(t, http.StatusBadRequest, code)
}

// wu0aWSConn opens an authenticated chat WebSocket against handler.
func wu0aWSConn(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)
	return conn
}

// wu0aSendAndCollect writes frame, then a ping, and returns the message of
// every error frame received before the pong. The read loop handles frames in
// order and validation runs synchronously before dispatch, so any schema
// error for frame is on the wire before the pong.
func wu0aSendAndCollect(t *testing.T, conn *websocket.Conn, frame string) []string {
	t.Helper()
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(frame)))
	ping, err := json.Marshal(generated.PingFrame{Type: string(generated.WsFrameTypePing)})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, ping))
	deadline := time.Now().Add(10 * time.Second)
	var errs []string
	for {
		require.NoError(t, conn.SetReadDeadline(deadline))
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "read before pong")
		var peek map[string]any
		require.NoError(t, json.Unmarshal(raw, &peek))
		switch peek["type"] {
		case string(generated.WsFrameTypePong):
			return errs
		case string(generated.WsFrameTypeError):
			var ef generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &ef))
			errs = append(errs, ef.Message)
		}
	}
}

// wu0aSchemaErrors keeps only schema-validation error messages naming schema.
func wu0aSchemaErrors(msgs []string, schema string) []string {
	var out []string
	for _, m := range msgs {
		if strings.Contains(m, "frame schema validation failed ("+schema+")") {
			out = append(out, m)
		}
	}
	return out
}

// wu0aWSShape is one new or changed inbound WS shape with its control and
// the bad frames validation ON must refuse.
type wu0aWSShape struct {
	schema  string
	control string
	bad     []wu0aRESTCase
}

func wu0aWSShapes() []wu0aWSShape {
	return []wu0aWSShape{
		{
			schema:  "AttachRoomFrame",
			control: `{"type":"attach_room","room_id":"r1","after_seq":0}`,
			bad: []wu0aRESTCase{
				{"missing room_id", `{"type":"attach_room","after_seq":0}`},
				{"negative after_seq", `{"type":"attach_room","room_id":"r1","after_seq":-1}`},
				{"null room_id", `{"type":"attach_room","room_id":null,"after_seq":0}`},
				{"extra field", `{"type":"attach_room","room_id":"r1","after_seq":0,"session_id":"s1"}`},
			},
		},
		{
			schema:  "MessageFrame",
			control: `{"type":"message","content":"set up a team","agent_id":"mia","workflow_start":{"workflow":"W18","target_workspace_id":"ws-sales"}}`,
			bad: []wu0aRESTCase{
				{"unknown workflow", `{"type":"message","content":"x","agent_id":"mia","workflow_start":{"workflow":"W9","target_workspace_id":"ws"}}`},
				{"missing target", `{"type":"message","content":"x","agent_id":"mia","workflow_start":{"workflow":"W1"}}`},
				{"extra field", `{"type":"message","content":"x","agent_id":"mia","workflow_start":{"workflow":"W1","target_workspace_id":"ws","go":true}}`},
				{"null workflow_start", `{"type":"message","content":"x","agent_id":"mia","workflow_start":null}`},
			},
		},
		{
			schema:  "WhatsAppPairingSubscribeFrame",
			control: `{"type":"whatsapp_pairing_subscribe","channel_id":"whatsapp_native","active":true,"request_id":"req-1","session_id":"s1"}`,
			bad: []wu0aRESTCase{
				{"empty request_id", `{"type":"whatsapp_pairing_subscribe","channel_id":"whatsapp_native","active":true,"request_id":""}`},
				{"numeric session_id", `{"type":"whatsapp_pairing_subscribe","channel_id":"whatsapp_native","active":true,"session_id":7}`},
				{"null request_id", `{"type":"whatsapp_pairing_subscribe","channel_id":"whatsapp_native","active":true,"request_id":null}`},
				{"extra field", `{"type":"whatsapp_pairing_subscribe","channel_id":"whatsapp_native","active":true,"pairing_code":"x"}`},
			},
		},
	}
}

// TestWU0aContracts_WSInbound_ValidationOn drives each new inbound WS shape
// through the real chat read loop with gateway.validate_inbound=true.
func TestWU0aContracts_WSInbound_ValidationOn(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	al.GetConfig().Gateway.ValidateInbound = true
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for _, shape := range wu0aWSShapes() {
		t.Run(shape.schema+"/control", func(t *testing.T) {
			conn := wu0aWSConn(t, srv)
			errs := wu0aSendAndCollect(t, conn, shape.control)
			assert.Empty(t, wu0aSchemaErrors(errs, shape.schema),
				"the control frame must pass %s validation", shape.schema)
		})
		for _, bc := range shape.bad {
			t.Run(shape.schema+"/"+bc.name, func(t *testing.T) {
				conn := wu0aWSConn(t, srv)
				errs := wu0aSendAndCollect(t, conn, bc.body)
				assert.Len(t, wu0aSchemaErrors(errs, shape.schema), 1,
					"%s must be refused with exactly one schema error naming %s; got %v", bc.name, shape.schema, errs)
			})
		}
	}
}

// TestWU0aContracts_WSInbound_ValidationOff proves that with validation off
// no schema error is produced for the new fields, a valid frame is not
// refused, and the typed decode still refuses a wrong JSON type.
func TestWU0aContracts_WSInbound_ValidationOff(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	al.GetConfig().Gateway.ValidateInbound = false
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for _, shape := range wu0aWSShapes() {
		t.Run(shape.schema+"/control", func(t *testing.T) {
			conn := wu0aWSConn(t, srv)
			errs := wu0aSendAndCollect(t, conn, shape.control)
			assert.Empty(t, wu0aSchemaErrors(errs, shape.schema), "validation off must not schema-check")
		})
	}

	t.Run("MessageFrame/wrong workflow type refused by typed decode", func(t *testing.T) {
		conn := wu0aWSConn(t, srv)
		errs := wu0aSendAndCollect(t, conn,
			`{"type":"message","content":"x","agent_id":"mia","workflow_start":{"workflow":5,"target_workspace_id":"ws"}}`)
		assert.Contains(t, errs, "malformed message frame",
			"the generated-type decode must refuse a numeric workflow with validation off")
	})
}

// TestWU0aContracts_NewInboundSchemasCompile pins that every schema file WU-0a
// adds to the embedded inbound mirror compiles under the gateway's validator
// (boot runs PreCompileAllInboundSchemas and refuses to start otherwise).
func TestWU0aContracts_NewInboundSchemasCompile(t *testing.T) {
	names := []string{
		"RoomRef", "WorkflowStart", "SecretRecipientDisplay", "SecretRequestField",
		"SecretRequestFrame", "SecretRequestResolvedFrame", "SessionStateSecretRequest",
		"SecretSubmitRequest", "SecretRequestState", "SecretNameEntry", "SecretOptionsUpdateRequest",
		"Room", "RoomCreateRequest", "RoomMembersUpdateRequest", "RoomMessageRequest",
		"RoomEntryAuthor", "RoomDeliveryState", "RoomEntry", "RoomEntryFrame",
		"RoomMemberStateFrame", "AttachRoomFrame", "RoomSnapshotFrame", "RoomMetaFrame",
		"AgentDependency", "AgentDependencyList",
	}
	for _, n := range names {
		_, err := compileInboundSchema(n)
		assert.NoError(t, err, "inbound schema %s must compile", n)
	}
	require.NoError(t, PreCompileAllInboundSchemas())
}
