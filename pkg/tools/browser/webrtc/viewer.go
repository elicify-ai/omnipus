//go:build !lite

package webrtc

import (
	"fmt"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

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
func (s *Session) HandleViewerOffer(viewerID string, sdpOffer string) (answer string, err error) {
	if viewerID == "" {
		return "", fmt.Errorf("webrtc: viewer offer: empty viewerID")
	}
	if sdpOffer == "" {
		return "", fmt.Errorf("webrtc: viewer offer: empty SDP")
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
		return "", fmt.Errorf(
			"webrtc: viewer %s: %w after waiting %s",
			prefix, ErrNoIngestVideoTrack, waitForTracksTimeout,
		)
	}

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", fmt.Errorf("webrtc: session closed")
	}

	pc, err := s.buildPeerConnection()
	if err != nil {
		return "", fmt.Errorf("webrtc: viewer %s: %w", prefix, err)
	}

	if sender, addErr := pc.AddTrack(videoTrack); addErr != nil {
		_ = pc.Close()
		return "", fmt.Errorf("webrtc: viewer %s: add video track: %w", prefix, addErr)
	} else {
		go s.drainViewerRTCP(prefix, sender)
	}
	if audioTrack != nil {
		if sender, addErr := pc.AddTrack(audioTrack); addErr != nil {
			_ = pc.Close()
			return "", fmt.Errorf("webrtc: viewer %s: add audio track: %w", prefix, addErr)
		} else {
			go s.drainViewerRTCP(prefix, sender)
		}
	} else {
		s.logf("%s no audio track yet, answering video-only", prefix)
	}

	vc := &viewerConn{pc: pc}
	s.viewersMu.Lock()
	if old, exists := s.viewers[viewerID]; exists {
		s.logf("%s replacing existing viewer connection for id %q", prefix, viewerID)
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
		return "", fmt.Errorf("webrtc: viewer %s: set remote description: %w", prefix, err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)

	var ans webrtc.SessionDescription
	ans, err = pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("webrtc: viewer %s: create answer: %w", prefix, err)
	}
	if err = pc.SetLocalDescription(ans); err != nil {
		return "", fmt.Errorf("webrtc: viewer %s: set local description: %w", prefix, err)
	}

	select {
	case <-gatherComplete:
		s.logf("%s server gathering complete, sending answer", prefix)
	case <-time.After(gatherTimeout):
		s.logf("%s WARNING: server gathering did not complete within %s, sending partial answer", prefix, gatherTimeout)
	}

	local := pc.LocalDescription()
	if local == nil {
		return "", fmt.Errorf("webrtc: viewer %s: no local description after SetLocalDescription", prefix)
	}

	s.logf("%s answer sent to viewer", prefix)
	s.pliBurstForNewViewer(prefix)

	return local.SDP, nil
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
var disconnectGracePeriod = 10 * time.Second

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
func (s *Session) removeViewer(viewerID string, pc *webrtc.PeerConnection) {
	s.viewersMu.Lock()
	cur, exists := s.viewers[viewerID]
	stillCurrent := exists && cur.pc == pc
	if stillCurrent {
		delete(s.viewers, viewerID)
	}
	count := len(s.viewers)
	s.viewersMu.Unlock()
	if !stillCurrent {
		return
	}
	s.logf("[viewer/%s] disconnected, count now %d", viewerID, count)
	go func() {
		if cerr := pc.Close(); cerr != nil {
			s.logf("[viewer/%s] removeViewer: close failed: %v", viewerID, cerr)
		}
	}()
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
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				s.forwardPLIThrottled(prefix)
			}
		}
	}
}
