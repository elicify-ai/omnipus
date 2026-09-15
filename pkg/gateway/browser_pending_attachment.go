package gateway

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

type browserAttachmentRequest struct {
	epoch uint64
	ctx   context.Context
	ready <-chan struct{}
}

func (s *browserConnState) attachmentRequest() browserAttachmentRequest {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attachmentCtx == nil && s.mgr == nil {
		return browserAttachmentRequest{}
	}
	return browserAttachmentRequest{epoch: s.attachEpoch, ctx: s.commandContextLocked(), ready: s.attachmentReady}
}

func (s *browserConnState) awaitAttachment(ctx context.Context, request browserAttachmentRequest) (browserAttachmentSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return browserAttachmentSnapshot{}, err
	}
	if request.ctx == nil {
		return browserAttachmentSnapshot{}, fmt.Errorf("browser attachment has not been requested")
	}
	if request.ready != nil {
		select {
		case <-ctx.Done():
			return browserAttachmentSnapshot{}, ctx.Err()
		case <-request.ctx.Done():
			return browserAttachmentSnapshot{}, request.ctx.Err()
		case <-request.ready:
		}
	}
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if err := ctx.Err(); err != nil {
		return browserAttachmentSnapshot{}, err
	}
	if request.ctx.Err() != nil || s.attachmentCtx != request.ctx || s.attachEpoch != request.epoch || s.attachmentPending {
		return browserAttachmentSnapshot{}, context.Canceled
	}
	if s.mgr == nil || s.sessionID == "" || s.panelSessionID == "" {
		return browserAttachmentSnapshot{}, fmt.Errorf("browser attachment has no committed route")
	}
	return browserAttachmentSnapshot{mgr: s.mgr, sessionID: s.sessionID, panelSessionID: s.panelSessionID, ctx: request.ctx}, nil
}

func (s *browserConnState) abandonAttachment(epoch uint64) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attachEpoch == epoch && s.attachmentPending {
		s.cancelAttachmentLocked()
	}
}

func (s *browserConnState) takePreviousAttachment(epoch uint64) (*browser.BrowserManager, string, string, bool) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attachEpoch != epoch {
		return nil, "", "", false
	}
	// Direct handler callers may not have dispatched an attachment request.
	// An existing request, including a canceled one, must never be replaced here.
	if s.attachmentReady == nil {
		s.cancelAttachmentLocked()
		s.attachmentCtx, s.attachmentCancel = context.WithCancel(context.Background())
		s.attachmentReady = make(chan struct{})
		s.attachmentPending = true
	}
	if !s.attachmentPending || s.attachmentCtx.Err() != nil {
		return nil, "", "", false
	}
	mgr, sessionID, panelSessionID := s.mgr, s.sessionID, s.panelSessionID
	s.mgr, s.sessionID, s.panelSessionID = nil, "", ""
	return mgr, sessionID, panelSessionID, true
}
