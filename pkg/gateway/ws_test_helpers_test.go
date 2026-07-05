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
	ID        string   `json:"id,omitempty"`         // device_pairing_response
	Decision  string   `json:"decision,omitempty"`   // device_pairing_response
	DeviceID  string   `json:"device_id,omitempty"`  // device_pairing_response
}

// wsConnDroppedForTest returns the current inboundDropped counter value from
// the wsConn registered under the given chatID in handler.sessions.
// Returns -1 if no matching session is found.
// Used exclusively in tests to assert the drop counter without changing the
// production API.
func wsConnDroppedForTest(h *WSHandler, chatID string) int32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	wc, ok := h.sessions[chatID]
	if !ok {
		return -1
	}
	return wc.inboundDropped.Load()
}

// wsConnChatIDsForTest returns a snapshot of all active chatIDs in handler.sessions.
// Used to find the first (usually only) active connection in a test.
func wsConnChatIDsForTest(h *WSHandler) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		ids = append(ids, id)
	}
	return ids
}

// makeTestConn creates a minimal wsConn with a buffered sendCh and an open doneCh.
// The sendCh must be buffered so sendConnGenFrame does not block in tests.
//
// Formerly defined in ws_approval_test.go (deleted with the retired
// wsApprovalHook gate, ADR-036 §3.4); relocated here because it is a
// general-purpose helper used broadly across the package's WebSocket tests,
// independent of that retired mechanism.
func makeTestConn() *wsConn {
	return &wsConn{
		sendCh: make(chan []byte, 16),
		doneCh: make(chan struct{}),
	}
}
