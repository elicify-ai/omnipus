package browser

// capture_session.go implements the ADR-047 / wave-plan W2-A per-agent WebRTC
// capture session: it owns the gateway-owned encoder page's lifecycle (the
// capture extension's chrome-extension://<id>/encoder.html target, created
// inside the CAPTURING AGENT'S OWN browser context/window so
// encoder.js's chrome.tabs.query({lastFocusedWindow:true}) resolves to that
// agent's own tab — see coordinator.go's LoadExtension doc comment) plus the
// Pion SFU relay Session that backs it. One CaptureSession exists per agent
// (BrowserManager.capture), created lazily on the first WebRTC-capable
// viewer offer and torn down on last-viewer-detach (after a grace period) or
// on browser death (live.go's watchForUnexpectedDeath) or manager Shutdown.
//
// Package boundary discipline (mirrors pkg/tools/browser/webrtc's own rule):
// this file does NOT import pkg/api/generated or pkg/audit. The gateway
// (pkg/gateway/browser_webrtc.go) is the only place that constructs wire
// frames and emits audit entries; this file exposes plain-typed methods
// (strings/bytes/callbacks) for the gateway to drive.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// captureTokenBytes is the size of the per-stream capability token minted
// for each CaptureSession (wave-plan W2-A item 2: "crypto/rand 256-bit
// token").
const captureTokenBytes = 32

// captureStartTimeout bounds the encoder-page inject+navigate CDP round trip
// (Start's chromedp.Run call) — generous for a cold managed-Chrome extension
// load, but bounded so a wedged CDP transport can't hang a viewer's offer
// forever.
const captureStartTimeout = 20 * time.Second

// captureGracePeriod is how long a CaptureSession stays alive with zero
// attached WebRTC viewers before RemoveViewer's grace timer stops it
// (wave-plan W2-A item 4: "stopped on last detach with a ~30s grace timer").
// A var (not const) so capture_session_test.go can shrink it for a fast
// deterministic grace-stop test without a real 30s sleep.
var captureGracePeriod = 30 * time.Second

// RelaySession is the subset of *webrtc.Session's API CaptureSession
// depends on. Exported (and named independently of the concrete type) so
// other packages — chiefly pkg/gateway's WebRTC signaling handler tests —
// can supply a fake relay without depending on real Pion/ICE machinery, per
// wave-plan W2-A's "fake webrtc.Session behind a narrow interface you define
// at the consumption point." Both webrtc.Session build variants (the real
// Pion-backed session.go and the lite stub.go) satisfy this structurally.
type RelaySession interface {
	HandleIngestOffer(sdpOffer string) (answer string, err error)
	HandleViewerOffer(viewerID string, sdpOffer string) (answer string, err error)
	CloseViewer(viewerID string)
	SignalRecapture()
	SendToViewer(viewerID string, msg []byte) error
	Stats() webrtc.Stats
	Close() error
}

// EncoderStarter creates (or re-creates) the gateway-owned encoder page for
// one capture session, injecting {token, ingestUrl} via
// Page.addScriptToEvaluateOnNewDocument BEFORE navigating to
// chrome-extension://<id>/encoder.html, and returns the resulting tab's
// chromedp context + its cancel func (canceling it closes just that one CDP
// target). Exported purely as a test-injection seam (mirrors this package's
// existing createTabFn/pipeLauncher/listTargets testability pattern) —
// production code always uses defaultEncoderStarter.
type EncoderStarter func(ctx context.Context, mgr *BrowserManager, tokenHex, ingestURL string) (tabCtx context.Context, tabCancel context.CancelFunc, err error)

