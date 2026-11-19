package gateway

// websocket_cancel_idle_test.go — RED (#1428 step 2 option C + #1841,
// qa-lead): the idle-cancel reply `cancel_idle {type, session_id}` (C-11,
// spec §4.14 + 1841-DESIGN.md) and its interaction rules N-2..N-6.
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - SPEC-C-server-binds-send-to-chat-r8.md §4.14 N-2..N-6: the idle reply
//     is NOT send_refused (N-2); order on the connection is command_received
//     FIRST (S-12), then the idle reply (N-3); an idle reply never produces a
//     refusal snapshot (N-4); never in the armed pre-registration window
//     (N-5); old tabs per N-6 as two separate cases.
//   - §6.1 SV-22 (lifecycle-backed fixture ONLY — the r8 prerequisite: an
//     idle verdict on a record-less fixture alone does not count): browser
//     connection bound to an idle chat A, cancel{A, origin {chat, A},
//     client_frame_id c9, scope tree} → frames in order command_received{
//     cancel, c9, A}, then cancel_idle{A}; ZERO send_refused; ZERO
//     session_state from the refusal-snapshot rule.
//   - 1841-DESIGN.md §1: the idle predicate (!Fired && !Armed && no
//     background kills && Reached/Unreachable/SkippedNewerGeneration empty &&
//     discarded == 0 && !staged) with BOTH fixtures (lifecycle-backed
//     completed and stopped; record-less) and the armed negative; the branch
//     table (fired; cascaded with reached; background kill; armed; idle
//     completed; idle stopped; idle record-less; record running with no live
//     turn; queued input discarded).
//   - Contract commit 32802e2a6: CancelIdleFrame = exactly
//     {type: "cancel_idle", session_id}; CommandReceivedFrame =
//     {command, client_frame_id, session_id, type}.
//
// RED STATE: neither command_received nor cancel_idle exists on the wire
// today. On the lifecycle-backed idle path handleCancelShown today sends the
// detached report (cancel_stage, empty reached); on the record-less path it
// sends nothing. Every RED test below fails on the missing receipt/reply —
// naming the frames that actually arrived. Armed, fired, record-running and
// requester-only are existing-behaviour guards; their failability proof belongs
// to CHECK. The strengthened queued-discarded row additionally requires a REAL
// discard of that exact input; it now fails loudly when this prerequisite is
// absent, instead of letting a timer and zero cancel_idle manufacture a pass.
//
// NOT TESTABLE HERE (reported, N-1/N-7/N-8 are process items): the branch
// table's "cascaded with reached" and "background kill" rows need real
// helper-tree / background-shell fixtures no gateway harness builds; they are
// recorded as gaps in the test plan, not silently dropped.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sv22Key follows the CONTRACT bounds (SendOrigin.yaml: 16..128 chars of
// ^[A-Za-z0-9_-]+$).
const sv22Key = "K-sv22-idle-origin1"

const (
	sv22User    = "sv22-browser-user"
	sv22C9      = "sv22-c9"
	idleWindow  = 4 * time.Second // bounded reply-collect window (failsafe, not a latency assertion)
	lifecycleEW = 20 * time.Second
)

// newIdleCancelServer is the production wiring a real install has: agent
// loop running (al.Run), lifecycle store + boot epoch + steer canceller,
// stream delegate, and a single human account for cookie auth.
func newIdleCancelServer(t *testing.T, provider providers.LLMProvider) (*httptest.Server, string, *agent.AgentLoop, *session.LifecycleStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cookie, hash, err := middleware.MintSessionToken()
	require.NoError(t, err, "SETUP: mint cookie + hash pair")
	workspaceDir := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 0,
			Users: []config.UserConfig{{Username: sv22User, SessionTokenHash: config.BcryptHash(hash)}},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: workspaceDir, DefaultModel: config.DefaultModel{Model: "idle-cancel"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)

	// Production steering wiring (gateway_boot.go::wireSteerDeps), the same
	// shape newRedirectStreamServer uses.
	boot := session.NewBootEpochStore(home)
	_, err = boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel).
		SetRevivalStateWriter(al.WriteSteerRevivalState))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(30 * time.Second):
			t.Log("agent loop Run did not exit within 30s")
		}
	})
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	// The minted cookie travels with the server: dial with
	// dialIdleCancelBrowser(t, srv, cookie).
	return srv, cookie, al, lifecycle
}

