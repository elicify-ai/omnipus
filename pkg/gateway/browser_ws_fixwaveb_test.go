// browser_ws_fixwaveb_test.go — regression coverage for the three measured
// gateway-side defects fixed in FIX WAVE B against pkg/gateway/browser_ws.go
// and pkg/gateway/browser_webrtc.go:
//
//   - A (HIGH): handleAttach and handleViewport ran INLINE on the WebSocket
//     read loop. browser_ws.go's own dispatchWebRTCOffer doc comment already
//     spells out why that is forbidden — gorilla services the registered
//     PongHandler only from inside a ReadMessage call, so a multi-second
//     handler starves every Pong and the peer tears the connection down —
//     and the offer handler was moved off the loop for exactly that reason.
//     These two were not. MEASURED: handleViewport -> SetViewport 6.95s
//     against a busy page; handleAttach -> Live().Attach -> ensureStarted
//     1.0-2.2s warm and ~9.5s for a whole first open on a fresh profile.
//     For the duration the panel accepted no clicks, keys, tab actions or
//     detach. Fixed by dispatchAttach/dispatchViewport +
//     browserConnWorkQueue + the attachEpoch commit discipline.
//   - B (MEDIUM): a non-controlling second viewer's browser_viewport frame
//     was dropped with slog.Debug and nothing else. MEASURED: the tab stayed
//     at the first viewer's size while the second watched a mis-shaped
//     picture with nothing in the product explaining why — while that same
//     viewer's clicks and keystrokes DID land, because LiveView.dispatchInput
//     deliberately has no control gate at all (operator directive
//     2026-08-03). Fixed by sending that viewer a real status message.
//   - C (MEDIUM): the ADR-062 tier-1 shared media socket bound a FIXED UDP
//     port, so a second Omnipus on the same host failed with `listen udp
//     :50000: bind: address already in use`, logged ERROR and silently
//     continued on ephemeral ports — which on a hosted install means live
//     video just stops working. Fixed by falling back to the next free port
//     with a loud WARN naming both ports. SUPERSEDED by round-2 finding F6:
//     that WARN was still only a log line, so the fallback could engage
//     invisibly. It is now an ERROR prefixed "OPERATOR ACTION REQUIRED" AND
//     a browser_status(error) frame that tells the viewer, in plain language,
//     that video works locally but will fail for remote viewers until the
//     port is freed or reconfigured (see browser_webrtc_media_port_test.go).
//
// Kept in its own file per this package's established convention
// (browser_webrtc_fixwave_test.go's header comment) rather than growing an
// existing file further.
//
// RESOURCE RULE: run narrowly —
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//	  -run '^TestBrowserWS_(ReadLoop|WorkQueue|AttachEpoch|HandleViewport_NonControlling)|^TestSharedMediaConn' \
//	  ./pkg/gateway/
//
// never the full gateway suite / ./... — see CLAUDE.md's "CI is the
// authority" rule.

package gateway

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// ---------------------------------------------------------------------------
// Fix A — the read loop must keep reading while a slow handler runs
// ---------------------------------------------------------------------------

// blockSlowHandler installs the browserConnWorkHook test seam so that the
// FIRST invocation for kind blocks until the returned release func is called.
// It returns a channel closed once the handler has actually been entered, so
// a test can wait for "the slow handler is now running" without sleeping.
//
// Subsequent invocations (and every other kind) pass straight through, so a
// test can still exercise later frames normally.
//
// MUST be called AFTER the connection is dialled. t.Cleanup runs LIFO, and
// srv.Close()/handler.Wait() both block until the server goroutine sitting in
// this hook is let go — so if the release cleanup were registered first it
// would run LAST and the test would deadlock instead of reporting, which is
// exactly what happened the first time this was written.
func blockSlowHandler(t *testing.T, kind browserConnWorkKind) (entered <-chan struct{}, release func()) {
	t.Helper()
	enteredCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var once sync.Once

	setBrowserConnWorkHook(func(got browserConnWorkKind) {
		if got != kind {
			return
		}
		first := false
		once.Do(func() { first = true })
		if !first {
			return
		}
		close(enteredCh)
		<-releaseCh
	})

	var releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(func() {
		release()
		setBrowserConnWorkHook(nil)
	})
	return enteredCh, release
}