// CaptureSession owns one agent's WebRTC capture stream end to end: the
// encoder-page CDP target, the minted ingest capability token, the Pion SFU
// relay Session, and the loopback capture-ingest connection's send/close
// callbacks (bound once browser_capture_hello authenticates — see
// BindIngest). See the file doc comment above for the package-boundary
// rule this type observes.
type CaptureSession struct {
	agentID      string
	mgr          *BrowserManager
	logf         func(string, ...any)
	relay        RelaySession
	startEncoder EncoderStarter
	token        []byte // captureTokenBytes random bytes, minted once in NewCaptureSession*

	mu sync.Mutex
	// startOnce/startErr collapse concurrent Start() callers into exactly one
	// startEncoder invocation — see Start's doc comment.
	startOnce   sync.Once
	startErr    error
	extVersion  string
	lastPingAt  time.Time
	tabCtx      context.Context
	tabCancel   context.CancelFunc
	started     bool
	stopped     bool
	ingestSend  func(action string, reason *string) error
	ingestClose func()
	// ingestEpoch increments on every BindIngest call — UnbindIngest only
	// clears ingestSend/ingestClose if the epoch it was handed still matches
	// the current one, guarding against a stale (superseded/reconnected)
	// ingest connection's close path clobbering a NEWER connection's
	// callbacks. A counter rather than comparing the callback funcs
	// themselves (func values are not comparable in Go beyond nil-checks).
	ingestEpoch uint64
	viewers     map[string]struct{}
	stopTimer   *time.Timer
	onStopped   func() // invoked exactly once when Stop() completes (gateway hook for registry cleanup)
}

// NewCaptureSession constructs a production CaptureSession: mgr is the
// agent's BrowserManager (its own browser context is where the encoder page
// will be created — see defaultEncoderStarter, which loads the extension via
// mgr.Coordinator()'s own BrowserConfig.ExtensionDir, set once at gateway
// boot), cfg is the Pion relay's ICE config (wave-plan item 7:
// Tools.Browser.WebRTCStunServer), sink receives every "input" data-channel
// message from every viewer (the gateway builds this — see
// browser_webrtc.go's webrtcInputSink — so this package never needs
// pkg/api/generated), and logf is a structured log sink (nil-safe).
func NewCaptureSession(mgr *BrowserManager, agentID string, cfg webrtc.Config, sink webrtc.InputSink, logf func(string, ...any)) (*CaptureSession, error) {
	token := make([]byte, captureTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("capture session: mint token: %w", err)
	}
	relay := webrtc.NewSession(cfg, sink, logf)
	return newCaptureSessionWithDeps(mgr, agentID, relay, defaultEncoderStarter, token, logf), nil
}

// NewCaptureSessionWithDeps is the fully-injectable constructor: relay
// satisfies RelaySession (a fake in tests, *webrtc.Session in production —
// see NewCaptureSession) and startEncoder satisfies EncoderStarter (a fake
// in tests that never touches real chromedp, defaultEncoderStarter in
// production). Exported for other packages' tests (pkg/gateway's WebRTC
// signaling handler tests construct a real *CaptureSession with these two
// seams faked, exercising the actual CaptureSession lifecycle logic against
// fake relay/encoder machinery — wave-plan W2-A's testing note).
func NewCaptureSessionWithDeps(mgr *BrowserManager, agentID string, relay RelaySession, startEncoder EncoderStarter, logf func(string, ...any)) (*CaptureSession, error) {
	token := make([]byte, captureTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("capture session: mint token: %w", err)
	}
	return newCaptureSessionWithDeps(mgr, agentID, relay, startEncoder, token, logf), nil
}

func newCaptureSessionWithDeps(mgr *BrowserManager, agentID string, relay RelaySession, startEncoder EncoderStarter, token []byte, logf func(string, ...any)) *CaptureSession {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &CaptureSession{
		agentID:      agentID,
		mgr:          mgr,
		logf:         logf,
		relay:        relay,
		startEncoder: startEncoder,
		token:        token,
		viewers:      make(map[string]struct{}),
	}
}

