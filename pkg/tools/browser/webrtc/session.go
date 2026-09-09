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
// Session. Since ADR-067 §10 step 14 retired the `lite` build variant (and
// with it stub.go, the only place this was ever false), it is true in every
// build; it stays a var, not a const, because pkg/gateway's gate ladder
// (ADR-047 D3 / wave-plan W2-A: "WebRTCEnabled, then WebRTC availability,
// then ClassifyVideoCapability, else attempt a real offer") still reads it
// and its tests still flip it to exercise the reason="lite_build" branch.
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

	// onBitrateTarget is invoked (no lock held) when the viewer leg's own RTCP
	// receiver reports move the congestion target. ADR-069 Finding 2: without
	// this the encoder only ever sees the loopback ingest hop and encodes for
	// a link that does not exist.
	onBitrateTarget func(bps int)
	// bitrateTarget is the current target in bits/sec (0 = none computed yet).
	bitrateTarget int
	// lastBitratePush throttles how often a new target reaches the encoder.
	lastBitratePush time.Time
	ingestPC        *webrtc.PeerConnection
	videoTrack      *webrtc.TrackLocalStaticRTP
	audioTrack      *webrtc.TrackLocalStaticRTP
	videoSSRC       webrtc.SSRC
	videoCodec      string
	audioCodec      string

	// feedSeq mints the tokens recorded in videoFeedID/audioFeedID — one per
	// attachIngestTrack invocation, monotonic for the Session's lifetime so a
	// token is never reused and a stale goroutine can never "un-retire" a
	// newer feed.
	feedSeq atomic.Int64
	// videoFeedID/audioFeedID identify the attachIngestTrack invocation that
	// is CURRENTLY forwarding RTP into videoTrack/audioTrack. 0 means nothing
	// is: the shared local track still exists, but no ingest connection is
	// writing to it, so answering a viewer from it would produce a black
	// panel.
	//
	// ISSUE #674 — why a token and NOT nil-ing videoTrack/audioTrack.
	//
	// The bug: videoTrack was assigned in exactly one place (attachIngestTrack)
	// and never set back to nil — Close() cleared only ingestPC. waitForTracks
	// returned ok as soon as videoTrack != nil, with no check that anything
	// still fed it, so after the FIRST successful ingest every later viewer
	// offer was answered instantly against a dead track and the panel never
	// recovered, however many times the user reopened it.
	//
	// The obvious fix — nil the pointers when the ingest dies — is the WRONG
	// one here, and would trade a dead panel for a worse, subtler failure. The
	// shared TrackLocalStaticRTP is not an implementation detail of one ingest
	// connection: it is the binding every ALREADY-ATTACHED viewer's RTPSender
	// holds (see Session's doc comment and attachIngestTrack's). Nil-ing it
	// makes the next attachIngestTrack construct a BRAND NEW local track, which
	// no existing viewer is bound to — every viewer that survived the blip
	// would go silently black forever, with no error anywhere, which is
	// precisely the class of failure the sequence-number rewrite exists to
	// prevent. The long-lived shared track is load-bearing; its LIVENESS is a
	// separate fact, so it gets separate state.
	//
	// Retired in three places, all of which must stay: the forwarding
	// goroutine's own exit (endFeed, the most precise signal — the source
	// really has stopped), clearIngestIfCurrent (the connection died, so no
	// feed of it can be alive even if its read loop has not unblocked yet),
	// and Close(). Guarded by mu, like the tracks themselves.
	videoFeedID int64
	audioFeedID int64

	// onIngestLive is invoked (in its own goroutine, no lock held) the moment
	// a VIDEO feed starts forwarding into the shared local track — the exact
	// counterpart to onIngestLost. The owner uses it to retire a bounded
	// automatic-recovery attempt sequence and tell the panel video is back;
	// without a positive "it worked" signal, recovery can only ever be timed
	// out, never confirmed. nil is a valid no-op.
	onIngestLive func()

	viewersMu sync.Mutex
	viewers   map[string]*viewerConn

	videoPktCount atomic.Int64
	audioPktCount atomic.Int64
	pliBursting   atomic.Bool
	// pliDeferred records that a keyframe request was asked for while the
	// ingest connection was not yet able to carry one (see sendPLI's
	// not-connected branch). attachIngestTrack redeems it via flushDeferredPLI
	// the moment a video track arrives on a live connection.
	pliDeferred atomic.Bool

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

