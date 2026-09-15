package webrtc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

type viewerPreparedCandidate struct {
	vc     *viewerConn
	answer string
}

func (s *Session) prepareViewerCandidate(ctx, parent context.Context, request *viewerRequestAdmission, prefix, viewerID, sdpOffer string, videoTrack, audioTrack *webrtc.TrackLocalStaticRTP, published *atomic.Pointer[ViewerHandle]) (_ *viewerPreparedCandidate, err error) {
	pc, err := s.buildPeerConnection(s.apiViewer, true) // internet-facing leg: fixed socket + public candidates
	if err != nil {
		return nil, fmt.Errorf("webrtc: viewer %s: %w", prefix, err)
	}

	var senders []*webrtc.RTPSender
	if sender, addErr := pc.AddTrack(videoTrack); addErr != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("webrtc: viewer %s: add video track: %w", prefix, addErr)
	} else {
		senders = append(senders, sender)
		go s.drainViewerRTCP(prefix, sender)
	}
	if audioTrack != nil {
		if sender, addErr := pc.AddTrack(audioTrack); addErr != nil {
			_ = pc.Close()
			return nil, fmt.Errorf("webrtc: viewer %s: add audio track: %w", prefix, addErr)
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
	inputCtx, inputCancel := context.WithCancel(parent)
	vc := &viewerConn{pc: pc, senders: senders, handle: pcHandle, inputCtx: inputCtx, inputCancel: inputCancel}
	defer func() {
		if err != nil {
			s.retireViewerCandidate(vc)
		}
	}()
	if request == nil {
		if err = s.installViewerCandidate(ctx, request, prefix, vc, published); err != nil {
			return nil, err
		}
	}

	// Same candidate/timing/selected-pair instrumentation the ingest leg
	// carries (icediag.go). The viewer leg is where a hosted install's ICE
	// actually has work to do -- srflx, TURN, ICE-Lite, a real network
	// between the peers -- so "-> failed" with no candidate record is, if
	// anything, LESS diagnosable here than on the loopback leg.
	diag := newICEDiag(prefix, "viewer", s.logf)
	pc.OnICECandidate(diag.noteLocalCandidate)
	pc.OnICEGatheringStateChange(diag.noteGatheringState)

	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) {
		s.logf("%s ICE connection state -> %s", prefix, st.String())
		diag.noteICEState(st, pc)
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
		s.bindViewerInputChannel(prefix, viewerID, pc, dc)
	})

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdpOffer}
	diag.noteRemoteOffer(offer.SDP)
	if err = pc.SetRemoteDescription(offer); err != nil {
		return nil, fmt.Errorf("webrtc: viewer %s: set remote description: %w", prefix, err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)

	var ans webrtc.SessionDescription
	ans, err = pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("webrtc: viewer %s: create answer: %w", prefix, err)
	}
	if err = pc.SetLocalDescription(ans); err != nil {
		return nil, fmt.Errorf("webrtc: viewer %s: set local description: %w", prefix, err)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("webrtc: viewer offer: %w", context.Cause(ctx))
	case <-inputCtx.Done():
		return nil, fmt.Errorf("webrtc: viewer offer: %w", inputCtx.Err())
	case <-gatherComplete:
		s.logf("%s server gathering complete, sending answer", prefix)
	case <-time.After(gatherTimeout):
		s.logf("%s WARNING: server gathering did not complete within %s, sending partial answer", prefix, gatherTimeout)
	}

	local := pc.LocalDescription()
	if local == nil {
		return nil, fmt.Errorf("webrtc: viewer %s: no local description after SetLocalDescription", prefix)
	}

	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return &viewerPreparedCandidate{vc: vc, answer: local.SDP}, nil
}

// installViewerCandidate publishes only after its caller has the required
// preparation result. It never performs transport IO under either registry lock.
func (s *Session) installViewerCandidate(ctx context.Context, request *viewerRequestAdmission, prefix string, vc *viewerConn, published *atomic.Pointer[ViewerHandle]) error {
	viewerID, pc, inputCtx := vc.handle.viewerID, vc.pc, vc.inputCtx
	// Match Stats' Session -> viewers lock order. Registration cannot race
	// past Session.Close after its final viewer snapshot.
	s.mu.Lock()
	s.viewersMu.Lock()
	if s.closed || inputCtx.Err() != nil || ctx.Err() != nil || !s.viewerRequestCurrentLocked(viewerID, request) {
		s.viewersMu.Unlock()
		s.mu.Unlock()
		if err := context.Cause(ctx); err != nil {
			return fmt.Errorf("webrtc: viewer offer: %w", err)
		}
		if request != nil {
			return errStaleViewerRequest
		}
		return fmt.Errorf("webrtc: session closed")
	}
	if state := pc.ConnectionState(); state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateFailed {
		s.viewersMu.Unlock()
		s.mu.Unlock()
		return fmt.Errorf("webrtc: viewer candidate is terminal")
	}
	old := s.viewers[viewerID]
	if old != nil {
		old.cancelInput()
	}
	s.viewers[viewerID] = vc
	if published != nil {
		published.Store(vc.handle)
	}
	viewerCount := len(s.viewers)
	s.viewersMu.Unlock()
	s.mu.Unlock()
	// Cancellation precedes publication; potentially blocking transport
	// cleanup runs after the registry locks are released.
	if old != nil {
		s.logf("%s replacing existing viewer connection for id %q", prefix, viewerID)
		s.stopViewerConn(old)
		go func() {
			if cerr := old.pc.Close(); cerr != nil {
				s.logf("%s closing previous viewer connection: %v", prefix, cerr)
			}
		}()
	}
	context.AfterFunc(inputCtx, func() { s.removeViewer(viewerID, pc) })
	s.logf("%s viewer count now %d", prefix, viewerCount)

	return nil
}

// Retire the exact candidate, whether it was published or is still private.
// The preparation slot remains occupied until this cleanup returns.
func (s *Session) retireViewerCandidate(vc *viewerConn) {
	s.stopViewerConn(vc)
	_ = vc.pc.Close()
	s.removeViewer(vc.handle.viewerID, vc.pc)
}
