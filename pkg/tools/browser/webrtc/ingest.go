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
func (s *Session) HandleIngestOffer(sdpOffer string) (string, error) {
	return s.handleIngestOffer(sdpOffer, 0, "")
}

func (s *Session) handleIngestOffer(sdpOffer string, generation uint64, targetID string) (answer string, err error) {
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
	installedDone := make(chan struct{})
	defer close(installedDone)
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		go func() {
			<-installedDone
			s.attachIngestTrack(prefix, pc, track, receiver, generation, targetID)
		}()
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
	s.videoFeedID = 0
	s.audioFeedID = 0
	s.videoForward.retire()
	s.audioForward.retire()
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

// HandleIngestOfferForGeneration binds an ingest to the server's display lineage.
func (s *Session) HandleIngestOfferForGeneration(sdp string, generation uint64, targetID string) (string, error) {
	if generation == 0 || generation > 9007199254740991 {
		return "", fmt.Errorf("webrtc: capture generation must be a positive safe integer")
	}
	if len(targetID) == 0 || len(targetID) > 128 {
		return "", fmt.Errorf("webrtc: capture target must contain 1 to 128 bytes")
	}
	return s.handleIngestOffer(sdp, generation, targetID)
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
		// Issue #674: retire the feed tokens too. The shared local tracks
		// deliberately outlive every ingest connection (see videoFeedID's doc
		// comment in session.go), so `videoTrack != nil` says nothing about
		// whether anything is still writing to them — and until this line
		// existed, nothing ever said otherwise. That is why a dead panel never
		// came back across repeated opens: the FIRST ingest set videoTrack,
		// and every viewer offer from then on was answered instantly against
		// a corpse.
		//
		// Retired here as well as in endFeed because the two signals are not
		// interchangeable. endFeed fires when the forwarding goroutine's
		// blocking Read finally unblocks, which on a degraded transport can
		// lag the connection's death by a long way; the connection's own
		// terminal state is available immediately and is just as conclusive.
		s.videoFeedID = 0
		s.audioFeedID = 0
		s.videoForward.retire()
		s.audioForward.retire()
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

// Shared local tracks keep viewer bindings alive across source replacement.
// Pion translates SSRC and payload type for each viewer; mediaForwarder
// translates sequence numbers and timestamps while excluding old writers.
// RTP payload bytes are untouched. Sender reports use the identical clock
// offset, preserving the source's audio/video synchronization mapping.
// seqReconnectGap advances to the next sequence number without inventing
// packet loss. mediaForwarder excludes the retired source at the write boundary.
const seqReconnectGap = 1

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

// rewrite maps a feed by a fixed offset, preserving real gaps and ordering.
// Its caller must exclude retired writers before invoking it. The high-water
// mark advances only forward in 16-bit serial-number space.
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

// endFeed retires feedID as the live feed for kind, if it still is one — the
// forwarding goroutine's own "I have stopped" signal (issue #674). Idempotent
// and identity-checked: a goroutine whose feed was already superseded by a
// newer attachment, or already retired by clearIngestIfCurrent/Close, must not
// clear the successor's token on its way out.
func (s *Session) endFeed(prefix string, kind webrtc.RTPCodecType, feedID int64) {
	s.mu.Lock()
	var cleared bool
	switch kind {
	case webrtc.RTPCodecTypeVideo:
		if s.videoFeedID == feedID {
			s.videoFeedID = 0
			s.videoForward.retire()
			cleared = true
		}
	case webrtc.RTPCodecTypeAudio:
		if s.audioFeedID == feedID {
			s.audioFeedID = 0
			s.audioForward.retire()
			cleared = true
		}
	}
	s.mu.Unlock()
	if cleared {
		s.logf("%s ingest %s feed ended — nothing is writing to the shared local track now", prefix, kind)
	}
}

func (s *Session) attachIngestTrack(prefix string, pc *webrtc.PeerConnection, remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver, generation uint64, targetID string) {
	codec := remote.Codec()
	kind := remote.Kind()
	// Minted before any lock is taken so the token is unique even if two
	// OnTrack callbacks for the same kind race (ingest replacement).
	feedID := s.feedSeq.Add(1)

	s.mu.Lock()
	if s.closed || s.ingestPC != pc {
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
	forward := &s.videoForward
	if kind == webrtc.RTPCodecTypeAudio {
		forward = &s.audioForward
	}
	forward.begin(feedID, codec.ClockRate)
	var live func()
	var boundary func(uint64, string, uint32)
	switch kind {
	case webrtc.RTPCodecTypeVideo:
		s.videoSSRC = remote.SSRC()
		s.videoCodec = codec.MimeType
		s.videoFeedID = feedID
		s.videoGeneration = generation
		s.videoTargetID = targetID
		live = s.onIngestLive
		boundary = s.onVideoBoundary
	case webrtc.RTPCodecTypeAudio:
		s.audioCodec = codec.MimeType
		s.audioFeedID = feedID
	}
	s.mu.Unlock()
	// From here on this goroutine OWNS the feed token, so every exit path must
	// retire it — including the RTCP-drain/forward loop's `return`s below.
	defer s.endFeed(prefix, kind, feedID)

	s.logf("%s ingest track arrived: kind=%s codec=%s clockRate=%d ssrc=%d payloadType=%d",
		prefix, kind, codec.MimeType, codec.ClockRate, remote.SSRC(), remote.PayloadType())

	// Video is what the panel shows, so it — not audio — is the signal that
	// says "the stream is genuinely back". Fired in its own goroutine, with no
	// lock held, matching onIngestLost's convention: the owner's handler calls
	// back into this package (Stats, Recapture) and must never do so under
	// s.mu.
	if live != nil {
		go live()
	}

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
				forward.senderReport(feedID, sr, func(report *rtcp.SenderReport) {
					s.forwardSenderReport(prefix, kind, report)
				})
			}
		}
	}()

	pktCounter := &s.videoPktCount
	if kind == webrtc.RTPCodecTypeAudio {
		pktCounter = &s.audioPktCount
	}

	buf := make([]byte, 1500)
	var lastLog time.Time
	boundarySent := false
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
		accepted, err := forward.write(feedID, &pkt, time.Now(), local.WriteRTP)
		if !accepted {
			return
		}
		if err != nil {
			// ErrClosedPipe just means no viewer is bound to this local
			// track yet -- not worth logging per-packet.
			if !errors.Is(err, io.ErrClosedPipe) {
				s.logf("%s forward write failed: kind=%s err=%v", prefix, kind, err)
			}
			continue
		}
		if !boundarySent && boundary != nil && generation > 0 {
			if timestamp, _, current := forward.boundary(feedID); current {
				boundarySent = true
				// Consumers recheck immutable identity when this queued
				// callback arrives. The first mapped timestamp is the floor
				// even if an earlier write partially failed for a viewer.
				go boundary(generation, targetID, timestamp)
			}
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
		// Issue #674: a track with no live feed is treated exactly as an
		// ABSENT track, so the existing video-mandatory / audio-grace policy
		// below is unchanged in every respect except that it now asks a
		// question with a true answer. Before this, the first successful
		// ingest made `videoTrack != nil` permanently true and every later
		// viewer offer was answered against whatever was left of it.
		if s.videoFeedID == 0 {
			v = nil
		}
		if s.audioFeedID == 0 {
			a = nil
		}
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
// mediaForwarder has already applied the same timestamp translation to
// this report as to its feed's RTP packets. What MUST be rewritten, once per viewer, is the packet's own SSRC
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
