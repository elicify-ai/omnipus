package webrtc

import (
	"fmt"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// ViewerHandle identifies the SPECIFIC viewer PeerConnection one
// HandleViewerOfferHandle call created, so a caller can later undo ONLY that
// exact attempt via CloseViewerIfCurrent -- safe even if a NEWER
// HandleViewerOffer[Handle] call for the SAME viewerID has since replaced it
// (e.g. two browser_webrtc_offer frames in flight at once for one
// connection: a superseded/failed offer's own cleanup running after a newer,
// already-committed offer for the same viewerID has taken over -- see
// CloseViewerIfCurrent's doc comment for the incident this closes). Opaque:
// callers must not inspect or compare its fields.
//
// HandleViewerOfferHandle/CloseViewerIfCurrent both declare this as `any`
// (not *ViewerHandle) at their signature boundary. That began as a build-tag
// requirement — pkg/tools/browser's viewerOfferHandler interface had to name
// the method signature in a file that also compiled under `-tags lite`, where
// this type did not exist — and ADR-067 §10 step 14 has since retired that
// variant. The `any` is kept because the interface is still an OPTIONAL
// capability satisfied by narrower test fakes that have no ViewerHandle of
// their own. Callers still get a concrete *ViewerHandle back dynamically;
// treat the `any` as opaque and pass it straight to CloseViewerIfCurrent
// rather than inspecting it.
type ViewerHandle struct {
	viewerID string
	pc       *webrtc.PeerConnection
}

// HandleViewerOffer is the signaling entry point for a VIEWER leg: an
// external browser (or, in tests, a fake Pion "viewer") creates a recvonly
// PeerConnection for video/audio and POSTs its offer here, keyed by
// viewerID (caller-assigned, e.g. the browser WS connection ID). Non-trickle:
// this call blocks until ICE gathering completes (or times out) and returns
// a complete answer SDP.
//
// The relay waits for the shared video track to exist (the encoder may not
// have connected yet) before answering; audio is attached opportunistically
// if already present (tolerate video-only, see waitForTracks). The viewer's
// own "input" data channel (label "input") is wired to the Session's
// InputSink via OnDataChannel, since the viewer is the offering side.
//
// If viewerID collides with an already-attached viewer (e.g. a reconnect
// that races the old connection's teardown), the old PeerConnection is
// closed and replaced -- same pattern as ingest replacement.
//
// This is a thin wrapper around HandleViewerOfferHandle that discards the
// handle, kept with this EXACT 2-return signature because it implements
// RelaySession (capture_session.go), which the package's test fakes must
// also keep satisfying unchanged. A caller that needs supersede-safe
// cleanup (CaptureSession) calls HandleViewerOfferHandle directly instead,
// via the optional-capability pattern (capture_session.go's
// viewerOfferHandler) rather than widening RelaySession itself.
func (s *Session) HandleViewerOffer(viewerID string, sdpOffer string) (answer string, err error) {
	answer, _, err = s.HandleViewerOfferHandle(viewerID, sdpOffer)
	return answer, err
}

// HandleViewerOfferHandle behaves exactly like HandleViewerOffer (identical
// contract -- see its doc comment) but additionally returns an opaque handle
// (dynamically a *ViewerHandle -- see its doc comment for why this method
// declares `any` rather than the concrete type) identifying the resulting
// viewer PeerConnection, valid from the moment this attempt registers itself
// in s.viewers (see the registration point below) even on a LATER failure in
// this same call (SetRemoteDescription/CreateAnswer/SetLocalDescription) --
// exactly the window a supersede-safe caller needs covered, since a
// concurrent newer offer for the same viewerID could replace this attempt's
// registration before this call even returns. A true nil interface before
// that registration point (empty viewerID/SDP, no ingest video track yet,
// closed session, PC/track-add failures) -- nothing exists yet for a caller
// to protect.
func (s *Session) HandleViewerOfferHandle(viewerID string, sdpOffer string) (answer string, handle any, err error) {
	if viewerID == "" {
		return "", nil, fmt.Errorf("webrtc: viewer offer: empty viewerID")
	}
	if sdpOffer == "" {
		return "", nil, fmt.Errorf("webrtc: viewer offer: empty SDP")
	}

	id := s.nextConnID()
	prefix := fmt.Sprintf("[viewer-%d/%s]", id, viewerID)
	s.logf("%s offer received (%d bytes SDP)", prefix, len(sdpOffer))

	videoTrack, audioTrack, ok := s.waitForTracks(waitForTracksTimeout)
	if !ok {
		// %w wraps ErrNoIngestVideoTrack (ingest.go) so the gateway
		// (browser_webrtc.go) can classify this SPECIFIC failure mode via
		// errors.Is, separately from every other HandleViewerOffer error —
		// see ErrNoIngestVideoTrack's doc comment. Message text is unchanged
		// from before this fix (still "no ingest video track after waiting
		// <N>s") so existing log-scraping/greps keep matching.
		return "", nil, fmt.Errorf(
			"webrtc: viewer %s: %w after waiting %s",
			prefix, ErrNoIngestVideoTrack, waitForTracksTimeout,
		)
	}

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", nil, fmt.Errorf("webrtc: session closed")
	}

	pc, err := s.buildPeerConnection(s.apiViewer, true) // internet-facing leg: fixed socket + public candidates
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: viewer %s: %w", prefix, err)
	}

	var senders []*webrtc.RTPSender
	if sender, addErr := pc.AddTrack(videoTrack); addErr != nil {
		_ = pc.Close()
		return "", nil, fmt.Errorf("webrtc: viewer %s: add video track: %w", prefix, addErr)
	} else {
		senders = append(senders, sender)
		go s.drainViewerRTCP(prefix, sender)
	}
	if audioTrack != nil {
		if sender, addErr := pc.AddTrack(audioTrack); addErr != nil {
			_ = pc.Close()
			return "", nil, fmt.Errorf("webrtc: viewer %s: add audio track: %w", prefix, addErr)
		} else {
			senders = append(senders, sender)
			go s.drainViewerRTCP(prefix, sender)
		}
	} else {
		s.logf("%s no audio track yet, answering video-only", prefix)
	}

	// pcHandle is minted HERE, before vc is ever published into s.viewers,
	// and stored on vc itself (viewerConn.handle) as well as returned below
	// as this call's `handle` -- the SAME pointer instance serves both
	// purposes so removeViewer's eventual onViewerRemoved notification (GAP 2
	// fix-wave finding) can hand a caller back the EXACT identity it was
	// given at registration time, letting it recognize its own registration
	// with a plain equality check rather than needing a second identity
	// scheme.
	pcHandle := &ViewerHandle{viewerID: viewerID, pc: pc}
	vc := &viewerConn{pc: pc, senders: senders, handle: pcHandle}
	s.viewersMu.Lock()
	if old, exists := s.viewers[viewerID]; exists {
		s.logf("%s replacing existing viewer connection for id %q", prefix, viewerID)
		// Explicit, synchronous termination (stopViewerConn) of old's own
		// RTPSenders/input-queue -- see viewerConn's doc comment (session.go)
		// for why this package does not rely solely on the async pc.Close()
		// below to unblock old's drainViewerRTCP/runInputQueue goroutines.
		// Safe under s.viewersMu: old's only other writer (the OnDataChannel
		// handler below) is gated by this same lock.
		s.stopViewerConn(old)
		go func() {
			if cerr := old.pc.Close(); cerr != nil {
				s.logf("%s closing previous viewer connection: %v", prefix, cerr)
			}
		}()
	}
	s.viewers[viewerID] = vc
	viewerCount := len(s.viewers)
	s.viewersMu.Unlock()
	s.logf("%s viewer count now %d", prefix, viewerCount)

	// This attempt is now registered -- from here on, a handle exists for a
	// caller to protect via CloseViewerIfCurrent, even if a later step below
	// fails (SetRemoteDescription/CreateAnswer/SetLocalDescription), because
	// a concurrent newer offer for the SAME viewerID could already have
	// replaced this registration before this call returns. Reuses pcHandle
	// (minted above, already stored on vc) rather than constructing a second,
	// distinct *ViewerHandle for the same registration -- see pcHandle's doc
	// comment for why the two must be the identical pointer.
	handle = pcHandle

	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) {
		s.logf("%s ICE connection state -> %s", prefix, st.String())
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.logf("%s peer connection state -> %s", prefix, st.String())
		switch st {
		case webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateFailed:
			// Terminal, unrecoverable states: evict immediately.
			s.removeViewer(viewerID, pc)
		case webrtc.PeerConnectionStateDisconnected:
			// Disconnected is often transient (a brief Wi-Fi blip Pion's own
			// ICE agent recovers from without ever reaching Failed) -- evict
			// only if it hasn't recovered within disconnectGracePeriod. See
			// scheduleDisconnectEviction's doc comment for the full fix-wave
			// CRIT rationale (removeViewer previously never closed the PC at
			// all on ANY of these three states, leaking it).
			s.scheduleDisconnectEviction(viewerID, pc)
		}
	})

	// Q4 pattern: the viewer creates a data channel labeled "input" on this
	// SAME PeerConnection alongside the recvonly media transceivers, so
	// input can never contend with media on a separate queue. Since the
	// viewer is the offering side, the channel arrives here via
	// OnDataChannel.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "input" {
			s.logf("%s unexpected data channel label %q, ignoring", prefix, dc.Label())
			return
		}
		s.viewersMu.Lock()
		if cur, exists := s.viewers[viewerID]; exists && cur.pc == pc {
			cur.dc = dc
		}
		s.viewersMu.Unlock()
		s.wireInputDataChannel(prefix, viewerID, dc)
	})

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdpOffer}
	if err = pc.SetRemoteDescription(offer); err != nil {
		return "", handle, fmt.Errorf("webrtc: viewer %s: set remote description: %w", prefix, err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)

	var ans webrtc.SessionDescription
	ans, err = pc.CreateAnswer(nil)
	if err != nil {
		return "", handle, fmt.Errorf("webrtc: viewer %s: create answer: %w", prefix, err)
	}
	if err = pc.SetLocalDescription(ans); err != nil {
		return "", handle, fmt.Errorf("webrtc: viewer %s: set local description: %w", prefix, err)
	}

	select {
	case <-gatherComplete:
		s.logf("%s server gathering complete, sending answer", prefix)
	case <-time.After(gatherTimeout):
		s.logf("%s WARNING: server gathering did not complete within %s, sending partial answer", prefix, gatherTimeout)
	}

	local := pc.LocalDescription()
	if local == nil {
		return "", handle, fmt.Errorf("webrtc: viewer %s: no local description after SetLocalDescription", prefix)
	}

	s.logf("%s answer sent to viewer", prefix)
	s.pliBurstForNewViewer(prefix)

	return local.SDP, handle, nil
}

