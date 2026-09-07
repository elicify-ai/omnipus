package webrtc

import (
	"context"

	"github.com/pion/webrtc/v4"
)

// ContextInputSink receives input with the originating attachment and peer lifetime.
// Every event on one channel receives the same context object. It is canceled
// when that attachment, peer, or channel ends, independently of each operation.
type ContextInputSink func(context.Context, string, []byte)

// NewSessionWithContextInput builds a relay whose input retains its source lifetime.
func NewSessionWithContextInput(cfg Config, sink ContextInputSink, logf func(string, ...any)) *Session {
	s := NewSession(cfg, nil, logf)
	s.contextSink = sink
	return s
}

func (vc *viewerConn) cancelInput() {
	if vc != nil && vc.inputCancel != nil {
		vc.inputCancel()
	}
}

// bindViewerInputChannel rejects late callbacks from replaced peers. The
// captured context belongs to this peer, never a later use of its viewer ID.
func (s *Session) bindViewerInputChannel(prefix, viewerID string, pc *webrtc.PeerConnection, dc *webrtc.DataChannel) bool {
	s.viewersMu.Lock()
	vc := s.viewers[viewerID]
	if vc == nil || vc.pc != pc || vc.inputCtx == nil || vc.inputCtx.Err() != nil || (vc.dc != nil && vc.dc != dc) {
		s.viewersMu.Unlock()
		if err := dc.Close(); err != nil {
			s.logf("%s rejected input channel close failed: %v", prefix, err)
		}
		return false
	}
	vc.dc = dc
	ctx := vc.inputCtx
	s.viewersMu.Unlock()
	s.wireInputDataChannel(ctx, vc.cancelInput, prefix, viewerID, dc)
	return true
}

// runInputQueueContext wakes on cancellation and checks before every sink
// call, including events already removed from the queue into a local batch.
func (s *Session) runInputQueueContext(ctx context.Context, viewerID string, queue *inputQueue) {
	stop := context.AfterFunc(ctx, queue.close)
	defer stop()
	for ctx.Err() == nil {
		batch, ok := queue.popBatch()
		if !ok {
			return
		}
		for _, raw := range coalesceInputBatch(batch) {
			if ctx.Err() != nil {
				return
			}
			if s.contextSink != nil {
				s.contextSink(ctx, viewerID, raw)
			} else if s.sink != nil {
				s.sink(viewerID, raw)
			}
		}
	}
}