// dialIdleCancelBrowser dials the server as the SV-22 browser connection.
func dialIdleCancelBrowser(t *testing.T, srv *httptest.Server, cookie string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/chat/ws"
	header := http.Header{}
	header.Set("Origin", srv.URL)
	header.Set("Cookie", middleware.SessionCookieName+"="+cookie)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, httpResp, err := dialer.Dial(wsURL, header)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "SETUP: browser-kind dial must upgrade")
	return conn
}

// mintIdleFixtureChat sends the browser chat's first message (carrying the
// new_chat origin, valid at GREEN too) and returns the minted session id.
func mintIdleFixtureChat(t *testing.T, conn *websocket.Conn, content string) string {
	t.Helper()
	frame := generated.MessageFrame{
		Type:    "message",
		AgentId: strPtr("mia"),
		Origin:  &generated.SendOrigin{Kind: "new_chat", NewChatKey: strPtr(sv22Key)},
		Content: content,
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw), "SETUP: first message write")
	started := readFrameOfType(t, conn, "session_started", lifecycleEW)
	require.NotEmpty(t, started.SessionID, "SETUP: the fixture chat must mint and acknowledge")
	return started.SessionID
}

// drainAdmissionOneShot reads the connection's post-auth admission frames
// (today the session_state one-shot, F-aa) so a later cancel-reply window
// observes only the cancel's own replies.
func drainAdmissionOneShot(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(lifecycleEW)))
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "SETUP: the admission frame must arrive")
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek))
		if peek.Type == "session_state" {
			return
		}
	}
}

// bindIdleCancelFixture drains the legitimate attach snapshot before cancel.
func bindIdleCancelFixture(t *testing.T, conn *websocket.Conn, sessionID string) {
	t.Helper()
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(lifecycleEW)))
	require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sessionID}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(lifecycleEW)))
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "SETUP: the idle chat's attach must finish")
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek))
		if peek.Type == "catch_up_complete" {
			return
		}
	}
}

// seedFixtureRecord hand-seeds a lifecycle record the way production's
// initializeOrdinaryRootRecord shapes an ordinary root (OwnerScopeKind human,
// origin chat), so the server reads a record indistinguishable from one its
// own admission wrote. stopped additionally requires a StopNote
// (persistLocked's rule for LifecycleStopped).
func seedFixtureRecord(t *testing.T, lc *session.LifecycleStore, sessionID string, state session.LifecycleState) {
	t.Helper()
	rec := &session.LifecycleRecord{
		SessionID:      sessionID,
		Generation:     1,
		State:          state,
		OwnerScopeKind: session.OwnerScopeHuman,
		AgentID:        "mia",
		Origin:         &session.Origin{Kind: session.OriginKind(session.SessionTypeChat)},
	}
	if state == session.LifecycleStopped {
		rec.StopNote = &session.StopNote{At: time.Now(), By: sv22User, Cause: session.StopCauseStop}
	}
	require.NoError(t, lc.Persist(rec), "SETUP: seed the %s record", state)
}

