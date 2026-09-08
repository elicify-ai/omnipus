package webrtc

import (
	"context"
	"errors"
	"fmt"
)

var errStaleViewerRequest = errors.New("webrtc: stale viewer request")

type viewerRequestAdmission struct {
	parent     context.Context
	ctx        context.Context
	cancel     context.CancelCauseFunc
	stopParent func() bool
	epoch      uint64
}

// HandleViewerOfferHandleRequest orders server-dispatched requests within one
// original attachment. Finishing or superseding negotiation does not cancel an
// established peer's input lifetime, which remains derived from parent.
func (s *Session) HandleViewerOfferHandleRequest(negotiation, parent context.Context, requestEpoch uint64, viewerID, sdpOffer string) (string, any, error) {
	if negotiation == nil || parent == nil || parent.Done() == nil {
		return "", nil, fmt.Errorf("webrtc: viewer request requires negotiation and bounded attachment contexts")
	}
	if err := context.Cause(negotiation); err != nil {
		return "", nil, err
	}
	if err := context.Cause(parent); err != nil {
		return "", nil, err
	}
	if requestEpoch == 0 || viewerID == "" || sdpOffer == "" {
		return "", nil, fmt.Errorf("webrtc: viewer request requires epoch, viewer ID and SDP")
	}
	r, err := s.reserveViewerRequest(negotiation, parent, requestEpoch, viewerID)
	if err != nil {
		return "", nil, err
	}
	defer r.cancel(context.Canceled)
	return s.handleViewerOfferHandle(r.ctx, parent, r, viewerID, sdpOffer)
}

func (s *Session) reserveViewerRequest(negotiation, parent context.Context, epoch uint64, viewerID string) (*viewerRequestAdmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewersMu.Lock()
	defer s.viewersMu.Unlock()
	if err := context.Cause(negotiation); err != nil {
		return nil, err
	}
	if err := context.Cause(parent); err != nil {
		return nil, err
	}
	if s.closed {
		return nil, fmt.Errorf("webrtc: session closed")
	}
	old := s.viewerRequests[viewerID]
	if old != nil {
		same := old.parent.Done() == parent.Done()
		if (same && epoch <= old.epoch) || (!same && old.parent.Err() == nil) {
			return nil, errStaleViewerRequest
		}
	}
	ctx, cancel := context.WithCancelCause(negotiation)
	r := &viewerRequestAdmission{parent: parent, ctx: ctx, cancel: cancel, epoch: epoch}
	if s.viewerRequests == nil {
		s.viewerRequests = make(map[string]*viewerRequestAdmission)
	}
	if old != nil {
		old.cancel(errStaleViewerRequest)
		if old.stopParent != nil {
			old.stopParent()
		}
	}
	s.viewerRequests[viewerID] = r
	r.stopParent = context.AfterFunc(parent, func() {
		r.cancel(context.Cause(parent))
		s.viewersMu.Lock()
		defer s.viewersMu.Unlock()
		if s.viewerRequests[viewerID] == r {
			delete(s.viewerRequests, viewerID)
		}
	})
	return r, nil
}

// The caller holds viewersMu; installation also holds Session.mu so Close
// cannot publish a closed session between admission and peer registration.
func (s *Session) viewerRequestCurrentLocked(viewerID string, r *viewerRequestAdmission) bool {
	return r == nil || (s.viewerRequests[viewerID] == r && r.ctx.Err() == nil && r.parent.Err() == nil)
}

// IsViewerCurrent is a brief exact-identity read, with no IO or callbacks. It
// deliberately does not require ICE Connected: the answer must be published
// before the client can finish connecting this newly registered peer.
func (s *Session) IsViewerCurrent(handle any) bool {
	h, ok := handle.(*ViewerHandle)
	if !ok || h == nil {
		return false
	}
	s.viewersMu.Lock()
	defer s.viewersMu.Unlock()
	vc := s.viewers[h.viewerID]
	return vc != nil && vc.handle == h && vc.pc == h.pc && vc.inputCtx != nil && vc.inputCtx.Err() == nil
}
