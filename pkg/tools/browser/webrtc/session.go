//go:build !lite

package webrtc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

// pliForwardMinInterval throttles viewer-triggered PLI/FIR forwarding to the
// ingest connection (drainViewerRTCP -> forwardPLIThrottled, viewer.go) to
// at most once per interval ACROSS EVERY attached viewer combined -- a PLI
// storm from N viewers all reporting loss around the same time would
// otherwise force the encoder into constant keyframes, collapsing bitrate
// exactly when the network is already struggling (fix-wave finding 1; see
// forwardPLIThrottled's doc comment in ingest.go). A var (not const) purely
// as a test seam, mirroring audioGraceTimeout/captureGracePeriod's
// established pattern in this package.
var pliForwardMinInterval = 750 * time.Millisecond

// Available reports whether this build compiles in the real Pion-backed
// Session (true here; false in stub.go's lite build). Exported so a caller
// deciding the ADR-047 D3 gate ladder (wave-plan W2-A: "WebRTCEnabled, then
// lite/webrtc.ErrUnavailable, then ClassifyVideoCapability, else attempt a
// real offer") can distinguish "this binary has no WebRTC at all" from a
// runtime failure WITHOUT first standing up a capture session/encoder page
// just to provoke an ErrUnavailable from a real call.
var Available = true

// Session is the SFU-style forwarding state shared by the ingest leg (the
// headless-Chrome tabCapture encoder page connects via HandleIngestOffer) and
// the viewer legs (external browsers connect via HandleViewerOffer). Exactly
// one active ingest connection is modeled at a time; a new ingest offer
// replaces the old one cleanly (HandleIngestOffer) while every existing
// viewer keeps working because both media kinds are backed by a single,
// long-lived TrackLocalStaticRTP each -- see attachIngestTrack in ingest.go
// for the two things that make that safe: (1) Pion rewrites SSRC/PayloadType
// per-viewer-binding on every write, so the shared local track is decoupled
// from whichever upstream ingest connection is currently feeding it, and (2)
// this package additionally rewrites each packet's SEQUENCE NUMBER by a
// constant per-connection offset anchored on a session-lifetime high-water
// mark per kind (videoLastOutSeq/audioLastOutSeq below) --
// without that second rewrite, a fresh ingest connection's independently-
// randomized packetizer sequence numbers cause every already-attached
// viewer's SRTP receive window to silently discard the "replayed"/
// out-of-window packets, which is invisible at this layer (Write returns no
// error) and was caught only by the Go<->Go ingest-replacement test
// asserting the VIEWER side actually keeps receiving packets, not just that
// the local track accepted the write.
//
// Adapted from the proven spike at spikes/wv1-webrtc/q4-bidir/{relay,ingest,
// viewer,inputdc}.go; deviations from that code are documented at each call
// site (the main ones being the two shared-track-survives-replacement fixes
// above -- the spike recreated a new local track on every ingest reconnect,
// which would have silently orphaned already-attached viewers, and never
// exercised a persistent viewer across a reconnect so the sequence-number
// issue never surfaced there).
type Session struct {
	api *webrtc.API
	cfg Config

	// apiViewer builds the VIEWER leg only; s.api builds the loopback ingest
	// leg. See NewSession for why they must not share a SettingEngine.
	// Never nil after NewSession (it aliases s.api in the degraded paths).
	apiViewer *webrtc.API
	sink      InputSink
	logfn     func(string, ...any)

	mu     sync.Mutex
	closed bool
	// onIngestLost is invoked (in its own goroutine, no lock held) when the
	// installed ingest connection dies — see the OnConnectionStateChange
	// handler in ingest.go. The owner uses it to ask the encoder for a fresh
	// capture; nil is a valid no-op.
	onIngestLost func()
	ingestPC     *webrtc.PeerConnection
	videoTrack   *webrtc.TrackLocalStaticRTP
	audioTrack   *webrtc.TrackLocalStaticRTP
	videoSSRC    webrtc.SSRC
	videoCodec   string
	audioCodec   string

	viewersMu sync.Mutex
	viewers   map[string]*viewerConn

	videoPktCount atomic.Int64
	audioPktCount atomic.Int64
	pliBursting   atomic.Bool

	// videoLastOutSeq/audioLastOutSeq are the session-lifetime OUTGOING
	// sequence-number high-water marks (RFC 1982 16-bit serial space, stored
	// widened) for the two shared local tracks. attachIngestTrack anchors
	// each new ingest connection's constant rewrite offset on them, and
	// advances them only forward as packets are forwarded. See the long
	// comment on attachIngestTrack in ingest.go for why a rewrite -- not
	// just the SSRC/PayloadType rewriting Pion already does per viewer
	// binding -- is required for ingest replacement to work at all, and the
	// forward-loop comment there for why it must be a constant offset, never
	// read-order renumbering (2026-08-13 corruption incident).
	videoLastOutSeq atomic.Uint32
	audioLastOutSeq atomic.Uint32

	connSeq atomic.Int64

	// pliForwardMu/lastPLIForwardAt implement pliForwardMinInterval's
	// cross-viewer throttle for viewer-triggered PLI/FIR forwarding -- see
	// forwardPLIThrottled in ingest.go. Deliberately a SEPARATE lock from mu
	// (which sendPLI itself takes internally) so a viewer's RTCP-drain
	// goroutine calling this never contends with the ingest/attach hot path.
	pliForwardMu     sync.Mutex
	lastPLIForwardAt time.Time

	// viewerRemovedMu/onViewerRemoved back SetOnViewerRemoved -- see its doc
	// comment. A separate lock from mu/viewersMu/pliForwardMu so a caller
	// invoking the callback (removeViewer, viewer.go) never contends with the
	// hot paths those other locks guard.
	viewerRemovedMu sync.Mutex
	onViewerRemoved func(viewerID string, handle any)
}