// waitForLifecycleState polls the lifecycle store until the session's record
// reaches the wanted state — the FIXTURE guard proving the idle precondition
// (a terminal record, or stopped after a real stop) actually holds. A fixture
// that never gets there fails loudly, never silently tests the wrong branch.
func waitForLifecycleState(t *testing.T, lc *session.LifecycleStore, sessionID string, want session.LifecycleState) {
	t.Helper()
	deadline := time.Now().Add(lifecycleEW)
	var last, lastErr string
	for time.Now().Before(deadline) {
		rec, err := lc.Load(sessionID)
		if err != nil {
			lastErr = err.Error()
		} else if rec != nil {
			last = string(rec.State)
			if rec.State == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("SETUP: lifecycle record for %s never reached %q within the failsafe (last state=%q err=%q) — "+
		"the idle fixture precondition does not hold", sessionID, want, last, lastErr)
}

// idleReply is one frame observed in the cancel-reply window.
type idleReply struct {
	Type string
	Raw  []byte
}

// collectCancelReplies observes the whole synchronous cancel-handling interval.
// The application ping is dispatched by the SAME read loop after cancel returns;
// its pong is a queue barrier, not a product reply. Do not stop at cancel_stage or
// cancel_idle: a forbidden refusal snapshot can be queued AFTER either one.
// Each caller uses this barrier only once per connection (pong is debounced).
func collectCancelReplies(t *testing.T, conn *websocket.Conn, window time.Duration) []idleReply {
	t.Helper()
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(window)))
	require.NoError(t, conn.WriteJSON(generated.PingFrame{Type: "ping"}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(window)))
	var out []idleReply
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "instrument: cancel's read-loop barrier must arrive; collected %v", replyTypes(out))
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek), "every reply frame must be valid JSON")
		if peek.Type == "pong" {
			return out
		}
		out = append(out, idleReply{Type: peek.Type, Raw: raw})
	}
}

// requireIdleReceiptSequence asserts the N-3 order and the N-2/N-4 negatives
// on a collected reply window. The pair's POSITION is the oracle: the receipt
// is the connection's FIRST reply frame (S-12: enqueued before the handler
// acts) and cancel_idle follows it. Other frames may legitimately follow
// (1841-DESIGN.md: the reply is sent IN ADDITION to whatever handleCancelShown
// sends today — on the lifecycle-backed idle path that is still the detached
// report) — so exclusivity is NOT asserted. SV-22 bans session_state FROM THE
// REFUSAL-SNAPSHOT RULE, not unrelated admission/attach or live state frames.
// Callers first drain admission/attach and settle the idle fixture; the pong
// barrier then isolates this cancel's synchronous output, with no live producer.
// Only in that isolated interval does zero session_state identify no snapshot.
func requireIdleReceiptSequence(t *testing.T, replies []idleReply, sessionID, clientFrameID, phase string) {
	t.Helper()
	types := make([]string, len(replies))
	for i, r := range replies {
		types[i] = r.Type
	}
	require.NotEmpty(t, replies,
		"%s: N-3 violated: an accepted idle cancel carrying client_frame_id must be answered; "+
			"the reply window holds nothing (%v)", phase, types)
	require.Equal(t, "command_received", replies[0].Type,
		"%s: N-3/S-12 violated: the FIRST reply must be command_received (enqueued before the handler acts), "+
			"got %q (window: %v)", phase, replies[0].Type, types)
	var receipt generated.CommandReceivedFrame
	require.NoError(t, json.Unmarshal(replies[0].Raw, &receipt), "%s: receipt must decode", phase)
	require.Equal(t, "cancel", receipt.Command, "%s: receipt.command must name the cancel", phase)
	require.Equal(t, clientFrameID, receipt.ClientFrameId, "%s: receipt must correlate the client_frame_id", phase)
	require.Equal(t, sessionID, receipt.SessionId, "%s: receipt must name the target", phase)

	idleIdx := -1
	for i, r := range replies[1:] {
		if r.Type == "cancel_idle" {
			idleIdx = i + 1
			break
		}
	}
	require.Greater(t, idleIdx, 0,
		"%s: #1841 violated: cancel_idle must follow command_received on the same connection; "+
			"window: %v", phase, types)
	var idle generated.CancelIdleFrame
	require.NoError(t, json.Unmarshal(replies[idleIdx].Raw, &idle), "%s: idle reply must decode", phase)
	require.Equal(t, sessionID, idle.SessionId,
		"%s: cancel_idle.session_id must name the target in which nothing was running", phase)
	for _, r := range replies {
		require.NotEqual(t, "send_refused", r.Type,
			"%s: N-2 violated: an idle cancel passed the origin check and was accepted — it must never "+
				"be answered with send_refused (%s)", phase, r.Raw)
		require.NotEqual(t, "session_state", r.Type,
			"%s: N-4 violated: an accepted idle cancel must not get a refusal snapshot in"+
				" this isolated interval (admission/attach drained, idle fixture, through pong barrier): %s", phase, r.Raw)
	}
}