// assertReadLoopStillReads writes an unknown-type frame and requires the
// server's "unknown frame type" rejection to come back promptly. That
// rejection is produced by readLoop's own default branch, so receiving it is
// direct proof that readLoop returned to ReadMessage rather than sitting
// inside a handler.
func assertReadLoopStillReads(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	probe := []byte(`{"type":"omnipus-readloop-liveness-probe"}`)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, probe))

	deadline := time.Now().Add(5 * time.Second)
	for {
		require.NoError(t, conn.SetReadDeadline(deadline))
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err,
			"the read loop must still be reading while the slow handler runs — "+
				"if this times out, the handler is back inline on readLoop and every Pong is starved")
		var f struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(raw, &f))
		if f.Type == "error" {
			assert.Contains(t, f.Message, "omnipus-readloop-liveness-probe",
				"the rejection must be for OUR probe frame")
			return
		}
		// Some other server frame (e.g. a browser_status) arrived first;
		// keep reading until the probe's rejection shows up.
	}
}

// dialAuthedBrowserWS spins up the handler behind an httptest server, dials
// the browser socket and completes dev-mode auth.
func dialAuthedBrowserWS(t *testing.T) *websocket.Conn {
	t.Helper()
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)
	return conn
}

// TestBrowserWS_ReadLoop_StaysResponsiveWhileViewportHandlerRuns is the
// primary regression test for fix A on the viewport path.
//
// BDD: Given an authenticated browser socket,
// When a browser_viewport frame's handler is still running (simulated at the
// browserConnWorkHook seam, standing in for the MEASURED 6.95s SetViewport),
// Then the connection still reads and answers the NEXT client frame.
//
// If handleViewport is ever moved back inline onto readLoop, the probe frame
// is never read and this fails by timeout.
func TestBrowserWS_ReadLoop_StaysResponsiveWhileViewportHandlerRuns(t *testing.T) {
	conn := dialAuthedBrowserWS(t)
	entered, release := blockSlowHandler(t, workKindViewport)

	frame, err := json.Marshal(generated.BrowserViewportFrame{
		Type:   string(generated.WsFrameTypeBrowserViewport),
		Width:  1024,
		Height: 768,
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, frame))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handleViewport was never entered — the browser_viewport frame was not dispatched at all")
	}

	assertReadLoopStillReads(t, conn)
	release()
}

// TestBrowserWS_ReadLoop_StaysResponsiveWhileAttachHandlerRuns is the same
// regression test for fix A on the attach path, which is the worse of the two
// in practice: handleAttach -> Live().Attach -> Session() -> ensureStarted
// creates the browser context and the first tab, MEASURED at 1.0-2.2s warm
// and ~9.5s for a whole first open on a fresh profile — i.e. a cold panel
// open used to make the connection deaf for most of its 60s read deadline
// before it had ever shown a frame.
//
// BDD: Given an authenticated browser socket,
// When a browser_attach frame's handler is still starting the browser,
// Then the connection still reads and answers the NEXT client frame.
func TestBrowserWS_ReadLoop_StaysResponsiveWhileAttachHandlerRuns(t *testing.T) {
	conn := dialAuthedBrowserWS(t)
	entered, release := blockSlowHandler(t, workKindAttach)

	frame, err := json.Marshal(generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   "fixwaveb-no-such-agent",
		SessionId: "fixwaveb-session",
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, frame))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handleAttach was never entered — the browser_attach frame was not dispatched at all")
	}

	assertReadLoopStillReads(t, conn)
	release()
}

// ---------------------------------------------------------------------------
// Fix A — the work queue's own three guarantees
// ---------------------------------------------------------------------------

