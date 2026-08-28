// cancel_latch_expired_notice_test.go — closes the last gap in the cancel-
// acknowledgement chain: handleCancel already tells the user "acknowledged"
// (cancel_stage "graceful") the instant CancelOutcome.Armed is true
// (cancel_armed_ack_test.go). But a pre-registration cancel latch
// (pkg/agent/cancel_prearm.go) armed in response to that click can age out
// (cancelPreArmTTL) with no turn ever registering to consume it — the user
// was told "acknowledged" and then never told it didn't happen. This file
// proves the fix: buildCancelHooks' OnLatchExpired wiring sends an honest
// ErrorFrame ("did not take effect"), never a second cancel_stage frame
// (which would over-signal — see the wiring's own doc comment for why
// cancel_stage's closed {graceful, hard, detached} enum has no member for
// this case, and why ErrorFrame is the correct existing wire type instead).
//
// Two tests, matching the two failure modes CancelHooks.OnLatchExpired must
// avoid:
//
//   - TestHandleCancel_LatchExpiredUnfired_SendsHonestErrorFrame proves the
//     fix: a latch armed for a session that never registers a turn, once it
//     ages past cancelPreArmTTL, produces an honest ErrorFrame — not silence,
//     not a second "graceful" claiming progress that never happened.
//
//   - TestHandleCancel_FiredImmediately_NoLatchExpiredFalseAlarm is the
//     over-signaling guard: an ordinary, immediately-successful cancel
//     (Fired:true from the start — a live turn was found, no latch ever
//     armed) must NEVER produce this ErrorFrame, across the whole real
//     graceful→hard→detached escalation. Without this test, a wiring bug
//     that called OnLatchExpired (or an equivalent frame) on every cancel
//     would ship a false alarm on every successful Stop click.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// genericWSTestFrame decodes the union of fields this file's assertions need
// across multiple frame types (cancel_stage's "stage", error's "message",
// every frame's "session_id") — replayFrameDecoder (websocket_session_test.go)
// covers everything except "stage", so a dedicated local decoder is simpler
// than extending that shared one for a single field only this file needs.
type genericWSTestFrame struct {
	Type      string `json:"type"`
	Stage     string `json:"stage,omitempty"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
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

// TestHandleCancel_LatchExpiredUnfired_SendsHonestErrorFrame drives the real
// pre-registration-latch machinery end to end, with NO shortcuts:
//
//  1. Cancel session A, which has never had a turn registered under it —
//     RequestCancel arms a latch (Armed:true) and handleCancel's existing
//     Armed-ack path sends the ordinary "graceful" cancel_stage frame.
//  2. Wait past the real cancelPreArmTTL (5s; pkg/gateway cannot shrink
//     pkg/agent's unexported var, so this uses the real default — matching
//     this codebase's own convention of real-timer cancel tests, e.g.
//     TestCancel_TwoStageTimer_GracefulThenHard's real 3s/5s escalation).
//     Session A's latch is now armed but unconsumed: nothing ever registered
//     for it.
//  3. Cancel an UNRELATED session B (also never registered). Per
//     armLocked's doc comment (cancel_prearm.go), a completely unrelated
//     cancel's own arm call is the ONLY discovery point for a latch whose
//     target turn never registers at all — this opportunistic sweep evicts
//     A's now-expired latch and asynchronously invokes A's ORIGINAL
//     OnLatchExpired hook (captured from THIS SAME connection when A was
//     canceled in step 1).
//  4. Assert an ErrorFrame arrives for session A, honestly stating the
//     cancel did not take effect — never a second "graceful" (which would
//     claim progress that never happened).
//
// This is also this fix's MUTATION TARGET: reverting the OnLatchExpired
// field in buildCancelHooks (websocket.go) makes step 4 time out (the base
// agent-level Warn log in notifyLatchExpired still fires, unconditionally,
// but no ErrorFrame is ever sent because notifyLatchExpired nil-checks the
// hook before calling it) — a clean, real RED, not a panic.
func TestHandleCancel_LatchExpiredUnfired_SendsHonestErrorFrame(t *testing.T) {
	// Not t.Parallel(): waits out a real multi-second timer (cancelPreArmTTL).
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	const sessionA = "cancel-latch-expiry-never-registered-a"
	const sessionB = "cancel-latch-expiry-never-registered-b"

	// armCancelOrFindActiveTurn (pkg/agent/cancel_prearm.go) now additionally
	// requires turnImminentForIdentity — real dispatcher evidence a turn is
	// actually about to exist, not merely "none is registered right now" (see
	// that function's doc comment). Both A and B need this primed: A so its
	// own cancel below arms a latch at all, and B so ITS cancel in step 3
	// reaches armLocked (cancel_prearm.go) — the opportunistic sweep that
	// discovers and evicts A's now-expired latch is inside armLocked, which
	// turnImminentForIdentity gates just as much as the arm itself.
	// PrimeSessionImminentForTest is the exported, test-only
	// (testing.Testing()-guarded) cross-package equivalent of pkg/agent's own
	// unexported primeImminentSessionWorker helper (cancel_prearm_test.go) —
	// see cancel_armed_ack_test.go's identical note for the full rationale.
	require.NoError(t, al.PrimeSessionImminentForTest(sessionA))
	require.NoError(t, al.PrimeSessionImminentForTest(sessionB))

	// Step 1: cancel A — arms a latch, Armed:true. Read exactly one frame
	// (the "graceful" ack); this succeeds without ever hitting conn's read
	// deadline, so conn's read state stays clean for the later read in step 4
	// (see readAllWSFramesFor's doc comment on the sticky-read-error trap).
	cancelA := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionA}
	dataA, err := json.Marshal(cancelA)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataA))

	// readFrameOfType's only job here is to consume exactly one clean frame
	// off the wire before moving on — the Armed-ack shape itself (that this
	// is "graceful") is already covered by cancel_armed_ack_test.go.
	readFrameOfType(t, conn, "cancel_stage", 2*time.Second)

	// Step 2: wait past the real cancelPreArmTTL (5s default) so A's latch is
	// expired-but-unconsumed by the time B's cancel arrives.
	time.Sleep(5500 * time.Millisecond)

	// Step 3: cancel B — an unrelated session, also never registered. Its own
	// arm call opportunistically sweeps A's now-expired latch (armLocked,
	// cancel_prearm.go) and fires A's ORIGINAL OnLatchExpired hook async.
	cancelB := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionB}
	dataB, err := json.Marshal(cancelB)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataB))

	// Step 4: find A's honest ErrorFrame among whatever else arrives (B's own
	// "graceful" ack for its own Armed:true outcome may interleave in either
	// order — this loop skips anything that isn't the error frame we want).
	deadline := time.Now().Add(3 * time.Second)
	var found *genericWSTestFrame
	for time.Now().Before(deadline) && found == nil {
		conn.SetReadDeadline(deadline) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		var f genericWSTestFrame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if f.Type == "error" && f.SessionID == sessionA {
			found = &f
		}
	}

	require.NotNil(t, found,
		"an expired-unfired pre-registration cancel latch must produce an honest "+
			"ErrorFrame for the session it stood in for — silence here means the "+
			"user was told 'acknowledged' and never told it didn't happen")
	assert.Contains(t, found.Message, cancelLatchExpiredMessageSubstring,
		"the message must honestly state the cancel did NOT take effect")
}

// TestHandleCancel_FiredImmediately_NoLatchExpiredFalseAlarm is the
// over-signaling guard: an ordinary cancel that succeeds immediately
// (Fired:true — a live turn is found on the very first lookup, so
// RequestCancel never reaches the pre-registration-latch path at all, see
// cancel.go) must NEVER produce the OnLatchExpired ErrorFrame, no matter how
// long the observer waits. Drives a real blocking turn through the full
// graceful→hard→detached escalation (the same shape as
// TestCancel_TwoStageTimer_GracefulThenHard) and asserts across that ENTIRE
// window that no frame carries the "did not take effect" wording.
func TestHandleCancel_FiredImmediately_NoLatchExpiredFalseAlarm(t *testing.T) {
	// Not parallel — real ~8s timer sequence, same rationale as
	// TestCancel_TwoStageTimer_GracefulThenHard.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Blocks for 20s — well past the 3s+5s graceful/hard/detached window.
	ip := newIronProvider(20 * time.Second)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18804, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "iron-provider"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. There is no implicit "main" sentinel
			// to fall back on any more (ADR-064), and handleChatMessage now
			// REFUSES a chat frame it cannot resolve an agent for rather than
			// creating a session with an empty owner -- so a config with no
			// agents produces no session_started frame at all.
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode: config.SandboxModeOff,
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, ip)

	ctx, cancelCtx := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancelCtx()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Logf("agent loop Run did not exit within %v", cancelTestTurnStartDeadline)
		}
	})
	t.Cleanup(ip.Shutdown)
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start iron turn"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	select {
	case <-ip.ready:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("BLOCKED: ironProvider never entered Chat")
	}

	// This turn is live and registered, so this cancel is Fired:true
	// immediately — the pre-registration latch path (and therefore
	// OnLatchExpired) is never reached at all.
	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// ONE continuous collection window spanning graceful (immediate), hard
	// (t+3s), and detached (t+8s) — see readAllWSFramesFor's doc comment for
	// why this must be a single call rather than several sequential ones.
	frames := readAllWSFramesFor(conn, 9*time.Second)

	var sawGraceful bool
	for _, f := range frames {
		if f.Type == "cancel_stage" && f.Stage == "graceful" {
			sawGraceful = true
		}
		if f.Type == "error" {
			assert.NotContains(t, f.Message, cancelLatchExpiredMessageSubstring,
				"a real, immediately-fired cancel (no latch ever armed) must "+
					"never emit the OnLatchExpired 'did not take effect' message — "+
					"doing so would be a false alarm on every successful Stop click")
		}
	}
	assert.True(t, sawGraceful,
		"sanity check: the ordinary Fired:true cancel path must still work")
}