// viewerConn tracks one attached viewer's PeerConnection plus its "input"
// data channel (nil until the viewer opens it, which happens asynchronously
// after the answer is sent -- SendToViewer must tolerate that window).
//
// senders backs an explicit, code-owned termination path for this viewer's
// per-connection goroutines -- see removeViewer's doc comment (viewer.go)
// for the CI incident this closes: this package must not rely SOLELY on
// Pion's own close cascade (PeerConnection.Close -> RTPTransceiver.Stop ->
// RTPSender.Stop for drainViewerRTCP; separately PeerConnection.Close ->
// SCTPTransport.Stop -> sctpAssociation.Abort -> per-stream read error ->
// DataChannel.OnClose for runInputQueue) to unblock those goroutines'
// blocking reads promptly -- that cascade is correct on a clean/fast
// transport but is several hops deep through a third-party library and,
// under real network conditions (packet loss, a degraded transport), was
// observed to leave both goroutines blocked well past a 60s bound.
// removeViewer/CloseViewer (via stopViewerConn) now call Stop() on senders
// AND dc.Close() directly and synchronously, in addition to (not instead
// of) the existing pc.Close() teardown -- dc.Close() (unlike relying on the
// whole SCTP association's abort) tears down JUST this one data channel's
// underlying stream directly, which is what runInputQueue's dc.OnClose
// handler (inputdc.go, unchanged) is already waiting on to fire.
type viewerConn struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel
	// senders holds every RTPSender this viewer's PeerConnection negotiated
	// (video, and audio if present) -- removeViewer/CloseViewer call
	// Stop() on each directly so drainViewerRTCP's blocking sender.Read()
	// unblocks deterministically, without waiting on pc.Close()'s own
	// (correct, but asynchronous) teardown to reach the same call.
	senders []*webrtc.RTPSender
	// handle is the EXACT *ViewerHandle instance minted for THIS registration
	// (HandleViewerOfferHandle, viewer.go) -- stored here, at construction,
	// before this viewerConn is ever published into s.viewers, so it can be
	// handed back verbatim by removeViewer's onViewerRemoved notification
	// (GAP 2 fix-wave finding: the notification previously carried only a
	// bare viewerID, giving a caller -- CaptureSession -- no way to tell a
	// legitimate eviction of THIS registration apart from a stale
	// notification racing a newer registration for the same viewerID; see
	// SetOnViewerRemoved's doc comment). Never mutated after construction, so
	// reading it in removeViewer under s.viewersMu is race-free even though
	// the pointer was set outside that lock.
	handle *ViewerHandle
}