// workQueueRecorder builds jobs that record their label, prove no two ever
// overlap, and optionally signal when they have STARTED and block until
// released. The started signal is what makes these tests deterministic:
// submit() returns as soon as the job is QUEUED, which is strictly earlier
// than the worker dequeuing it, so a test that submits and immediately
// submits again would otherwise race the worker.
type workQueueRecorder struct {
	mu       sync.Mutex
	order    []string
	inFlight int
	maxSeen  int
}

func (r *workQueueRecorder) job(label string, started chan<- struct{}, block <-chan struct{}) func() {
	return func() {
		r.mu.Lock()
		r.inFlight++
		if r.inFlight > r.maxSeen {
			r.maxSeen = r.inFlight
		}
		r.order = append(r.order, label)
		r.mu.Unlock()
		if started != nil {
			close(started)
		}
		if block != nil {
			<-block
		}
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}
}

func (r *workQueueRecorder) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...), r.maxSeen
}

// TestBrowserWS_WorkQueue_SerializesOrdersAndCoalesces pins the three
// properties browserConnWorkQueue's doc comment promises, which together are
// the "single-slot-per-connection discipline so two frames of the same kind
// cannot interleave" this fix was required to preserve:
//
//  1. submit never blocks its caller (that is what keeps readLoop reading);
//  2. jobs run one at a time, in arrival order, ACROSS kinds (so the SPA's
//     attach-then-viewport sequence still applies in that order);
//  3. a newer job of the same kind REPLACES an older queued one, keeping its
//     position (so a resize drag costs one CDP resize, not one per frame).
func TestBrowserWS_WorkQueue_SerializesOrdersAndCoalesces(t *testing.T) {
	var (
		q   browserConnWorkQueue
		wg  sync.WaitGroup
		rec workQueueRecorder
	)

	started := make(chan struct{})
	gate := make(chan struct{})
	q.submit(&wg, workKindAttach, rec.job("attach-1", started, gate))
	<-started // attach-1 is now genuinely RUNNING, not merely queued

	// Everything below is submitted while the worker is busy — the exact
	// situation readLoop is in during a real resize.
	submitStart := time.Now()
	q.submit(&wg, workKindViewport, rec.job("viewport-superseded-a", nil, nil))
	q.submit(&wg, workKindViewport, rec.job("viewport-superseded-b", nil, nil))
	q.submit(&wg, workKindViewport, rec.job("viewport-final", nil, nil))
	q.submit(&wg, workKindAttach, rec.job("attach-2", nil, nil))
	submitElapsed := time.Since(submitStart)

	require.Less(t, submitElapsed, 2*time.Second,
		"submit must never block its caller even while a job is running — a blocking submit is the very "+
			"read-loop starvation this fix removes")

	close(gate)
	wg.Wait()

	order, maxSeen := rec.snapshot()
	require.Equal(t, 1, maxSeen, "jobs must never overlap — exactly one runs at a time")
	require.Equal(t, []string{"attach-1", "viewport-final", "attach-2"}, order,
		"superseded same-kind jobs must be dropped, the survivor must keep its queue position, "+
			"and cross-kind arrival order must be preserved")
}

// TestBrowserWS_WorkQueue_SupersedesAJobNotYetStarted pins the coalescing
// window explicitly: a job is supersedable from the moment it is queued until
// the worker actually picks it up, NOT only once something else is running.
// That is what makes a burst of browser_viewport frames arriving faster than
// the worker drains them cost one resize rather than one per frame — and it
// is safe for browser_attach too, because the newest attach is the only one
// whose commit the attachEpoch would accept anyway.
func TestBrowserWS_WorkQueue_SupersedesAJobNotYetStarted(t *testing.T) {
	var (
		q   browserConnWorkQueue
		wg  sync.WaitGroup
		rec workQueueRecorder
	)

	// Submitted back to back with no started-signal in between, so the second
	// submit races (and must win against) a first job the worker may not have
	// dequeued yet.
	q.submit(&wg, workKindViewport, rec.job("viewport-first", nil, nil))
	q.submit(&wg, workKindViewport, rec.job("viewport-second", nil, nil))
	wg.Wait()

	order, _ := rec.snapshot()
	require.NotContains(t, order, "viewport-first",
		"a viewport frame the worker had not started yet must be superseded by the newer one, "+
			"never run in addition to it")
	require.Equal(t, []string{"viewport-second"}, order)
}