// requireNoIdleReply asserts a reply window stays free of cancel_idle — the
// GUARD oracle for every branch where #1841 forbids the reply.
func requireNoIdleReply(t *testing.T, replies []idleReply, phase string) {
	t.Helper()
	for _, r := range replies {
		require.NotEqual(t, "cancel_idle", r.Type,
			"%s: violated: cancel_idle must never be sent on this branch (observed: %s)", phase, r.Raw)
	}
}

// TestCancelIdle_SV22_LifecycleBackedCompleted is SV-22 on the r8-pinned
// fixture: an ordinary web chat whose last turn COMPLETED (lifecycle-backed).
// The browser connection binds to it, then cancels it with origin +
// client_frame_id. Expected per N-2..N-4: command_received{cancel, c9, A}
// first, then cancel_idle{A}; zero send_refused; zero session_state.
func TestCancelIdle_SV22_LifecycleBackedCompleted(t *testing.T) {
	srv, cookie, _, lc := newIdleCancelServer(t, &restMockProvider{})
	conn := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = conn.Close() })

	// Fixture: chat A exists, its turn completed, the record is terminal.
	sid := mintIdleFixtureChat(t, conn, "SV-22 fixture: an ordinary turn that completes")
	waitDone := readFrameOfType(t, conn, "done", lifecycleEW)
	require.NotEmpty(t, waitDone.SessionID, "SETUP: the fixture turn must complete with done")
	waitForLifecycleState(t, lc, sid, session.LifecycleCompleted)

	// Bind the browser connection to A (SV-22: bound to the idle chat).
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(lifecycleEW)))
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "SETUP: the attach must answer")
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek))
		if peek.Type == "catch_up_complete" {
			break
		}
	}

	// SV-22: cancel{A, origin {chat, A}, client_frame_id c9, scope tree}.
	scope := "tree"
	cancelFrame := generated.CancelFrame{
		Type:          "cancel",
		SessionId:     sid,
		Scope:         &scope,
		ClientFrameId: strPtr(sv22C9),
		Origin:        &generated.SendOrigin{Kind: "chat", SessionId: &sid},
	}
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteJSON(cancelFrame))

	replies := collectCancelReplies(t, conn, idleWindow)
	requireIdleReceiptSequence(t, replies, sid, sv22C9, "SV-22 lifecycle-backed completed")
}

// TestCancelIdle_LifecycleBackedStopped is the second lifecycle-backed idle
// fixture (1841-DESIGN.md §1): a chat whose record is STOPPED ("a chat whose
// last turn was stopped is stopped"). The server's verdict is read off the
// record (StopAcceptAlreadyLanded → the root lands in NO list → idle), so the
// fixture hand-seeds that record the way production's own settle path leaves
// it; driving a real live-stop to get there would only re-test today's stop
// machinery, which is not this spec's scope.
func TestCancelIdle_LifecycleBackedStopped(t *testing.T) {
	srv, cookie, al, lc := newIdleCancelServer(t, &restMockProvider{})
	conn := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = conn.Close() })

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "SETUP: stopped-fixture session")
	seedFixtureRecord(t, lc, meta.ID, session.LifecycleStopped)
	// Admission and attach are allowed to send session_state. Drain both before
	// the accepted-cancel interval; bind to the target so a snapshot mutant is visible.
	drainAdmissionOneShot(t, conn)
	bindIdleCancelFixture(t, conn, meta.ID)

	cancelFrame := generated.CancelFrame{
		Type:          "cancel",
		SessionId:     meta.ID,
		Scope:         strPtr("tree"),
		ClientFrameId: strPtr("sv22-idle-after-stop"),
		Origin:        &generated.SendOrigin{Kind: "chat", SessionId: &meta.ID},
	}
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteJSON(cancelFrame))
	replies := collectCancelReplies(t, conn, idleWindow)
	requireIdleReceiptSequence(t, replies, meta.ID, "sv22-idle-after-stop", "lifecycle-backed stopped")
}

