package webrtc

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// gatherTimeout bounds how long HandleIngestOffer/HandleViewerOffer wait for
// ICE gathering to finish before sending a (possibly partial) answer. Both
// legs are non-trickle per wave-plan decision 6, matching the spike.
const gatherTimeout = 10 * time.Second

// waitForTracksTimeout bounds how long HandleViewerOffer waits for the
// ingest side's video track to exist before rejecting the viewer (the
// encoder may not have connected yet).
//
// Root-caused live on uat-omnipus (2026-07-28, DEBUG-level relay logs
// captured across two independent capture-session cycles, see the incident
// writeup this const's history references): with the encoder already warm
// (extension loaded, Chrome running), the FULL ingest handshake — encoder
// page reachable, tabCapture, offer, ICE-connect, audio's first RTP packet —
// consistently completed in UNDER 1 SECOND. The VIDEO track's first RTP
// packet, however, consistently arrived ~5s AFTER that (VP8 software-encoder
// warm-up/first-keyframe latency on this deployment's shared vCPU), i.e.
// almost EXACTLY at the boundary of the previous 5s value here — losing the
// race on both observed cycles by a hair, 100% reproducibly, not a flake.
// Bumped to 15s: 3x the observed ~5s video-track latency, while staying
// safely inside the SPA's firstAnswerTimeoutMs budget (30s,
// src/lib/browserWebRTC.ts) for the overwhelmingly common warm-Chrome case
// this deployment exhibited. A genuinely cold Start() (extension/Chrome
// never launched before, up to captureStartTimeout=20s +
// bringToFrontTimeout=5s in capture_session.go) can still exhaust the SPA's
// 30s budget regardless of this value — that pre-existing, documented
// cold-start risk is unchanged by this fix and is mitigated by the SPA's
// own one-shot automatic retry (see captureGracePeriod's doc comment in
// capture_session.go for why THAT retry needed its own alignment fix too).
const waitForTracksTimeout = 15 * time.Second

// audioGraceTimeout bounds how much LONGER waitForTracks keeps waiting for
// the audio track once video is already present. The viewer PeerConnection
// has no renegotiation path — whatever tracks exist at answer time are ALL
// that viewer will ever receive — and the encoder's tabCapture always
// requests audio (captureext/embedded/encoder.js: audio is mandatory), so on
// a cold capture start the audio track reliably arrives within milliseconds
// of video (Chrome's Opus encoder sends packets continuously, silence
// included). Without this grace the FIRST viewer — the one whose offer
// triggered the capture start — routinely won its race against the audio
// track's OnTrack and was answered video-only forever (UAT: "video works,
// no audio"). Kept short so a hypothetical genuinely audio-less ingest only
// delays that first answer, never blocks it (video-only remains tolerated,
// per the W1-C requirement). A var (not const) purely as a test seam,
// mirroring browser.captureGracePeriod.
var audioGraceTimeout = 2 * time.Second

