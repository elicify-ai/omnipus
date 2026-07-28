package browser

// capture_session.go implements the ADR-047 / wave-plan W2-A per-agent WebRTC
// capture session: it owns the gateway-owned encoder page's lifecycle (the
// capture extension's chrome-extension://<id>/encoder.html target, created in
// the DEFAULT browser context — Chrome refuses extension pages inside
// CDP-created contexts, see defaultEncoderStarter — resolving the agent's tab
// via encoder.js's chrome.tabs.query. Fix-wave comment correction: an
// earlier version of this paragraph attributed that resolution to the
// extension being loaded enableInIncognito, but ADR-048 established
// (real Chrome 150) that enableInIncognito only grants the extension
// VISIBILITY into per-agent CDP-created contexts' tabs, never CAPTURABILITY
// of them — see coordinator.go's LoadExtension doc comment. The actual
// mechanism that lets encoder.js resolve the RIGHT tab is
// tools.browser.capture_shared_context (ADR-048 condition 1): when enabled,
// the agent's OWN session is also bootstrapped into this SAME default
// context the encoder page lives in, so chrome.tabs.query and
// chrome.tabCapture both operate on a tab that is genuinely in-context, no
// cross-context visibility trick required) plus the Pion SFU relay Session that backs it. One CaptureSession exists per agent
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
// — NOT Start() itself (Start contains no chromedp.Run call of its own), but
// the EncoderStarter it invokes: defaultEncoderStarter derives its own
// runCtx from this constant around its chromedp.Run(injectScript, Navigate)
// call, below. Generous for a cold managed-Chrome extension load, but
// bounded so a wedged CDP transport can't hang a viewer's offer forever.
const captureStartTimeout = 20 * time.Second

