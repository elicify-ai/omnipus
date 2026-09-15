package browser

import (
	"context"
	"fmt"
	"math"
)

type requestViewerOfferHandler interface {
	viewerOfferHandler
	HandleViewerOfferHandleRequest(negotiation, parent context.Context, epoch uint64, viewerID, sdp string) (string, any, error)
	IsViewerCurrent(handle any) bool
}

type captureViewerRequest struct {
	parent  context.Context
	epoch   uint64
	gen     uint64
	pending bool
	stop    func() bool
}

// HandleViewerOfferRequest separates temporary negotiation from the original
// attachment lifetime. Server epochs order attempts; wire offer IDs do not.
// A pending attempt never replaces an active viewer's accounting entry.
func (cs *CaptureSession) HandleViewerOfferRequest(negotiation, parent context.Context, requestEpoch uint64, viewerID, sdp string) (string, *ViewerAttachHandle, error) {
	if negotiation == nil || parent == nil || parent.Done() == nil {
		return "", nil, fmt.Errorf("capture session: viewer request requires negotiation and bounded attachment contexts")
	}
	if err := negotiation.Err(); err != nil {
		return "", nil, err
	}
	if err := parent.Err(); err != nil {
		return "", nil, err
	}
	if requestEpoch == 0 || viewerID == "" || sdp == "" {
		return "", nil, fmt.Errorf("capture session: viewer request requires epoch, viewer ID and SDP")
	}
	handler, ok := cs.relay.(requestViewerOfferHandler)
	if !ok {
		cs.mu.Lock()
		cs.armGraceStopLocked()
		cs.mu.Unlock()
		return "", nil, fmt.Errorf("capture session: relay does not support fenced viewer requests")
	}
	r, err := cs.reserveViewerRequest(negotiation, parent, requestEpoch, viewerID)
	if err != nil {
		return "", nil, err
	}
	answer, relayHandle, offerErr := handler.HandleViewerOfferHandleRequest(negotiation, parent, requestEpoch, viewerID, sdp)
	handle := &ViewerAttachHandle{viewerID: viewerID, gen: r.gen, relay: relayHandle}

	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.viewerRequests[viewerID] == r {
		r.pending = false
	}
	defer cs.armGraceStopLocked()
	if err := negotiation.Err(); err != nil {
		return "", handle, err
	}
	if err := parent.Err(); err != nil {
		return "", handle, err
	}
	if cs.stopped || cs.viewerRequests[viewerID] != r {
		return "", handle, fmt.Errorf("capture session: stale viewer request")
	}
	if offerErr != nil {
		return "", handle, offerErr
	}
	// The relay removal callback also takes cs.mu after releasing its own
	// viewer lock. It therefore either precedes this check, or observes the
	// published exact handle and removes it afterward.
	if !handler.IsViewerCurrent(relayHandle) {
		return "", handle, fmt.Errorf("capture session: viewer removed before publication")
	}
	cs.viewers[viewerID] = viewerRegistration{gen: r.gen, relayHandle: relayHandle}
	return answer, handle, nil
}

func (cs *CaptureSession) reserveViewerRequest(negotiation, parent context.Context, epoch uint64, viewerID string) (*captureViewerRequest, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := negotiation.Err(); err != nil {
		return nil, err
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if cs.stopped {
		return nil, fmt.Errorf("capture session: stopped")
	}
	old := cs.viewerRequests[viewerID]
	if old != nil {
		same := old.parent.Done() == parent.Done()
		if (same && epoch <= old.epoch) || (!same && old.parent.Err() == nil) {
			return nil, fmt.Errorf("capture session: stale viewer request")
		}
	}
	if cs.viewerGenSeq == math.MaxUint64 {
		return nil, fmt.Errorf("capture session: viewer generations exhausted")
	}
	cs.viewerGenSeq++
	r := &captureViewerRequest{parent: parent, epoch: epoch, gen: cs.viewerGenSeq, pending: true}
	if cs.viewerRequests == nil {
		cs.viewerRequests = make(map[string]*captureViewerRequest)
	}
	if old != nil && old.stop != nil {
		old.stop()
	}
	cs.viewerRequests[viewerID] = r
	r.stop = context.AfterFunc(parent, func() {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.viewerRequests[viewerID] != r {
			return
		}
		delete(cs.viewerRequests, viewerID)
		cs.armGraceStopLocked()
	})
	if cs.stopTimer != nil {
		cs.stopTimer.Stop()
		cs.stopTimer = nil
	}
	return r, nil
}

func (cs *CaptureSession) hasPendingViewerRequestsLocked() bool {
	for _, r := range cs.viewerRequests {
		if r.pending {
			return true
		}
	}
	return false
}

// stopViewerRequestsLocked releases attachment listeners when the capture stops.
// The relay owns cancellation of its pending transport negotiations.
func (cs *CaptureSession) stopViewerRequestsLocked() {
	for _, r := range cs.viewerRequests {
		if r.stop != nil {
			r.stop()
		}
	}
	clear(cs.viewerRequests)
}
