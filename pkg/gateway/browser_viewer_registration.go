package gateway

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// registerWebRTCViewerConnForCapture publishes only the original committed
// attachment. Lock order is attachMu then webrtcMu; capture snapshots are read
// before either lock so capture observers never participate in that order.
func (h *BrowserWSHandler) registerWebRTCViewerConnForCapture(state *browserConnState, epoch uint64, attachmentCtx context.Context, viewerID string, wc *browserWSConn, sessionID string, cs *browser.CaptureSession) bool {
	if state == nil || attachmentCtx == nil || wc == nil || cs == nil || viewerID == "" || sessionID == "" {
		return false
	}
	captureID := cs.FrameState().CaptureID
	state.attachMu.Lock()
	defer state.attachMu.Unlock()
	if state.attachmentCtx != attachmentCtx || state.sessionID != sessionID || state.mgr == nil || state.attachmentPending {
		return false
	}
	state.webrtcMu.Lock()
	defer state.webrtcMu.Unlock()
	if state.webrtcEpoch != epoch || state.webrtc == nil || state.webrtc.capture != cs {
		return false
	}
	current := func() bool {
		select {
		case <-attachmentCtx.Done():
			return false
		case <-cs.Done():
			return false
		case <-wc.doneCh:
			return false
		default:
			return true
		}
	}
	if !current() {
		return false
	}
	entry := &webrtcViewerConn{wc: wc, sessionID: sessionID, attachmentCtx: attachmentCtx, capture: cs, captureID: captureID}
	h.viewerConns.Store(viewerID, entry)
	// Stop can race registry publication. Either its callback sees this entry,
	// or this check removes the entry that missed the stop notification.
	if !current() {
		h.viewerConns.CompareAndDelete(viewerID, entry)
		return false
	}
	return true
}