// disconnectGracePeriod bounds how long a viewer PeerConnection may sit in
// the Disconnected state -- a transient, often-recoverable ICE blip (e.g. a
// brief Wi-Fi drop) that Pion's own ICE agent can recover from on its own
// without ever reaching Failed -- before this package gives up on it.
// Fix-wave CRIT design decision: Disconnected must NOT be evicted
// immediately like Failed/Closed (that would cost a full session/encoder
// re-negotiation for every momentary network hiccup), but it also must not
// be tracked forever: if the state hasn't recovered to Connected within this
// window, treat it the same as an outright failure. A var (not const) purely
// as a test seam, mirroring captureGracePeriod's established pattern in the
// sibling pkg/tools/browser package.
// Raised 10s -> 30s (2026-07-30 UAT, Batam->Fly over VPN on iPad Safari).
// The relay must never evict a viewer the CLIENT is still trying to recover:
// src/lib/browserWebRTC.ts tolerates an ICE 'disconnected' for
// DEFAULT_DISCONNECTED_GRACE_MS (raised to 15s in the same fix) before it
// gives up, so anything below that here makes the server the first to quit
// and guarantees the client's recovery attempt lands on an already-evicted
// registration. 30s leaves clear headroom above the client's 15s.
var disconnectGracePeriod = 30 * time.Second

