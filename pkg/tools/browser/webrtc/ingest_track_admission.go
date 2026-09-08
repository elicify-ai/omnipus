package webrtc

import (
	"context"

	"github.com/pion/webrtc/v4"
)

func (s *Session) ingestTrackLifetime(pc *webrtc.PeerConnection) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pc == nil || s.closed || s.ingestPC != pc {
		return nil, ErrStaleIngestOffer
	}
	// Also supports direct installed-peer fixtures and legacy callers. Normal
	// signaling creates this lifetime in installIngestCandidate.
	if s.ingestMediaCtx == nil {
		s.ingestMediaCtx, s.ingestMediaCancel = context.WithCancel(context.Background())
	}
	return s.ingestMediaCtx, nil
}

// acquireIngestTrackInstallation returns with writer and Session.mu owned.
// Only short track/identity initialization may run before release; no IO.
func (s *Session) acquireIngestTrackInstallation(pc *webrtc.PeerConnection, kind webrtc.RTPCodecType) (*mediaForwarder, func(), error) {
	lifetime, err := s.ingestTrackLifetime(pc)
	if err != nil {
		return nil, nil, err
	}
	forward := &s.videoForward
	if kind == webrtc.RTPCodecTypeAudio {
		forward = &s.audioForward
	}
	if err := forward.mu.LockContext(lifetime); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	release := func() { s.mu.Unlock(); forward.mu.Unlock() }
	if s.closed || s.ingestPC != pc || s.ingestMediaCtx != lifetime || lifetime.Err() != nil {
		release()
		return nil, nil, ErrStaleIngestOffer
	}
	return forward, release, nil
}
