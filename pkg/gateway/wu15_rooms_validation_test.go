package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Chief Q3 A: malformed/missing/extra/null fields = 400; default semantic
// refusals = 422. With schema validation on, a schema-bound refusal can be
// 400 or 422. In every case the rejected write must leave state unchanged.
func TestWU15Rooms_CreateValidationOnAndOffSavesNothingForInvalidBodies(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"malformed", `{`, 400},
		{"missing_kind", `{"agent_ids":["mia","jim"]}`, 400},
		{"missing_agents", `{"kind":"adhoc"}`, 400},
		{"null_agents", `{"kind":"adhoc","agent_ids":null}`, 400},
		{"wrong_type", `{"kind":"adhoc","agent_ids":"mia,jim"}`, 400},
		{"unknown_field", `{"kind":"adhoc","agent_ids":["mia","jim"],"surprise":true}`, 400},
		{"missing_squad_name", `{"kind":"squad","agent_ids":["mia","jim"]}`, 422},
		{"zero_agents", `{"kind":"adhoc","agent_ids":[]}`, 422},
		{"one_agent", `{"kind":"adhoc","agent_ids":["mia"]}`, 422},
		{"duplicate_agents", `{"kind":"adhoc","agent_ids":["mia","mia"]}`, 422},
		{"empty_name", `{"kind":"squad","name":"","agent_ids":["mia","jim"]}`, 422},
	}
	for _, validation := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("validation_%t/%s", validation, tc.name), func(t *testing.T) {
				h := wu15NewHarness(t)
				h.cfg.Gateway.ValidateInbound = validation
				h.create("squad", "Control", "mia", "jim")
				before := h.listRooms(wu15Workspace)
				status, body := h.rawHTTP(http.MethodPost, "/api/v1/workspaces/sales/rooms", []byte(tc.body))
				if validation && tc.status == 422 {
					require.Contains(t, []int{400, 422}, status, "Q3 A: bound refusal, never 2xx/404")
				} else {
					require.Equal(t, tc.status, status, "Q3 A: exact refusal class: %s", body)
				}
				wu15RequireError(t, status, body, status, "")
				require.ElementsMatch(t, before, h.listRooms(wu15Workspace), "§7.2: rejected creation saved no room")
			})
		}
	}
}

func TestWU15Rooms_MessageValidationOnAndOffHasNoTranscriptOrAdmissionSideEffects(t *testing.T) {
	cases := []struct{ name, body string }{
		{"malformed", `{`},
		{"missing_id", `{"text":"hello","mentions":[]}`},
		{"missing_text", `{"client_message_id":"bad","mentions":[]}`},
		{"missing_mentions", `{"client_message_id":"bad","text":"hello"}`},
		{"null_text", `{"client_message_id":"bad","text":null,"mentions":[]}`},
		{"null_mentions", `{"client_message_id":"bad","text":"hello","mentions":null}`},
		{"wrong_text_type", `{"client_message_id":"bad","text":12,"mentions":[]}`},
		{"extra_author", `{"client_message_id":"bad","text":"hello","mentions":[],"author":{"kind":"agent","agent_id":"jim"}}`},
	}
	for _, validation := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("validation_%t/%s", validation, tc.name), func(t *testing.T) {
				h := wu15NewHarness(t)
				room := h.create("squad", "Launch", "mia", "jim")
				h.cfg.Gateway.ValidateInbound = validation
				h.post(room.Id, "valid-control", "VALID_MESSAGE_CONTROL", "mia")
				control := wu15FindEntry(t, h.snapshot(room.Id), "valid-control")
				require.Equal(t, "VALID_MESSAGE_CONTROL", control.Text)
				require.Equal(t, []string{"mia"}, control.Addressees)
				before := h.snapshot(room.Id)
				mia, jim := h.messages("mia"), h.messages("jim")
				status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(tc.body))
				wu15RequireError(t, status, body, http.StatusBadRequest, "")
				wu15UnchangedSnapshot(t, before, h.snapshot(room.Id))
				require.Equal(t, mia, h.messages("mia"))
				require.Equal(t, jim, h.messages("jim"))
			})
		}
	}
}

func TestWU15Rooms_ClientMessageIDBoundaryAndRejectedWriteHasNoEffect(t *testing.T) {
	for _, length := range []int{0, 1, 2, 127, 128, 129} {
		t.Run(fmt.Sprintf("id_length_%d", length), func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim")
			before := h.snapshot(room.Id)
			status, body := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: strings.Repeat("i", length), Text: "hello", Mentions: []string{}})
			if length == 0 || length == 129 {
				wu15RequireError(t, status, body, http.StatusBadRequest, "")
				wu15UnchangedSnapshot(t, before, h.snapshot(room.Id))
			} else {
				require.Equal(t, http.StatusAccepted, status, "K26: id length 1..128 is valid: %s", body)
				require.Equal(t, "hello", wu15FindEntry(t, h.snapshot(room.Id), strings.Repeat("i", length)).Text)
			}
		})
	}
}

func wu15UnchangedSnapshot(t *testing.T, before, after gen.RoomSnapshotFrame) {
	t.Helper()
	require.Equal(t, before.Entries, after.Entries, "§7.2: rejected input saved no room transcript")
	require.Equal(t, before.MemberStates, after.MemberStates, "§7.2: rejected input created no member delivery")
	require.Equal(t, before.Members, after.Members)
	require.Equal(t, before.Muted, after.Muted)
	require.Equal(t, before.Paused, after.Paused)
	require.Equal(t, before.Generation, after.Generation)
	require.Equal(t, before.AllocatedCostMicroUsd, after.AllocatedCostMicroUsd)
}
