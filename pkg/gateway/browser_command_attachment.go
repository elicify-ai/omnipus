package gateway

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browserAttachmentSnapshot is immutable execution identity, never wire data.
type browserAttachmentSnapshot struct {
	mgr            *browser.BrowserManager
	sessionID      string
	panelSessionID string
	ctx            context.Context
}

func (s *browserConnState) commandContextLocked() context.Context {
	if s.attachmentCtx == nil {
		s.attachmentCtx, s.attachmentCancel = context.WithCancel(context.Background())
	}
	return s.attachmentCtx
}
func (s *browserConnState) cancelAttachmentLocked() {
	s.commandContextLocked()
	s.attachmentCancel()
}
func (s *browserConnState) commandAttachment() browserAttachmentSnapshot {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return browserAttachmentSnapshot{mgr: s.mgr, sessionID: s.sessionID, panelSessionID: s.panelSessionID, ctx: s.commandContextLocked()}
}

// bindContext combines queue/peer cancellation with this exact attachment. A
// replacement attachment never inherits an earlier command's work or deadline.
func (a browserAttachmentSnapshot) bindContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(a.ctx, cancel)
	if a.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

// withCommandAttachment is only for brief in-memory control mutations. The
// callback must not perform I/O; tab/CDP operations instead use bindContext.
func (s *browserConnState) withCommandAttachment(ctx context.Context, a browserAttachmentSnapshot, fn func()) bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if ctx.Err() != nil || a.ctx.Err() != nil || s.attachmentCtx != a.ctx {
		return false
	}
	fn()
	return true
}

func commandWasSuperseded(ctx context.Context, a browserAttachmentSnapshot) bool {
	return a.ctx.Err() != nil || ctx.Err() == context.Canceled
}

func (s *browserConnState) shouldSendInputFailure(a browserAttachmentSnapshot, kind, message string, now time.Time) bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attachmentCtx != a.ctx || a.ctx.Err() != nil {
		return false
	}
	if !inputKindIsDiscrete(kind) && message == s.lastInputErrorMessage && now.Sub(s.lastInputErrorSentAt) < minInputErrorInterval {
		return false
	}
	s.lastInputErrorSentAt = now
	s.lastInputErrorMessage = message
	return true
}

func operationErrorStatus(sessionID, message string) generated.BrowserStatusFrame {
	status := sessionErrorStatus(sessionID, message)
	status.OperationOnly = boolPtr(true)
	return status
}