// scheduleDisconnectEviction arms a one-shot timer for a viewer
// PeerConnection that just entered the Disconnected state (see
// disconnectGracePeriod's doc comment for the design rationale). When the
// timer fires it re-checks pc's CURRENT state: if the connection has
// recovered to Connected in the meantime, this is a no-op and the viewer is
// left alone; otherwise (still Disconnected, or it worsened) it is evicted
// and closed exactly like a Failed/Closed connection via removeViewer.
// Multiple Disconnected callbacks for the same pc (Pion can re-fire) simply
// arm multiple redundant, harmless timers -- removeViewer's own
// cur.pc==pc registry check makes eviction idempotent.
func (s *Session) scheduleDisconnectEviction(viewerID string, pc *webrtc.PeerConnection) {
	time.AfterFunc(disconnectGracePeriod, func() {
		if pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
			return
		}
		s.removeViewer(viewerID, pc)
	})
}

// stopViewerConn explicitly and synchronously terminates the two things
// vc's per-connection goroutines (drainViewerRTCP x{1,2}, runInputQueue x1)
// depend on to unblock: each RTPSender's Stop() (unblocking drainViewerRTCP's
// blocking sender.Read() -- RTPSender.Stop, if the sender has ever sent,
// closes its srtpStream directly, which is what the blocked Read() call is
// ultimately reading from) and vc.dc's own Close() (unblocking
// runInputQueue indirectly: dc.Close() tears down JUST this one data
// channel's underlying SCTP stream, which is what fires the dc.OnClose
// handler already wired in inputdc.go's wireInputDataChannel, which is what
// closes the input queue runInputQueue ranges over) -- see viewerConn's doc
// comment (session.go) for the CI-confirmed incident this closes: this
// package must not rely SOLELY on pc.Close()'s own close cascade (correct on
// a clean/fast transport, but several hops deep through Pion/its SCTP
// dependency -- PeerConnection.Close -> SCTPTransport.Stop ->
// sctpAssociation.Abort -> EVERY stream's read erroring -- and dependent on
// that abort signaling actually reaching this side over the wire) to reap
// these goroutines. Under real network conditions (packet loss, a degraded
// transport) that whole-association cascade was observed to leave both
// goroutines blocked well past a 60s bound; calling Stop()/dc.Close()
// directly here does not depend on the SCTP association at all.
//
// pc.Close() itself is still called by every caller of this method
// (removeViewer, CloseViewer, Close, and the same-viewerID replace branch
// above) -- this only covers the two things that must not wait on it. Safe
// to call on a nil vc (no-op), one whose senders are still unset (Stop() on
// a nil slice is simply zero iterations), or one whose dc is still nil (the
// viewer's "input" data channel hasn't opened yet -- an ordinary, momentary
// window; nothing to close since wireInputDataChannel/runInputQueue haven't
// started).
//
// A method on *Session (not a free function) so it can log via s.logf --
// fix-wave finding: every OTHER teardown call in this package
// (pc.Close() at each of stopViewerConn's own call sites, plus Close's
// ingest.Close()) logs its error; snd.Stop()/vc.dc.Close() silently
// discarding theirs via a bare `_ =` was the one inconsistency, meaning a
// systemic SRTP/DTLS teardown failure here would have been invisible even
// though every other failure in the same teardown path is surfaced.
func (s *Session) stopViewerConn(vc *viewerConn) {
	if vc == nil {
		return
	}
	for _, snd := range vc.senders {
		if err := snd.Stop(); err != nil {
			s.logf("webrtc: stopViewerConn: RTP sender stop failed: %v", err)
		}
	}
	if vc.dc != nil {
		if err := vc.dc.Close(); err != nil {
			s.logf("webrtc: stopViewerConn: data channel close failed: %v", err)
		}
	}
}