// TestBrowserWS_WorkQueue_CloseDropsQueuedJobs pins the close() contract
// readLoop's cleanup defer depends on: a frame that arrived moments before
// the socket died must be dropped, not acted on against a dead connection,
// while a job already running is allowed to finish (the attachEpoch, not a
// blocking wait, is what makes ITS late result safe).
func TestBrowserWS_WorkQueue_CloseDropsQueuedJobs(t *testing.T) {
	var (
		q   browserConnWorkQueue
		wg  sync.WaitGroup
		rec workQueueRecorder
	)

	started := make(chan struct{})
	gate := make(chan struct{})
	q.submit(&wg, workKindAttach, rec.job("running", started, gate))
	<-started // the worker is genuinely inside this job now

	q.submit(&wg, workKindViewport, rec.job("queued-must-not-run", nil, nil))
	q.close()
	q.submit(&wg, workKindViewport, rec.job("post-close-must-not-run", nil, nil))
	close(gate)
	wg.Wait()

	order, _ := rec.snapshot()
	require.Equal(t, []string{"running"}, order,
		"close must drop queued work and refuse new submissions; only the already-started job may finish")
}

// ---------------------------------------------------------------------------
// Fix A — the attach epoch, which is what makes async attach SAFE
// ---------------------------------------------------------------------------

// TestBrowserWS_AttachEpoch_SupersededCommitIsRefused covers the three ways a
// dispatched attach can be superseded while Live().Attach is still starting
// the browser. Without this discipline, moving handleAttach off the read loop
// would leave a viewer (and possibly a control lock) registered on a
// connection that had already detached or closed.
func TestBrowserWS_AttachEpoch_SupersededCommitIsRefused(t *testing.T) {
	t.Run("uncontested commit succeeds", func(t *testing.T) {
		var state browserConnState
		epoch := state.beginAttach()
		require.True(t, state.bindAttachment(epoch, &browser.BrowserManager{}, "sess", "key/operator"),
			"an attach nothing superseded must commit")
		mgr, sessionID, panelSessionID := state.attachment()
		require.NotNil(t, mgr)
		require.Equal(t, "sess", sessionID)
		require.Equal(t, "key/operator", panelSessionID,
			"the resolved panel tab set is pinned alongside the chat session id (#671)")
	})

	t.Run("a newer attach supersedes an older in-flight one", func(t *testing.T) {
		var state browserConnState
		older := state.beginAttach()
		_ = state.beginAttach() // a second browser_attach frame arrived
		require.False(t, state.bindAttachment(older, &browser.BrowserManager{}, "stale", "key/stale"),
			"the older attach must not commit once a newer one has been dispatched")
		mgr, sessionID, panelSessionID := state.attachment()
		require.Nil(t, mgr)
		require.Empty(t, sessionID)
		require.Empty(t, panelSessionID,
			"a refused commit must not leave a resolved tab set behind either")
	})

	t.Run("invalidateAttach supersedes an in-flight attach", func(t *testing.T) {
		var state browserConnState
		epoch := state.beginAttach()
		state.invalidateAttach() // an explicit detach, or the connection closing
		require.False(t, state.bindAttachment(epoch, &browser.BrowserManager{}, "stale", "key/stale"),
			"a detach or close during Live().Attach must make the commit fail so the caller tears it down")
	})
}

