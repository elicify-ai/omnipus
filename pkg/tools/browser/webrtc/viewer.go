//go:build !lite

package webrtc

import (
	"fmt"
	"time"

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
		return "", fmt.Errorf("webrtc: viewer %s: no ingest video track after waiting %s", prefix, waitForTracksTimeout)
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
		go drainRTCP(sender)
	}
	if audioTrack != nil {
		if sender, addErr := pc.AddTrack(audioTrack); addErr != nil {
			_ = pc.Close()
			return "", fmt.Errorf("webrtc: viewer %s: add audio track: %w", prefix, addErr)
		} else {
			go drainRTCP(sender)
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
		case webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected:
			s.removeViewer(viewerID, pc)
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

// removeViewer drops viewerID from the registry, but only if the entry still
// points at pc -- guards against a stale close callback firing after the
// viewer has already been replaced (HandleViewerOffer reconnect) or removed
// (CloseViewer).
func (s *Session) removeViewer(viewerID string, pc *webrtc.PeerConnection) {
	s.viewersMu.Lock()
	cur, exists := s.viewers[viewerID]
	if exists && cur.pc == pc {
		delete(s.viewers, viewerID)
	}
	count := len(s.viewers)
	s.viewersMu.Unlock()
	if exists && cur.pc == pc {
		s.logf("[viewer/%s] disconnected, count now %d", viewerID, count)
	}
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

// drainRTCP reads and discards RTCP on an outgoing RTPSender so its buffer
// never blocks -- standard Pion requirement for any sender not otherwise
// inspected.
func drainRTCP(sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}
