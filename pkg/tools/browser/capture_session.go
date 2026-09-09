package browser

// capture_session.go implements the ADR-047 / wave-plan W2-A per-agent WebRTC
// capture session: it owns the gateway-owned encoder page's lifecycle (the
// capture extension's chrome-extension://<id>/encoder.html target), plus the
// Pion SFU relay Session that backs it. One CaptureSession exists per agent
// (BrowserManager.capture), created lazily on the first WebRTC-capable viewer
// offer and torn down on last-viewer-detach (after a grace period) or on
// browser death (live.go's watchForUnexpectedDeath) or manager Shutdown.
//
// WHY encoder.js can resolve the tab it is asked to capture, stated for the
// world this code now lives in: every browsing session and the encoder page
// alike live in the browser's ONE default context, so chrome.tabs.query and
// chrome.tabCapture both operate on tabs that are genuinely in-context. There
// is nothing to arrange and no setting to get right.
//
// That used not to be true, and the history is kept because it is the reason
// the encoder page is created the way it is rather than a footnote. Sessions
// were once placed in per-agent CDP-CREATED browser contexts, and against
// those Chrome refuses to host chrome-extension:// pages at all
// (net::ERR_BLOCKED_BY_CLIENT) and chrome.tabCapture answers "Invalid tab
// specified." for any tab inside one — enableInIncognito grants the extension
// VISIBILITY of such tabs and never CAPTURABILITY (ADR-048, verified against
// real Chrome 150). A tools.browser.capture_shared_context knob chose between
// the two placements.
//
// ADR-075 FR-031 retired CDP browser contexts and that knob outright. DO NOT
// read the paragraph above as a live mechanism: there is no CDP-created
// context to avoid, no capture_shared_context to enable, and no cross-context
// visibility trick in play. Isolation moved down a level — one Chrome PROCESS
// and one --user-data-dir per workspace (FR-037) — where it is enforced by the
// OS, survives a restart, and does not cost the operator video.
// TestNoCDPBrowserContextIsEverCreated guards the identifiers; it cannot guard
// prose, which is why this paragraph is explicit instead of merely deleted.
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
// capture time) and had to restart the encoder from scratch.
//
// THE MARGIN, computed against the arm point rather than T0 (this is the
// step the two previous versions of this comment both got wrong, in
// opposite directions):
//
// The grace timer does NOT start at T0. It is armed by RemoveViewerIfCurrent
// -> armGraceStopLocked, reached only when the offer FAILS — i.e. at
// T0+waitForTracksTimeout. The client's single retry is scheduled from that
// same instant (it reacts to the failure frame immediately, no client-side
// timeout wait) plus retryDelayMs. So both sides start counting from the
// same event and waitForTracksTimeout CANCELS OUT:
//
//	grace expires at:  T0 + waitForTracksTimeout + captureGracePeriod = T0+75s
//	retry arrives at:  T0 + waitForTracksTimeout + retryDelayMs       = T0+30s
//	margin           = captureGracePeriod - retryDelayMs = 60-15      = 45s
//
// The margin is therefore 45s, INDEPENDENT of waitForTracksTimeout. Retuning
// waitForTracksTimeout does not require retuning this value — the earlier
// "30s of margin" figure came from comparing the retry's T0-relative arrival
// against captureGracePeriod as though the timer also started at T0, which
// understated the real margin by exactly one waitForTracksTimeout. The old
// logs above corroborate the arm point: with the OLD 5s wait and 30s grace,
// capture stopped at T+35s = 5+30, not at T+30.
//
// So 60s leaves 45s of headroom over the retry, and a retry now reaches a
// session that is either already flowing (instant answer) or at worst
// mid-negotiation, instead of always finding it torn down.
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

// viewerRemover is the optional RelaySession capability for learning about a
// RELAY-SIDE-ONLY viewer eviction (a terminal ICE/PeerConnection state, or an
// unrecovered Disconnected timeout) -- see webrtc.Session.SetOnViewerRemoved's
// doc comment for the full incident this closes ("nothing removes this
// session's stale viewer registration once WebRTC dies mid-session with the
// signaling connection still open" -- the browser_ws connection stays alive
// on an ICE failure, so no browser_detach frame ever arrives to trigger the
// existing AddViewer/RemoveViewer/Stop cleanup call sites, and the grace-stop
// timer never arms for a viewer that's actually gone).
//
// GAP 2 fix-wave finding: the callback additionally receives the relay's own
// identity handle for the evicted registration (dynamically a
// *webrtc.ViewerHandle -- opaque `any` for the same build-tag-neutral reason
// viewerOfferHandler's handle parameters are, below), NOT just the bare
// viewerID the original version of this interface passed. Without it,
// CaptureSession had no way to tell a legitimate eviction of an OLD,
// already-superseded relay registration (e.g. an ICE failure on a
// PeerConnection a NEWER HandleViewerOffer call for the same viewerID has
// since replaced) apart from a genuinely-current one -- the relay's OWN
// removeViewer already identity-checks internally before ever firing this
// callback (cur.pc == pc), but that only protects the RELAY's registry; it
// says nothing about whether CaptureSession's OWN cs.viewers entry for
// viewerID still corresponds to the same registration. See
// recordViewerRelayHandle/removeViewerByRelayHandle for how the handle is
// used to close that second, independent gap.
//
// Detected via a type assertion in newCaptureSessionWithDeps rather than
// added to RelaySession itself, so:
//   - the lite build's stub Session (never evicts a viewer on its own --
//     HandleViewerOffer always errors there) needs no matching method and
//     keeps compiling unchanged.
//   - test fakes (fakeRelay, capture_session_test.go) that don't exercise
//     this path don't need to implement it either.
type viewerRemover interface {
	SetOnViewerRemoved(fn func(viewerID string, handle any))
}

// ingestLossNotifier is the optional RelaySession capability for being told
// that the ingest connection died (live-diagnosed 2026-08-03).
//
// Why it matters: the relay used to only LOG a dead ingest peer connection, so
// its ingestPC kept pointing at a closed connection forever. Every later
// recapture's keyframe request then failed with "io: read/write on closed
// pipe", no new frame ever arrived, and the panel stayed frozen on whatever it
// last received — the operator watched the start page persist while the tab
// title and URL bar advanced through several real sites.
//
// Detected via type assertion, same discipline (and same lite-stub/test-fake
// reasons) as viewerRemover above.
type ingestLossNotifier interface {
	SetOnIngestLost(fn func())
}

// ingestLiveNotifier is the optional RelaySession capability for being told
// that a VIDEO feed has started forwarding — the positive counterpart to
// ingestLossNotifier (issue #674).
//
// Why it matters: the automatic recovery in capture_video_health.go is
// bounded, and a bound is only meaningful if success can be observed. Without
// this signal a recapture that WORKED would look identical to one that did
// not, so the attempt budget would drain on a perfectly healthy stream and the
// panel could never be told the video came back.
//
// Detected via type assertion, same discipline (and same test-fake reasons) as
// ingestLossNotifier above.
type ingestLiveNotifier interface {
	SetOnIngestLive(fn func())
}

// bitrateTargetNotifier is the optional RelaySession capability that reports a
// congestion target derived from the VIEWER leg's RTCP receiver reports
// (ADR-069 Finding 2). Detected by type assertion for the same reason as the
// interfaces above: the lite stub and the test fakes do not implement it.
type bitrateTargetNotifier interface {
	SetOnBitrateTarget(fn func(bps int))
}