// removeViewer drops viewerID from the registry, but only if the entry still
// points at pc -- guards against a stale close callback firing after the
// viewer has already been replaced (HandleViewerOffer reconnect) or removed
// (CloseViewer). Also closes pc asynchronously (fix-wave CRIT: this
// previously only deleted the map entry, leaking the PeerConnection itself
// plus its two drainViewerRTCP goroutines, its runInputQueue goroutine
// (inputdc.go), and its UDP sockets on every ICE-terminal-state transition
// -- see OnConnectionStateChange's call sites above). Close is idempotent
// (Pion tolerates redundant calls), matching the same close-guarded-by-
// registry-membership pattern HandleViewerOffer's same-viewerID replacement
// already uses (:79-83 above).
//
// CI-confirmed fix-wave: calls stopViewerConn SYNCHRONOUSLY (before spawning
// the async pc.Close() below) so drainViewerRTCP/runInputQueue are given an
// explicit, code-owned termination signal immediately, rather than depending
// solely on pc.Close()'s own (correct, but asynchronous and multi-hop)
// cascade to reach the same result eventually -- see stopViewerConn's doc
// comment for the CI incident (TestSessionViewerICEDisconnect_
// ClientVanishesWithoutSignaling_ServerEvictsAndClosesPC timing out waiting
// for both goroutines to exit under real network conditions) this closes.
//
// Fix-wave: notifies onViewerRemoved (SetOnViewerRemoved's doc comment) on
// every ACTUAL eviction (stillCurrent), AFTER s.viewersMu is released and
// with no other Session-internal lock held -- so the callback is free to
// call back into this Session (e.g. Stats()) without risking a self-deadlock.
// This is the sole call site for that notification: both the terminal
// ICE/PeerConnection-state path (OnConnectionStateChange, above) and the
// unrecovered-disconnect-grace-period path (scheduleDisconnectEviction,
// below) funnel through here, and CloseViewerIfCurrent (an external caller's
// identity-checked close) delegates to this exact function too, so it gets
// the same notification for free.
func (s *Session) removeViewer(viewerID string, pc *webrtc.PeerConnection) {
	s.viewersMu.Lock()
	cur, exists := s.viewers[viewerID]
	stillCurrent := exists && cur.pc == pc
	var handle *ViewerHandle
	if stillCurrent {
		handle = cur.handle
		delete(s.viewers, viewerID)
	}
	count := len(s.viewers)
	s.viewersMu.Unlock()
	if !stillCurrent {
		return
	}
	s.logf("[viewer/%s] disconnected, count now %d", viewerID, count)
	s.stopViewerConn(cur)
	go func() {
		if cerr := pc.Close(); cerr != nil {
			s.logf("[viewer/%s] removeViewer: close failed: %v", viewerID, cerr)
		}
	}()
	// handle is the exact *ViewerHandle instance minted for cur at
	// registration time (viewerConn.handle, set once in
	// HandleViewerOfferHandle and never mutated) -- passed through verbatim
	// so a caller (CaptureSession.removeViewerByRelayHandle) can recognize
	// its own registration by equality, not just by viewerID (GAP 2
	// fix-wave finding).
	s.notifyViewerRemoved(viewerID, handle)
}

