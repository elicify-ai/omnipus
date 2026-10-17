package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestWU15Rooms_AttachValidationOnAndOffRejectsMalformedMissingExtraNull(t *testing.T) {
	cases := []struct{ name, body string }{
		{"malformed", `{"type":"attach_room"`},
		{"missing_room", `{"type":"attach_room","after_seq":0}`},
		{"missing_cursor", `{"type":"attach_room","room_id":%q}`},
		{"null_room", `{"type":"attach_room","room_id":null,"after_seq":0}`},
		{"null_cursor", `{"type":"attach_room","room_id":%q,"after_seq":null}`},
		{"wrong_cursor_type", `{"type":"attach_room","room_id":%q,"after_seq":"zero"}`},
		{"negative_cursor", `{"type":"attach_room","room_id":%q,"after_seq":-1}`},
		{"unknown_field", `{"type":"attach_room","room_id":%q,"after_seq":0,"agent_id":"mia"}`},
	}
	for _, validation := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("validation_%t/%s", validation, tc.name), func(t *testing.T) {
				h := wu15NewHarness(t)
				room := h.create("squad", "Launch", "mia", "jim")
				h.cfg.Gateway.ValidateInbound = validation
				conn := h.connect()
				before := h.attach(conn, room.Id, 0) // real valid control in the same mode
				body := tc.body
				if strings.Contains(body, "%q") {
					body = fmt.Sprintf(body, room.Id)
				}
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(body)))
				var refusal gen.ErrorFrame
				require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "error"), &refusal))
				require.NotEmpty(t, refusal.Message, "§7.2: explicit validation refusal, never quiet empty snapshot")
				wu15UnchangedSnapshot(t, before, h.snapshot(room.Id))
			})
		}
	}
}