// TestBrowserWS_HandleDetach_InvalidatesAnInFlightAttach is the end of that
// same story at the real call site: browser_detach must invalidate even when
// NOTHING is attached yet, because the attach it needs to cancel may still be
// inside Live().Attach on the worker with nothing committed.
//
// BDD: Given a browser_attach has been dispatched but has not yet committed,
// When the client sends browser_detach,
// Then the attach's later commit is refused (and handleAttach detaches what
// it built instead of leaving a dangling viewer).
func TestBrowserWS_HandleDetach_InvalidatesAnInFlightAttach(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc := &browserWSConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	var state browserConnState

	epoch := state.beginAttach() // dispatchAttach ran; handleAttach is "still negotiating"

	handler.handleDetach(wc, &state, "viewer-1", "user-1")

	require.False(t, state.bindAttachment(epoch, &browser.BrowserManager{}, "sess", "key/operator"),
		"browser_detach must invalidate an in-flight attach even though nothing was attached yet — "+
			"otherwise the attach commits after the user closed the panel and leaks a viewer")
}

// ---------------------------------------------------------------------------
// Fix B — a refused resize must say so
// ---------------------------------------------------------------------------

func marshalViewportFrame(t *testing.T, width, height int) []byte {
	t.Helper()
	data, err := json.Marshal(generated.BrowserViewportFrame{
		Type:   string(generated.WsFrameTypeBrowserViewport),
		Width:  width,
		Height: height,
	})
	require.NoError(t, err)
	return data
}

// TestBrowserWS_HandleViewport_NonControllingViewer_ExplainsRefusal is the
// regression test for fix B.
//
// BDD: Given viewer A holds control of the shared tab,
// When viewer B (attached, not controlling) sends browser_viewport,
// Then viewer B receives a browser_status(error) explaining that another
// viewer is driving, that their input still works, and what will change when
// control is released — instead of the previous silence, which left them
// watching a mis-shaped picture with no explanation anywhere in the product.
func TestBrowserWS_HandleViewport_NonControllingViewer_ExplainsRefusal(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	mgr, _, panelSessionID := state.attachment()
	require.True(t, mgr.Live().TakeControl(panelSessionID, "viewer-A"),
		"viewer A must be able to take control of a fresh session")

	handler.handleViewport(wc, state, "viewer-B", marshalViewportFrame(t, 900, 1010))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "another viewer is driving",
		"the refused viewer must be told WHY their panel is the wrong shape")
	assert.Contains(t, resp.Message, "clicks and typing still work",
		"the message must reflect the deliberate policy split — input is NOT control-gated, resize is")
	require.LessOrEqual(t, len(resp.Message), 512,
		"BrowserStatusFrame.message has a 512-char contract maximum")
}