// CloseViewerIfCurrent undoes the specific viewer attempt handle identifies
// -- closing and evicting its PeerConnection -- but ONLY if it is STILL the
// currently-registered connection for its viewerID, i.e. no newer
// HandleViewerOffer[Handle] call for the SAME viewerID has replaced it in the
// meantime. A safe no-op if handle is nil, was never obtained from
// HandleViewerOfferHandle (e.g. a nil interface, or one of the wrong
// dynamic type), or is already superseded/removed.
//
// handle is declared `any` (dynamically a *ViewerHandle) for the same
// build-tag-neutral-interface reason ViewerHandle's own doc comment
// explains -- treat it as opaque and pass through whatever
// HandleViewerOfferHandle returned, never construct one directly.
//
// This closes the exact gap CloseViewer(viewerID string) alone cannot: that
// method closes WHATEVER is currently registered for viewerID, trusting the
// caller that it is still theirs -- unsafe once two HandleViewerOffer(Handle)
// calls for the same viewerID can be in flight at once. Concretely: a
// superseded or ultimately-failed offer's own cleanup (pkg/gateway/
// browser_webrtc.go's handleWebRTCOffer, on the "offer failed" and "offer
// superseded before commit" branches) used to run plain
// CloseViewer(viewerID) + CaptureSession.RemoveViewer(viewerID) -- both keyed
// ONLY by viewerID -- which, if that cleanup happened to run AFTER a NEWER,
// already-committed offer for the same viewerID had replaced the registry
// entry, would close and evict the WINNING, actively-viewed connection
// instead of the losing one.
//
// Delegates to removeViewer -- the SAME identity-checked (cur.pc == pc)
// delete-and-async-close this package already uses for a relay-side ICE
// eviction -- so a still-current close here ALSO fires the onViewerRemoved
// notification exactly as an ICE failure would (see removeViewer's doc
// comment): a caller doesn't need any separate viewerID-keyed bookkeeping
// call alongside this one.
func (s *Session) CloseViewerIfCurrent(handle any) {
	h, ok := handle.(*ViewerHandle)
	if !ok || h == nil {
		return
	}
	s.removeViewer(h.viewerID, h.pc)
}