// captureInjectPayload is the exact shape encoder.js's readConfig() expects
// at window.__omnipusCapture — {token, ingestUrl} (see
// pkg/tools/browser/captureext/embedded/encoder.js's readConfig doc
// comment). Not a cross-gateway-boundary wire type in the Constraint #8
// sense (it never round-trips through pkg/gateway's REST/WS surface — it is
// injected directly into a CDP-driven page via
// Page.addScriptToEvaluateOnNewDocument), so a package-local struct here is
// correct, not a lint violation.
type captureInjectPayload struct {
	Token     string `json:"token"`
	IngestURL string `json:"ingestUrl"`
}

// defaultEncoderStarter is the production EncoderStarter: it ensures the
// agent's default browsing session exists (so its browserCtx is available),
// ensures the capture extension is loaded into the shared Chrome, creates an
// UNTRACKED sibling CDP target in the SAME browser context/window as the
// agent's own tabs (via mgr.createTab against the session's browserCtx —
// deliberately NOT mgr.OpenTab, which would register the target in the
// agent's visible tab strip/MaxTabs budget; the encoder page is a
// gateway-internal target the agent/user never see), injects
// window.__omnipusCapture BEFORE navigating (Page.
// addScriptToEvaluateOnNewDocument runs before any of the target document's
// own scripts, per its CDP doc comment), then navigates to
// chrome-extension://<captureext.ExtensionID>/encoder.html.
func defaultEncoderStarter(ctx context.Context, mgr *BrowserManager, tokenHex, ingestURL string) (context.Context, context.CancelFunc, error) {
	if mgr == nil {
		return nil, nil, fmt.Errorf("capture session: no browser manager")
	}

	// 1. Ensure the agent's default tab/browsing context exists — the
	// encoder page must share ITS window (see coordinator.go's
	// LoadExtension / this file's top-of-file doc comment for why).
	if _, err := mgr.Session(DefaultSessionID); err != nil {
		return nil, nil, fmt.Errorf("capture session: ensure agent browsing context: %w", err)
	}

	// 2. Ensure the capture extension is loaded into the shared Chrome.
	coord := mgr.Coordinator()
	if coord == nil {
		return nil, nil, fmt.Errorf("capture session: no shared-Chrome coordinator attached (WebRTC capture requires the shared-Chrome coordinator)")
	}
	if coord.LoadedExtensionID() != captureext.ExtensionID {
		if _, err := coord.LoadExtension(ctx); err != nil {
			return nil, nil, fmt.Errorf("capture session: load capture extension: %w", err)
		}
	}

	// 3. Get the agent's browserCtx as the parent for a new sibling target
	// (mirrors OpenTab's own "append an additional tab as a child of the
	// session's own browserCtx" pattern — manager.go's OpenTab doc comment
	// — but WITHOUT going through OpenTab, since that registers the target
	// in m.sessions[sessionID].tabs, the agent-visible tab set/MaxTabs
	// budget this encoder target must stay outside of).
	browserCtx := mgr.defaultSessionBrowserCtx()
	if browserCtx == nil {
		return nil, nil, fmt.Errorf("capture session: agent has no active browsing context to attach the encoder page to")
	}

	tab, err := mgr.createTab(browserCtx, "")
	if err != nil {
		return nil, nil, fmt.Errorf("capture session: create encoder target: %w", err)
	}

	payload, err := json.Marshal(captureInjectPayload{Token: tokenHex, IngestURL: ingestURL})
	if err != nil {
		tab.cancel()
		return nil, nil, fmt.Errorf("capture session: marshal inject payload: %w", err)
	}
	injectScript := "window.__omnipusCapture = " + string(payload) + ";"
	encoderURL := "chrome-extension://" + captureext.ExtensionID + "/encoder.html"

	runCtx, cancel := context.WithTimeout(tab.ctx, captureStartTimeout)
	runErr := chromedp.Run(runCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(injectScript).Do(ctx)
			return err
		}),
		chromedp.Navigate(encoderURL),
	)
	cancel()
	if runErr != nil {
		tab.cancel()
		return nil, nil, fmt.Errorf("capture session: inject config + navigate encoder page: %w", runErr)
	}

	return tab.ctx, tab.cancel, nil
}