// captureGracePeriod is how long a CaptureSession stays alive with zero
// attached WebRTC viewers before RemoveViewer's grace timer stops it
// (wave-plan W2-A item 4: "stopped on last detach with a ~30s grace timer").
// A var (not const) so capture_session_test.go can shrink it for a fast
// deterministic grace-stop test without a real 30s sleep.
//
// Bumped 30s -> 60s (2026-07-28 incident, live DEBUG-level evidence on
// uat-omnipus, timeline corrected 2026-07-28 per frontend-lead review of
// src/lib/browserWebRTC.ts): a failed offer's own failure signal — a
// browser_webrtc_state frame with available=true and a non-empty reason —
// can take up to waitForTracksTimeout (15s, webrtc/ingest.go) to arrive
// after the ORIGINAL offer was sent, since that's how long HandleViewerOffer
// waits for the encoder's video track before giving up. The SPA's
// applyState reacts to that reason-bearing frame IMMEDIATELY (it does not
// wait out a fixed client-side timeout first) and then fires its ONE
// automatic re-offer retryDelayMs (15s) later — so the worst-case retry
// arrival is roughly waitForTracksTimeout + retryDelayMs =~ 30s after the
// original offer, not the ~45s this comment previously assumed (that
// earlier estimate was based on a client-side firstAnswerTimeoutMs wait that
// does not actually gate this path, and was itself already stale the moment
// it was written — it computed the margin against the OLD 5s
// waitForTracksTimeout even though this same fix-wave raised it to 15s).
// At the old 30s captureGracePeriod value the grace timer could expire right
// around when that retry was due, racing it — a retry sometimes hit an
// already-Stop()'d session and had to cold-start the whole encoder pipeline
// from zero, losing the identical video-track race (see
// waitForTracksTimeout's doc comment) a second time with no further retry
// left. Confirmed in the captured logs: capture[ray] stopped at T+35s, the
// SPA's retry offer arrived at T+45s (under the timeline in effect at
// capture time) and had to restart the encoder from scratch. 60s comfortably
// outlives the corrected ~30s worst-case retry arrival (30s of margin), so a
// retry now reaches a session that is either already flowing (instant
// answer) or at worst mid-negotiation, instead of always finding it torn
// down.
var captureGracePeriod = 60 * time.Second

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
// target). stunServer is the configured tools.browser.webrtc_stun_server
// value (empty = omit — see captureInjectPayload's StunServer field), threaded
// through so the encoder's own browser-side RTCPeerConnection can be
// configured with the same ICE policy the Go-side Pion relay uses (fix-wave
// LOW finding: this was previously never passed to the encoder page at all).
// Exported purely as a test-injection seam (mirrors this package's existing
// createTabFn/pipeLauncher/listTargets testability pattern) — production code
// always uses defaultEncoderStarter.
type EncoderStarter func(ctx context.Context, mgr *BrowserManager, tokenHex, ingestURL, stunServer string) (tabCtx context.Context, tabCancel context.CancelFunc, err error)

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
	// stunServer is threaded into every startEncoder call (see EncoderStarter's
	// doc comment) — set once from cfg.StunServer by NewCaptureSession
	// (production), empty for NewCaptureSessionWithDeps callers (tests) unless
	// they choose to care about it.
	stunServer string

	mu sync.Mutex
	// startOnce/startErr collapse concurrent Start() callers into exactly one
	// startEncoder invocation — see Start's doc comment.
	startOnce  sync.Once
	startErr   error
	extVersion string
	lastPingAt time.Time
	tabCtx     context.Context
	tabCancel  context.CancelFunc
	started    bool
	// starting is true only for the narrow window between Start() entering
	// its one-time startOnce.Do body and cs.startEncoder returning (success
	// or failure) — see IsStarting's doc comment for why the gateway's
	// ADR-048 condition-2 fence needs to distinguish this from both "never
	// started" and "fully started."
	starting    bool
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
	// done is closed exactly once, when Stop() completes — see Done()'s doc
	// comment. Lets a caller (the gateway's encoder-liveness watchdog)
	// select on session lifetime without a redundant onStopped wiring.
	done chan struct{}
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
func NewCaptureSession(
	mgr *BrowserManager,
	agentID string,
	cfg webrtc.Config,
	sink webrtc.InputSink,
	logf func(string, ...any),
) (*CaptureSession, error) {
	token := make([]byte, captureTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("capture session: mint token: %w", err)
	}
	relay := webrtc.NewSession(cfg, sink, logf)
	cs := newCaptureSessionWithDeps(mgr, agentID, relay, defaultEncoderStarter, token, logf)
	cs.stunServer = cfg.StunServer
	return cs, nil
}

// NewCaptureSessionWithDeps is the fully-injectable constructor: relay
// satisfies RelaySession (a fake in tests, *webrtc.Session in production —
// see NewCaptureSession) and startEncoder satisfies EncoderStarter (a fake
// in tests that never touches real chromedp, defaultEncoderStarter in
// production). Exported for other packages' tests (pkg/gateway's WebRTC
// signaling handler tests construct a real *CaptureSession with these two
// seams faked, exercising the actual CaptureSession lifecycle logic against
// fake relay/encoder machinery — wave-plan W2-A's testing note).
func NewCaptureSessionWithDeps(
	mgr *BrowserManager,
	agentID string,
	relay RelaySession,
	startEncoder EncoderStarter,
	logf func(string, ...any),
) (*CaptureSession, error) {
	token := make([]byte, captureTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("capture session: mint token: %w", err)
	}
	return newCaptureSessionWithDeps(mgr, agentID, relay, startEncoder, token, logf), nil
}

func newCaptureSessionWithDeps(
	mgr *BrowserManager,
	agentID string,
	relay RelaySession,
	startEncoder EncoderStarter,
	token []byte,
	logf func(string, ...any),
) *CaptureSession {
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
		done:         make(chan struct{}),
	}
}

