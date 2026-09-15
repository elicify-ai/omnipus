package gateway

// finishAttachmentFailure completes pending work without retiring its result's
// delivery scope. Waiting offers wake and find no route; the same request can
// never commit afterward. Replacement, detach, or socket teardown retires the
// original scope and therefore also any queued failure message.
func (s *browserConnState) finishAttachmentFailure(request browserAttachmentRequest) bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if request.ctx == nil || request.ctx.Err() != nil || s.attachEpoch != request.epoch || s.attachmentCtx != request.ctx || !s.attachmentPending {
		return false
	}
	s.attachmentPending = false
	close(s.attachmentReady)
	return true
}