// HandleIngestOffer is the signaling entry point for the ENCODER leg: the
// headless-Chrome tabCapture page (or, in tests, a fake Pion "encoder")
// offers its captured video (+ optional audio) track here. Non-trickle: this
// call blocks until ICE gathering completes (or times out) and returns a
// complete answer SDP.
//
// Ingest replacement: if a previous ingest connection is still active (e.g.
// the encoder reconnected after a recapture or a crash/restart), it is
// swapped out and closed asynchronously. Existing viewers are unaffected --
// attachIngestTrack reuses the SAME shared TrackLocalStaticRTP per kind
// across reconnects rather than creating a new one, so already-bound viewer
// RTPSenders keep receiving packets the moment the new ingest connection's
// OnTrack fires. This is the one deliberate deviation from the spike's
// relay.go, which allocated a fresh local track on every attach and would
// have orphaned existing viewers on reconnect.
func (s *Session) HandleIngestOffer(sdpOffer string) (answer string, err error) {
	if sdpOffer == "" {
		return "", fmt.Errorf("webrtc: ingest offer: empty SDP")
	}

	id := s.nextConnID()
	prefix := fmt.Sprintf("[ingest-%d]", id)
	s.logf("%s offer received (%d bytes SDP)", prefix, len(sdpOffer))

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", fmt.Errorf("webrtc: session closed")
	}

	pc, err := s.buildPeerConnection(s.api, false) // loopback encoder leg: no public rewrite, no shared mux
	if err != nil {
		return "", fmt.Errorf("webrtc: ingest %s: %w", prefix, err)
	}
	// Fix-wave finding 2b: this new pc is NOT installed as s.ingestPC (and
	// the OLD ingest connection is NOT closed) until negotiation below has
	// FULLY succeeded -- SetRemoteDescription/CreateAnswer/
	// SetLocalDescription all completing and a non-nil answer SDP in hand.
	// The previous ordering swapped+closed FIRST, so a bad recapture offer
	// that failed partway through negotiation killed a perfectly healthy
	// ingest connection for a replacement that never came up, leaving every
	// attached viewer stranded. installed is flipped true only once the
	// swap actually happens (see below); every error return before that
	// point closes THIS pc (which was never installed anywhere) and leaves
	// the previous ingest connection running untouched.
	installed := false
	defer func() {
		if !installed {
			if cerr := pc.Close(); cerr != nil {
				s.logf("%s closing failed new ingest connection: %v", prefix, cerr)
			}
		}
	}()

	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) {
		s.logf("%s ICE connection state -> %s", prefix, st.String())
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.logf("%s peer connection state -> %s", prefix, st.String())

		// Clear a DEAD ingest connection instead of only logging it
		// (live-diagnosed 2026-08-03). Previously this handler was
		// log-only, so s.ingestPC kept pointing at a closed connection
		// forever: every subsequent recapture called sendPLI ->
		// WriteRTCP -> "io: read/write on closed pipe", no keyframe ever
		// arrived, and the panel stayed frozen on whatever frame it last
		// received — the operator saw the start page persist while the tab
		// title and URL bar advanced through several real sites.
		//
		// Only the CURRENTLY-INSTALLED connection is cleared: a late state
		// change from a connection that a newer offer already replaced must
		// not wipe its healthy successor.
		switch st {
		case webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			s.mu.Lock()
			cleared := s.ingestPC == pc
			if cleared {
				s.ingestPC = nil
			}
			notify := s.onIngestLost
			s.mu.Unlock()
			if !cleared {
				return
			}
			s.logf("%s ingest connection %s — cleared; a fresh capture is required", prefix, st.String())
			// Ask the owner (CaptureSession) to re-establish capture. Without
			// this the session sits with no ingest at all and nothing ever
			// asks the encoder to reconnect, which is indistinguishable to
			// the user from a hung browser.
			if notify != nil {
				go notify()
			}
		}
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		go s.attachIngestTrack(prefix, track, receiver)
	})

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdpOffer}
	if err = pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("webrtc: ingest %s: set remote description: %w", prefix, err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)

	var ans webrtc.SessionDescription
	ans, err = pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("webrtc: ingest %s: create answer: %w", prefix, err)
	}
	if err = pc.SetLocalDescription(ans); err != nil {
		return "", fmt.Errorf("webrtc: ingest %s: set local description: %w", prefix, err)
	}

	select {
	case <-gatherComplete:
		s.logf("%s server gathering complete, sending answer", prefix)
	case <-time.After(gatherTimeout):
		s.logf("%s WARNING: server gathering did not complete within %s, sending partial answer", prefix, gatherTimeout)
	}

	local := pc.LocalDescription()
	if local == nil {
		return "", fmt.Errorf("webrtc: ingest %s: no local description after SetLocalDescription", prefix)
	}

	// Negotiation fully succeeded -- NOW swap the new connection in and
	// close whatever ingest connection preceded it (if any). Only this late
	// swap, not the earlier build/negotiate steps, ever tears down a
	// previously-healthy ingest connection.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", fmt.Errorf("webrtc: session closed")
	}
	old := s.ingestPC
	s.ingestPC = pc
	s.mu.Unlock()
	installed = true

	if old != nil {
		s.logf("%s replacing previous ingest connection", prefix)
		go func() {
			if cerr := old.Close(); cerr != nil {
				s.logf("%s closing previous ingest connection: %v", prefix, cerr)
			}
		}()
	}

	s.logf("%s answer sent to encoder", prefix)
	return local.SDP, nil
}

