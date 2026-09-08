package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

type captureIngestRelay interface {
	BeginIngestBinding(context.Context) (uint64, error)
	HandleIngestOfferForBinding(context.Context, uint64, uint64, string, uint64, string) (string, error)
}

// BindIngestContext publishes one authenticated socket and its independent relay
// reservation atomically. It leaves closing the previous socket to the caller.
func (cs *CaptureSession) BindIngestContext(ctx context.Context, send func(string, *string, int, int, int) error, closeConn func()) (func(), uint64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("capture session: ingest binding requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.stopped {
		return nil, 0, fmt.Errorf("capture session: already stopped")
	}
	relay, ok := cs.relay.(captureIngestRelay)
	if !ok {
		return nil, 0, fmt.Errorf("capture session: context-bound ingest is unsupported")
	}
	if cs.ingestEpoch == ^uint64(0) {
		return nil, 0, fmt.Errorf("capture session: ingest epoch exhausted")
	}
	binding, cancel := context.WithCancel(ctx)
	token, err := relay.BeginIngestBinding(binding)
	if err != nil {
		cancel()
		return nil, 0, err
	}
	if token == 0 {
		cancel()
		return nil, 0, fmt.Errorf("capture session: relay returned an empty binding token")
	}
	previous := cs.ingestClose
	cs.cancelIngestBindingLocked()
	cs.ingestEpoch++
	cs.ingestContextBound = true
	cs.ingestBindingToken = token
	cs.ingestBindingCtx, cs.ingestBindingCancel = binding, cancel
	cs.ingestSend, cs.ingestClose = send, closeConn
	cs.lastPingAt = time.Now()
	cs.captureHealth = CaptureHealthObservation{}
	return previous, cs.ingestEpoch, nil
}

// CurrentIngestBinding returns the control socket epoch and relay reservation.
// The installed media token may still describe the previous socket until its
// replacement successfully negotiates; the relay owns that separate identity.
func (cs *CaptureSession) CurrentIngestBinding() (epoch, relayToken uint64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.stopped || cs.ingestBindingCtx == nil || cs.ingestBindingCtx.Err() != nil {
		return 0, 0
	}
	return cs.ingestEpoch, cs.ingestBindingToken
}

// HandleIngestOfferForBinding keeps frame, socket and offer ownership through
// negotiation. Relay calls and cleanup callbacks run after releasing cs.mu.
func (cs *CaptureSession) HandleIngestOfferForBinding(ctx context.Context, epoch, offerID uint64, sdp string, generation uint64, targetID string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("capture session: ingest offer requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cs.mu.Lock()
	relay, ok := cs.relay.(captureIngestRelay)
	if !ok || offerID == 0 || offerID > 9007199254740991 || offerID <= cs.ingestOfferID || !cs.matchesIngestFrameLocked(epoch, generation, targetID) {
		cs.mu.Unlock()
		return "", webrtc.ErrStaleIngestOffer
	}
	if cs.frameCtx == nil {
		cs.frameCtx, cs.frameCancel = context.WithCancel(context.Background())
	}
	request, cancel := context.WithCancel(ctx)
	stopBinding := context.AfterFunc(cs.ingestBindingCtx, cancel)
	stopFrame := context.AfterFunc(cs.frameCtx, cancel)
	if cs.ingestOfferCancel != nil {
		cs.ingestOfferCancel()
	}
	cs.ingestOfferID, cs.ingestOfferCancel = offerID, cancel
	token := cs.ingestBindingToken
	cs.mu.Unlock()
	defer func() {
		stopBinding()
		stopFrame()
		cancel()
		cs.mu.Lock()
		if cs.ingestEpoch == epoch && cs.ingestOfferID == offerID {
			cs.ingestOfferCancel = nil
		}
		cs.mu.Unlock()
	}()
	answer, err := relay.HandleIngestOfferForBinding(request, token, offerID, sdp, generation, targetID)
	cs.mu.Lock()
	current := cs.matchesIngestFrameLocked(epoch, generation, targetID) && cs.ingestBindingToken == token && cs.ingestOfferID == offerID
	cs.mu.Unlock()
	if !current {
		return "", webrtc.ErrStaleIngestOffer
	}
	if request.Err() != nil {
		return "", request.Err()
	}
	if err != nil {
		return "", err
	}
	return answer, nil
}

func (cs *CaptureSession) matchesIngestFrameLocked(epoch, generation uint64, targetID string) bool {
	frame := cs.frameStateLocked()
	return !cs.stopped && epoch != 0 && epoch == cs.ingestEpoch && cs.ingestBindingToken != 0 && cs.ingestBindingCtx != nil && cs.ingestBindingCtx.Err() == nil && generation != 0 && generation == frame.Generation && targetID != "" && targetID == frame.TargetID
}

// These hooks only cancel standard-library contexts while holding cs.mu. They
// perform no transport calls. Direct cancellation closes the final admission
// window instead of relying solely on an asynchronously scheduled AfterFunc.
func (cs *CaptureSession) cancelIngestBindingLocked() {
	if cs.ingestOfferCancel != nil {
		cs.ingestOfferCancel()
	}
	if cs.ingestBindingCancel != nil {
		cs.ingestBindingCancel()
	}
	cs.ingestOfferCancel, cs.ingestBindingCancel = nil, nil
	cs.ingestBindingCtx = nil
	cs.ingestBindingToken, cs.ingestOfferID = 0, 0
}

func (cs *CaptureSession) replaceFrameLifetimeLocked() {
	if cs.ingestOfferCancel != nil {
		cs.ingestOfferCancel()
	}
	if cs.frameCancel != nil {
		cs.frameCancel()
	}
	cs.frameCtx, cs.frameCancel = context.WithCancel(context.Background())
}