// captureInjectPayload is the exact shape encoder.js's readConfig() expects
// at window.__omnipusCapture — {token, ingestUrl, stunServer} (see
// pkg/tools/browser/captureext/embedded/encoder.js's readConfig doc
// comment). Not a cross-gateway-boundary wire type in the Constraint #8
// sense (it never round-trips through pkg/gateway's REST/WS surface — it is
// injected directly into a CDP-driven page via
// Page.addScriptToEvaluateOnNewDocument), so a package-local struct here is
// correct, not a lint violation. StunServer (fix-wave LOW finding) carries
// the configured tools.browser.webrtc_stun_server value through to the
// encoder's own browser-side RTCPeerConnection config — omitted entirely
// (not an empty string) when unset, matching token/ingestUrl's always-
// present shape only where a value actually exists.
type captureInjectPayload struct {
	Token      string `json:"token"`
	IngestURL  string `json:"ingestUrl"`
	StunServer string `json:"stunServer,omitempty"`
}

// defaultEncoderStarter is the production EncoderStarter: it ensures the
// agent's default browsing session exists (so there is a tab to capture),
// ensures the capture extension is loaded into the shared Chrome, creates an
// UNTRACKED CDP target in the DEFAULT browser context (see the step-3 comment
// below for why it cannot live in the agent's own CDP context; deliberately
// NOT via mgr.OpenTab, which would register the target in the agent's
// visible tab strip/MaxTabs budget — the encoder page is a gateway-internal
// target the agent/user never see), injects window.__omnipusCapture BEFORE
// navigating (Page.addScriptToEvaluateOnNewDocument runs before any of the
// target document's own scripts, per its CDP doc comment), then navigates to
// chrome-extension://<captureext.ExtensionID>/encoder.html. stunServer (may
// be empty) is forwarded into the injected captureInjectPayload verbatim.
func defaultEncoderStarter(
	ctx context.Context,
	mgr *BrowserManager,
	tokenHex, ingestURL, stunServer string,
) (context.Context, context.CancelFunc, error) {
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
		return nil, nil, fmt.Errorf(
			"capture session: no shared-Chrome coordinator attached (WebRTC capture requires the shared-Chrome coordinator)",
		)
	}
	if coord.LoadedExtensionID() != captureext.ExtensionID {
		if _, err := coord.LoadExtension(ctx); err != nil {
			return nil, nil, fmt.Errorf("capture session: load capture extension: %w", err)
		}
	}

	// 3. Create the encoder target in the DEFAULT browser context (a child of
	// the coordinator's pipe rootCtx), NOT the agent's per-agent context.
	// W3 e2e finding (verified against real Chrome 150): Chrome refuses to
	// load chrome-extension:// pages inside CDP-created browser contexts —
	// the navigation fails with net::ERR_BLOCKED_BY_CLIENT even when the
	// extension was loaded with enableInIncognito:true. That flag grants the
	// extension VISIBILITY of the CDP contexts' tabs (chrome.tabs.query sees
	// them) — it does NOT permit hosting extension pages inside them, AND
	// (ADR-048, a separate and more fundamental restriction) it does NOT
	// make those tabs capturable: chrome.tabCapture still fails "Invalid tab
	// specified." for any tab living in a CDP-created context, regardless of
	// this flag. So the encoder page lives in the default context and
	// resolves the agent's tab via encoder.js's tab-selection query
	// (chrome.tabs.query sees whichever tabs are in the default context) —
	// which only finds the agent's OWN tab there when
	// tools.browser.capture_shared_context is enabled (ADR-048 condition 1,
	// coordinator.go's Register) and the agent's session was therefore
	// bootstrapped into this SAME default context, not an isolated
	// per-agent one. Like the previous
	// same-context design, the target is deliberately created WITHOUT
	// mgr.OpenTab so it never appears in the agent's visible tab
	// strip/MaxTabs budget (and the default context isn't an agent context
	// anyway).
	rootCtx := coord.rootContext()
	if rootCtx == nil {
		return nil, nil, fmt.Errorf("capture session: shared Chrome is not live (no root context for the encoder page)")
	}

	tab, err := mgr.createTab(rootCtx, "")
	if err != nil {
		return nil, nil, fmt.Errorf("capture session: create encoder target: %w", err)
	}

	payload, err := json.Marshal(captureInjectPayload{Token: tokenHex, IngestURL: ingestURL, StunServer: stunServer})
	if err != nil {
		tab.cancel()
		return nil, nil, fmt.Errorf("capture session: marshal inject payload: %w", err)
	}
	injectScript := "window.__omnipusCapture = " + string(payload) + ";"
	encoderURL := "chrome-extension://" + captureext.ExtensionID + "/encoder.html"

	runCtx, cancel := context.WithTimeout(tab.ctx, captureStartTimeout)
	runErr := chromedp.Run(
		runCtx,
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
		cs.mu.Lock()
		cs.starting = true
		cs.mu.Unlock()
		defer func() {
			cs.mu.Lock()
			cs.starting = false
			cs.mu.Unlock()
		}()

		tabCtx, tabCancel, startErr := cs.startEncoder(
			ctx,
			cs.mgr,
			hex.EncodeToString(cs.token),
			ingestURL,
			cs.stunServer,
		)
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
		// Deterministic capture-target binding (UAT 2026-07-18 "video black
		// when the agent drives" root cause): encoder.js resolves its capture
		// target as "the ACTIVE tab in the LAST-FOCUSED window"
		// (findActiveTargetTab). The encoder target startEncoder just created
		// lives in its own freshly-created window, which is the last-focused
		// window at resolution time — the active-tab query then finds only
		// the (filtered-out) extension page and falls back to "first
		// non-extension tab", i.e. the OLDEST tab in the shared Chrome. With
		// one agent that is coincidentally correct; with a second agent's
		// session present the fallback binds the WRONG agent's tab. Bringing
		// the REQUESTING agent's active tab to front here — after the
		// encoder-target creation (the last window-focus-stealing step) and
		// strictly before the encoder resolves its target (which happens only
		// after its page loads AND the ingest-WS hello/config round-trip
		// completes) — makes the last-focused window deterministically this
		// agent's, so the active-tab query resolves it directly, no fallback.
		// This is what lets handleWebRTCOffer's ADR-048 condition-2 fence be
		// scoped to ACTIVELY-VIEWED conflicting captures instead of denying
		// on any other live agent session. (Same BringToFront-before-capture
		// precedent as live.go's StartScreencast/rebindScreencast.)
		cs.bringAgentTabToFront(ctx)
		cs.logf("capture[%s]: started (encoder page navigating)", cs.agentID)
	})

	cs.mu.Lock()
	startErr := cs.startErr
	cs.mu.Unlock()
	return ranNow, startErr
}