// attachIngestTrack is invoked (in its own goroutine, from HandleIngestOffer's
// OnTrack callback) once per remote track the ingest PeerConnection receives.
// It reuses the existing shared local track for this kind if one already
// exists (ingest reconnect case) or creates it on first arrival, then pumps
// RTP packets from the remote track to the local one (raw copy, no
// transcode -- payload bytes are untouched) until the remote track ends.
//
// Why reusing the local track across reconnects is safe:
//
//  1. Pion's TrackLocalStaticRTP.WriteRTP rewrites the packet's SSRC and
//     PayloadType to each bound viewer-sender's own negotiated values on
//     every write (see pion/webrtc's track_local_static.go:writeRTP) -- the
//     local track object is decoupled from whichever upstream connection is
//     currently producing bytes for it as far as SSRC/PayloadType go.
//  2. That alone is NOT sufficient: each ingest connection's
//     TrackLocalStaticSample/packetizer on the ENCODER side picks its own
//     independent, randomized starting sequence number (rtp.NewRandomSequencer).
//     Forwarding that number as-is means a fresh ingest connection produces a
//     sequence-number discontinuity relative to whatever an already-attached
//     viewer's SRTP receive window has already advanced past -- SRTP's
//     mandatory anti-replay window (RFC 3711) then silently drops the
//     "out of window" packets on the VIEWER side, with no error visible
//     anywhere on this side (TrackLocalStaticRTP.Write still returns nil).
//     This package therefore rewrites SequenceNumber by a constant
//     per-connection OFFSET (chosen at each connection's first packet to
//     continue past Session.videoLastOutSeq/audioLastOutSeq, the shared
//     outgoing high-water marks), so the local track's OUTGOING sequence
//     stream stays within one continuous serial-number progression across
//     source switches while intra-connection gaps and ordering pass through
//     intact for the viewer's loss recovery (see the offset-rewrite comment
//     in the forward loop below). Timestamp is passed through
//     unmodified -- a timestamp discontinuity on source switch is normal,
//     expected WebRTC behavior (decoders resync on the next keyframe, which
//     pliBurstForNewViewer already requests) and, unlike sequence number, is
//     not subject to any crypto-layer replay check.
//
// Two overlapping writers (the outgoing old connection finishing its last
// few packets, and the new one starting) can safely write concurrently: the
// sequence counter is an atomic, and the old one simply stops once its
// remote.Read returns an error after the old PeerConnection closes.
// seqReconnectGap is the deliberate forward jump opened in the outgoing RTP
// sequence numbering when a new ingest connection takes over the shared
// local track (see the offset-rewrite comment in attachIngestTrack's forward
// loop). Big enough that reordering straggler packets from the outgoing old
// connection can never overlap the new connection's range, small enough that
// the viewer's brief NACK burst for the phantom gap is negligible.
const seqReconnectGap = 64

// seq16Ahead reports whether a is strictly ahead of b in RFC 1982 16-bit
// serial-number space (the RTP sequence-number ordering).
func seq16Ahead(a, b uint16) bool {
	return a != b && (a-b) < 0x8000
}

// seqRewriter applies one ingest connection's constant sequence-number
// offset (see the offset-rewrite comment in attachIngestTrack's forward
// loop) and maintains the session-lifetime outgoing high-water mark shared
// with past and future connections.
type seqRewriter struct {
	lastOut    *atomic.Uint32
	haveOffset bool
	offset     uint16
}

// rewrite maps one inbound sequence number to the outgoing stream. The
// offset is fixed at the connection's FIRST packet: lastOut + seqReconnectGap
// deliberately opens a forward gap at every source switch (forward jumps are
// legal loss as far as the viewer is concerned -- it NACKs briefly and
// resyncs on the keyframe that pliBurstForNewViewer/recapture already
// requests), whereas overlapping the previous connection's numbering risks
// the SRTP anti-replay drop described on attachIngestTrack. Every later
// packet shifts by that same constant, preserving intra-connection gaps and
// ordering exactly.
//
// The high-water mark advances only forward in RFC 1982 space: late
// (reordered/retransmitted) packets must not drag it backwards, and at
// reconnect the outgoing old writer's stragglers must not fight the new
// writer's fresh range.
func (r *seqRewriter) rewrite(in uint16) uint16 {
	if !r.haveOffset {
		r.offset = uint16(r.lastOut.Load()) + seqReconnectGap - in
		r.haveOffset = true
	}
	out := in + r.offset
	for {
		cur := r.lastOut.Load()
		if !seq16Ahead(out, uint16(cur)) {
			break
		}
		if r.lastOut.CompareAndSwap(cur, uint32(out)) {
			break
		}
	}
	return out
}