// Start idempotently begins this capture session's encoder-page lifecycle.
// Concurrent Start calls (two viewer offers racing to be "first" — a real
// possibility, since HandleWebRTCOffer runs per-connection) are collapsed by
// startOnce into exactly one startEncoder invocation; every caller observes
// the SAME outcome. Returns justStarted=true only to the ONE caller whose
// call actually ran the startup (so the gateway can audit "stream started"
// exactly once), and an error if construction failed OR the session was
// already stopped (a stopped session must not be reused — callers create a
// fresh CaptureSession via BrowserManager.ClearCaptureSession + EnsureCaptureSession).
// ingestURL is the loopback capture-ingest WS URL
// (ws://127.0.0.1:<gateway port>/api/v1/browser/capture-ingest) the encoder
// page will connect to.
func (cs *CaptureSession) Start(ctx context.Context, ingestURL string) (justStarted bool, err error) {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return false, fmt.Errorf("capture session: already stopped")
	}
	cs.mu.Unlock()

	ranNow := false
	cs.startOnce.Do(func() {
		ranNow = true
		tabCtx, tabCancel, startErr := cs.startEncoder(ctx, cs.mgr, hex.EncodeToString(cs.token), ingestURL)
		if startErr != nil {
			cs.mu.Lock()
			cs.startErr = startErr
			cs.mu.Unlock()
			return
		}
		cs.mu.Lock()
		if cs.stopped {
			// A Stop() (e.g. browser death detected concurrently) raced this
			// Start() and won — tear down what we just built rather than
			// leaving an orphaned encoder target nobody will ever close.
			cs.startErr = fmt.Errorf("capture session: stopped while starting")
			cs.mu.Unlock()
			tabCancel()
			return
		}
		cs.tabCtx = tabCtx
		cs.tabCancel = tabCancel
		cs.started = true
		cs.mu.Unlock()
		cs.logf("capture[%s]: started (encoder page navigating)", cs.agentID)
	})

	cs.mu.Lock()
	startErr := cs.startErr
	cs.mu.Unlock()
	return ranNow, startErr
}

// Relay returns this session's RelaySession (the Pion-backed webrtc.Session
// in production), for the gateway to call HandleViewerOffer directly. Never
// nil for a CaptureSession constructed via NewCaptureSession(WithDeps).
func (cs *CaptureSession) Relay() RelaySession {
	return cs.relay
}

// AgentID returns the agent this capture session belongs to.
func (cs *CaptureSession) AgentID() string {
	return cs.agentID
}

// ValidateToken reports whether candidateHex (the token field of an inbound
// browser_capture_hello frame) matches this session's minted token, using a
// constant-time comparison (wave-plan W2-A item 3: "constant-time compare").
// A malformed (non-hex, wrong-length) candidate is rejected without ever
// reaching the constant-time compare (hex.DecodeString on attacker input is
// not a secret-dependent operation, so no timing-safety is lost by
// short-circuiting here).
func (cs *CaptureSession) ValidateToken(candidateHex string) bool {
	candidate, err := hex.DecodeString(candidateHex)
	if err != nil || len(candidate) != len(cs.token) {
		return false
	}
	return subtle.ConstantTimeCompare(candidate, cs.token) == 1
}

// TokenHex returns this session's minted token, hex-encoded — the same
// encoding the encoder page's injected window.__omnipusCapture.token uses
// and a browser_capture_hello frame's token field carries. Exported for
// tests (this package's own and pkg/gateway's WebRTC signaling handler
// tests) that need to construct a valid hello frame against a real
// CaptureSession without reaching into an unexported field cross-package.
func (cs *CaptureSession) TokenHex() string {
	return hex.EncodeToString(cs.token)
}

