//go:build !cgo

// ws_test_helpers_test.go — shared test-only type helpers for WebSocket tests.
//
// wsClientFrameTestHelper is a convenience struct for constructing client→server
// JSON frames in tests. It mirrors the union of all client frame fields so tests
// can send any frame type without importing each generated type separately.
// It is only used to marshal outbound frames FROM test code TO the server;
// it is never used as a server-side decode target.

package gateway

// wsClientFrameTestHelper is a test-only convenience struct for marshaling
// client→server WebSocket frames. Production code uses generated types.
// This struct carries the superset of all client frame fields so that tests
// can construct any frame type with a simple struct literal.
//
// Field naming mirrors the JSON wire format so json.Marshal produces correct frames.
type wsClientFrameTestHelper struct {
	Type      string   `json:"type"`
	Token     string   `json:"token,omitempty"`      // auth frame
	Content   string   `json:"content,omitempty"`    // message frame
	SessionID string   `json:"session_id,omitempty"` // message/cancel/attach_session/session_close
	AgentID   string   `json:"agent_id,omitempty"`   // message frame (route to specific agent)
	Media     []string `json:"media,omitempty"`      // message frame (media:// attachment refs)
	ID        string   `json:"id,omitempty"`         // exec_approval_response
	Decision  string   `json:"decision,omitempty"`   // exec_approval_response / device_pairing_response
	DeviceID  string   `json:"device_id,omitempty"`  // device_pairing_response
}