// NewSession builds a Session backed by a fresh Pion API: an explicit
// MediaEngine with the default codec set (VP8/H264 + Opus, among others) so
// Chrome's tabCapture-derived offer negotiates cleanly, and the default
// Interceptor registry (NACK/RTCP reports -- what makes getStats() on the
// browser side report framesDecoded/audioLevel etc). sink receives every
// "input" data-channel message from every viewer; logf receives structured
// log lines (may be nil, in which case logging is a no-op).
func NewSession(cfg Config, sink InputSink, logf func(string, ...any)) *Session {
	s := &Session{
		cfg:     cfg,
		sink:    sink,
		logfn:   logf,
		viewers: make(map[string]*viewerConn),
	}

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		// RegisterDefaultCodecs only fails on a duplicate/invalid static
		// registration, which cannot happen on a fresh MediaEngine -- but if
		// it ever does, fail loudly rather than silently building a Session
		// that can never negotiate media.
		s.logf("webrtc: register default codecs failed: %v (session will reject all offers)", err)
		s.api = webrtc.NewAPI()
		s.apiViewer = s.api
		return s
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		s.logf("webrtc: register default interceptors failed: %v (session will reject all offers)", err)
		s.api = webrtc.NewAPI()
		s.apiViewer = s.api
		return s
	}

	se := webrtc.SettingEngine{}
	// Always gather loopback candidates in ADDITION to normal host
	// candidates. This never changes production behavior (Fly pods have a
	// real interface, so this just adds an extra, usually-useless candidate)
	// but makes same-host connectivity -- including the Go<->Go unit tests in
	// session_test.go -- robust on any runner, including ones where only
	// "lo" is present.
	se.SetIncludeLoopbackCandidate(true)

	// ADR-062 tier 1 applies to the VIEWER leg ONLY, so the settings are
	// split in two from here on.
	//
	// The legs are not alike and must not share a SettingEngine. The INGEST
	// leg is the gateway talking to its OWN headless Chrome over loopback;
	// it needs no public address and no shared socket. The VIEWER leg is a
	// browser somewhere on the internet. Rewriting host candidates to a
	// public address on the shared engine would hand the loopback encoder an
	// address it cannot reach -- breaking capture on EVERY install,
	// including laptops, in exchange for fixing hosted ones. (Caught in
	// adversarial review of ADR-062 before it shipped; both legs run through
	// buildPeerConnection, which made the blast radius easy to miss.)
	viewerSE := se
	if cfg.MediaConn != nil {
		// One gateway-owned socket, shared by every agent's Session: Pion's
		// UDP mux demultiplexes concurrent ICE agents on it by ufrag. See
		// Config.MediaConn for why a per-Session bind would break the
		// second agent.
		viewerSE.SetICEUDPMux(webrtc.NewICEUDPMux(nil, cfg.MediaConn))
	}
	if cfg.MediaTCP != nil {
		// ADR-062 tier 2. Default Pion network types omit TCP; without
		// SetNetworkTypes the mux is installed and never advertised.
		viewerSE.SetICETCPMux(webrtc.NewICETCPMux(nil, cfg.MediaTCP, 8))
		viewerSE.SetNetworkTypes([]webrtc.NetworkType{
			webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6, webrtc.NetworkTypeTCP4,
		})
	}
	if len(cfg.PublicIPs) > 0 {
		// ICECandidateTypeHost with Pion's default mode APPENDS for srflx and
		// replaces for host. The socket really is reachable at this address
		// once the provider routes the fixed port, so "host" is honest and
		// earns its higher priority; the loopback/private candidates remain
		// in the list for same-host viewers.
		viewerSE.SetNAT1To1IPs(cfg.PublicIPs, webrtc.ICECandidateTypeHost)
	}

	s.api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	s.apiViewer = webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(ir),
		webrtc.WithSettingEngine(viewerSE),
	)
	if cfg.MediaConn != nil || cfg.MediaTCP != nil || len(cfg.PublicIPs) > 0 {
		s.logf("webrtc: viewer leg using fixed media udp=%v tcp=%v public=%v", cfg.MediaConn != nil, cfg.MediaTCP != nil, cfg.PublicIPs)
	}
	return s
}