// TestCancelIdle_RecordLessIdle is the record-less idle fixture: a session
// with metadata but NO lifecycle record and no live turn. StopSession's
// record-less path finds nothing running and nothing imminent — the reply
// pair must still be command_received then cancel_idle (the design's
// record-less proof leg).
func TestCancelIdle_RecordLessIdle(t *testing.T) {
	srv, cookie, al, lc := newIdleCancelServer(t, &restMockProvider{})
	conn := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = conn.Close() })
	drainAdmissionOneShot(t, conn)

	// Fixture: session in the store, never admitted — no lifecycle record.
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "SETUP: record-less fixture session")
	_, loadErr := lc.Load(meta.ID)
	require.Error(t, loadErr, "SETUP: the fixture session must have NO lifecycle record")
	bindIdleCancelFixture(t, conn, meta.ID)

	cancelFrame := generated.CancelFrame{
		Type:          "cancel",
		SessionId:     meta.ID,
		Scope:         strPtr("tree"),
		ClientFrameId: strPtr("sv22-idle-recordless"),
		Origin:        &generated.SendOrigin{Kind: "chat", SessionId: &meta.ID},
	}
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, conn.WriteJSON(cancelFrame))

	replies := collectCancelReplies(t, conn, idleWindow)
	requireIdleReceiptSequence(t, replies, meta.ID, "sv22-idle-recordless", "record-less idle")
}

// TestCancelIdle_ArmedPrearmNeverIdle is N-5's guard: in the pre-arm window
// (the Armed latch on the record-less path) the idle reply is NEVER sent —
// today's graceful frame stays.
func TestCancelIdle_ArmedPrearmNeverIdle(t *testing.T) {
	srv, cookie, al, lc := newIdleCancelServer(t, &restMockProvider{})
	conn := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = conn.Close() })

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "SETUP: armed-fixture session")
	_, loadErr := lc.Load(meta.ID)
	require.Error(t, loadErr, "SETUP: the armed fixture needs a record-less session")
	require.NoError(t, al.PrimeSessionImminentForTest(meta.ID),
		"SETUP: arm the pre-registration latch for the imminent turn")

	cancelFrame := generated.CancelFrame{
		Type:          "cancel",
		SessionId:     meta.ID,
		Scope:         strPtr("tree"),
		ClientFrameId: strPtr("sv22-armed"),
		Origin:        &generated.SendOrigin{Kind: "chat", SessionId: &meta.ID},
	}
	require.NoError(t, conn.WriteJSON(cancelFrame))
	replies := collectCancelReplies(t, conn, idleWindow)
	requireNoIdleReply(t, replies, "N-5: the armed pre-registration window must never answer cancel_idle")
}