// viewerOfferHandler is the optional RelaySession capability for
// supersede-safe viewer-offer handling -- see webrtc.Session.
// HandleViewerOfferHandle and CloseViewerIfCurrent's doc comments for the
// race this closes (a superseded/failed offer's cleanup tearing down a
// NEWER, already-committed offer's live connection for the same viewerID).
// Same detection discipline as viewerRemover above (a type assertion, not a
// RelaySession method) for the same lite-stub/test-fake reasons -- only the
// real Pion-backed *webrtc.Session implements it.
//
// The handle parameters are declared `any`, NOT the concrete
// *webrtc.ViewerHandle, deliberately: this file has no build tag (it
// compiles into the lite build too), while *webrtc.ViewerHandle is defined
// only in webrtc/viewer.go (//go:build !lite) -- naming it here would break
// -tags lite with an "undefined: webrtc.ViewerHandle" compile error. `any`
// lets this interface (and ViewerAttachHandle.relay below) exist in both
// builds; treat the value as opaque and pass it straight to
// CloseViewerIfCurrent, exactly as webrtc.Session's own doc comments direct.
type viewerOfferHandler interface {
	HandleViewerOfferHandle(viewerID, sdpOffer string) (answer string, handle any, err error)
	CloseViewerIfCurrent(handle any)
}

// viewerRegistration is the value CaptureSession.viewers stores per attached
// viewerID (see that field's doc comment): the generation token AddViewer
// minted for the current attach attempt, and — once known — the relay's own
// identity handle for that same attempt's registration (dynamically a
// *webrtc.ViewerHandle, opaque `any` for the same build-tag-neutral reason
// documented on viewerOfferHandler above). relayHandle is nil until
// recordViewerRelayHandle sets it (there is a real window, inside
// HandleViewerOffer, between minting gen via AddViewer and the relay actually
// registering a connection) and stays nil forever for an attempt that never
// reaches registration (see CleanupViewerOffer's CRITICAL fix-wave finding)
// or for a relay with no viewerOfferHandler capability at all.
type viewerRegistration struct {
	gen         uint64
	relayHandle any
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
//
// panelSessionID (issue #671) is the manager-level tab set the LIVE PANEL
// resolved for this capture — the encoder page must be raised alongside
// THAT set's tab, not the operator's workspace-owned one, or the video
// shows a different tab from the one the panel's clicks drive. Empty means
// "no panel context" and falls back to the operator's set, which is what
// every fake starter in the tests passes.
type EncoderStarter func(ctx context.Context, mgr *BrowserManager, panelSessionID, tokenHex, ingestURL, stunServer string) (tabCtx context.Context, tabCancel context.CancelFunc, err error)

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
	// offerHandler is relay's viewerOfferHandler capability, if it has one
	// (the real Pion-backed *webrtc.Session; never the lite build's stub, and
	// not every test fake) -- set once at construction (see
	// newCaptureSessionWithDeps), nil-safe everywhere it's read (see
	// HandleViewerOffer/CleanupViewerOffer).
	offerHandler viewerOfferHandler
	// stunServer is threaded into every startEncoder call (see EncoderStarter's
	// doc comment) — set once from cfg.StunServer by NewCaptureSession
	// (production), empty for NewCaptureSessionWithDeps callers (tests) unless
	// they choose to care about it.
	stunServer string
	// panelSessionID is the manager-level tab set the live panel resolved for
	// this capture (issue #671), fixed at construction. Empty means "no panel
	// context": panelTabSet() then falls back to the operator's
	// workspace-owned set, which is exactly the pre-#671 behaviour and what
	// every NewCaptureSessionWithDeps caller gets.
	//
	// Fixed rather than mutable on purpose. One capture serves one browser
	// (EnsureCaptureSession memoizes per browsing key), so the first offer's
	// resolution is the one the encoder page is built against; letting a later
	// viewer re-point it would move the video out from under the viewers
	// already watching it.
	panelSessionID string

	mu sync.Mutex
	// startOnce/startErr collapse concurrent Start() callers into exactly one
	// startEncoder invocation — see Start's doc comment.
	startOnce sync.Once
	startErr  error
	// captureScale is the controlling viewer's devicePixelRatio (see
	// SetCaptureScale). Guarded by mu. Zero means "never set" -> treated as 1.
	captureScale float64
	extVersion   string
	lastPingAt   time.Time
	tabCtx       context.Context
	tabCancel    context.CancelFunc
	started      bool
	// starting is true only for the narrow window between Start() entering
	// its one-time startOnce.Do body and cs.startEncoder returning (success
	// or failure) — see IsStarting's doc comment for why the gateway's
	// ADR-048 condition-2 fence needs to distinguish this from both "never
	// started" and "fully started."
	starting bool
	stopped  bool
	// ingestSend additionally carries the CDP-verified expected CSS viewport
	// dimensions for a recapture (expectedW, expectedH; 0,0 = absent) — see
	// RecaptureAt's doc comment for the race this closes: a recapture racing
	// a viewport resize otherwise pins the WebRTC stream to a stale tab size
	// (docs/internal/browser-viewport-input-rootcause-2026-07-31.md
	// follow-up, measured 2026-07-31 — stream stuck at 1278x632 launch
	// geometry while the tab was CDP-verified at 615x744 in the same
	// second).
	ingestSend  func(action string, reason *string, expectedW, expectedH, maxBitrate int) error
	ingestClose func()
	// ingestEpoch increments on every BindIngest call — UnbindIngest only
	// clears ingestSend/ingestClose if the epoch it was handed still matches
	// the current one, guarding against a stale (superseded/reconnected)
	// ingest connection's close path clobbering a NEWER connection's
	// callbacks. A counter rather than comparing the callback funcs
	// themselves (func values are not comparable in Go beyond nil-checks).
	ingestEpoch uint64
	// viewers maps an attached viewerID to its MOST RECENT registration
	// attempt's viewerRegistration (generation token + relay identity
	// handle) -- see AddViewer, RemoveViewerIfCurrent, recordViewerRelayHandle
	// and removeViewerByRelayHandle's doc comments. Two independent identity
	// checks are needed because two DIFFERENT things can race the SAME
	// viewerID: a superseded/failed offer's own cleanup racing a newer,
	// winning offer (guarded by gen, via RemoveViewerIfCurrent) and a
	// relay-confirmed eviction of an OLD registration racing a newer one
	// (guarded by relayHandle, via removeViewerByRelayHandle) -- only the
	// CURRENT registration's own cleanup/eviction may remove the entry.
	viewers map[string]viewerRegistration
	// viewerGenSeq is the monotonic counter AddViewer draws each new
	// generation token from. Guarded by mu, same as viewers itself.
	viewerGenSeq uint64
	stopTimer    *time.Timer
	onStopped    func() // invoked exactly once when Stop() completes (gateway hook for registry cleanup)
	// done is closed exactly once, when Stop() completes — see Done()'s doc
	// comment. Lets a caller (the gateway's encoder-liveness watchdog)
	// select on session lifetime without a redundant onStopped wiring.
	done chan struct{}

	// tabChangeRecaptureRunning/Pending coalesce concurrent
	// RecaptureForTabChange calls onto a SINGLE background worker — see that
	// method's doc comment. Guarded by mu.
	tabChangeRecaptureRunning bool
	tabChangeRecapturePending bool

	// tabChangeRecaptureW/H carry the CDP-verified CSS viewport the NEXT
	// worker pass should hand the encoder (0,0 = "no measurement to offer",
	// same convention as RecaptureAt). Written by every
	// RecaptureForTabChangeAt call — including one that only coalesces into a
	// running worker — and read fresh at the top of each pass, so a burst of
	// tab changes converges on the LAST caller's geometry rather than
	// replaying the first one's. Guarded by mu.
	tabChangeRecaptureW int
	tabChangeRecaptureH int

	// ── Bounded automatic video recovery (#674) ─────────────────────────────
	// All guarded by mu. See capture_video_health.go for the state machine
	// these back and for why every one of them is needed.
	//
	// ingestVideoLive is the relay's last reported verdict on whether video is
	// actually flowing (SetOnIngestLive / SetOnIngestLost).
	ingestVideoLive bool
	// ingestRecoveryAttempts counts automatic recaptures issued since video
	// was last confirmed live. Reset to 0 by onIngestVideoLive.
	ingestRecoveryAttempts int
	// ingestRecoveryTimer is the single armed evaluation of the recovery state
	// machine; nil when none is pending. Exactly one may exist at a time —
	// that is what stops a burst of loss notifications becoming a burst of
	// recaptures.
	ingestRecoveryTimer *time.Timer
	// ingestRecoveryGaveUp latches once the attempt budget is spent. It stops
	// the loop dead and is cleared only by video actually coming back, so the
	// failure ends in a named, reported error rather than an endless retry.
	ingestRecoveryGaveUp bool
	// recapturePendingUntil is when the most recently ISSUED recapture (from
	// any source — this state machine, a viewport resize, a tab change) stops
	// being plausibly in flight. A recapture tears the ingest connection down
	// on its way to rebuilding it, so a loss inside this window is the
	// replacement, not a death, and must not stack a second capture on top of
	// an in-flight one.
	recapturePendingUntil time.Time
	// onVideoHealth is the gateway's observer, installed via SetOnVideoHealth
	// (see BrowserManager.SetVideoHealthObserver). nil is a valid no-op.
	onVideoHealth func(VideoHealthEvent)

	// foregroundAssertFn is the test seam for the CDP foreground re-assert
	// RecaptureForTabChange performs (production: bringAgentTabToFront).
	// Mirrors BrowserManager.tabFocusFn's rationale exactly: the fake tab
	// contexts unit tests build have no CDP connection behind them, so the
	// real chromedp.Run inside bringAgentTabToFront would burn its whole
	// bringToFrontTimeout budget rather than doing anything observable. nil
	// in production. Read under mu, invoked with NO lock held.
	foregroundAssertFn func(ctx context.Context) bool
}