// SetOnIngestLost registers cb, invoked when the installed ingest connection
// dies (failed/closed/disconnected). The owner uses it to ask the encoder for a
// fresh capture — without it a dead ingest is never noticed and every later PLI
// write fails against a closed pipe, freezing the stream on its last frame.
// Safe to call at any time; pass nil to unregister.
func (s *Session) SetOnIngestLost(cb func()) {
	s.mu.Lock()
	s.onIngestLost = cb
	s.mu.Unlock()
}

func (s *Session) logf(format string, args ...any) {
	if s.logfn != nil {
		s.logfn(format, args...)
	}
}

// nextConnID returns a small monotonic counter used only for log-line
// correlation (ingest-N / viewer-N prefixes), mirroring the spike's
// nextConnID().
func (s *Session) nextConnID() int64 {
	return s.connSeq.Add(1)
}

// buildPeerConnection returns a PeerConnection configured with the
// Session's STUN policy (empty Config.StunServer -> host candidates only,
// per wave-plan decision 7).
// buildPeerConnection builds a PC on the api the CALLER names, because the
// two legs need different ICE settings (ADR-062): pass s.api for the loopback
// ingest leg, s.apiViewer for a viewer. Making it a parameter rather than a
// field read means a new call site has to state which leg it is, instead of
// silently inheriting whichever engine happened to be default.
func (s *Session) buildPeerConnection(api *webrtc.API) (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{}
	if s.cfg.StunServer != "" {
		config.ICEServers = []webrtc.ICEServer{{URLs: []string{s.cfg.StunServer}}}
	}
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("webrtc: new peer connection: %w", err)
	}
	return pc, nil
}

// SignalRecapture is a hook the gateway calls when the encoder's tab
// selection changes (active-tab switch -- wave-plan decision 2). This
// package never talks to the Chrome extension directly (that control message
// travels over the gateway-owned capture-ingest WS, outside this package's
// scope per the W1-C task boundary), so there is nothing for it to signal
// downstream. What IS this package's responsibility: once the encoder
// recaptures and sends a fresh ingest offer, HandleIngestOffer already
// handles that as an ordinary ingest replacement. The one useful thing this
// hook can do proactively is prime viewers for the resulting brief video gap
// by requesting a fresh keyframe, so playback recovers as fast as possible
// once the new ingest track's first frames land -- so it triggers the same
// PLI burst used for a newly-joining viewer. Safe to call with no ingest
// connected (no-op).
func (s *Session) SignalRecapture() {
	s.pliBurstForNewViewer("[recapture]")
}

// Stats returns a point-in-time snapshot of the relay state.
func (s *Session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.viewersMu.Lock()
	viewers := len(s.viewers)
	s.viewersMu.Unlock()

	return Stats{
		Viewers:      viewers,
		HasVideo:     s.videoTrack != nil,
		HasAudio:     s.audioTrack != nil,
		VideoCodec:   s.videoCodec,
		AudioCodec:   s.audioCodec,
		VideoPackets: s.videoPktCount.Load(),
		AudioPackets: s.audioPktCount.Load(),
	}
}

