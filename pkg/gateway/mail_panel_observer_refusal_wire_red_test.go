package gateway

// RED: PANEL-MAIL-REFUSAL-WIRE-RED-1817 / TW2 A (foreground final ruling).
// ERROR correlations only: omit unavailable observer/workspace IDs, preserve a
// recoverable nonempty sibling. REQUEST/ACK stay strict. Oracle is the ruling,
// contracts/asyncapi.yaml::MailPanelObserverErrorFrame and US-5 AS-6 in
// docs/internal/specs/mail-live-access-w3-panel-and-settings-spec.md.
//
// REAL: isolated httptest WebSocket auth, readLoop, raw request decode,
// dispatch, sendMailPanelObserverError, generated Go type, writePump bytes.
// Nothing returns a manufactured/typed mock server refusal. Separate Vitest
// contract tests cover generated client validation and real frame handling.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestMailPanelObserverTW2RefusalWireOmission(t *testing.T) {
	h, workspace := newPresenceTestHandler(t)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	const observer = "qa-mail-observer"

	if !t.Run("valid_ack_is_owned_and_other_connection_cannot_revoke", func(t *testing.T) {
		owner, other := tw2RefusalSocket(t, server), tw2RefusalSocket(t, server)
		for _, step := range []struct {
			action string
			other  bool
			count  int
		}{{action: "open", count: 1}, {action: "close", other: true, count: 1}, {action: "close", count: 0}} {
			conn := owner
			if step.other {
				conn = other
			}
			raw := tw2RefusalExchange(t, conn, tw2RefusalRequest(t, step.action, observer, workspace))
			var ack generated.MailPanelObserverAckFrame
			require.NoError(t, json.Unmarshal(raw, &ack))
			require.Equal(t, generated.MailPanelObserverAckFrame{
				Type: "mail_panel_observer_ack", Action: step.action, ObserverId: observer, WorkspaceId: workspace,
			}, ack, "accepted ACK retains its exact generated shape")
			tw2RefusalAssertKeys(t, raw, []string{"type", "action", "observer_id", "workspace_id"})
			require.Equal(t, step.count, h.mailPresenceOf().Count(workspace), "presence authority belongs to the real connection")
		}
	}) {
		t.Fatal("BLOCKED: real socket/ACK/ownership positive control failed; no omission RED established")
	}
	if !t.Run("valid_malformed_action_preserves_both_ids", func(t *testing.T) {
		raw := tw2RefusalExchange(t, tw2RefusalSocket(t, server), tw2RefusalRequest(t, "qa-invalid-action", observer, workspace))
		tw2RefusalAssertError(t, raw, "malformed_frame", tw2RefusalString(observer), tw2RefusalString(workspace))
	}) {
		t.Fatal("BLOCKED: valid-ID malformed refusal control failed; no omission RED established")
	}
	if !t.Run("valid_unknown_workspace_preserves_both_ids", func(t *testing.T) {
		const unknownWorkspace = "qa-mail-unknown-workspace"
		raw := tw2RefusalExchange(t, tw2RefusalSocket(t, server), tw2RefusalRequest(t, "open", observer, unknownWorkspace))
		tw2RefusalAssertError(t, raw, "unauthorized_workspace", tw2RefusalString(observer), tw2RefusalString(unknownWorkspace))
	}) {
		t.Fatal("BLOCKED: valid-ID unauthorized refusal control failed; no omission RED established")
	}
	if !t.Run("real_unrelated_ping_keeps_exact_pong", func(t *testing.T) {
		raw := tw2RefusalExchange(t, tw2RefusalSocket(t, server), []byte(`{"type":"ping"}`))
		var pong generated.PongFrame
		require.NoError(t, json.Unmarshal(raw, &pong))
		require.Equal(t, generated.PongFrame{Type: "pong"}, pong)
		tw2RefusalAssertKeys(t, raw, []string{"type"})
	}) {
		t.Fatal("BLOCKED: unrelated real-frame control failed; no omission RED established")
	}

	cases := []struct {
		name, field string
		value       any
		omit        bool
		observer    *string
		workspace   *string
	}{
		{name: "missing_observer", field: "observer_id", omit: true, workspace: tw2RefusalString(workspace)},
		{name: "empty_observer", field: "observer_id", value: "", workspace: tw2RefusalString(workspace)},
		{name: "whitespace_observer", field: "observer_id", value: " \t ", workspace: tw2RefusalString(workspace)},
		{name: "null_observer", field: "observer_id", value: nil, workspace: tw2RefusalString(workspace)},
		{name: "numeric_observer", field: "observer_id", value: 7, workspace: tw2RefusalString(workspace)},
		{name: "boolean_observer", field: "observer_id", value: true, workspace: tw2RefusalString(workspace)},
		{name: "object_observer", field: "observer_id", value: map[string]any{"qa": true}, workspace: tw2RefusalString(workspace)},
		{name: "array_observer", field: "observer_id", value: []any{"qa"}, workspace: tw2RefusalString(workspace)},
		{name: "missing_workspace", field: "workspace_id", omit: true, observer: tw2RefusalString(observer)},
		{name: "empty_workspace", field: "workspace_id", value: "", observer: tw2RefusalString(observer)},
		{name: "whitespace_workspace", field: "workspace_id", value: " \n ", observer: tw2RefusalString(observer)},
		{name: "null_workspace", field: "workspace_id", value: nil, observer: tw2RefusalString(observer)},
		{name: "numeric_workspace", field: "workspace_id", value: 7, observer: tw2RefusalString(observer)},
		{name: "boolean_workspace", field: "workspace_id", value: false, observer: tw2RefusalString(observer)},
		{name: "object_workspace", field: "workspace_id", value: map[string]any{"qa": true}, observer: tw2RefusalString(observer)},
		{name: "array_workspace", field: "workspace_id", value: []any{"qa"}, observer: tw2RefusalString(observer)},
		{name: "nonstring_action_preserves_valid_ids", field: "action", value: false, observer: tw2RefusalString(observer), workspace: tw2RefusalString(workspace)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{"type": "mail_panel_observer", "action": "open", "observer_id": observer, "workspace_id": workspace}
			if test.omit {
				delete(fields, test.field)
			} else {
				fields[test.field] = test.value
			}
			request, err := json.Marshal(fields)
			require.NoError(t, err)
			raw := tw2RefusalExchange(t, tw2RefusalSocket(t, server), request)
			tw2RefusalAssertError(t, raw, "malformed_frame", test.observer, test.workspace)
			require.Zero(t, h.mailPresenceOf().Count(workspace), "a malformed refusal must not bind presence")
		})
	}
}