// TestBrowserWS_HandleViewport_RefusalIsThrottled pins the flood guard: a
// resize DRAG emits a frame per debounce interval and every one is refused
// identically, so only the first may be sent inside the cooldown — the same
// content-aware cooldown handleInput uses for repeated input errors.
func TestBrowserWS_HandleViewport_RefusalIsThrottled(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	mgr, _, panelSessionID := state.attachment()
	require.True(t, mgr.Live().TakeControl(panelSessionID, "viewer-A"))

	handler.handleViewport(wc, state, "viewer-B", marshalViewportFrame(t, 900, 1010))
	handler.handleViewport(wc, state, "viewer-B", marshalViewportFrame(t, 901, 1011))
	handler.handleViewport(wc, state, "viewer-B", marshalViewportFrame(t, 902, 1012))

	_ = readWCFrame(t, wc, 2*time.Second) // the first refusal
	select {
	case extra := <-wc.sendCh:
		t.Fatalf("an identical refusal inside the cooldown must be suppressed, got a second frame: %s", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestBrowserWS_HandleViewport_ControllingViewer_NotRefused guards the other
// direction: fix B must not start refusing the viewer who legitimately holds
// control, nor a lone viewer on an uncontrolled session.
func TestBrowserWS_HandleViewport_ControllingViewer_NotRefused(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	mgr, _, panelSessionID := state.attachment()
	require.True(t, mgr.Live().TakeControl(panelSessionID, "viewer-A"))

	// No live tab is bound on this never-started manager, so SetViewport
	// returns (false, nil) and handleViewport takes its documented "no live
	// view bound yet" branch — which sends nothing. The point of the test is
	// that the CONTROL GATE does not fire for the controller.
	handler.handleViewport(wc, state, "viewer-A", marshalViewportFrame(t, 900, 1010))

	select {
	case frame := <-wc.sendCh:
		t.Fatalf("the controlling viewer's own resize must not be refused, got: %s", frame)
	case <-time.After(200 * time.Millisecond):
	}
}

// ---------------------------------------------------------------------------
// Fix C — a taken media UDP port must fall back, loudly, not silently vanish
// ---------------------------------------------------------------------------

// occupyUDPPort binds a wildcard UDP socket and returns its port, so the port
// is genuinely unavailable to a second wildcard bind on every platform (a
// loopback-only blocker would not reliably collide with ":port" on Windows).
func occupyUDPPort(t *testing.T) int {
	t.Helper()
	blocker, err := net.ListenPacket("udp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = blocker.Close() })

	_, portStr, err := net.SplitHostPort(blocker.LocalAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port
}

func mediaConnPort(t *testing.T, conn net.PacketConn) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(conn.LocalAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port
}

// TestSharedMediaConn_ConfiguredPortTaken_FallsBackToNextFreePort is the
// regression test for fix C.
//
// BDD: Given tools.browser.webrtc_media_udp_port names a port already bound
// by another process (measured: a second Omnipus on the same host, `listen
// udp :50000: bind: address already in use`),
// When the gateway binds its shared media socket,
// Then it binds the next free port instead of returning nil and silently
// dropping every Session back to ephemeral ports.
func TestSharedMediaConn_ConfiguredPortTaken_FallsBackToNextFreePort(t *testing.T) {
	taken := occupyUDPPort(t)

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaUDPPort = taken

	h := &BrowserWSHandler{}
	conn := h.sharedMediaConn(cfg)
	require.NotNil(t, conn,
		"a taken configured port must NOT collapse to nil — nil drops every Session to ephemeral ports, "+
			"which is exactly the silent live-video failure this fixes")
	t.Cleanup(func() { _ = conn.Close() })

	got := mediaConnPort(t, conn)
	assert.NotEqual(t, taken, got, "the taken port cannot have been bound")
	assert.Greater(t, got, taken, "the fallback must walk UPWARD from the configured port")
	assert.LessOrEqual(t, got, taken+mediaPortFallbackSpan,
		"the fallback must stay inside the documented probe span")

	// The socket is memoised for the process's lifetime.
	assert.Same(t, conn, h.sharedMediaConn(cfg), "the shared media socket must be bound once and reused")
}

// TestSharedMediaConn_ConfiguredPortFree_BindsExactlyThatPort guards the other
// direction: the explicitly configured port stays the FIRST choice, because it
// is the only port the operator has declared to their provider/firewall.
func TestSharedMediaConn_ConfiguredPortFree_BindsExactlyThatPort(t *testing.T) {
	// Take a port, then release it — a cheap way to name a port that is
	// almost certainly free right now.
	probe, err := net.ListenPacket("udp", ":0")
	require.NoError(t, err)
	free := mediaConnPort(t, probe)
	require.NoError(t, probe.Close())

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaUDPPort = free

	h := &BrowserWSHandler{}
	conn := h.sharedMediaConn(cfg)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close() })

	assert.Equal(t, free, mediaConnPort(t, conn),
		"an available configured port must be bound exactly — never quietly moved")
}

// TestSharedMediaConn_Unconfigured_ReturnsNil pins the pre-ADR-062 default:
// port 0 means "ephemeral, as before", and must not trigger any probing.
func TestSharedMediaConn_Unconfigured_ReturnsNil(t *testing.T) {
	cfg := &config.Config{}
	h := &BrowserWSHandler{}
	require.Nil(t, h.sharedMediaConn(cfg),
		"fixed-port media is opt-in; 0 must stay the untouched ephemeral-port default")
}