// bringToFrontTimeout bounds the ENTIRE bringAgentTabToFront effort —
// session resolution AND the tab-focus action together, not just the
// chromedp.Run half. cs.mgr.Session() takes no context parameter, so the
// first call for an agent whose shared Chrome has never launched yet blocks
// for as long as that launch takes: up to cdppipe's CDP-liveness-probe dial
// timeout (~20s, cdppipe.defaultDialTimeout) if the resolved Chromium binary
// is slow, broken, or unreachable. An earlier version of this function only
// timed the chromedp.Run half, leaving Session()'s own resolution unbounded
// on the capture-start critical path — directly contradicting this
// function's "best-effort, a transient CDP hiccup must not cost the viewer
// their stream" design intent (regression: TestWebRTCEndToEndInProcess
// flaked on CI-worker CPU contention, spending most of the viewer's own
// answer-read deadline blocked here against a deliberately-broken decoy
// exec_path before this fix).
const bringToFrontTimeout = 5 * time.Second

// bringAgentTabToFront focuses this agent's current active tab (Page.
// bringToFront on the DefaultSessionID session's active-tab context) so
// encoder.js's active-in-last-focused-window tab resolution binds THIS
// agent's tab — see the call site in Start for the full rationale.
// Best-effort by design: on any failure OR timeout the capture proceeds with
// the historical fallback resolution (first non-extension tab) rather than
// failing the whole start — a transient CDP hiccup must not cost the viewer
// their stream. A no-op when cs.mgr is nil (test-construction pattern).
func (cs *CaptureSession) bringAgentTabToFront(ctx context.Context) {
	if cs.mgr == nil {
		return
	}
	// Session resolution AND chromedp.Run both happen inside this one
	// goroutine, raced against bringToFrontTimeout as a single bound — see
	// that const's doc comment for why Session() itself must be included,
	// not just the chromedp.Run call.
	done := make(chan struct{})
	go func() {
		defer close(done)
		tabCtx, err := cs.mgr.Session(DefaultSessionID)
		if err != nil {
			cs.logf("capture[%s]: bring agent tab to front: resolve session: %v", cs.agentID, err)
			return
		}
		// Fix-wave MED (reviewer 6): cs.mgr.Session() above can block for a
		// while on a cold shared-Chrome launch — long enough for THIS
		// capture session to have been superseded/Stop()'d by a newer one in
		// the meantime (e.g. another agent's offer won the ADR-048
		// condition-2 fence, see config.go's CaptureSharedContext doc
		// comment). Re-check before stealing window focus: a late-firing
		// BringToFront from an already-stopped session would steal focus
		// from whichever agent's tab is now legitimately active, for a
		// capture that is dead either way.
		cs.mu.Lock()
		stopped := cs.stopped
		cs.mu.Unlock()
		if stopped {
			cs.logf(
				"capture[%s]: bring agent tab to front: session already stopped, skipping (would have stolen window focus for nothing)",
				cs.agentID,
			)
			return
		}
		// runCtx is deliberately derived from tabCtx alone, NOT from ctx —
		// tabCtx must stay the chromedp parent so the run actually targets
		// the agent's tab. The caller's ctx (Start's ctx) is honored a
		// different way: the outer select below races this goroutine's own
		// "done" signal against ctx.Done() directly, rather than by
		// threading ctx into runCtx here.
		runCtx, cancel := context.WithTimeout(tabCtx, bringToFrontTimeout)
		defer cancel()
		if runErr := chromedp.Run(runCtx, chromedp.ActionFunc(func(c context.Context) error {
			return page.BringToFront().Do(c)
		})); runErr != nil {
			cs.logf(
				"capture[%s]: bring agent tab to front failed (capture may fall back to first-tab resolution): %v",
				cs.agentID,
				runErr,
			)
		}
	}()
	// This select is what actually honors ctx's cancellation (Start's ctx)
	// and bringToFrontTimeout — both race directly against the goroutine
	// above completing, independent of whatever context that goroutine's own
	// runCtx was derived from.
	select {
	case <-done:
	case <-time.After(bringToFrontTimeout):
		cs.logf(
			"capture[%s]: bring agent tab to front: timed out after %s waiting for session resolution (capture proceeds; may fall back to first-tab resolution)",
			cs.agentID,
			bringToFrontTimeout,
		)
	case <-ctx.Done():
	}
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

// IsStarting reports whether Start() is currently in the middle of its
// one-time encoder-page startup — true only for the narrow window between
// Start() being invoked and its cs.startEncoder call returning (success or
// failure); false before Start() is ever called AND false again once it has
// completed (successfully or not). Fix-wave HIGH (mid-startup supersede
// snipe): used by the gateway's ADR-048 condition-2 fence
// (browser_webrtc.go's handleWebRTCOffer) to avoid superseding (Stop()-ing)
// another agent's capture session that is still starting. Stop() during this
// window is already SAFE — Start's own "stopped while starting" branch tears
// down cleanly (see TestCaptureSession_StopWhileStarting_NoOrphanedEncoderTarget)
// — but not FAIR: an unrelated agent's fence check would otherwise be able
// to abort a legitimate, already-in-flight capture start before its own
// first viewer even gets a chance to register (AddViewer only happens once
// Start AND HandleViewerOffer both succeed, so a starting session always
// LOOKS viewerless to ViewerCount()).
func (cs *CaptureSession) IsStarting() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.starting
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
func (cs *CaptureSession) BindIngest(
	send func(action string, reason *string) error,
	closeConn func(),
) (previousClose func(), epoch uint64) {
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

// LastPingAt returns the timestamp of the most recent
// browser_capture_control{ping} beacon RecordPing observed — the zero
// time.Time if none has arrived yet (before BindIngest, or if the encoder
// never connects at all). Consumed by the gateway's encoder-liveness
// watchdog (fix-wave finding: this field previously had zero readers, so a
// wedged/crashed encoder that never disconnects cleanly could leave a
// capture session "started" forever with no signal to anyone).
func (cs *CaptureSession) LastPingAt() time.Time {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.lastPingAt
}

// Done returns a channel that is closed exactly once, when Stop() completes
// — callers (the gateway's encoder-liveness watchdog) select on this to
// exit their own per-session goroutine as soon as the session stops, for
// ANY reason (grace timer, browser death, ensure/start failure, or explicit
// shutdown), without needing a redundant onStopped-callback wiring of their
// own.
func (cs *CaptureSession) Done() <-chan struct{} {
	return cs.done
}

// ViewerIDs returns a snapshot of the currently-attached WebRTC viewer IDs.
// Stop() does not clear cs.viewers (only the relay-side CloseViewer calls
// happen there), so this remains accurate to call even from inside an
// onStopped callback — the gateway uses it there to push a final
// browser_webrtc_state{available:false} frame to every viewer that was
// still attached when the session stopped, rather than letting them learn
// of the stop only once their own ICE connection eventually times out.
func (cs *CaptureSession) ViewerIDs() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := make([]string, 0, len(cs.viewers))
	for id := range cs.viewers {
		out = append(out, id)
	}
	return out
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
//
// FIX WAVE A finding 2: reconciles the JPEG screencast pause decision again
// on a successful return, not just on the AddViewer/RemoveViewer/Stop viewer-
// set-change events ReconcileScreencast's other call sites already cover.
// This closes a real gap: for a BRAND NEW capture session (the ordinary
// single-viewer case), AddViewer runs BEFORE the ingest side has connected at
// all, so its own ReconcileScreencast call is guaranteed to observe
// Stats().HasVideo == false and correctly leave the JPEG screencast running
// (see TestCaptureSession_ReconcileScreencast_StaysResumedWhenVideoNotFlowingYet)
// — and nothing else ever re-checked afterward, so BOTH the CDP screencast
// encode and the WebRTC encode ran for the ENTIRE session even once video was
// flowing (the "choppy input under heavy video" UAT regression). The relay's
// own waitForTracks (pkg/tools/browser/webrtc/viewer.go) blocks until the
// ingest video track exists before HandleViewerOffer can ever return a
// success answer, so a nil err here is exactly the moment this session's
// video-flowing transition (false->true) becomes observable — reconciling
// right here catches it unconditionally, independent of whether this was the
// viewer whose AddViewer call happened to run before or after that
// transition.
func (cs *CaptureSession) HandleViewerOffer(viewerID, sdp string) (answer string, err error) {
	answer, err = cs.relay.HandleViewerOffer(viewerID, sdp)
	if err == nil {
		cs.ReconcileScreencast()
	}
	return answer, err
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
// already-registered viewerID. Fix-wave finding 3: also recomputes whether
// the JPEG screencast should now be paused (this new WebRTC viewer may be
// the one that makes every JPEG-attached viewer WebRTC-covered) — see
// ReconcileScreencast.
func (cs *CaptureSession) AddViewer(viewerID string) {
	cs.mu.Lock()
	cs.viewers[viewerID] = struct{}{}
	if cs.stopTimer != nil {
		cs.stopTimer.Stop()
		cs.stopTimer = nil
	}
	cs.mu.Unlock()
	cs.ReconcileScreencast()
}

// RemoveViewer unregisters viewerID. If no viewers remain, arms a
// captureGracePeriod timer that calls Stop() when it fires (wave-plan W2-A
// item 4: "stopped on last detach with a ~30s grace timer") — giving a
// viewer that's merely reloading the panel a window to reconnect without
// tearing down and re-provisioning the whole encoder page. A subsequent
// AddViewer within the grace window cancels the timer. Fix-wave finding 3:
// also recomputes the JPEG screencast pause state — a departing WebRTC
// viewer (e.g. its ICE connection failed and it fell back to JPEG per
// ADR-047 D3's per-viewer degradation) may be the one still attached to the
// JPEG live view, which must resume the screencast for it. See
// ReconcileScreencast.
func (cs *CaptureSession) RemoveViewer(viewerID string) {
	cs.mu.Lock()
	delete(cs.viewers, viewerID)
	empty := len(cs.viewers) == 0
	if empty && cs.stopTimer == nil && !cs.stopped {
		cs.stopTimer = time.AfterFunc(captureGracePeriod, cs.Stop)
	}
	cs.mu.Unlock()
	cs.ReconcileScreencast()
}

// ReconcileScreencast recomputes whether the agent's JPEG screencast
// (pkg/tools/browser/live.go) should be paused (ADR-047 fix-wave finding 3,
// UAT: "inputs feel dead / choppy under heavy video" traced to pod CPU
// saturation from running BOTH the CDP screencast and the WebRTC encoder
// simultaneously). Paused when this session has at least one WebRTC viewer
// AND the relay reports video actually flowing (Stats().HasVideo) AND EVERY
// viewer currently attached to the JPEG live view (cs.mgr.Live().
// AttachedViewerIDs) is ALSO one of this session's own WebRTC viewers — i.e.
// nobody is relying on the JPEG stream for pixels right now. Resumed in
// every other case: this session has stopped, no WebRTC viewers yet, video
// not flowing yet, or at least one JPEG-only (mixed-viewer, e.g. a
// per-viewer ICE fallback per ADR-047 D3) attachment remains.
//
// Called from this session's own viewer-set changes (AddViewer/RemoveViewer/
// Stop above) AND exported for the gateway to call from
// browser_ws.go's handleAttach/detach when the JPEG-viewer set changes on
// ITS side instead — a plain browser_attach/browser_detach that never
// touches WebRTC at all is invisible to this session otherwise. A safe no-op
// if cs.mgr is nil (NewCaptureSessionWithDeps(nil, ...) is a documented,
// supported test-construction pattern — see its doc comment).
func (cs *CaptureSession) ReconcileScreencast() {
	if cs.mgr == nil {
		return
	}
	live := cs.mgr.Live()

	cs.mu.Lock()
	stopped := cs.stopped
	webrtcViewers := make(map[string]struct{}, len(cs.viewers))
	for id := range cs.viewers {
		webrtcViewers[id] = struct{}{}
	}
	cs.mu.Unlock()

	if stopped || len(webrtcViewers) == 0 || !cs.relay.Stats().HasVideo {
		live.ResumeScreencast(DefaultSessionID)
		return
	}
	for _, id := range live.AttachedViewerIDs(DefaultSessionID) {
		if _, ok := webrtcViewers[id]; !ok {
			live.ResumeScreencast(DefaultSessionID)
			return
		}
	}
	live.PauseScreencast(DefaultSessionID)
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
	close(cs.done)
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
	// Fix-wave finding 3: unconditionally resume the JPEG screencast now
	// that WebRTC capture has stopped entirely. ReconcileScreencast's own
	// cs.stopped check (true by this point, set at the top of Stop()) always
	// resolves to "resume" here regardless of cs.viewers' stale contents
	// (Stop() deliberately does not clear cs.viewers — see ViewerIDs' doc
	// comment), and safely no-ops if cs.mgr is nil (test-constructed
	// sessions).
	cs.ReconcileScreencast()
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