func (s *Session) attachIngestTrack(prefix string, remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	codec := remote.Codec()
	kind := remote.Kind()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	var local *webrtc.TrackLocalStaticRTP
	switch kind {
	case webrtc.RTPCodecTypeVideo:
		local = s.videoTrack
	case webrtc.RTPCodecTypeAudio:
		local = s.audioTrack
	}
	if local == nil {
		var err error
		local, err = webrtc.NewTrackLocalStaticRTP(codec.RTPCodecCapability, kind.String(), "omnipus-browser")
		if err != nil {
			s.mu.Unlock()
			s.logf("%s attachIngestTrack: NewTrackLocalStaticRTP(%s) failed: %v", prefix, kind, err)
			return
		}
		switch kind {
		case webrtc.RTPCodecTypeVideo:
			s.videoTrack = local
		case webrtc.RTPCodecTypeAudio:
			s.audioTrack = local
		}
	}
	switch kind {
	case webrtc.RTPCodecTypeVideo:
		s.videoSSRC = remote.SSRC()
		s.videoCodec = codec.MimeType
	case webrtc.RTPCodecTypeAudio:
		s.audioCodec = codec.MimeType
	}
	s.mu.Unlock()

	s.logf("%s ingest track arrived: kind=%s codec=%s clockRate=%d ssrc=%d payloadType=%d",
		prefix, kind, codec.MimeType, codec.ClockRate, remote.SSRC(), remote.PayloadType())

	// Drain RTCP on the receiver so its buffer never blocks (standard Pion
	// requirement for any track we only ever read from) -- and, fix-wave
	// finding 2, inspect every packet for a Sender Report to forward to
	// every attached viewer (see forwardSenderReport's doc comment for why:
	// UAT symptom "audio slightly delayed vs video"). Every other RTCP
	// packet type on this stream (receiver reports, NACKs the interceptor
	// stack already handles, etc) is simply drained/discarded, exactly as
	// before.
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, err := receiver.Read(buf)
			if err != nil {
				return
			}
			pkts, unmarshalErr := rtcp.Unmarshal(buf[:n])
			if unmarshalErr != nil {
				continue
			}
			for _, pkt := range pkts {
				sr, ok := pkt.(*rtcp.SenderReport)
				if !ok {
					continue
				}
				s.forwardSenderReport(prefix, kind, sr)
			}
		}
	}()

	lastOutSeq := &s.videoLastOutSeq
	pktCounter := &s.videoPktCount
	if kind == webrtc.RTPCodecTypeAudio {
		lastOutSeq = &s.audioLastOutSeq
		pktCounter = &s.audioPktCount
	}

	buf := make([]byte, 1500)
	var lastLog time.Time
	rewriter := seqRewriter{lastOut: lastOutSeq}
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.logf("%s ingest track ended (EOF): kind=%s", prefix, kind)
			} else {
				s.logf("%s ingest track read error, stopping forward: kind=%s err=%v", prefix, kind, err)
			}
			return
		}

		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			s.logf("%s dropping unparseable RTP packet: kind=%s err=%v", prefix, kind, err)
			continue
		}
		// See the long comment on attachIngestTrack above: the sequence
		// number is rewritten by a CONSTANT PER-CONNECTION OFFSET, chosen at
		// this connection's first packet so its outgoing stream continues
		// ahead of wherever the previous ingest connection left off --
		// without that continuity, an already-attached viewer's SRTP
		// anti-replay window silently discards every packet from a freshly
		// reconnected ingest source.
		//
		// A constant offset -- NOT a per-packet monotonic counter, which is
		// what shipped first (renumbering packets in READ order). Read-order
		// renumbering destroyed the two loss-recovery signals the viewer leg
		// depends on (operator-visible as persistent cyan macroblock
		// smearing during scroll, 2026-08-13): (a) an ingest-leg packet loss
		// left NO GAP in the outgoing numbering, so the viewer's decoder
		// could not detect the loss -- no NACK, no prompt PLI, just a broken
		// bitstream decoded as garbage until some unrelated keyframe; and
		// (b) a packet recovered late by ingest-leg retransmission was
		// renumbered into its READ position, splicing it into the wrong
		// bitstream position with perfectly sequential numbering, actively
		// corrupting the decode. Offset rewriting preserves intra-connection
		// gaps and relative order, so the viewer's jitter buffer reorders
		// correctly, its NACK generator sees real gaps (answered by the
		// default-interceptor NACK responder from the local track's send
		// cache), and unrecoverable loss escalates to the PLI path
		// (forwardPLIThrottled) for a proper keyframe resync.
		pkt.SequenceNumber = rewriter.rewrite(pkt.SequenceNumber)

		if err := local.WriteRTP(&pkt); err != nil {
			// ErrClosedPipe just means no viewer is bound to this local
			// track yet -- not worth logging per-packet.
			if !errors.Is(err, io.ErrClosedPipe) {
				s.logf("%s forward write failed: kind=%s err=%v", prefix, kind, err)
			}
			continue
		}
		count := pktCounter.Add(1)
		if time.Since(lastLog) > 5*time.Second {
			s.logf("%s RTP forward progress: kind=%s packets=%d", prefix, kind, count)
			lastLog = time.Now()
		}
	}
}