// BindIngest registers the send/close callbacks for the currently-
// authenticated capture-ingest connection (called by the gateway once a
// browser_capture_hello's token validates), returning the PREVIOUS
// send/close pair's close callback (or nil if none) so the caller can
// invoke it — implementing "a second hello with the same token
// supersedes/closes the old conn" (wave-plan W2-A item 3) — and this bind's
// epoch, which the caller must pass back to UnbindIngest so a stale
// (already-superseded) connection's teardown can never clobber a NEWER
// connection's callbacks. send pushes a browser_capture_control frame's
// {action, reason} to the encoder over the raw ingest WS (NOT a WebRTC data
// channel — recapture/shutdown are signaled on the signaling connection
// itself, independent of whether media has even connected yet).
func (cs *CaptureSession) BindIngest(send func(action string, reason *string) error, closeConn func()) (previousClose func(), epoch uint64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	previousClose = cs.ingestClose
	cs.ingestEpoch++
	cs.ingestSend = send
	cs.ingestClose = closeConn
	cs.lastPingAt = time.Now()
	return previousClose, cs.ingestEpoch
}

// UnbindIngest clears the ingest send/close callbacks IFF epoch still
// matches the current binding (guards a stale unbind racing a newer
// BindIngest — same "only touch it if it's still mine" discipline as
// BrowserManager.ClearCaptureSession). Called when the ingest WS connection
// itself closes (encoder disconnect/crash/reconnect), independent of
// whether the capture session as a whole is stopping.
func (cs *CaptureSession) UnbindIngest(epoch uint64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.ingestEpoch == epoch {
		cs.ingestSend = nil
		cs.ingestClose = nil
	}
}

// RecordExtVersion records the ext_version reported on a validated
// browser_capture_hello, for logging/diagnostics.
func (cs *CaptureSession) RecordExtVersion(v string) {
	cs.mu.Lock()
	cs.extVersion = v
	cs.mu.Unlock()
}

// RecordPing updates the liveness timestamp on a browser_capture_control
// {action:ping} frame (the encoder's own health beacon, per
// encoder.js/startPingBeacon).
func (cs *CaptureSession) RecordPing() {
	cs.mu.Lock()
	cs.lastPingAt = time.Now()
	cs.mu.Unlock()
}

// HandleIngestOffer delegates to the relay's HandleIngestOffer — the
// encoder page's SDP offer, arriving as a browser_capture_offer frame on the
// ingest WS.
func (cs *CaptureSession) HandleIngestOffer(sdp string) (answer string, err error) {
	return cs.relay.HandleIngestOffer(sdp)
}

// HandleViewerOffer delegates to the relay's HandleViewerOffer — a viewer's
// SDP offer, arriving as a browser_webrtc_offer frame on the main browser
// WS.
func (cs *CaptureSession) HandleViewerOffer(viewerID, sdp string) (answer string, err error) {
	return cs.relay.HandleViewerOffer(viewerID, sdp)
}

// Recapture signals both halves of the recapture path (wave-plan W2-A item
// 5): a browser_capture_control{recapture} frame is pushed to the encoder
// over the ingest WS (if currently bound) so it re-binds
// chrome.tabCapture to the newly-active tab, AND the relay's
// SignalRecapture() primes attached viewers for the resulting brief gap
// with an immediate + bursted PLI so playback recovers as fast as possible.
// Safe to call with no ingest connection bound (a no-op send) or no relay
// tracks yet (SignalRecapture is itself a no-op then, per its doc comment).
func (cs *CaptureSession) Recapture() {
	cs.requestControl("recapture", nil)
	cs.relay.SignalRecapture()
}

