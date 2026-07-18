//go:build !lite

package webrtc

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

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
// this package additionally rewrites each packet's SEQUENCE NUMBER to a
// session-lifetime monotonic counter per kind (videoSeq/audioSeq below) --
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
	api   *webrtc.API
	cfg   Config
	sink  InputSink
	logfn func(string, ...any)

	mu         sync.Mutex
	closed     bool
	ingestPC   *webrtc.PeerConnection
	videoTrack *webrtc.TrackLocalStaticRTP
	audioTrack *webrtc.TrackLocalStaticRTP
	videoSSRC  webrtc.SSRC
	videoCodec string
	audioCodec string

	viewersMu sync.Mutex
	viewers   map[string]*viewerConn

	videoPktCount atomic.Int64
	audioPktCount atomic.Int64
	pliBursting   atomic.Bool

	// videoSeq/audioSeq are session-lifetime, monotonically-incrementing
	// OUTGOING sequence-number generators for the two shared local tracks,
	// used by attachIngestTrack to rewrite each forwarded packet's sequence
	// number before it reaches any viewer binding. See the long comment on
	// attachIngestTrack in ingest.go for why this rewrite -- not just
	// SSRC/PayloadType rewriting, which Pion already does per viewer binding
	// -- is required for ingest replacement to work at all.
	videoSeq atomic.Uint32
	audioSeq atomic.Uint32

	connSeq atomic.Int64
}

// viewerConn tracks one attached viewer's PeerConnection plus its "input"
// data channel (nil until the viewer opens it, which happens asynchronously
// after the answer is sent -- SendToViewer must tolerate that window).
type viewerConn struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel
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
		return s
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		s.logf("webrtc: register default interceptors failed: %v (session will reject all offers)", err)
		s.api = webrtc.NewAPI()
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

	s.api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	return s
}

// log writes a structured log line if a logf was supplied to NewSession.
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
func (s *Session) buildPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{}
	if s.cfg.StunServer != "" {
		config.ICEServers = []webrtc.ICEServer{{URLs: []string{s.cfg.StunServer}}}
	}
	pc, err := s.api.NewPeerConnection(config)
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

	for id, vc := range viewers {
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