// waitForTracks polls for the shared local tracks to exist, up to timeout.
// Video is mandatory (returns ok=false if it never arrives); audio is
// expected but ultimately optional -- per the W1-C requirement ("one video +
// one audio track expected but tolerate video-only"). Because the viewer leg
// has no renegotiation (tracks attached at answer time are final for that
// viewer), video alone is NOT immediately good enough: once video is
// present, this keeps waiting up to audioGraceTimeout longer for audio (the
// two OnTrack firings are normally milliseconds apart -- the grace only
// actually elapses for a genuinely audio-less ingest) and only then answers
// video-only. This mirrors the WV1 spike's waitForTracks, which waited for
// BOTH tracks -- relaxing it to video-only-immediately is what introduced
// the first-viewer no-audio race (UAT 2026-07-18).
func (s *Session) waitForTracks(timeout time.Duration) (video, audio *webrtc.TrackLocalStaticRTP, ok bool) {
	deadline := time.Now().Add(timeout)
	var audioDeadline time.Time // armed once video is first observed
	for {
		s.mu.Lock()
		v, a := s.videoTrack, s.audioTrack
		s.mu.Unlock()
		if v != nil && a != nil {
			return v, a, true
		}
		now := time.Now()
		if v != nil {
			if audioDeadline.IsZero() {
				audioDeadline = now.Add(audioGraceTimeout)
			}
			if now.After(audioDeadline) {
				return v, a, true // audio never arrived within the grace -- answer video-only
			}
		} else if now.After(deadline) {
			return v, a, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// sendPLI asks the current ingest connection for a fresh keyframe by writing
// a PictureLossIndication RTCP packet referencing the video track's current
// media SSRC. Audio (Opus) never needs this.
func (s *Session) sendPLI(prefix string) {
	s.mu.Lock()
	pc := s.ingestPC
	ssrc := s.videoSSRC
	s.mu.Unlock()
	if pc == nil || ssrc == 0 {
		return
	}
	if err := pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)}}); err != nil {
		s.logf("%s PLI send failed: %v", prefix, err)
		return
	}
	s.logf("%s PLI sent to encoder (ssrc=%d)", prefix, ssrc)
}

// forwardPLIThrottled is called by drainViewerRTCP (viewer.go) when ANY
// attached viewer's own decoder reports a PictureLossIndication or
// FullIntraRequest -- the standard browser-side signal "I lost a reference
// frame, send me a fresh keyframe." Forwarded to the ingest connection via
// sendPLI, but throttled to at most once per pliForwardMinInterval ACROSS
// EVERY viewer combined (fix-wave finding 1, the CRIT "video froze while
// audio kept playing" UAT symptom): without this dead-ended path, a
// viewer's own loss-recovery request never reached the encoder at all, so a
// decoder that lost its keyframe reference stayed frozen until the NEXT
// unrelated PLI burst (a fresh viewer joining, or a recapture) happened to
// arrive. The throttle exists so N viewers reporting loss around the same
// time (a shared network hiccup) can't each independently trigger a PLI,
// which would force the encoder into a constant-keyframe spiral and
// collapse bitrate exactly when the network is already struggling.
func (s *Session) forwardPLIThrottled(prefix string) {
	s.pliForwardMu.Lock()
	now := time.Now()
	if now.Sub(s.lastPLIForwardAt) < pliForwardMinInterval {
		s.pliForwardMu.Unlock()
		return
	}
	s.lastPLIForwardAt = now
	s.pliForwardMu.Unlock()
	s.sendPLI(prefix)
}