// TestCancelIdle_NeverIdle_BranchGuards covers the branch table's not-idle
// rows that are drivable without helper-tree/background fixtures: fired
// (live turn), record running with NO live turn, queued input discarded.
// Each asserts the window stays free of cancel_idle (green by design; the
// mutation that sends an idle reply on any of them dies here).
func TestCancelIdle_NeverIdle_BranchGuards(t *testing.T) {
	t.Run("fired live turn", func(t *testing.T) {
		iron := newIronProvider(30 * time.Second)
		srv, cookie, _, lc := newIdleCancelServer(t, iron)
		t.Cleanup(iron.Shutdown) // after the server's cleanups (LIFO): unblock the provider first
		conn := dialIdleCancelBrowser(t, srv, cookie)
		t.Cleanup(func() { _ = conn.Close() })
		sid := mintIdleFixtureChat(t, conn, "fired-guard: a live turn")
		waitForLifecycleState(t, lc, sid, session.LifecycleRunning)
		require.NoError(t, conn.WriteJSON(generated.CancelFrame{
			Type: "cancel", SessionId: sid, Scope: strPtr("tree"),
			ClientFrameId: strPtr("guard-fired"), Origin: &generated.SendOrigin{Kind: "chat", SessionId: &sid},
		}))
		replies := collectCancelReplies(t, conn, idleWindow)
		requireNoIdleReply(t, replies, "branch 'fired': a cancel that fires the turn is not idle")
	})

	t.Run("record running with no live turn", func(t *testing.T) {
		srv, cookie, al, lc := newIdleCancelServer(t, &restMockProvider{})
		conn := dialIdleCancelBrowser(t, srv, cookie)
		t.Cleanup(func() { _ = conn.Close() })
		store := al.GetSessionStore()
		require.NotNil(t, store)
		meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
		require.NoError(t, err, "SETUP: running-record fixture session")
		seedFixtureRecord(t, lc, meta.ID, session.LifecycleRunning)

		require.NoError(t, conn.WriteJSON(generated.CancelFrame{
			Type: "cancel", SessionId: meta.ID, Scope: strPtr("tree"),
			ClientFrameId: strPtr("guard-running"), Origin: &generated.SendOrigin{Kind: "chat", SessionId: &meta.ID},
		}))
		replies := collectCancelReplies(t, conn, idleWindow)
		requireNoIdleReply(t, replies,
			"branch 'record running with no live turn': a granted durable stop is not idle")
	})

	t.Run("queued input discarded", func(t *testing.T) {
		iron := newIronProvider(30 * time.Second)
		srv, cookie, al, lc := newIdleCancelServer(t, iron)
		t.Cleanup(iron.Shutdown) // after the server's cleanups (LIFO): unblock the provider first
		conn := dialIdleCancelBrowser(t, srv, cookie)
		t.Cleanup(func() { _ = conn.Close() })
		sid := mintIdleFixtureChat(t, conn, "queued-guard: the running turn")
		waitForLifecycleState(t, lc, sid, session.LifecycleRunning)
		select {
		case <-iron.ready:
		case <-time.After(lifecycleEW):
			t.Fatal("SETUP: provider must be blocked before queuing the follow-up")
		}
		turnID, _, _, live := al.ActiveForegroundTurnInfo(sid)
		require.True(t, live, "SETUP: a real foreground turn owns this queue")
		sub := al.SubscribeEvents(64)
		t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
		// Queue exactly one follow-up while the provider cannot consume it.
		queued := generated.MessageFrame{
			Type: "message", AgentId: strPtr("mia"), SessionId: &sid,
			ClientMessageId: strPtr("guard-queued-msg"), Content: "queued while the turn runs",
			Origin: &generated.SendOrigin{Kind: "chat", SessionId: &sid},
		}
		require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
		queuedRaw, marshalErr := json.Marshal(queued)
		require.NoError(t, marshalErr)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, queuedRaw))
		waitForIdleGuardQueue(t, sub, turnID, len(queued.Content))
		require.NoError(t, conn.WriteJSON(generated.CancelFrame{
			Type: "cancel", SessionId: sid, Scope: strPtr("tree"),
			ClientFrameId: strPtr("guard-queued-cancel"), Origin: &generated.SendOrigin{Kind: "chat", SessionId: &sid},
		}))
		replies := collectCancelReplies(t, conn, idleWindow)
		var discarded []generated.MessageStatusFrame
		for _, reply := range replies {
			if reply.Type != "message_status" {
				continue
			}
			var status generated.MessageStatusFrame
			require.NoError(t, json.Unmarshal(reply.Raw, &status))
			if status.ClientMessageId == "guard-queued-msg" && status.State == "discarded" {
				discarded = append(discarded, status)
			}
		}
		if assert.Len(t, discarded, 1, "#1841 queued-discard prerequisite: Stop must discard the EXACT queued client message; frames=%v", replyTypes(replies)) {
			assert.Equal(t, sid, discarded[0].SessionId)
			assert.Equal(t, strPtr("stopped_before_delivery"), discarded[0].Reason)
			t.Log("instrument: guard-queued-msg discarded / stopped_before_delivery before the read-loop barrier")
		}
		queuedEntryID := ""
		for _, reply := range replies {
			if reply.Type != "user_message" {
				continue
			}
			var echo generated.UserMessageFrame
			require.NoError(t, json.Unmarshal(reply.Raw, &echo))
			if echo.ClientMessageId != nil && *echo.ClientMessageId == "guard-queued-msg" {
				require.Equal(t, sid, echo.SessionId)
				require.Equal(t, queued.Content, echo.Content)
				queuedEntryID = echo.Id
			}
		}
		require.NotEmpty(t, queuedEntryID, "instrument: observe the exact follow-up's persisted archive id")
		dispositions, err := al.GetSessionStore().InputDispositions(sid)
		require.NoError(t, err)
		disposition, found := dispositions[queuedEntryID]
		assert.True(t, found, "#1841 queued-discard prerequisite: Stop must durably discard this archived input, not merely send a frame")
		assert.Equal(t, queuedEntryID, disposition.MessageID)
		assert.Equal(t, "discarded", disposition.State)
		assert.Equal(t, "stopped_before_delivery", disposition.Reason)
		requireNoIdleReply(t, replies,
			"branch 'queued input discarded': a stop that discards queued input is not idle")
	})
}