// requestControl pushes a browser_capture_control{action, reason} frame to
// the bound ingest connection, if any. Errors are logged, not returned —
// every call site (Recapture, Stop) is best-effort: the encoder's own
// reconnect watchdog (encoder.js) and this session's own Stop() teardown are
// what actually guarantee termination, not a successfully-delivered control
// frame.
func (cs *CaptureSession) requestControl(action string, reason *string) {
	cs.mu.Lock()
	send := cs.ingestSend
	cs.mu.Unlock()
	if send == nil {
		return
	}
	if err := send(action, reason); err != nil {
		cs.logf("capture[%s]: send control %s failed: %v", cs.agentID, action, err)
	}
}

// AddViewer registers viewerID as an attached WebRTC viewer, canceling any
// pending grace-stop timer (wave-plan W2-A item 4). Idempotent for an
// already-registered viewerID.
func (cs *CaptureSession) AddViewer(viewerID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.viewers[viewerID] = struct{}{}
	if cs.stopTimer != nil {
		cs.stopTimer.Stop()
		cs.stopTimer = nil
	}
}

// RemoveViewer unregisters viewerID. If no viewers remain, arms a
// captureGracePeriod timer that calls Stop() when it fires (wave-plan W2-A
// item 4: "stopped on last detach with a ~30s grace timer") — giving a
// viewer that's merely reloading the panel a window to reconnect without
// tearing down and re-provisioning the whole encoder page. A subsequent
// AddViewer within the grace window cancels the timer.
func (cs *CaptureSession) RemoveViewer(viewerID string) {
	cs.mu.Lock()
	delete(cs.viewers, viewerID)
	empty := len(cs.viewers) == 0
	if empty && cs.stopTimer == nil && !cs.stopped {
		cs.stopTimer = time.AfterFunc(captureGracePeriod, cs.Stop)
	}
	cs.mu.Unlock()
}

// ViewerCount returns the current number of attached WebRTC viewers.
func (cs *CaptureSession) ViewerCount() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.viewers)
}

// SetOnStopped registers a callback invoked exactly once when Stop()
// completes — the gateway uses this to remove the session from its
// token-lookup registry (see browser_webrtc.go's captureRegistry) without
// this package needing to know that registry exists.
func (cs *CaptureSession) SetOnStopped(fn func()) {
	cs.mu.Lock()
	cs.onStopped = fn
	cs.mu.Unlock()
}

// Stop tears the capture session down: sends browser_capture_control
// {shutdown} to the encoder (best-effort), closes every relay viewer
// connection, cancels the encoder page's CDP target, closes the relay
// Session, and clears this manager's CaptureSession reference. Idempotent —
// safe to call multiple times (grace timer firing concurrently with an
// explicit Stop from browser-death detection) and safe to use directly as a
// time.AfterFunc callback (see RemoveViewer).
func (cs *CaptureSession) Stop() {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return
	}
	cs.stopped = true
	if cs.stopTimer != nil {
		cs.stopTimer.Stop()
		cs.stopTimer = nil
	}
	tabCancel := cs.tabCancel
	viewerIDs := make([]string, 0, len(cs.viewers))
	for id := range cs.viewers {
		viewerIDs = append(viewerIDs, id)
	}
	onStopped := cs.onStopped
	cs.mu.Unlock()

	shutdownReason := "capture session stopping"
	cs.requestControl("shutdown", &shutdownReason)

	for _, id := range viewerIDs {
		cs.relay.CloseViewer(id)
	}
	if err := cs.relay.Close(); err != nil {
		cs.logf("capture[%s]: relay close: %v", cs.agentID, err)
	}
	if tabCancel != nil {
		tabCancel()
	}
	if cs.mgr != nil {
		cs.mgr.ClearCaptureSession(cs)
	}
	cs.logf("capture[%s]: stopped", cs.agentID)
	if onStopped != nil {
		onStopped()
	}
}

// Stats returns the relay's current point-in-time stats (viewer count,
// codec/track presence) — used to populate has_audio on browser_webrtc_state.
func (cs *CaptureSession) Stats() webrtc.Stats {
	return cs.relay.Stats()
}