// CloseViewer closes and removes the viewer connection for viewerID, if any.
// Safe to call for an unknown/already-gone viewerID (no-op).
func (s *Session) CloseViewer(viewerID string) {
	s.viewersMu.Lock()
	vc, exists := s.viewers[viewerID]
	if exists {
		delete(s.viewers, viewerID)
	}
	s.viewersMu.Unlock()
	if !exists {
		return
	}
	// Explicit, synchronous termination of vc's RTPSenders/input-queue --
	// see stopViewerConn's doc comment for why this package does not rely
	// solely on the pc.Close() call below to unblock
	// drainViewerRTCP/runInputQueue.
	s.stopViewerConn(vc)
	if err := vc.pc.Close(); err != nil {
		s.logf("[viewer/%s] CloseViewer: close failed: %v", viewerID, err)
	}
}

// drainViewerRTCP reads RTCP on one viewer's outgoing RTPSender (video or
// audio) so its buffer never blocks -- standard Pion requirement for any
// sender not otherwise inspected -- AND, unlike the plain discard this
// replaced, inspects every packet for a PictureLossIndication or
// FullIntraRequest: the browser's own decoder generates these on packet
// loss to ask for a fresh keyframe. Before this fix that feedback dead-ended
// here (the fix-wave's CRIT finding: "video froze while audio kept
// playing" -- exactly what happens when a viewer loses a keyframe reference
// and never gets a new one to resync). It is forwarded to the ingest
// connection via forwardPLIThrottled, throttled ACROSS ALL viewers combined
// so a storm of loss reports from several viewers can't force the encoder
// into a constant-keyframe death spiral.
func (s *Session) drainViewerRTCP(prefix string, sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		n, _, err := sender.Read(buf)
		if err != nil {
			return
		}
		pkts, unmarshalErr := rtcp.Unmarshal(buf[:n])
		if unmarshalErr != nil {
			continue
		}
		for _, pkt := range pkts {
			switch p := pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				s.forwardPLIThrottled(prefix)
			case *rtcp.ReceiverReport:
				// ADR-069 Finding 2: this is the only place the gateway ever
				// learns what the VIEWER's link is actually doing. It used to
				// be dropped on the floor, so the encoder congestion-
				// controlled against the loopback ingest hop instead. Take the
				// WORST block in the report: one bad receiver is congestion.
				var worst float64
				for _, rb := range p.Reports {
					if f := normalizeFractionLost(rb.FractionLost); f > worst {
						worst = f
					}
				}
				s.noteViewerLoss(worst, time.Now())
			}
		}
	}
}
