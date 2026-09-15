package gateway

import "context"

// browserWebRTCOfferRequest is an immutable server-dispatched operation; the
// attachment lifetime remains separate from its temporary negotiation context.
type browserWebRTCOfferRequest struct {
	epoch      uint64
	attachment browserAttachmentRequest
	ctx        context.Context
	cancel     context.CancelFunc
}

func (s *browserConnState) webRTCOfferRequest(epoch uint64) (*browserWebRTCOfferRequest, bool) {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	r := s.webrtcRequest
	if epoch == 0 || r == nil || r.epoch != epoch || r.ctx.Err() != nil {
		return nil, false
	}
	return r, true
}

func (s *browserConnState) finishWebRTCOffer(epoch uint64) {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	if r := s.webrtcRequest; r != nil && r.epoch == epoch {
		r.cancel()
		s.webrtcRequest = nil
	}
}

// This check also remains useful after temporary negotiation has finished:
// queued answers and input errors retain the original attachment and epoch.
func (s *browserConnState) webRTCRequestOriginCurrent(r *browserWebRTCOfferRequest) bool {
	if r == nil || r.attachment.ctx == nil {
		return false
	}
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	return s.attachmentCtx == r.attachment.ctx && r.attachment.ctx.Err() == nil && s.webrtcEpoch == r.epoch
}

func (s *browserConnState) commitWebRTCAttachmentForRequest(r *browserWebRTCOfferRequest, att *webrtcAttachment) bool {
	if r == nil || att == nil || r.attachment.ctx == nil {
		return false
	}
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	if s.attachmentPending || s.attachmentCtx != r.attachment.ctx || r.attachment.ctx.Err() != nil ||
		s.webrtcEpoch != r.epoch || s.webrtcRequest != r || r.ctx.Err() != nil {
		return false
	}
	s.webrtc = att
	return true
}

func (s *browserConnState) webRTCAttachmentCurrent(r *browserWebRTCOfferRequest, att *webrtcAttachment) bool {
	if r == nil || att == nil || r.attachment.ctx == nil {
		return false
	}
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	return !s.attachmentPending && s.attachmentCtx == r.attachment.ctx && r.attachment.ctx.Err() == nil &&
		s.webrtcEpoch == r.epoch && s.webrtc == att
}
