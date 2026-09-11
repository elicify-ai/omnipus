package webrtc

import (
	"context"
	"fmt"

	"github.com/pion/webrtc/v4"
)

// installIngestCandidate waits outside Session.mu for the old write boundary,
// then revalidates the exact reservation before changing any installed state.
// Writer order is video, audio, Session.mu. The caller closes the previous peer.
func (s *Session) installIngestCandidate(ctx context.Context, pc *webrtc.PeerConnection, admission *ingestAdmission, generation uint64, targetID string) (*webrtc.PeerConnection, error) {
	var oldCancel context.CancelFunc
	// Registered first so cancellation runs after all admission locks release.
	defer func() {
		if oldCancel != nil {
			oldCancel()
		}
	}()
	if err := s.videoForward.mu.LockContext(ctx); err != nil {
		return nil, s.ingestInstallWaitError(admission, err)
	}
	defer s.videoForward.mu.Unlock()
	if err := s.audioForward.mu.LockContext(ctx); err != nil {
		return nil, s.ingestInstallWaitError(admission, err)
	}
	defer s.audioForward.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("webrtc: session closed")
	}
	if err := s.ingestAdmissionErrorLocked(admission); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	old := s.ingestPC
	oldCancel = s.ingestMediaCancel
	s.ingestMediaCtx, s.ingestMediaCancel = context.WithCancel(context.Background())
	s.ingestPC = pc
	s.ingestInstalledBindingToken = 0
	s.ingestInstalledOfferID = 0
	s.ingestInstalledGeneration = generation
	s.ingestInstalledTargetID = targetID
	if admission != nil {
		s.ingestInstalledBindingToken = admission.bindingToken
		s.ingestInstalledOfferID = admission.offerID
	}
	s.videoFeedID, s.audioFeedID = 0, 0
	// Both writers are already owned, so this cannot wait under Session.mu.
	s.videoForward.feed, s.audioForward.feed = 0, 0
	return old, nil
}

// A superseded offer keeps its identity error even when supersession also
// canceled its writer wait. This check uses only the short session lock.
func (s *Session) ingestInstallWaitError(admission *ingestAdmission, waitErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ingestAdmissionErrorLocked(admission); err != nil {
		return err
	}
	return waitErr
}
