//go:build !lite

package webrtc

import (
	"fmt"
	"io"
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
const waitForTracksTimeout = 5 * time.Second

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

	pc, err := s.buildPeerConnection()
	if err != nil {
		return "", fmt.Errorf("webrtc: ingest %s: %w", prefix, err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = pc.Close()
		return "", fmt.Errorf("webrtc: session closed")
	}
	old := s.ingestPC
	s.ingestPC = pc
	s.mu.Unlock()

	if old != nil {
		s.logf("%s replacing previous ingest connection", prefix)
		go func() {
			if cerr := old.Close(); cerr != nil {
				s.logf("%s closing previous ingest connection: %v", prefix, cerr)
			}
		}()
	}

	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) {
		s.logf("%s ICE connection state -> %s", prefix, st.String())
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.logf("%s peer connection state -> %s", prefix, st.String())
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
//     This package therefore rewrites SequenceNumber to a session-lifetime
//     monotonic counter per kind (Session.videoSeq/audioSeq) before handing
//     the packet to the local track, so the local track's OUTGOING sequence
//     stream is always continuous regardless of which upstream ingest
//     connection is currently feeding it. Timestamp is passed through
//     unmodified -- a timestamp discontinuity on source switch is normal,
//     expected WebRTC behavior (decoders resync on the next keyframe, which
//     pliBurstForNewViewer already requests) and, unlike sequence number, is
//     not subject to any crypto-layer replay check.
//
// Two overlapping writers (the outgoing old connection finishing its last
// few packets, and the new one starting) can safely write concurrently: the
// sequence counter is an atomic, and the old one simply stops once its
// remote.Read returns an error after the old PeerConnection closes.
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
	// requirement for any track we only ever read from).
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := receiver.Read(buf); err != nil {
				return
			}
		}
	}()

	seqCounter := &s.videoSeq
	pktCounter := &s.videoPktCount
	if kind == webrtc.RTPCodecTypeAudio {
		seqCounter = &s.audioSeq
		pktCounter = &s.audioPktCount
	}

	buf := make([]byte, 1500)
	var lastLog time.Time
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			if err == io.EOF {
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
		// See the long comment on attachIngestTrack above: rewriting the
		// sequence number to a session-lifetime monotonic counter is what
		// makes reusing the shared local track across ingest reconnects
		// actually work (not just compile) -- without it, an already
		// -attached viewer's SRTP anti-replay window silently discards
		// every packet from a freshly-reconnected ingest source.
		pkt.SequenceNumber = uint16(seqCounter.Add(1))

		if err := local.WriteRTP(&pkt); err != nil {
			// ErrClosedPipe just means no viewer is bound to this local
			// track yet -- not worth logging per-packet.
			if err != io.ErrClosedPipe {
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