// forwardSenderReport rewrites and forwards an ingest-side RTCP Sender
// Report to every currently-attached viewer's PeerConnection (fix-wave
// finding 2, UAT symptom "audio slightly delayed vs video"): Chrome's own
// WebRTC stack uses a stream's Sender Reports (NTP wall-clock time paired
// with that stream's RTP timestamp) to align independently-clocked audio
// and video tracks into one presentation timeline. Without ANY Sender
// Report ever reaching the viewer -- true before this fix, since the
// ingest-side RTCP drain simply discarded everything -- the browser falls
// back to jitter-buffer-only heuristics that can drift out of sync under
// load.
//
// The RTP TIMESTAMP domain forwarded here is unmodified -- this package
// only ever rewrites RTP sequence numbers (see attachIngestTrack's long
// comment), never timestamps -- so an SR's RTPTime field stays meaningful
// as-is. What MUST be rewritten, once per viewer, is the packet's own SSRC
// field: a browser only accepts (or correctly correlates) an SR whose SSRC
// matches the SSRC of the RTP stream it is actually receiving, and Pion
// rewrites every forwarded RTP packet's SSRC to each viewer-binding's own
// negotiated outgoing SSRC (see TrackLocalStaticRTP.WriteRTP), which is NOT
// the ingest-side SSRC this Sender Report arrived with.
func (s *Session) forwardSenderReport(prefix string, kind webrtc.RTPCodecType, sr *rtcp.SenderReport) {
	s.viewersMu.Lock()
	conns := make([]*viewerConn, 0, len(s.viewers))
	for _, vc := range s.viewers {
		conns = append(conns, vc)
	}
	s.viewersMu.Unlock()

	for _, vc := range conns {
		ssrc, ok := outgoingSSRCForKind(vc.pc, kind)
		if !ok {
			continue
		}
		out := *sr
		out.SSRC = ssrc
		if err := vc.pc.WriteRTCP([]rtcp.Packet{&out}); err != nil {
			s.logf("%s forward sender report to viewer failed: kind=%s err=%v", prefix, kind, err)
		}
	}
}

// outgoingSSRCForKind returns the SSRC pc negotiated for its outgoing
// RTPSender of the given kind (video/audio) -- i.e. the SSRC value Pion
// rewrites every forwarded RTP packet to for THIS specific viewer binding
// (see forwardSenderReport's doc comment). ok is false if pc has no sender
// of that kind (e.g. a video-only viewer asked about audio) or the sender
// has no negotiated encoding yet.
func outgoingSSRCForKind(pc *webrtc.PeerConnection, kind webrtc.RTPCodecType) (uint32, bool) {
	for _, sender := range pc.GetSenders() {
		track := sender.Track()
		if track == nil || track.Kind() != kind {
			continue
		}
		params := sender.GetParameters()
		if len(params.Encodings) == 0 {
			continue
		}
		return uint32(params.Encodings[0].SSRC), true
	}
	return 0, false
}

// pliBurstForNewViewer sends an immediate PLI plus one every 3s for 15s so a
// late-joining viewer's decoder gets a keyframe promptly. Guarded so
// overlapping callers (a new viewer joining while a recapture-triggered burst
// is already running) don't stack unbounded goroutines -- an in-flight burst
// already covers the new caller since it's a shared, short window.
func (s *Session) pliBurstForNewViewer(prefix string) {
	if !s.pliBursting.CompareAndSwap(false, true) {
		s.sendPLI(prefix)
		return
	}
	go func() {
		defer s.pliBursting.Store(false)
		s.sendPLI(prefix)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			<-ticker.C
			s.sendPLI(prefix)
		}
	}()
}