// waitForIdleGuardQueue observes the real queue-admission event, not a delay.
// This fixture has one live turn and sends exactly one follow-up after subscribing.
func waitForIdleGuardQueue(t *testing.T, sub agent.EventSubscription, turnID string, contentLen int) {
	t.Helper()
	timer := time.NewTimer(lifecycleEW)
	defer timer.Stop()
	for {
		select {
		case event, open := <-sub.C:
			require.True(t, open, "SETUP: queue event subscription must stay open")
			if event.Kind != agent.EventKindInterruptReceived || event.Meta.TurnID != turnID {
				continue
			}
			payload, ok := event.Payload.(agent.InterruptReceivedPayload)
			require.True(t, ok, "instrument: real interrupt event must carry its typed payload")
			if payload.Kind != agent.InterruptKindSteering {
				continue
			}
			require.Equal(t, "user", payload.Role)
			require.Equal(t, contentLen, payload.ContentLen)
			require.Equal(t, 1, payload.QueueDepth, "SETUP: exactly the follow-up is queued behind the blocked provider")
			return
		case <-timer.C:
			t.Fatal("SETUP: the follow-up never reached the real steering queue; a no-idle verdict would prove the wrong branch")
		}
	}
}

// TestCancelIdle_ReplyIsRequesterOnly is N-6's first old-tab case, server
// leg: cancel_idle goes to the requesting connection ONLY — a second bound
// tab (an old parser would drop the unknown type) never receives it.
func TestCancelIdle_ReplyIsRequesterOnly(t *testing.T) {
	srv, cookie, _, lc := newIdleCancelServer(t, &restMockProvider{})
	conn := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = conn.Close() })
	sid := mintIdleFixtureChat(t, conn, "requester-only: the idle chat")
	readFrameOfType(t, conn, "done", lifecycleEW)
	waitForLifecycleState(t, lc, sid, session.LifecycleCompleted)

	// A second browser tab bound to the same chat.
	other := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = other.Close() })
	require.NoError(t, other.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
	require.NoError(t, other.SetReadDeadline(time.Now().Add(lifecycleEW)))
	for {
		_, raw, err := other.ReadMessage()
		require.NoError(t, err, "SETUP: the second tab's attach must answer")
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek))
		if peek.Type == "catch_up_complete" {
			break
		}
	}

	require.NoError(t, conn.WriteJSON(generated.CancelFrame{
		Type: "cancel", SessionId: sid, Scope: strPtr("tree"),
		ClientFrameId: strPtr("sv22-requester-only"), Origin: &generated.SendOrigin{Kind: "chat", SessionId: &sid},
	}))
	_ = collectCancelReplies(t, conn, idleWindow) // the requester's window; its shape is SV-22's claim

	// The other tab must not see the reply: drain briefly and assert.
	deadline := time.Now().Add(idleWindow)
	for time.Now().Before(deadline) {
		require.NoError(t, other.SetReadDeadline(time.Now().Add(time.Until(deadline))))
		_, raw, err := other.ReadMessage()
		if err != nil {
			break // window exhausted with nothing received — the wanted shape
		}
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek))
		require.NotEqual(t, "cancel_idle", peek.Type,
			"N-6(i): cancel_idle is requester-only — a second bound tab must never receive it (got %s)", raw)
	}
}

