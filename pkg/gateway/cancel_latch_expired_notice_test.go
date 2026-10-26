// cancel_latch_expired_notice_test.go — closes the last gap in the cancel-
// acknowledgement chain: handleCancel already tells the user "acknowledged"
// (cancel_stage "graceful") the instant CancelOutcome.Armed is true
// (cancel_armed_ack_test.go). But a pre-registration cancel latch
// (pkg/agent/cancel_prearm.go) armed in response to that click can age out
// (cancelPreArmTTL) with no turn ever registering to consume it — the user
// was told "acknowledged" and then never told it didn't happen. This file
// proves the fix: buildCancelHooks' OnLatchExpired wiring sends an honest
// stop_notice ("did not take effect"), never a terminal chat error or a second
// cancel_stage frame. The latter's closed {graceful, hard, detached} enum has
// no member for this case. The historical test identifier is retained below.
//
// Two tests, matching the two failure modes CancelHooks.OnLatchExpired must
// avoid:
//
//   - TestHandleCancel_LatchExpiredUnfired_SendsHonestErrorFrame proves the
//     fix: a latch armed for a session that never registers a turn, once it
//     ages past cancelPreArmTTL, produces an honest stop_notice — not silence,
//     not a second "graceful" claiming progress that never happened.
//
//   - TestHandleCancel_FiredImmediately_NoLatchExpiredFalseAlarm is the
//     over-signaling guard: an ordinary, immediately-successful cancel
//     (Fired:true from the start — a live turn was found, no latch ever
//     armed) must NEVER produce this false-alarm notice, across the whole real
//     graceful→hard→detached escalation. Without this test, a wiring bug
//     that called OnLatchExpired (or an equivalent frame) on every cancel
//     would ship a false alarm on every successful Stop click.
package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/notifications"
)

// configureStopNoticeTestUser makes the live socket authenticate a named
// recipient through the real auth path instead of anonymous development bypass.
func configureStopNoticeTestUser(t *testing.T, h *WSHandler) {
	t.Helper()
	h.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{{Username: "stop-notice-test-user", TokenHash: mustBcryptHash(t, "dev-token")}}
	h.notifStore = notifications.NewStore(t.TempDir())
}

// genericWSTestFrame decodes the union of fields this file's assertions need
// across multiple frame types (cancel_stage's "stage", error's "message",
// every frame's "session_id") — replayFrameDecoder (websocket_session_test.go)
// covers everything except "stage", so a dedicated local decoder is simpler
// than extending that shared one for a single field only this file needs.
// not-wire-format: assertion projection, not a product transport type.
type genericWSTestFrame struct {
	Type             string `json:"type"`
	Stage            string `json:"stage,omitempty"`
	Message          string `json:"message,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	Body             string `json:"body,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
}

// readAllWSFramesFor drains every frame conn delivers for exactly one bounded
// window and returns them all, decoded. Used instead of readFrameOfType
// (which returns on the FIRST match) when a test needs to inspect the WHOLE
// set of frames a real timer sequence produces — e.g. proving something is
// ABSENT across a multi-second graceful→hard→detached run.
//
// Deliberately a SINGLE continuous collection call, not several separate
// bounded reads on the same conn: gorilla/websocket's Conn caches its first
// read error and replays that SAME cached error on every later ReadMessage
// call regardless of a fresh deadline (confirmed empirically, see
// cancel_armed_ack_test.go's TestHandleCancel_SecondClickOnAlreadyClaimedTurn_
// SendsNoAckFrame). A second, separate bounded call on a conn that already
// hit a deadline in an EARLIER call would return empty in microseconds no
// matter what the server sent afterward — a vacuous pass. Collecting
// everything relevant in one call (this function is called exactly once per
// conn in this file) avoids that trap without needing a second connection —
// unlike the over-signaling guard in cancel_armed_ack_test.go, there is no
// second click here for a second connection to represent, and no frame in
// this file's tests is delivered anywhere other than the single conn that
// issued the cancel (buildCancelHooks(wc) closes over exactly that
// connection).
func readAllWSFramesFor(conn *websocket.Conn, timeout time.Duration) []genericWSTestFrame {
	deadline := time.Now().Add(timeout)
	var frames []genericWSTestFrame
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f genericWSTestFrame
		if json.Unmarshal(raw, &f) == nil {
			frames = append(frames, f)
		}
	}
	return frames
}

// cancelLatchExpiredMessageSubstring is the honest-failure substring
// websocket.go's OnLatchExpired wiring sends. Kept as one constant so the
// production string and the test assertion cannot silently drift apart.
const cancelLatchExpiredMessageSubstring = "did not take effect"