// SetOnViewerRemoved registers fn to be invoked, with the evicted viewer's
// ID and the relay-identity handle that registration held (dynamically a
// *ViewerHandle -- see ViewerHandle's doc comment for why this is declared
// `any` rather than the concrete type), whenever THIS SESSION -- not an
// external caller -- decides a viewer's PeerConnection is gone: a terminal
// ICE/PeerConnection state (Failed/Closed) or an unrecovered Disconnected
// state past disconnectGracePeriod (see removeViewer in viewer.go, the sole
// call site, and CloseViewerIfCurrent, which delegates to it). This is how a
// caller (CaptureSession, pkg/tools/browser/capture_session.go) learns of a
// RELAY-SIDE-ONLY eviction that the signaling layer above never sees a
// browser_detach frame for -- e.g. an ICE failure while the browser_ws
// connection stays open and the SPA silently falls back to its JPEG sink
// (fix-wave incident: "nothing resumes the JPEG screencast once WebRTC dies
// mid-session").
//
// GAP 2 fix-wave finding: the handle parameter (added alongside the
// pre-existing viewerID one) lets CaptureSession identity-check this
// notification against whichever registration it currently has recorded for
// viewerID (recordViewerRelayHandle) before removing anything, via
// RemoveViewerIfCurrent -- previously the notification carried only a bare
// viewerID, so a legitimate eviction of an OLD, already-superseded relay
// connection racing a NEWER AddViewer registration for the same viewerID
// could delete the newer registration instead (the relay's OWN
// cur.pc==pc identity check inside removeViewer already prevented a
// stale notification from firing at all -- this closes the SAME class of gap
// one layer up, at the CaptureSession/relay boundary, where no such check
// previously existed).
//
// fn is invoked on whatever goroutine removeViewer runs on (Pion's own
// PeerConnection state-change callback, or the disconnectGracePeriod
// time.AfterFunc) -- AFTER every Session-internal lock removeViewer holds has
// already been released, so fn is free to call back into this Session (e.g.
// Stats()) without risking a self-deadlock. Not invoked for an explicit
// CloseViewer(viewerID) -- every CloseViewer caller already knows the viewer
// is gone and drives its own cleanup directly; it IS invoked via
// CloseViewerIfCurrent, since that method is itself just an identity-checked
// call into removeViewer.
//
// A nil fn (the zero value, if SetOnViewerRemoved is never called) makes
// removeViewer's notification a no-op, matching InputSink's own nil-is-valid
// convention. Intended to be called exactly once, right after NewSession,
// before any offer can possibly be handled -- see capture_session.go's
// newCaptureSessionWithDeps.
func (s *Session) SetOnViewerRemoved(fn func(viewerID string, handle any)) {
	s.viewerRemovedMu.Lock()
	s.onViewerRemoved = fn
	s.viewerRemovedMu.Unlock()
}

// notifyViewerRemoved invokes the registered onViewerRemoved callback, if
// any -- see SetOnViewerRemoved's doc comment. Never called while any
// Session-internal lock is held (removeViewer's sole call site is after its
// own s.viewersMu.Unlock()).
func (s *Session) notifyViewerRemoved(viewerID string, handle any) {
	s.viewerRemovedMu.Lock()
	fn := s.onViewerRemoved
	s.viewerRemovedMu.Unlock()
	if fn != nil {
		fn(viewerID, handle)
	}
}

// Close tears down the ingest connection and every viewer connection. The
// Session must not be used afterwards.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ingest := s.ingestPC
	s.ingestPC = nil
	s.mu.Unlock()

	var errs []error
	if ingest != nil {
		if err := ingest.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ingest: %w", err))
		}
	}

	s.viewersMu.Lock()
	viewers := s.viewers
	s.viewers = make(map[string]*viewerConn)
	s.viewersMu.Unlock()

	// Fix-wave finding: this is the FOURTH viewer-teardown site in this
	// package (removeViewer, CloseViewer, and HandleViewerOfferHandle's
	// same-viewerID replace branch are the other three) and was the one
	// still calling vc.pc.Close() WITHOUT stopViewerConn(vc) first -- exactly
	// the gap those three were hardened to close (see viewerConn's doc
	// comment, session.go, and stopViewerConn's doc comment, viewer.go): under
	// real network conditions, pc.Close()'s own close cascade alone can leave
	// drainViewerRTCP/runInputQueue blocked well past a 60s bound. A full
	// Session.Close() tearing down every attached viewer at once (e.g. an
	// agent shutdown with several live viewers) is exactly the same leak
	// class, just at shutdown instead of a single-viewer eviction.
	for id, vc := range viewers {
		s.stopViewerConn(vc)
		if err := vc.pc.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close viewer %s: %w", id, err))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	msg := "webrtc: session close encountered errors:"
	for _, e := range errs {
		msg += " " + e.Error() + ";"
	}
	return fmt.Errorf("%s", msg)
}