// hostedViewerLeg reports whether the VIEWER leg has a declared public
// address, i.e. whether this gateway can honestly say "connect to me here".
// It is the single source of truth for the two settings that must never
// disagree: ICE-Lite (NewSession) and skipping STUN (buildPeerConnection).
func hostedViewerLeg(cfg Config) bool {
	return len(cfg.PublicIPs) > 0
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
	// Forward pion's OWN internal logging (ICE agent, DTLS, mux) into this
	// Session's log sink. Without this, pion uses its default factory, which
	// is LogLevelError AND writes to a stderr that never reaches gateway.log
	// -- so the one line that names the cause of a loopback ICE failure,
	// pion/ice's Warn "Failed to discover mDNS candidate <name>: <err>", was
	// discarded twice over. See icediag.go's header and pionLogEnv.
	se.LoggerFactory = &pionLogBridge{level: pionLogLevel(), logf: s.logf}
	// Always gather loopback candidates in ADDITION to normal host
	// candidates. This never changes production behavior (Fly pods have a
	// real interface, so this just adds an extra, usually-useless candidate)
	// but makes same-host connectivity -- including the Go<->Go unit tests in
	// session_test.go -- robust on any runner, including ones where only
	// "lo" is present.
	se.SetIncludeLoopbackCandidate(true)

	// ADR-069 tier 1 applies to the VIEWER leg ONLY, so the settings are
	// split in two from here on.
	//
	// The legs are not alike and must not share a SettingEngine. The INGEST
	// leg is the gateway talking to its OWN headless Chrome over loopback;
	// it needs no public address and no shared socket. The VIEWER leg is a
	// browser somewhere on the internet. Rewriting host candidates to a
	// public address on the shared engine would hand the loopback encoder an
	// address it cannot reach -- breaking capture on EVERY install,
	// including laptops, in exchange for fixing hosted ones. (Caught in
	// adversarial review of ADR-069 before it shipped; both legs run through
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
		// ADR-069 tier 2. Default Pion network types omit TCP entirely, so the
		// mux would be installed and never advertised. Widen to include TCP;
		// deliberately KEEP both UDP families -- an earlier revision narrowed
		// this to UDP4 only, which silently removed every IPv6 host candidate
		// and made an IPv6-only viewer unconnectable. The Fly 6PN/ephemeral
		// candidates that motivated that narrowing are removed by the
		// fly-global-services bind and by ICE-Lite dropping srflx, not by the
		// network-type list (with a UDP mux, pion's gatherCandidatesLocalUDPMux
		// ignores networkTypes and just enumerates the mux's addresses).
		viewerSE.SetICETCPMux(webrtc.NewICETCPMux(nil, cfg.MediaTCP, 8))
		viewerSE.SetNetworkTypes([]webrtc.NetworkType{
			webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6, webrtc.NetworkTypeTCP4,
		})
	}
	if hostedViewerLeg(cfg) {
		// We have a declared public address, so this is client-to-SERVER: we
		// listen and say where, and the viewer checks toward us. ICE-Lite says
		// exactly that, and it drops the server-reflexive candidates that a
		// hosted box gathers but no viewer can use (Fly answer dump
		// 2026-08-17: 138.199.24.232:36534 + a 6PN address alongside the real
		// one; Chrome tried them and never completed DTLS).
		//
		// Gated on PublicIPs ALONE, not on a fixed media port: a self-hoster
		// who pins a port for a firewall forward but declares no public
		// address still NEEDS srflx (their NAT mapping is the only way in),
		// and lite would take it away.
		viewerSE.SetLite(true)
	}
	if len(cfg.PublicIPs) > 0 {
		// ICECandidateTypeHost with Pion's default mode APPENDS for srflx and
		// replaces for host. The socket really is reachable at this address
		// once the provider routes the fixed port, so "host" is honest and
		// earns its higher priority; the loopback/private candidates remain
		// in the list for same-host viewers.
		//
		// SetICEAddressRewriteRules replaces the deprecated SetNAT1To1IPs
		// (pion/webrtc v4.2.16). This is a like-for-like migration following
		// that deprecation note exactly: External is the old `ips` argument,
		// AsCandidateType the old `candidateType`, and Mode is deliberately
		// left at its zero value, ICEAddressRewriteModeUnspecified, which pion
		// documents as the LEGACY default — replace for host candidates,
		// append for server reflexive. Naming a Mode here would CHANGE the
		// behaviour the comment above describes rather than preserve it.
		//
		// The error is unreachable on this path: SetICEAddressRewriteRules
		// fails only for an empty rule set (one rule is always passed) or when
		// the legacy NAT1To1IPs field is also populated (nothing sets it now
		// that this call is gone). It is logged rather than dropped so that a
		// future pion release adding a validation case cannot turn the public
		// address silently into a no-op — which would leave every hosted
		// viewer advertising an unroutable private candidate.
		if err := viewerSE.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External:        cfg.PublicIPs,
			AsCandidateType: webrtc.ICECandidateTypeHost,
		}); err != nil {
			s.logf("webrtc: viewer leg could not advertise public addresses %v — pion rejected the ICE address rewrite rule: %v", cfg.PublicIPs, err)
		}
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
// SetOnBitrateTarget registers cb, invoked when the viewer leg's RTCP receiver
// reports move the congestion target (ADR-069 Finding 2). The owner wires it
// to a browser_capture_control{set_bitrate} push so the encoder finally
// congestion-controls against the VIEWER's link rather than the loopback
// ingest hop. Safe to call at any time; pass nil to unregister.
func (s *Session) SetOnBitrateTarget(cb func(bps int)) {
	s.mu.Lock()
	s.onBitrateTarget = cb
	s.mu.Unlock()
}