// NewCaptureSession constructs a production CaptureSession: mgr is the
// agent's BrowserManager — NOT because the encoder page is created in a
// context of its own (it is not; there is one default context per browser
// since FR-031), but because mgr is how this reaches the workspace's
// coordinator, whose BrowserConfig.ExtensionDir defaultEncoderStarter loads
// the capture extension from, set once at gateway boot. cfg is the Pion
// relay's ICE config (wave-plan item 7:
// Tools.Browser.WebRTCStunServer), sink receives every "input" data-channel
// message from every viewer (the gateway builds this — see
// browser_webrtc.go's webrtcInputSink — so this package never needs
// pkg/api/generated), and logf is a structured log sink (nil-safe).
func NewCaptureSession(
	mgr *BrowserManager,
	agentID string,
	panelSessionID string,
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
	// The encoder page gets NO STUN server, whatever the operator configured.
	// tools.browser.webrtc_stun_server governs the VIEWER leg, which is the
	// only leg with a real network between its peers; the encoder page is this
	// gateway's own headless Chrome dialling ws://127.0.0.1, so a
	// server-reflexive candidate on that leg can never be part of a usable
	// pair. Measured cost of asking for one anyway, in a Linux container whose
	// STUN server was unreachable: encoder.js's waitIceGatheringComplete sat
	// out its full 10s timeout and shipped the offer late, which together with
	// the relay's own 5s STUN gather took time-to-first-frame from ~1s to
	// ~17.5s. encoder.js reads "" as its explicit host-candidates-only mode
	// (resolveIceServers's tri-state), which is exactly what this leg wants.
	cs.stunServer = ""
	cs.panelSessionID = panelSessionID
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
	cs := &CaptureSession{
		agentID:      agentID,
		mgr:          mgr,
		logf:         logf,
		relay:        relay,
		startEncoder: startEncoder,
		token:        token,
		viewers:      make(map[string]viewerRegistration),
		done:         make(chan struct{}),
	}
	// Wire the two optional RelaySession capabilities (viewerRemover,
	// viewerOfferHandler) if the concrete relay supports them -- see their
	// doc comments. Detected via a type assertion rather than widening
	// RelaySession itself, so the lite build's stub Session and narrower
	// test fakes need no matching methods.
	if vr, ok := relay.(viewerRemover); ok {
		// removeViewerByRelayHandle (NOT the plain, unconditional
		// RemoveViewer this replaces as the wiring target) -- GAP 2 fix-wave
		// finding: the plain variant would delete cs.viewers[viewerID]
		// unconditionally on ANY relay-confirmed eviction notification, even
		// one describing an OLD registration a NEWER AddViewer call has
		// since superseded. See removeViewerByRelayHandle's doc comment.
		vr.SetOnViewerRemoved(cs.removeViewerByRelayHandle)
	}
	if oh, ok := relay.(viewerOfferHandler); ok {
		cs.offerHandler = oh
	}
	if il, ok := relay.(ingestLossNotifier); ok {
		// A dead ingest means the encoder is gone; ask it to re-capture so the
		// stream recovers on its own. Without this the session sits with no
		// ingest at all and nothing ever asks for a new one, which is
		// indistinguishable to the user from a hung browser. The recapture is
		// BOUNDED — see capture_video_health.go.
		il.SetOnIngestLost(cs.onIngestLost)
	}
	if il, ok := relay.(ingestLiveNotifier); ok {
		// The other half of the pair: without a positive "video is flowing
		// again" signal, the bounded recovery above can only ever exhaust its
		// budget — it would keep counting failures against a stream that had
		// already come back, and could never tell the panel it recovered.
		il.SetOnIngestLive(cs.onIngestVideoLive)
	}
	if bt, ok := relay.(bitrateTargetNotifier); ok {
		// Close the congestion loop: the viewer leg measures the real path,
		// the encoder is the only thing that can act on it, and this is the
		// wire between them (ADR-069 Finding 2).
		bt.SetOnBitrateTarget(cs.SetMaxBitrate)
	}
	return cs
}

// captureInjectPayload is the exact shape encoder.js's readConfig() expects
// at window.__omnipusCapture — {token, ingestUrl, stunServer} (see
// pkg/tools/browser/captureext/embedded/encoder.js's readConfig doc
// comment). Not a cross-gateway-boundary wire type in the Constraint #8
// sense (it never round-trips through pkg/gateway's REST/WS surface — it is
// injected directly into a CDP-driven page via
// Page.addScriptToEvaluateOnNewDocument), so a package-local struct here is
// correct, not a lint violation.
//
// StunServer carries the STUN policy for the encoder's own browser-side
// RTCPeerConnection. It MUST NOT be `omitempty`, and that is not a style
// preference — encoder.js's resolveIceServers reads this field as a
// TRI-state:
//
//	"stun:…" present -> use that server
//	""      present  -> host candidates only, no STUN at all
//	key ABSENT       -> back-compat fallback to Google's public STUN server
//
// `omitempty` erases the difference between the middle case and the last
// one, so an explicit "no STUN" was silently delivered to the page as "use
// stun.l.google.com". Measured 2026-09-05 with the repro harness: with
// omitempty in place, an encoder configured for host-only ICE still offered
// server-reflexive candidates from a public STUN server it had been told not
// to use. Emitting the empty string is what makes the middle case reachable
// at all.
type captureInjectPayload struct {
	Token      string `json:"token"`
	IngestURL  string `json:"ingestUrl"`
	StunServer string `json:"stunServer"`
}

// panelTabSet reports the manager-level tab set this capture is bound to
// (issue #671): the id the live panel resolved, or — when this session was
// built without one (every NewCaptureSessionWithDeps caller, and any path with
// no panel context) — the operator's workspace-owned set, which is the
// pre-#671 behaviour.
//
// Returns "" for a session with neither, i.e. a nil-manager test construction:
// callers treat that as "no tab set to act on" rather than substituting one.
func (cs *CaptureSession) panelTabSet() string {
	if cs.panelSessionID != "" {
		return cs.panelSessionID
	}
	if cs.mgr == nil {
		return ""
	}
	return cs.mgr.OperatorSessionID()
}