// TestCancelIdle_OldTabOriginLessCancelRefusedNotIdle is spec r9 §4.14
// N-6's second old-tab case. Section 4.3.4's cancel row requires the existing
// error carrier with exact OUTDATED_STOP text and a synthetic message id,
// followed by session_state only when bound to the target (R1/R2). It is NOT
// send_refused: that carrier is for a present origin (§4.3.2 P1a/P1b/P1c).
// An origin-less browser cancel never reaches the idle handler, even on an
// idle chat. SV-19(d): the completed chat's snapshot has no active_turn.
func TestCancelIdle_OldTabOriginLessCancelRefusedNotIdle(t *testing.T) {
	srv, cookie, _, lc := newIdleCancelServer(t, &restMockProvider{})
	conn := dialIdleCancelBrowser(t, srv, cookie)
	t.Cleanup(func() { _ = conn.Close() })
	sid := mintIdleFixtureChat(t, conn, "old-tab: the idle chat cancelled origin-less")
	readFrameOfType(t, conn, "done", lifecycleEW)
	waitForLifecycleState(t, lc, sid, session.LifecycleCompleted)
	// Bind explicitly and drain attach's legitimate snapshot so only the
	// refusal's ordered error + snapshot can satisfy the assertion below.
	bindIdleCancelFixture(t, conn, sid)

	// The old tab's shape: NO origin, NO client_frame_id.
	require.NoError(t, conn.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: sid, Scope: strPtr("tree")}))
	replies := collectCancelReplies(t, conn, idleWindow)

	for _, r := range replies {
		require.NotEqual(t, "cancel_idle", r.Type,
			"N-6(ii): a refused origin-less cancel never reaches the idle handler — no cancel_idle (got %s)", r.Raw)
	}
	require.Equal(t, []string{"error", "session_state"}, replyTypes(replies),
		"N-6(ii)/spec r9 §4.3.4: old-tab cancel must be refused with the outdated error then the bound snapshot, "+
			"never executed or answered with send_refused/command_received (actual: %v)", replyTypes(replies))
	var refusal generated.ErrorFrame
	require.NoError(t, json.Unmarshal(replies[0].Raw, &refusal), "old-tab refusal must decode with the existing error carrier")
	require.Equal(t, "error", refusal.Type)
	require.Equal(t, "Not stopped. Reload this page.", refusal.Message, "§4.3.4: OUTDATED_STOP exact text")
	require.Equal(t, &sid, refusal.SessionId, "§4.3.4: message-scoped refusal names the target, never a global error")
	require.NotNil(t, refusal.ClientMessageId, "§4.3.4: synthetic id keeps the old reducer's refusal message-scoped")
	require.Regexp(t, `^outdated-[A-Za-z0-9_-]{12}$`, *refusal.ClientMessageId, "§4.3.4: outdated- plus 12 URL-safe chars")
	require.Nil(t, refusal.FirstMessageError, "§4.3.4: this is not a first-message failure")
	var snapshot generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(replies[1].Raw, &snapshot), "old-tab refusal snapshot must decode")
	require.Equal(t, "session_state", snapshot.Type)
	require.Equal(t, &sid, snapshot.SessionId, "§4.3.4: snapshot belongs to the bound target")
	require.Nil(t, snapshot.ActiveTurn, "SV-19(d): a completed idle chat has no active turn")
	var snapshotFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(replies[1].Raw, &snapshotFields))
	require.NotContains(t, snapshotFields, "active_turn", "SV-19(d): absent when idle, not an active_turn:null placeholder")
}

func replyTypes(replies []idleReply) []string {
	types := make([]string, len(replies))
	for i, r := range replies {
		types[i] = r.Type
	}
	return types
}