// noteViewerLoss feeds one RTCP receiver-report block into the congestion
// policy and pushes a new target to the encoder when it moves and the throttle
// allows. Returns the target it settled on (for tests).
func (s *Session) noteViewerLoss(fractionLost float64, now time.Time) int {
	s.mu.Lock()
	prev := s.bitrateTarget
	next := nextBitrateTarget(prev, bitrateSample{FractionLost: fractionLost})
	s.bitrateTarget = next
	cb := s.onBitrateTarget
	due := now.Sub(s.lastBitratePush) >= bitrateUpdateMinInterval
	// Compare against what the encoder is EFFECTIVELY using, not against
	// "unset": with no target yet the encoder is already running at its own
	// ceiling, so a first clean report that computes the ceiling has changed
	// nothing and must not cost a frame. A first LOSSY report has.
	effectivePrev := prev
	if effectivePrev <= 0 {
		effectivePrev = bitrateCeiling
	}
	changed := next != effectivePrev
	if cb != nil && changed && due {
		s.lastBitratePush = now
	}
	s.mu.Unlock()

	if cb != nil && changed && due {
		cb(next)
	}
	return next
}

func (s *Session) SetOnIngestLost(cb func()) {
	s.mu.Lock()
	s.onIngestLost = cb
	s.mu.Unlock()
}

// SetOnIngestLive registers cb, invoked once each time a VIDEO feed begins
// forwarding into the shared local track — i.e. video is genuinely flowing
// again. It is the positive half of the pair SetOnIngestLost opens: an owner
// running a bounded automatic recovery needs to know an attempt SUCCEEDED, or
// it can only ever exhaust its attempt budget on a stream that is already
// healthy. Safe to call at any time; pass nil to unregister.
func (s *Session) SetOnIngestLive(cb func()) {
	s.mu.Lock()
	s.onIngestLive = cb
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
// two legs need different ICE settings (ADR-069): pass s.api for the loopback
// ingest leg, s.apiViewer for a viewer. Making it a parameter rather than a
// field read means a new call site has to state which leg it is, instead of
// silently inheriting whichever engine happened to be default.
func (s *Session) buildPeerConnection(api *webrtc.API, viewerLeg bool) (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{}
	// MUST agree with the SetLite gate in NewSession. pion rejects a lite
	// agent that is also handed an ICE URL (ErrUselessUrlsProvided: lite
	// forces candidateTypes=[host], which makes a STUN URL useless), and
	// CreateOffer then fails outright -- no offer, no nameable ICE failure.
	// One predicate, used by both sites, is what keeps that from happening.
	hostedViewer := viewerLeg && hostedViewerLeg(s.cfg)
	// STUN is a VIEWER-leg setting only. The ingest leg is this gateway
	// talking to its OWN headless Chrome over ws://127.0.0.1 -- the two peers
	// are on the same host by construction, so a server-reflexive candidate
	// there describes a NAT mapping neither of them will ever traverse.
	//
	// It was not merely useless, it was expensive (measured 2026-09-05, the
	// harness in pkg/tools/browser/webrtc_ingest_repro_test.go):
	//
	//   - reachable STUN: gathering 124ms, connected at +131ms, and the srflx
	//     candidate was NEVER the selected pair in any of ~35 successful
	//     connections across macOS and Linux -- every one chose host/host.
	//   - unroutable STUN: gathering 5008ms, exactly pion's
	//     defaultSTUNGatherTimeout, and because this leg is non-trickle the
	//     ANSWER is withheld for the whole of it. End to end, time to first
	//     video frame in a Linux container went from ~1s to ~17.5s: 5s here
	//     plus 10s in encoder.js's own waitIceGatheringComplete timeout.
	//
	// A CI runner or a hardened container that cannot reach a public STUN
	// server is exactly the environment where the live panel is least able to
	// afford 15 seconds of its 30s budget, and it buys a candidate that has
	// never once been used.
	if s.cfg.StunServer != "" && viewerLeg && !hostedViewer {
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
		Viewers: viewers,
		// Issue #674: liveness, not mere existence. The shared local tracks
		// are never torn down (see videoFeedID's doc comment), so
		// `videoTrack != nil` stays true forever after the first ingest — and
		// this is what the gateway turns into the panel's has_audio and what
		// an operator reads in a stats dump. Reporting a track that nothing
		// feeds as present is the same lie waitForTracks used to tell.
		HasVideo:     s.videoTrack != nil && s.videoFeedID != 0,
		HasAudio:     s.audioTrack != nil && s.audioFeedID != 0,
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
	// Retire both feeds (#674). A closed Session must never report a live
	// video feed: waitForTracks is reachable from a viewer offer that raced
	// this Close, and answering it from a track nothing writes to is the exact
	// dead-panel failure the feed tokens exist to prevent.
	s.videoFeedID = 0
	s.audioFeedID = 0
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