// defaultEncoderStarter is the production EncoderStarter: it ensures the
// tab set the live panel resolved exists (so there is a tab to capture),
// ensures the capture extension is loaded into this workspace's Chrome,
// creates an UNTRACKED CDP target — deliberately NOT via mgr.OpenTab, which
// would register it in the visible tab strip; the encoder page is a
// gateway-internal target the agent and the user never see — injects
// window.__omnipusCapture BEFORE
// navigating (Page.addScriptToEvaluateOnNewDocument runs before any of the
// target document's own scripts, per its CDP doc comment), then navigates to
// chrome-extension://<captureext.ExtensionID>/encoder.html. stunServer (may
// be empty) is forwarded into the injected captureInjectPayload verbatim.
func defaultEncoderStarter(
	ctx context.Context,
	mgr *BrowserManager,
	panelSessionID, tokenHex, ingestURL, stunServer string,
) (context.Context, context.CancelFunc, error) {
	if mgr == nil {
		return nil, nil, fmt.Errorf("capture session: no browser manager")
	}

	// 1. Ensure the tab set being watched exists — the encoder page must share
	// ITS window (see this file's top-of-file doc comment for why).
	//
	// panelSessionID is what the live panel resolved for this viewer (issue
	// #671): the watched chat's own tab set, or the operator's workspace-owned
	// one. Passing the operator's unconditionally is the bug — with an empty
	// operator set THIS call was what lazily created the blank /browser-start
	// tab the video then showed, while the agent browsed in the chat's set.
	// Empty falls back to the operator's set, the behaviour every caller
	// without panel context had before.
	//
	// Either way it is a real (key, owner) tab set, never a hardcoded default
	// session: that identity was deleted by FR-002b, and
	// TestNoResidualDefaultSessionID exists to keep it deleted.
	if panelSessionID == "" {
		panelSessionID = mgr.OperatorSessionID()
	}
	if _, err := mgr.Session(panelSessionID); err != nil {
		return nil, nil, fmt.Errorf("capture session: ensure agent browsing context: %w", err)
	}

	// 2. Ensure the capture extension is loaded into THIS WORKSPACE'S Chrome.
	// There is one per workspace now (FR-037), not one shared by the gateway,
	// so the extension is loaded per browser and not once per process.
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

	// 3. Create the encoder target as a child of the coordinator's pipe
	// rootCtx — i.e. in this workspace's browser, alongside every tab that
	// browser holds. There is exactly one browser context here (FR-031), so
	// "which context" is no longer a decision this code makes; encoder.js's
	// tab-selection query and chrome.tabCapture both see the workspace's own
	// tabs because there is nowhere else for them to be.
	//
	// The target is still created WITHOUT mgr.OpenTab, and that part is a
	// live decision rather than history: OpenTab would register the encoder
	// page in the visible tab strip, and it is a gateway-internal page the
	// agent and the user must never see or be able to drive.
	//
	// Why the encoder page is not simply put wherever is convenient — the
	// constraint that shaped this, kept because it is the reason the code
	// looks like this and not because it still binds: against the retired
	// per-agent CDP-created contexts, Chrome refused to load
	// chrome-extension:// pages at all (net::ERR_BLOCKED_BY_CLIENT, even with
	// enableInIncognito:true), and chrome.tabCapture answered "Invalid tab
	// specified." for any tab inside one (ADR-048, real Chrome 150). Both
	// findings are what make a per-workspace Chrome PROCESS the right
	// isolation boundary and a CDP context the wrong one. Do not reintroduce
	// a CDP context to "isolate" anything here: it would break capture and
	// nothing else, which is a failure that shows up only on a machine with a
	// real browser.
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
			cs.panelTabSet(),
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
		// non-extension tab", i.e. the OLDEST tab in THIS WORKSPACE'S Chrome.
		// With one agent that is coincidentally correct; and since FR-037 put
		// every agent on a workspace into that one browser, a second agent on
		// the same workspace is the ordinary case, not the exotic one — the
		// fallback then binds the WRONG agent's tab. Bringing
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
		if !cs.bringAgentTabToFront(ctx) {
			cs.reassertForegroundAsync()
		}
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
// first call for an agent whose WORKSPACE'S Chrome has never launched yet
// blocks
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

// foregroundReassertDelay is how long reassertForegroundAsync waits before its
// single retry. Long enough that a cold shared-Chrome launch (the usual reason
// the first attempt loses its budget) has finished, short enough that a viewer
// is not left watching a ~0.5fps stream while it waits.
const foregroundReassertDelay = 6 * time.Second

// bringAgentTabToFront focuses the active tab of the tab set this capture is
// bound to (Page.bringToFront on panelTabSet()'s active-tab context) so
// encoder.js's active-in-last-focused-window tab resolution binds THIS
// agent's tab — see the call site in Start for the full rationale.
// Best-effort by design: on any failure OR timeout the capture proceeds with
// the historical fallback resolution (first non-extension tab) rather than
// failing the whole start — a transient CDP hiccup must not cost the viewer
// their stream. A no-op when cs.mgr is nil (test-construction pattern).
func (cs *CaptureSession) bringAgentTabToFront(ctx context.Context) bool {
	if cs.mgr == nil {
		return false
	}
	landed := make(chan struct{}, 1)
	// Session resolution AND chromedp.Run both happen inside this one
	// goroutine, raced against bringToFrontTimeout as a single bound — see
	// that const's doc comment for why Session() itself must be included,
	// not just the chromedp.Run call.
	done := make(chan struct{})
	go func() {
		defer close(done)
		sid := cs.panelTabSet()
		// Focus the tab that EXISTS; never manufacture one as a side effect of
		// focusing. By the time this runs on the real path the encoder starter
		// has already ensured the workspace-owned browsing context (step 1 of
		// defaultEncoderStarter), so a missing context here means the capture is
		// running against a manager that has none — and lazily creating one
		// would open a tab nobody asked for, on a code path whose whole
		// contract is best-effort.
		if !cs.mgr.sessionExists(sid) {
			cs.logf("capture[%s]: bring agent tab to front: no browsing context to focus", cs.agentID)
			return
		}
		tabCtx, err := cs.mgr.Session(sid)
		if err != nil {
			cs.logf("capture[%s]: bring agent tab to front: resolve session: %v", cs.agentID, err)
			return
		}
		// Fix-wave MED (reviewer 6): cs.mgr.Session() above can block for a
		// while on a cold shared-Chrome launch — long enough for THIS
		// capture session to have been superseded/Stop()'d by a newer one in
		// the meantime (e.g. another agent's offer won the ADR-048
		// condition-2 fence). Re-check before stealing window focus: a late-firing
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
		// foregroundTabActions (manager.go), NOT a local bringToFront+focus
		// pair: this used to be the ONLY place focus emulation was applied,
		// so the tab-switch path gave a captured tab a DIFFERENT treatment
		// and one browser_switch_tab undid half of what capture start did
		// (review finding F9, 2026-08-13). One definition, every path.
		if runErr := chromedp.Run(runCtx, foregroundTabActions()...); runErr != nil {
			cs.logf(
				"capture[%s]: bring agent tab to front failed (capture may fall back to first-tab resolution): %v",
				cs.agentID,
				runErr,
			)
			return
		}
		landed <- struct{}{}
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

	select {
	case <-landed:
		return true
	default:
		return false
	}
}

// reassertForegroundAsync re-runs the foreground assert ONCE, shortly after a
// first attempt failed to land.
//
// CORRECTION (2026-08-06): the measurement this rationale was originally
// written from was UNSOUND, and the retry below did NOT fix the
// browser-live-video e2e failure it was written for — that test still fails
// identically with this in place. The probe compared Page.startScreencast frame
// counts without ever sending Page.screencastFrameAck; Chrome throttles
// delivery to a trickle when frames go unacked (production acks every frame,
// see live.go's runAckWorker), so the foreground-vs-background difference it
// showed was noise on an already-stalled stream, not evidence of compositing
// throttling. Do not cite those numbers.
//
// What justifies keeping this anyway is narrower and independently true: the
// first attempt shares ONE 5s budget with cs.mgr.Session(), which on a cold
// shared-Chrome launch can alone take ~20s (see bringToFrontTimeout's own doc),
// so under load the focus action can never run at all and nothing notices. A
// single warm retry closes that gap cheaply. It is a robustness fix, NOT a fix
// for the frozen-video test, whose root cause remains open.
//
// Original (unsupported) rationale follows, kept only so the next reader can
// see what was disproved: a tab that is not foregrounded
// composites at roughly ONE frame every two seconds; foregrounded it produces
// several times that (probed directly against this project's own Chrome build
// via Page.startScreencast frame counts). Chrome's anti-backgrounding flags do
// NOT change it: --disable-renderer-backgrounding,
// --disable-background-timer-throttling and
// --disable-backgrounding-occluded-windows are all already set (chromedp's
// defaults carry them too) and a background tab still composites at ~0.5fps.
// Animation TIMELINES keep advancing at full rate, which is what makes this so
// easy to misdiagnose: the page is genuinely animating, it just is not being
// painted for the capture.
//
// So a first attempt that times out is not cosmetic — it is the difference
// between a live stream and one that looks frozen. The first attempt shares a
// single 5s budget with cs.mgr.Session(), which on a cold shared-Chrome launch
// can alone take ~20s (see bringToFrontTimeout), so under load the focus action
// frequently never runs at all. By the time we retry, that session is resolved
// and warm, so the retry costs a single CDP round trip.
//
// Deliberately ONE retry, and it re-checks stopped first: window focus is a
// shared, global resource in the shared-Chrome model, and repeatedly stealing
// it would fight other agents' captures for it — the exact hazard the original
// call site's comment warns about.
func (cs *CaptureSession) reassertForegroundAsync() {
	go func() {
		select {
		case <-time.After(foregroundReassertDelay):
		case <-cs.done:
			return
		}
		cs.mu.Lock()
		stopped := cs.stopped
		cs.mu.Unlock()
		if stopped {
			return
		}
		if cs.bringAgentTabToFront(context.Background()) {
			cs.logf("capture[%s]: foreground re-assert landed on retry", cs.agentID)
		}
	}()
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
// {action, reason, expected_width?, expected_height?} to the encoder over
// the raw ingest WS (NOT a WebRTC data channel — recapture/shutdown are
// signaled on the signaling connection itself, independent of whether media
// has even connected yet). expectedW/expectedH (0,0 = absent) carry the
// CDP-verified CSS viewport RecaptureAt wants the encoder to converge on —
// see that method's doc comment for why this exists.
func (cs *CaptureSession) BindIngest(
	send func(action string, reason *string, expectedW, expectedH, maxBitrate int) error,
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

// ViewerAttachHandle identifies one specific CaptureSession.HandleViewerOffer
// call's attempt to attach viewerID -- pass it to CleanupViewerOffer instead
// of the old viewerID-only "Relay().CloseViewer(viewerID); RemoveViewer(
// viewerID)" pair when tearing down a failed offer, or one superseded before
// its commit (see CleanupViewerOffer's doc comment for the race this
// replaces). Never nil from a successful HandleViewerOffer call. Opaque:
// callers must not inspect its fields.
type ViewerAttachHandle struct {
	viewerID string
	gen      uint64
	// relay is nil (a true nil `any`) unless the underlying RelaySession
	// implements viewerOfferHandler (the real Pion-backed Session; never the
	// lite build's stub, and not every test fake) AND this specific attempt
	// reached the point HandleViewerOfferHandle registers it (see that
	// method's doc comment) -- CleanupViewerOffer falls back to the
	// historical viewerID-only relay close when nil. Declared `any` (holding
	// a *webrtc.ViewerHandle dynamically in production) rather than the
	// concrete type for the same build-tag-neutral reason viewerOfferHandler
	// above documents.
	relay any
}

// HandleViewerOffer delegates to the relay's HandleViewerOffer — a viewer's
// SDP offer, arriving as a browser_webrtc_offer frame on the main browser
// WS. gen is the generation token AddViewer returned for THIS SAME viewerID
// attempt (called by the caller just before this). Returns a
// *ViewerAttachHandle (never nil) identifying this specific attempt — pass
// it to CleanupViewerOffer, NOT the raw viewerID, when tearing down a failed
// or superseded-before-commit offer.
func (cs *CaptureSession) HandleViewerOffer(
	viewerID, sdp string,
	gen uint64,
) (answer string, handle *ViewerAttachHandle, err error) {
	var relayHandle any
	if cs.offerHandler != nil {
		answer, relayHandle, err = cs.offerHandler.HandleViewerOfferHandle(viewerID, sdp)
	} else {
		answer, err = cs.relay.HandleViewerOffer(viewerID, sdp)
	}
	// GAP 2 fix-wave finding: record the relay's own identity handle for
	// THIS registration (if any -- see viewerRegistration's doc comment for
	// when it's nil) alongside gen, so a later relay-confirmed eviction
	// notification (removeViewerByRelayHandle) can tell whether it describes
	// the registration currently on file for viewerID before removing
	// anything. Recorded regardless of err: even a call that later fails
	// further down (SetRemoteDescription/CreateAnswer/SetLocalDescription)
	// may already have registered a live PeerConnection at the relay (see
	// HandleViewerOfferHandle's own doc comment on the registration point) —
	// that registration is exactly what CleanupViewerOffer's
	// CloseViewerIfCurrent branch will need to find in order to close it.
	cs.recordViewerRelayHandle(viewerID, gen, relayHandle)
	handle = &ViewerAttachHandle{viewerID: viewerID, gen: gen, relay: relayHandle}
	return answer, handle, err
}

// recordViewerRelayHandle records relayHandle — the exact `any` value
// HandleViewerOfferHandle returned for THIS attempt — as viewerID's current
// relay identity, but ONLY if gen still matches the CURRENTLY-registered
// generation (mirrors RemoveViewerIfCurrent's own identity discipline): a
// no-op if a NEWER AddViewer call for the same viewerID has since superseded
// gen (a winning attempt racing this one), so a superseded attempt's own
// registration can never overwrite the winning attempt's relay identity. Also
// a no-op if relayHandle is nil (no viewerOfferHandler capability at all, or
// this specific attempt never reached registration — see CleanupViewerOffer's
// CRITICAL fix-wave finding for that second case) — nothing meaningful to
// record either way.
func (cs *CaptureSession) recordViewerRelayHandle(viewerID string, gen uint64, relayHandle any) {
	if relayHandle == nil {
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	reg, exists := cs.viewers[viewerID]
	if !exists || reg.gen != gen {
		return
	}
	reg.relayHandle = relayHandle
	cs.viewers[viewerID] = reg
}

// removeViewerByRelayHandle is wired as the relay's SetOnViewerRemoved
// callback (see viewerRemover's doc comment) in place of the plain,
// unconditional RemoveViewer this replaces as that specific wiring target
// (RemoveViewer itself is UNCHANGED and stays correct for its own remaining
// legitimate caller, an explicit browser_detach/WS-close — see its doc
// comment). Removes viewerID's CaptureSession-level registration ONLY if
// relayHandle is STILL the exact relay identity CURRENTLY recorded for it
// (recordViewerRelayHandle) — a safe no-op if a NEWER registration for the
// same viewerID (a fresh AddViewer + HandleViewerOffer, superseding this one)
// has since taken over, or if viewerID has no relay handle recorded at all
// yet. Delegates the actual removal to RemoveViewerIfCurrent so both the
// gen-keyed (CaptureSession-side, offer-cleanup callers) and
// relay-handle-keyed (this callback's own, relay-eviction callers) identity
// checks funnel through the SAME single removal path.
//
// GAP 2 fix-wave finding this closes: the relay's own removeViewer already
// identity-checks internally (cur.pc == pc) before ever notifying —
// that only protects the RELAY's registry. Without this check on the
// CaptureSession side too, a relay-confirmed eviction of an OLD,
// already-superseded registration racing a NEWER AddViewer call for the same
// viewerID would have deleted the newer registration's cs.viewers entry
// (via the plain, unconditional RemoveViewer this used to be wired to),
// potentially arming the 60s grace-stop timer under an actively-viewed newer
// connection.
func (cs *CaptureSession) removeViewerByRelayHandle(viewerID string, relayHandle any) {
	cs.mu.Lock()
	reg, exists := cs.viewers[viewerID]
	if !exists || reg.relayHandle != relayHandle {
		cs.mu.Unlock()
		return
	}
	gen := reg.gen
	cs.mu.Unlock()
	cs.RemoveViewerIfCurrent(viewerID, gen)
}

// CleanupViewerOffer undoes ONE SPECIFIC HandleViewerOffer call's attempt to
// attach viewerID — the identity-safe replacement for the old
// "cs.Relay().CloseViewer(viewerID); cs.RemoveViewer(viewerID)" pair that
// both handleWebRTCOffer's failure branch and its superseded-before-commit
// branch used to run (pkg/gateway/browser_webrtc.go). That pair is keyed
// ONLY by viewerID: when two HandleViewerOffer calls for the SAME viewerID
// are (or were) in flight — e.g. a slow, ultimately-failing/superseded offer
// whose cleanup runs AFTER a newer offer for the same viewerID has already
// committed and is being actively viewed — neither the relay's own viewer
// registry nor CaptureSession.viewers (a plain per-viewerID entry, with no
// notion of "which attempt") can tell the losing attempt's cleanup apart
// from the winning one, so viewerID-keyed CloseViewer/RemoveViewer would
// close and evict the WINNING connection instead of the losing one —
// dropping ViewerCount() to 0 and arming the 60s capture grace-stop timer
// while the winning viewer is still actively watching.
//
// handle (from THIS call's own HandleViewerOffer) makes both halves of the
// cleanup identity-safe, independently:
//   - relay-side: if the underlying relay supports it (viewerOfferHandler,
//     the real Pion-backed Session — never the lite stub, which has no live
//     viewer connection to protect), CloseViewerIfCurrent is a no-op once
//     handle's connection has already been superseded/removed, and otherwise
//     closes+evicts it via the SAME identity-checked path
//     (webrtc.Session.removeViewer) a relay-side ICE eviction uses — which
//     also fires the onViewerRemoved notification that keeps
//     CaptureSession.viewers in sync for THIS generation.
//   - CaptureSession-side: RemoveViewerIfCurrent only removes viewerID's
//     entry if handle.gen still matches the CURRENTLY-registered generation
//     — a no-op if a newer AddViewer call for the same viewerID (a winning
//     offer racing this one) has since superseded it.
//
// Falls back to the historical viewerID-only relay close ONLY when the
// underlying relay has NO supersede-safe capability whatsoever
// (cs.offerHandler == nil — a lite-build stub, or a RelaySession test fake
// that doesn't implement viewerOfferHandler) — no worse than before this fix
// in that case, since no identity information was ever available to make it
// safer.
//
// CRITICAL fix (fix-wave review, 2026-07-28): handle.relay == nil does NOT
// by itself mean "no identity information available" — a REAL, identity-safe
// *webrtc.Session (cs.offerHandler != nil) still returns a true nil handle
// from HandleViewerOfferHandle on any of its SEVEN early-return paths (empty
// viewerID/SDP, no-ingest-video-track timeout, session closed,
// buildPeerConnection failure, video/audio AddTrack failure) — all of which
// run BEFORE that attempt ever registers a viewerConn in the relay's own
// registry (see viewer.go's doc comment for the exact registration point).
// THIS attempt has nothing of its own to close there. The old code treated
// "cs.offerHandler != nil && handle.relay == nil" identically to
// "cs.offerHandler == nil" and fell through to the unconditional,
// viewerID-only cs.Relay().CloseViewer(handle.viewerID) — closing WHATEVER
// currently sits at that key, which can be a completely unrelated, live,
// already-registered sibling attempt for the SAME viewerID (viewerID is
// per-CONNECTION, not per-offer — pkg/gateway/browser_ws.go mints it once per
// connection, and dispatchWebRTCOffer spawns one unserialized goroutine per
// offer frame, so two concurrent offers for the same viewerID are a real,
// reachable race: an older offer stalls in waitForTracks for up to 15s and
// ultimately times out while a newer offer for the same viewerID has already
// registered, committed, and is being actively viewed). Confirmed by
// TestCaptureSession_CleanupViewerOffer_EarlyFailureMustNotClobberLiveSibling.
// The correct behavior for that case is to skip the relay-side close
// entirely — there is nothing THIS attempt registered to undo.
//
// The CaptureSession-side half (RemoveViewerIfCurrent) stays identity-safe
// (gen-checked) regardless of which of the three cases below applies.
//
// Safe to call with a nil handle (no-op) — e.g. a caller that never reached
// HandleViewerOffer at all.
func (cs *CaptureSession) CleanupViewerOffer(handle *ViewerAttachHandle) {
	if handle == nil {
		return
	}
	switch {
	case cs.offerHandler != nil && handle.relay != nil:
		// Registered, and the relay supports identity-safe closing — undo
		// ONLY if this exact attempt's connection is still the current one.
		cs.offerHandler.CloseViewerIfCurrent(handle.relay)
	case cs.offerHandler == nil:
		// No supersede-safe capability at all — the historical,
		// viewerID-only fallback is the best available and is no less safe
		// than before this fix (no identity information was ever available
		// here to begin with).
		cs.Relay().CloseViewer(handle.viewerID)
	default:
		// cs.offerHandler != nil but handle.relay == nil: this attempt
		// failed BEFORE it ever registered anything at the relay (see the
		// CRITICAL fix comment above) — nothing of ITS OWN exists there to
		// close, and the unconditional fallback would risk closing an
		// unrelated, live sibling attempt for the same viewerID instead.
	}
	cs.RemoveViewerIfCurrent(handle.viewerID, handle.gen)
}

// Recapture signals both halves of the recapture path (wave-plan W2-A item
// 5) with no expected-geometry hint. Delegates to RecaptureAt(0, 0) (0,0 =
// absent, mirroring ingestSend's convention) — kept as its own method
// because most callers (e.g. live.go's onTabsChanged, on an active-tab
// switch) have no CDP-verified viewport measurement to offer, so the
// encoder falls back to its own chrome.tabs.get-based stability poll. See
// RecaptureAt's doc comment for the dimension-carrying variant a caller
// should prefer whenever one IS available (a viewport resize).
func (cs *CaptureSession) Recapture() {
	cs.RecaptureAt(0, 0)
}

// RecaptureAt signals both halves of the recapture path (wave-plan W2-A item
// 5): a browser_capture_control{recapture} frame is pushed to the encoder
// over the ingest WS (if currently bound) so it re-binds chrome.tabCapture
// to the newly-active tab, AND the relay's SignalRecapture() primes attached
// viewers for the resulting brief gap with an immediate + bursted PLI so
// playback recovers as fast as possible. expectedW/expectedH (0,0 = absent)
// additionally carry the CDP-verified CSS viewport the encoder should
// converge on.
//
// Why this exists (follow-up to
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md, measured
// 2026-07-31): a viewport resize (pkg/gateway/browser_ws.go's
// handleViewport) calls LiveViewRegistry.SetViewport, which reads back the
// tab's ACTUAL CSS viewport via Page.getLayoutMetrics immediately after
// applying it — the one piece of CDP-VERIFIED truth that exists at that
// moment — then triggers a recapture so the WebRTC stream follows the new
// size. Without threading that measurement through, the encoder's own
// chrome.tabs.get-based resolution (encoder.js's captureActiveTabStream)
// races the OS window reflow: chrome.tabs.get lags behind the CDP-verified
// layout, so a recapture landing mid-reflow pins the stream to a STALE tab
// size. Live evidence: the stream stuck at the 1278x632 launch geometry
// while the SAME tab was already CDP-verified at 615x744 in the same
// second — encoder.js's old "two agreeing chrome.tabs.get reads" stability
// poll is fooled by this, because two STALE reads can agree with each other
// just as readily as two settled ones. Passing the verified dimensions lets
// the encoder poll chrome.tabs.get against a KNOWN target and fall back to
// that known-good value on a poll timeout, instead of trusting mere
// agreement between reads — see encoder.js's captureActiveTabStream for the
// convergence logic this drives.
//
// Safe to call with no ingest connection bound (a no-op send) or no relay
// tracks yet (SignalRecapture is itself a no-op then, per its doc comment).
// SetCaptureScale records the deviceScaleFactor the captured tab renders at
// (the controlling viewer's window.devicePixelRatio, threaded through the
// viewport frame — the same source SetViewport's Emulation override uses).
// The gateway's ingest send closure reads it back via CaptureScale when
// building a recapture control frame, so the encoder can size its tabCapture
// constraints in PHYSICAL pixels. Values below 1 (including the zero value)
// are treated as 1 by CaptureScale — capture-at-CSS-resolution, the pre-fix
// behavior — so an SPA that never sends a scale changes nothing.
func (cs *CaptureSession) SetCaptureScale(scale float64) {
	cs.mu.Lock()
	cs.captureScale = scale
	cs.mu.Unlock()
}

// CaptureScale returns the last SetCaptureScale value, clamped to >= 1.
func (cs *CaptureSession) CaptureScale() float64 {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.captureScale < 1 {
		return 1
	}
	return cs.captureScale
}

// ResetAdaptation asks the encoder to restore full quality WITHOUT rebuilding
// the capture: it resets the adaptation loop's state and re-applies the sender
// constraints. Used at the boot-warm handover, where the resolution the loop
// settled on with nobody watching must not be inherited by the first real
// viewer.
//
// Deliberately not Recapture(): a rebuild there measured ~17s to first frame
// against ~4s without it (hosted box, 2026-08-17), which is what made keeping
// a warm capture alive past its idle window worse than letting it stop. This
// keeps the guarantee and drops the cost, so the warm capture stays useful for
// a panel opened long after boot.
// SetMaxBitrate pushes a viewer-derived bitrate ceiling to the encoder
// (ADR-069 Finding 2). The value comes from the viewer leg's own RTCP
// receiver reports; before this existed the encoder only ever measured the
// loopback ingest hop and happily encoded 24 Mbps for a link delivering 355
// kbps.
func (cs *CaptureSession) SetMaxBitrate(bps int) {
	cs.requestControlBitrate(bps)
}

func (cs *CaptureSession) ResetAdaptation(reason string) {
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	cs.requestControl("adapt_reset", reasonPtr, 0, 0)
}

func (cs *CaptureSession) RecaptureAt(expectedW, expectedH int) {
	// Open the in-flight window BEFORE the control frame goes out (#674): the
	// encoder tears its PeerConnection down as the first step of a recapture,
	// so the resulting ingest loss can arrive before this function returns. A
	// window opened afterwards would already have missed it, and the bounded
	// recovery would spend an attempt on a recapture that was working.
	cs.noteRecaptureIssued()
	cs.requestControl("recapture", nil, expectedW, expectedH)
	cs.relay.SignalRecapture()
}

// RecaptureForTabChange is the recapture entry point for "the active tab
// moved", as opposed to "the viewport was resized" (RecaptureAt). It does
// everything Recapture() does AND first re-asserts this agent's CURRENT
// model-active tab as Chrome's foreground tab, so the encoder's own
// chrome.tabs.query({active: true, lastFocusedWindow: true}) resolution
// (captureext/embedded/encoder.js findActiveTargetTab) cannot answer with a
// tab this manager no longer considers active.
//
// Why a SEPARATE entry point rather than putting the re-assert in
// RecaptureAt (measured trade-off, 2026-08-15): RecaptureAt is also the
// viewport-resize path, which the SPA drives at drag frequency
// (pkg/gateway/browser_ws.go's handleViewport, and its Recapture() fallback
// branches when the CDP resize handle fails). Adding a CDP round trip to
// EVERY recapture would put a Page.bringToFront on that high-frequency path
// — on a 2-CPU hosted box, CDP starvation is already what produced the
// measured "auto-attach: failed to adopt new tab target ... timed out after
// 20s". A tab change is a human clicking a tab strip: low frequency, one
// extra round trip, paid only where it buys something. So the resize path
// stays exactly as cheap as it was, and only this path pays.
//
// Why the re-assert is not redundant with the caller's own
// activateTabInChrome (manager.go): that call is best-effort and its failure
// is a WARN log, nothing more — the exact silence this whole defect class
// lives in. This re-assert is an independent second attempt that resolves
// the tab through mgr.Session (which recreates a dead tab context) rather
// than through a context captured earlier, and it completes BEFORE the
// control frame is pushed, so the encoder never re-queries Chrome ahead of
// it.
//
// Runs on its own goroutine so a slow or wedged CDP round trip cannot add
// latency to the caller's tab switch, and coalesces: a second call while a
// worker is in flight sets a pending flag instead of spawning a second
// goroutine, and the worker loops once more. Coalescing is SAFE rather than
// lossy because the worker resolves the then-current model-active tab from
// scratch on each pass — two rapid switches converge on the last one, which
// is the correct answer anyway. A no-op once Stop() has run.
//
// Carries no expected geometry — see RecaptureForTabChangeAt for the variant
// a caller that HAS a CDP-verified measurement (live.go's post-viewport-
// re-apply tab-change path) must prefer.
func (cs *CaptureSession) RecaptureForTabChange() {
	cs.RecaptureForTabChangeAt(0, 0)
}

// RecaptureForTabChangeAt is RecaptureForTabChange carrying the CDP-verified
// CSS viewport the encoder should converge on (0,0 = absent — see
// RecaptureAt's doc comment for why a verified measurement beats the
// encoder's own chrome.tabs.get stability poll, which two equally-stale reads
// can satisfy).
//
// Why this exists (round-2 finding F3, 2026-08-16): the foreground re-assert
// was reachable ONLY from BrowserManager.SwitchTab's "the model did not move"
// branch — the rare recovery path — while the ORDINARY tab switch went
// LiveView.onTabsChanged -> plain Recapture(), with no re-assert at all. That
// is backwards: the re-assert exists precisely because
// BrowserManager.activateTabInChrome is best-effort and its failure is a WARN
// log and nothing more, so the path a user takes on every single tab click is
// the one that most needs a second, independent attempt. live.go's tab-change
// path now comes through here — and it has a verified viewport to offer by
// then (it re-applies the panel's viewport to the new target first, because
// Chrome's deviceScaleFactor override is per TARGET), which is why the
// geometry-carrying variant is the one it needs.
//
// The dimensions are stored rather than captured by the worker goroutine, so
// a call that merely coalesces into a running worker still gets ITS geometry
// used on the next pass.
func (cs *CaptureSession) RecaptureForTabChangeAt(expectedW, expectedH int) {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return
	}
	cs.tabChangeRecaptureW, cs.tabChangeRecaptureH = expectedW, expectedH
	if cs.tabChangeRecaptureRunning {
		cs.tabChangeRecapturePending = true
		cs.mu.Unlock()
		return
	}
	cs.tabChangeRecaptureRunning = true
	// Same reason as RecaptureAt: a tab-change recapture is a real teardown,
	// and the loss it produces must not be read as a death (#674).
	cs.recapturePendingUntil = time.Now().Add(ingestRecoverySettle)
	cs.mu.Unlock()

	// Prime attached viewers for the coming gap NOW, on the caller's own
	// goroutine, rather than behind the foreground re-assert. SignalRecapture
	// is pure relay-side signalling (an immediate + bursted PLI) with no CDP
	// in it, and there is no reason to make a viewer wait for a browser round
	// trip before it starts recovering. The ENCODER's half — the control frame
	// that makes it re-bind chrome.tabCapture — is the half that genuinely
	// must come after the re-assert, and that is the half the worker still
	// owns. Exactly one signal per pass either way; only the first one moved
	// earlier.
	cs.relay.SignalRecapture()

	go func() {
		firstPass := true
		for {
			cs.mu.Lock()
			w, h := cs.tabChangeRecaptureW, cs.tabChangeRecaptureH
			cs.mu.Unlock()

			if !firstPass {
				cs.relay.SignalRecapture()
			}
			firstPass = false

			cs.assertForeground(context.Background())
			cs.noteRecaptureIssued()
			cs.requestControl("recapture", nil, w, h)

			cs.mu.Lock()
			if !cs.tabChangeRecapturePending || cs.stopped {
				cs.tabChangeRecaptureRunning = false
				cs.tabChangeRecapturePending = false
				cs.mu.Unlock()
				return
			}
			cs.tabChangeRecapturePending = false
			cs.mu.Unlock()
		}
	}()
}

// assertForeground routes the foreground re-assert through the
// foregroundAssertFn test seam when one is installed, and to the real
// bringAgentTabToFront otherwise. Best-effort in both cases: the boolean is
// informational (bringAgentTabToFront logs its own failures), and a false
// result never blocks the recapture — a recapture that binds the wrong tab
// is still strictly better than no recapture at all, which is the state that
// left the picture frozen on the old tab indefinitely.
func (cs *CaptureSession) assertForeground(ctx context.Context) bool {
	cs.mu.Lock()
	fn := cs.foregroundAssertFn
	cs.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return cs.bringAgentTabToFront(ctx)
}

// requestControl pushes a browser_capture_control{action, reason,
// expected_width?, expected_height?} frame to the bound ingest connection,
// if any. expectedW/expectedH carry the CDP-verified CSS viewport a
// recapture should converge on (0,0 = absent — see RecaptureAt's doc
// comment); Stop's shutdown call always passes 0,0, since there is no
// geometry to convey on teardown. Errors are logged, not returned — every
// call site (RecaptureAt, Stop) is best-effort: the encoder's own reconnect
// watchdog (encoder.js) and this session's own Stop() teardown are what
// actually guarantee termination, not a successfully-delivered control
// frame.
// requestControlBitrate pushes browser_capture_control{set_bitrate}. Separate
// from requestControl because it is the only action that carries max_bitrate,
// and threading a bitrate through every recapture/shutdown call site would
// make the common path lie about what it sends.
func (cs *CaptureSession) requestControlBitrate(bps int) {
	cs.mu.Lock()
	send := cs.ingestSend
	cs.mu.Unlock()
	if send == nil {
		return
	}
	if err := send("set_bitrate", nil, 0, 0, bps); err != nil {
		cs.logf("capture[%s]: send control set_bitrate failed: %v", cs.agentID, err)
	}
}

func (cs *CaptureSession) requestControl(action string, reason *string, expectedW, expectedH int) {
	cs.mu.Lock()
	send := cs.ingestSend
	cs.mu.Unlock()
	if send == nil {
		return
	}
	if err := send(action, reason, expectedW, expectedH, 0); err != nil {
		cs.logf("capture[%s]: send control %s failed: %v", cs.agentID, action, err)
	}
}

// AddViewer registers viewerID as an attached WebRTC viewer, canceling any
// pending grace-stop timer (wave-plan W2-A item 4). Idempotent for an
// already-registered viewerID (a fresh generation is still minted — see
// below).
//
// Returns a generation token unique to THIS call, stored as viewerID's
// current registration — pass it to RemoveViewerIfCurrent (via
// ViewerAttachHandle/CleanupViewerOffer), NOT the plain, unconditional
// RemoveViewer, when tearing down a failed or superseded-before-commit offer
// attempt: two attempts to attach the SAME viewerID can be in flight at
// once (e.g. a losing offer's cleanup running after a winning offer for the
// same viewerID has already re-registered it), and only the CURRENT
// generation's own cleanup may remove the entry — see
// CleanupViewerOffer's doc comment for the incident this closes. Existing
// callers that don't need this (the plain, unconditional RemoveViewer's own
// legitimate callers — an explicit browser_detach, or a relay-confirmed
// eviction notification) are unaffected: discarding the return value is
// valid Go.
func (cs *CaptureSession) AddViewer(viewerID string) uint64 {
	cs.mu.Lock()
	cs.viewerGenSeq++
	gen := cs.viewerGenSeq
	// A fresh registration always starts with no recorded relay identity —
	// recordViewerRelayHandle fills it in once HandleViewerOffer knows one
	// (there is a real window here, inside handleWebRTCOffer, between this
	// call and the relay actually registering a connection).
	cs.viewers[viewerID] = viewerRegistration{gen: gen}
	if cs.stopTimer != nil {
		cs.stopTimer.Stop()
		cs.stopTimer = nil
	}
	cs.mu.Unlock()
	return gen
}

// armGraceStopLocked arms the captureGracePeriod stop timer (wave-plan W2-A
// item 4: "stopped on last detach with a ~30s grace timer") if no viewers
// remain and one isn't already armed or the session already stopped — shared
// by RemoveViewer and RemoveViewerIfCurrent. Caller must hold cs.mu.
func (cs *CaptureSession) armGraceStopLocked() {
	if len(cs.viewers) == 0 && cs.stopTimer == nil && !cs.stopped {
		cs.stopTimer = time.AfterFunc(captureGracePeriod, cs.Stop)
	}
}

// RemoveViewer unconditionally unregisters viewerID, regardless of which
// generation (AddViewer call) is currently registered for it. Both of this
// package's OWN identity-sensitive teardown paths — an explicit
// browser_detach/WS-close (pkg/gateway/browser_webrtc.go's
// detachWebRTCViewer) and a relay-confirmed eviction notification
// (viewerRemover's SetOnViewerRemoved wiring in newCaptureSessionWithDeps) —
// now go through the identity-checked CleanupViewerOffer/
// removeViewerByRelayHandle instead of this method (fix-wave findings: both
// were found to bypass the identity check that HandleViewerOffer's own
// failure-cleanup path already used, exactly the class of bug
// RemoveViewerIfCurrent/CleanupViewerOffer exist to prevent — see their doc
// comments). RemoveViewer itself is kept as a general-purpose, correct
// primitive for any FUTURE caller that has its own independent guarantee
// that viewerID's CURRENT registration is the one to remove (no such
// guarantee exists for cleaning up a specific HandleViewerOffer attempt that
// may have already been superseded by a newer one for the same viewerID —
// that case MUST use RemoveViewerIfCurrent/CleanupViewerOffer, whose doc
// comments detail the race this distinction avoids), and is exercised
// directly by this package's own tests to document exactly why the
// unconditional variant is unsafe for offer-cleanup/eviction-notification
// use (TestCaptureSession_RemoveViewer_UnconditionalVariant_WouldClobberNewerAttempt).
//
// If no viewers remain, arms a captureGracePeriod timer that calls Stop()
// when it fires — giving a viewer that's merely reloading the panel a
// window to reconnect without tearing down and re-provisioning the whole
// encoder page. A subsequent AddViewer within the grace window cancels the
// timer.
func (cs *CaptureSession) RemoveViewer(viewerID string) {
	cs.mu.Lock()
	delete(cs.viewers, viewerID)
	cs.armGraceStopLocked()
	cs.mu.Unlock()
}

// RemoveViewerIfCurrent unregisters viewerID ONLY if gen — the generation
// token AddViewer returned for THIS SPECIFIC registration attempt — still
// matches the currently-registered generation for viewerID. A safe no-op if
// a NEWER AddViewer call for the same viewerID has since superseded it (a
// winning offer that raced this one), or if viewerID isn't registered at
// all. See CleanupViewerOffer's doc comment for the full rationale and
// RemoveViewer's doc comment for why the PLAIN (unconditional) variant
// remains correct for its own, different callers.
func (cs *CaptureSession) RemoveViewerIfCurrent(viewerID string, gen uint64) {
	cs.mu.Lock()
	if cur, exists := cs.viewers[viewerID]; !exists || cur.gen != gen {
		cs.mu.Unlock()
		return
	}
	delete(cs.viewers, viewerID)
	cs.armGraceStopLocked()
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
	close(cs.done)
	if cs.stopTimer != nil {
		cs.stopTimer.Stop()
		cs.stopTimer = nil
	}
	// A pending recovery evaluation must not outlive the session it would
	// recapture (#674) — Recapture on a stopped session is a no-op, but the
	// timer would keep a dead CaptureSession reachable and log noise flowing.
	cs.stopIngestRecoveryLocked()
	tabCancel := cs.tabCancel
	viewerIDs := make([]string, 0, len(cs.viewers))
	for id := range cs.viewers {
		viewerIDs = append(viewerIDs, id)
	}
	onStopped := cs.onStopped
	cs.mu.Unlock()

	shutdownReason := "capture session stopping"
	cs.requestControl("shutdown", &shutdownReason, 0, 0)

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