func tw2RefusalSocket(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	require.NoError(t, err, "BLOCKED: isolated socket setup, not omission RED")
	t.Cleanup(func() { _ = conn.Close() })
	// Isolated existing dev-bypass helper; no live/configured account or secret.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"auth","token":"test"}`)))
	initial := tw2RefusalRead(t, conn)
	var initialFields map[string]any
	require.NoError(t, json.Unmarshal(initial, &initialFields))
	require.Equal(t, "session_state", initialFields["type"], "must reach actual authenticated dispatch")
	return conn
}

func tw2RefusalRead(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	// Hang bound only; the behaviour oracle is actual frame content, not time.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	kind, raw, err := conn.ReadMessage()
	require.NoError(t, err, "BLOCKED: socket did not produce a readable response, not omission RED")
	require.Equal(t, websocket.TextMessage, kind)
	digest := sha256.Sum256(raw)
	t.Logf("TW2 actual_server_bytes_sha256=%x", digest)
	if directory := os.Getenv("TW2_MAIL_RECEIPTS_DIR"); directory != "" {
		require.NoError(t, os.MkdirAll(directory, 0o700))
		name := fmt.Sprintf("%s_%x.json", strings.ReplaceAll(t.Name(), "/", "_"), digest)
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), raw, 0o600))
	}
	return raw
}

func tw2RefusalExchange(t *testing.T, conn *websocket.Conn, request []byte) []byte {
	t.Helper()
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, request))
	return tw2RefusalRead(t, conn)
}

func tw2RefusalRequest(t *testing.T, action, observer, workspace string) []byte {
	t.Helper()
	request, err := json.Marshal(map[string]string{
		"type": "mail_panel_observer", "action": action, "observer_id": observer, "workspace_id": workspace,
	})
	require.NoError(t, err)
	return request
}

func tw2RefusalString(value string) *string { return &value }

func tw2RefusalAssertError(t *testing.T, raw []byte, code string, observer, workspace *string) {
	t.Helper()
	var frame generated.MailPanelObserverErrorFrame
	require.NoError(t, json.Unmarshal(raw, &frame), "ACTUAL generated Go type must decode captured server bytes")
	require.Equal(t, "mail_panel_observer_error", frame.Type, "no generic ErrorFrame workaround")
	require.Equal(t, code, frame.Code)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	keys := []string{"type", "code", "error"}
	for key, expected := range map[string]*string{"observer_id": observer, "workspace_id": workspace} {
		value, present := fields[key]
		if expected == nil {
			require.False(t, present, "TW2 A: unavailable %s must be OMITTED from real refusal bytes, not echoed as empty/null; actual=%s", key, raw)
		} else {
			require.Equal(t, *expected, value, "TW2 A: recoverable nonempty sibling %s must survive", key)
			keys = append(keys, key)
		}
	}
	tw2RefusalAssertKeys(t, raw, keys)
	// Round-trip invariant, not an invented error-message oracle: generated Go
	// re-encoding must preserve exactly the captured contract keys and values.
	roundTrip, err := json.Marshal(frame)
	require.NoError(t, err)
	var decodedFields map[string]any
	require.NoError(t, json.Unmarshal(roundTrip, &decodedFields))
	require.Equal(t, fields, decodedFields)
}

func tw2RefusalAssertKeys(t *testing.T, raw []byte, expected []string) {
	t.Helper()
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	actual := make([]string, 0, len(fields))
	for key := range fields {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	require.Equal(t, expected, actual, "refusal/ACK must keep the exact contract key set")
}
