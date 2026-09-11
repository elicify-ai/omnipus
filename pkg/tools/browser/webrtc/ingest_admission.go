package webrtc

import (
	"context"
	"errors"
	"fmt"

	"github.com/pion/webrtc/v4"
)

// ErrStaleIngestOffer indicates a superseded socket or negotiation.
var ErrStaleIngestOffer = errors.New("webrtc: ingest offer superseded")

type ingestAdmission struct {
	ctx          context.Context
	negotiation  context.Context
	bindingToken uint64
	offerID      uint64
}

// BeginIngestBinding reserves a new authenticated socket lifetime without
// disturbing the installed media while its replacement negotiates.
func (s *Session) BeginIngestBinding(parent context.Context) (uint64, error) {
	if parent == nil {
		return 0, errors.New("webrtc: ingest binding: nil context")
	}
	if err := parent.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, errors.New("webrtc: session closed")
	}
	if err := parent.Err(); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if s.ingestBindingToken == ^uint64(0) {
		s.mu.Unlock()
		return 0, errors.New("webrtc: ingest binding token exhausted")
	}
	oldBinding, oldOffer := s.ingestBindingCancel, s.ingestOfferCancel
	s.ingestBindingToken++
	token := s.ingestBindingToken
	s.ingestBindingCtx, s.ingestBindingCancel = context.WithCancel(parent)
	s.ingestOfferID, s.ingestOfferCancel = 0, nil
	s.mu.Unlock()
	// Cancellation may invoke transport callbacks; never invoke it under mu.
	if oldOffer != nil {
		oldOffer()
	}
	if oldBinding != nil {
		oldBinding()
	}
	return token, nil
}

// HandleIngestOfferForBinding negotiates under both the original socket
// lifetime and the independent request deadline. Only its reservation can install.
func (s *Session) HandleIngestOfferForBinding(ctx context.Context, bindingToken, offerID uint64, sdp string, generation uint64, targetID string) (string, error) {
	if ctx == nil {
		return "", errors.New("webrtc: ingest offer: nil negotiation context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if offerID == 0 || offerID > 9007199254740991 {
		return "", errors.New("webrtc: ingest offer_id must be a positive safe integer")
	}
	if err := validateCaptureIdentity(generation, targetID); err != nil {
		return "", err
	}
	if sdp == "" {
		return "", errors.New("webrtc: ingest offer: empty SDP")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", errors.New("webrtc: session closed")
	}
	if bindingToken == 0 || bindingToken != s.ingestBindingToken || s.ingestBindingCtx == nil || offerID <= s.ingestOfferID {
		s.mu.Unlock()
		return "", ErrStaleIngestOffer
	}
	if err := s.ingestBindingCtx.Err(); err != nil {
		s.mu.Unlock()
		return "", err
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return "", err
	}
	offerCtx, cancel := context.WithCancelCause(s.ingestBindingCtx)
	admission := &ingestAdmission{ctx: offerCtx, negotiation: ctx, bindingToken: bindingToken, offerID: offerID}
	oldCancel := s.ingestOfferCancel
	s.ingestOfferID = offerID
	s.ingestOfferCancel = func() { cancel(ErrStaleIngestOffer) }
	s.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	stop := context.AfterFunc(ctx, func() { cancel(ctx.Err()) })
	if err := ctx.Err(); err != nil {
		cancel(err)
	}
	defer func() {
		stop()
		cancel(context.Canceled)
		s.mu.Lock()
		if s.ingestBindingToken == bindingToken && s.ingestOfferID == offerID {
			s.ingestOfferCancel = nil
		}
		s.mu.Unlock()
	}()
	return s.handleIngestOffer(sdp, generation, targetID, admission)
}

// ingestAdmissionErrorLocked shares the installation lock. Direct parent checks
// avoid relying on asynchronous cancellation callbacks to enforce a deadline.
func (s *Session) ingestAdmissionErrorLocked(admission *ingestAdmission) error {
	if admission == nil {
		if s.ingestBindingToken != 0 {
			return ErrStaleIngestOffer
		}
		return nil
	}
	if admission.bindingToken == 0 || admission.bindingToken != s.ingestBindingToken || admission.offerID != s.ingestOfferID || s.ingestBindingCtx == nil {
		return ErrStaleIngestOffer
	}
	if err := admission.negotiation.Err(); err != nil {
		return err
	}
	if err := s.ingestBindingCtx.Err(); err != nil {
		return err
	}
	return context.Cause(admission.ctx)
}

func validateCaptureIdentity(generation uint64, targetID string) error {
	if generation == 0 || generation > 9007199254740991 {
		return fmt.Errorf("webrtc: capture generation must be a positive safe integer")
	}
	if len(targetID) == 0 || len(targetID) > 128 {
		return fmt.Errorf("webrtc: capture target must contain 1 to 128 bytes")
	}
	return nil
}

// prepareIngestAnswer owns only its candidate; cancellation can close that
// candidate concurrently, and this worker can never publish it to the Session.
func prepareIngestAnswer(pc *webrtc.PeerConnection, sdpOffer, prefix string) error {
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdpOffer}); err != nil {
		return fmt.Errorf("webrtc: ingest %s: set remote description: %w", prefix, err)
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("webrtc: ingest %s: create answer: %w", prefix, err)
	}
	if err := pc.SetLocalDescription(ans); err != nil {
		return fmt.Errorf("webrtc: ingest %s: set local description: %w", prefix, err)
	}
	return nil
}
