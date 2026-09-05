package webrtc

import (
	"errors"
	"fmt"
	"io"
	"strings"
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

	// ice-diag: full candidate/timing/selected-pair instrumentation, on the
	// SUCCESS path as well as the failure path -- see icediag.go's header for
	// why a failure's candidate set is uninterpretable without a success's.
	// Created BEFORE the PeerConnection so the offer can be described (and
	// rejected) without building one; every method takes the pc explicitly.
	diag := newICEDiag(prefix, "ingest", s.logf)

	// Log what the ENCODER offered before anything else happens, and
	// unconditionally. This is the one dump that must not wait for an outcome:
	// whether Chrome offered usable candidates at all has to be answerable for
	// the runs that SUCCEEDED too, or an intermittent failure can never be
	// told apart from a constant condition.
	diag.noteRemoteOffer(sdpOffer)

	// Refuse an offer that carries no candidate this agent could ever check
	// against. REPRODUCED 2026-09-05 in a Linux container with no non-loopback
	// interface (docker --network none): Chrome does not gather loopback host
	// candidates, so with no other interface and no reachable STUN server its
	// offer contained zero a=candidate lines -- 6082 bytes of perfectly valid
	// SDP describing a connection that could not exist.
	//
	// The gateway used to ANSWER that offer and install it as the live ingest
	// connection. Pion then sat in `checking`, logging "Failed to ping without
	// candidate pairs" at a WARN level nothing was listening to, and reported
	// the only thing an operator ever saw -- "ICE connection state -> failed"
	// -- exactly 30s later (pion's disconnectedTimeout 5s + failedTimeout 25s).
	// Thirty seconds of a black panel and a spinner, for a fact that was fully
	// determined the instant the offer arrived.
	//
	// Rejecting is safe here specifically because this leg is NON-TRICKLE
	// (wave-plan decision 6): encoder.js waits for its gathering to complete,
	// or 10s, before sending, so every candidate it will ever have is already
	// in this SDP. A trickle peer would legitimately offer none up front, and
	// this check would be wrong for one.
	//
	// The error travels back as an ErrorFrame, which closes the ingest WS and
	// engages encoder.js's existing reconnect backoff -- its documented
	// recovery path -- so a genuinely unconfigurable environment retries with
	// backoff and a NAMED reason instead of silently burning 30s per attempt.
	if usableRemoteCandidateCount(sdpOffer) == 0 {
		return "", fmt.Errorf("webrtc: ingest %s: %w", prefix, ErrOfferHasNoUsableCandidates)
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

	pc.OnICECandidate(diag.noteLocalCandidate)
	pc.OnICEGatheringStateChange(diag.noteGatheringState)

	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) {
		s.logf("%s ICE connection state -> %s", prefix, st.String())
		diag.noteICEState(st, pc)
		if st == webrtc.ICEConnectionStateFailed {
			// An ICE failure on the LOOPBACK ingest leg is the single most
			// consequential startup failure this package has (it leaves the
			// shared local tracks in place with nothing feeding them, so every
			// viewer answered from them shows a black panel), and until this
			// log existed the record of one was three words long: "->
			// failed". Nothing said which candidates either side actually
			// offered, so the one question that would identify the cause --
			// did this connection have a usable host pair at all, or was it
			// relying on srflx/mDNS candidates that a container cannot use --
			// was unanswerable after the fact. Logged only on failure, so a
			// healthy connection costs nothing.
			s.logf("%s ICE failed; local candidates: %s", prefix, describeSDPCandidates(descriptionSDP(pc.LocalDescription())))
			s.logf("%s ICE failed; remote candidates: %s", prefix, describeSDPCandidates(descriptionSDP(pc.RemoteDescription())))
		}
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.logf("%s peer connection state -> %s", prefix, st.String())
		s.handleIngestStateChange(prefix, pc, st)
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

	// gatherStart is deliberately taken AFTER SetLocalDescription (which is
	// what actually starts the gatherer), so the duration reported is the
	// gathering itself and not the SDP work preceding it. A gathering that
	// takes ~0ms is host-candidates-only; one that takes seconds is a STUN
	// round trip, and one that hits gatherTimeout is a STUN server that never
	// answered -- three different diagnoses the previous log could not
	// separate, because it reported only which of the two branches was taken.
	gatherStart := time.Now()
	select {
	case <-gatherComplete:
		s.logf("%s server gathering complete in %dms, sending answer", prefix, time.Since(gatherStart).Milliseconds())
	case <-time.After(gatherTimeout):
		s.logf("%s WARNING: server gathering did not complete within %s, sending partial answer (%s)",
			prefix, gatherTimeout, describeSDPCandidates(descriptionSDP(pc.LocalDescription())))
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

// ingestDisconnectGracePeriod bounds how long the INSTALLED ingest connection
// may sit in the Disconnected state before this package gives up on it and
// asks the owner for a fresh capture. A var (not const) purely as a test seam,
// mirroring disconnectGracePeriod (the viewer leg's equivalent, viewer.go) and
// audioGraceTimeout.
//
// Why a grace exists at all, when Failed/Closed are still handled instantly:
//
//  1. Disconnected is NOT a terminal state. Pion reaches it after 5s of failed
//     ICE consent checks (pion/ice defaultDisconnectedTimeout) and leaves it
//     again the moment consent is restored; only after a further 25s
//     (defaultFailedTimeout) does it become Failed. On a CPU-starved box --
//     the exact condition under which the encoder's headless Chrome is least
//     able to answer a consent check on time -- a loopback connection that is
//     perfectly healthy can still cross that 5s line. Treating that as death
//     spends a FULL capture teardown and renegotiation, which is the most
//     expensive and most failure-prone thing this pipeline does, at precisely
//     the moment the machine can least afford it.
//
//  2. Disconnected is also what a NORMAL encoder-side recapture produces.
//     encoder.js's runCaptureAndOffer tears its PeerConnection down FIRST and
//     only then captures, negotiates and offers; the replacement offer can
//     easily be more than 5s behind the teardown on a slow box (a cold
//     tabCapture is budgeted at up to 20s in capture_session.go). In that
//     window the old connection is still s.ingestPC, so the pre-grace code
//     cleared it and asked the owner for ANOTHER recapture -- while the first
//     one was still in flight. encoder.js coalesces that into a rerun, which
//     tears down the connection it just built, which produces another
//     Disconnected, which asks for another recapture. Every recapture fed the
//     next one. The re-check below breaks that loop directly: if a newer offer
//     has already installed its own connection, this one's Disconnected is
//     ancient history and means nothing.
var ingestDisconnectGracePeriod = 8 * time.Second

// handleIngestStateChange is the body of the ingest PeerConnection's
// OnConnectionStateChange handler, split out so the eviction policy is
// reachable from tests without standing up a full Go<->Go wire flow.
//
// Failed and Closed are terminal and are acted on immediately. Disconnected is
// not terminal and is given ingestDisconnectGracePeriod to recover first -- see
// that var's doc comment for the two distinct failure modes an immediate
// eviction caused. Every other state is ignored.
func (s *Session) handleIngestStateChange(prefix string, pc *webrtc.PeerConnection, st webrtc.PeerConnectionState) {
	switch st {
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		s.clearIngestIfCurrent(prefix, pc, st.String())
	case webrtc.PeerConnectionStateDisconnected:
		s.scheduleIngestDisconnectEviction(prefix, pc)
	}
}

// scheduleIngestDisconnectEviction arms a one-shot timer for an ingest
// connection that just entered Disconnected. When it fires it re-checks the
// world: a connection that recovered to Connected is left alone, and one that
// a newer ingest offer has already replaced is left alone too (that newer
// connection is the live one; HandleIngestOffer closed this one itself).
// Anything else is treated exactly like a Failed connection.
//
// Multiple Disconnected callbacks for the same pc simply arm multiple
// redundant timers -- clearIngestIfCurrent's own identity check makes the
// eviction idempotent, mirroring scheduleDisconnectEviction on the viewer leg.
func (s *Session) scheduleIngestDisconnectEviction(prefix string, pc *webrtc.PeerConnection) {
	s.logf("%s ingest connection disconnected — waiting %s for it to recover", prefix, ingestDisconnectGracePeriod)
	time.AfterFunc(ingestDisconnectGracePeriod, func() {
		if pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
			s.logf("%s ingest connection recovered from disconnected — keeping it", prefix)
			return
		}
		s.clearIngestIfCurrent(prefix, pc, pc.ConnectionState().String())
	})
}

// clearIngestIfCurrent clears pc as the installed ingest connection and asks
// the owner (CaptureSession) for a fresh capture -- but ONLY if pc is still the
// installed one. A late state change from a connection that a newer offer
// already replaced must not wipe its healthy successor, nor trigger a
// recapture the successor does not need.
//
// Clearing matters beyond bookkeeping (live-diagnosed 2026-08-03): a stale
// s.ingestPC pointing at a dead connection made every subsequent sendPLI fail
// against a closed pipe, so no keyframe ever arrived and the panel stayed
// frozen on its last frame while the tab title and URL bar advanced through
// several real sites.
//
// Returns whether it cleared, for tests.
func (s *Session) clearIngestIfCurrent(prefix string, pc *webrtc.PeerConnection, reason string) bool {
	s.mu.Lock()
	cleared := s.ingestPC == pc
	if cleared {
		s.ingestPC = nil
	}
	notify := s.onIngestLost
	s.mu.Unlock()
	if !cleared {
		return false
	}
	s.logf("%s ingest connection %s — cleared; a fresh capture is required", prefix, reason)
	// Ask the owner (CaptureSession) to re-establish capture. Without this the
	// session sits with no ingest at all and nothing ever asks the encoder to
	// reconnect, which is indistinguishable to the user from a hung browser.
	if notify != nil {
		go notify()
	}
	return true
}

// descriptionSDP is SessionDescription-or-nil -> SDP string, so a caller can
// read a PeerConnection's local/remote description in a log line without
// guarding each one.
func descriptionSDP(desc *webrtc.SessionDescription) string {
	if desc == nil {
		return ""
	}
	return desc.SDP
}

// describeSDPCandidates summarises the ICE candidates carried in an SDP as a
// short, log-safe string: a count per candidate type ("host=2 srflx=1"), plus
// an explicit mdns count for host candidates whose address is an obfuscated
// ".local" name rather than an IP.
//
// The mdns split is the point of this function. A Chrome peer that has not been
// granted a media-device permission publishes its host candidates as
// <uuid>.local names, which the receiving agent must resolve over multicast DNS
// before it can check them. Multicast DNS routinely does not work inside a
// container, and when it does not, an SDP that LOOKS like it offered perfectly
// good host candidates has in fact offered nothing usable -- a distinction
// invisible in a plain "ICE -> failed" log line, and the difference between
// "the network was slow" and "these two processes could never have connected".
//
// Returns "none" for an SDP with no candidates at all (also the empty-SDP case)
// so the log line never reads as though the field were missing.
func describeSDPCandidates(sdp string) string {
	counts := map[string]int{}
	order := []string{}
	mdns := 0
	total := 0
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		attr, ok := strings.CutPrefix(line, "a=candidate:")
		if !ok {
			continue
		}
		// RFC 5245 §15.1: <foundation> <component> <transport> <priority>
		// <connection-address> <port> typ <type> ...
		fields := strings.Fields(attr)
		if len(fields) < 8 || fields[6] != "typ" {
			continue
		}
		total++
		typ := fields[7]
		if _, seen := counts[typ]; !seen {
			order = append(order, typ)
		}
		counts[typ]++
		if typ == "host" && strings.HasSuffix(strings.ToLower(fields[4]), ".local") {
			mdns++
		}
	}
	if total == 0 {
		return "none"
	}
	parts := make([]string, 0, len(order)+1)
	for _, typ := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", typ, counts[typ]))
	}
	if mdns > 0 {
		parts = append(parts, fmt.Sprintf("mdns=%d", mdns))
	}
	return strings.Join(parts, " ")
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

	// Redeem any keyframe request sendPLI had to skip while this connection
	// was still negotiating -- see flushDeferredPLI. Only on video: a PLI
	// names the video SSRC, which is set just above, and Opus never needs one.
	if kind == webrtc.RTPCodecTypeVideo {
		s.flushDeferredPLI(prefix)
	}

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
	// A PLI is an RTCP packet written over the ingest connection's DTLS/SRTP
	// transport, and that transport does not exist until ICE has connected and
	// the DTLS handshake has completed. Writing before then is not a slow send
	// or a retryable one: pion's DTLSTransport.WriteRTCP looks up the SRTCP
	// session, finds none, and returns "the DTLS transport has not started
	// yet" WITHOUT touching the network.
	//
	// This is reachable on the ordinary startup path, not just in a crash.
	// pliBurstForNewViewer arms a 15s repeating burst when a viewer is
	// answered, and the gateway issues a corrective recapture immediately
	// AFTER answering that same viewer (applyColdStartRecapture,
	// pkg/gateway/browser_webrtc.go) -- so the encoder replaces its whole
	// PeerConnection while the burst is still running, and every remaining
	// tick lands on a connection that has not finished negotiating. The result
	// was five "PLI send failed" lines that read like a transport fault while
	// the actual problem was upstream, plus a keyframe request that was simply
	// lost: by the time the new connection WAS ready, the burst window had
	// expired and nothing asked again.
	//
	// So: skip the doomed write, and record that a keyframe is owed.
	// attachIngestTrack redeems it the moment a video track arrives on a live
	// connection (see flushDeferredPLI).
	if st := pc.ConnectionState(); st != webrtc.PeerConnectionStateConnected {
		s.pliDeferred.Store(true)
		s.logf("%s PLI deferred: ingest connection is %s, not connected", prefix, st.String())
		return
	}
	if err := pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)}}); err != nil {
		s.logf("%s PLI send failed: %v", prefix, err)
		return
	}
	s.logf("%s PLI sent to encoder (ssrc=%d)", prefix, ssrc)
}

// flushDeferredPLI sends the keyframe request that sendPLI skipped while the
// ingest connection was still negotiating, if there was one. Called from
// attachIngestTrack once a video track has arrived -- which, because pion only
// fires OnTrack after DTLS/SRTP is up and the first RTP packet has been read,
// is the earliest point at which a PLI can actually reach the encoder.
//
// A no-op when nothing was deferred, so a healthy connection pays nothing. The
// flag is consumed with CompareAndSwap, so several deferred requests collapse
// into the single keyframe they were all asking for.
func (s *Session) flushDeferredPLI(prefix string) {
	if !s.pliDeferred.CompareAndSwap(true, false) {
		return
	}
	s.logf("%s ingest connection is up — sending the keyframe request deferred while it negotiated", prefix)
	s.sendPLI(prefix)
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
