package gateway

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// publishCurrentVideoHealth replays the capture's current immutable claim to
// the original registered attachment. True means enqueue admission only.
func (h *BrowserWSHandler) publishCurrentVideoHealth(viewerID string, cs *browser.CaptureSession, attachmentCtx context.Context) bool {
	if cs == nil {
		return false
	}
	event, ok := cs.CurrentVideoHealthEvent()
	if !ok {
		return false
	}
	value, ok := h.viewerConns.Load(viewerID)
	if !ok {
		return false
	}
	vc, ok := value.(*webrtcViewerConn)
	return ok && h.publishVideoHealth(viewerID, vc, cs, attachmentCtx, event)
}

// currentVideoViewer preserves the registry entry and original attachment;
// replacing either retires any publication already waiting for the writer.
func (h *BrowserWSHandler) currentVideoViewer(viewerID string, vc *webrtcViewerConn, cs *browser.CaptureSession, captureID string, origin context.Context) bool {
	if vc == nil || vc.wc == nil || cs == nil || captureID == "" || origin == nil || origin.Err() != nil || vc.capture != cs || vc.captureID != captureID || vc.attachmentCtx != origin {
		return false
	}
	value, ok := h.viewerConns.Load(viewerID)
	return ok && value == vc
}

func (h *BrowserWSHandler) publishVideoHealth(viewerID string, vc *webrtcViewerConn, cs *browser.CaptureSession, origin context.Context, event browser.VideoHealthEvent) bool {
	if event.Frame.Generation == 0 || event.Frame.TargetID == "" {
		return false
	}
	current := func() bool {
		return h.currentVideoViewer(viewerID, vc, cs, event.Frame.CaptureID, origin) && cs.IsCurrentVideoHealthEvent(event)
	}
	if !current() {
		return false
	}
	frame := videoHealthFrame(event)
	frame.SessionId = &vc.sessionID
	return vc.wc.sendLatestScopedGen(browserLatestVideoState, frame, origin, current)
}

// videoHealthFrame serializes only the claimed frame, never a later capture
// snapshot. Pending geometry omits dimensions and the presentation marker.
func videoHealthFrame(event browser.VideoHealthEvent) generated.BrowserVideoHealthFrame {
	frame := generated.BrowserVideoHealthFrame{
		Type:  string(generated.WsFrameTypeBrowserVideoHealth),
		State: string(event.State),
	}
	if event.Frame.CaptureID != "" {
		frame.CaptureId = &event.Frame.CaptureID
	}
	if event.Frame.Generation > 0 {
		generation := int(event.Frame.Generation)
		frame.CaptureGeneration = &generation
	}
	if event.Frame.TargetID != "" {
		frame.TargetId = &event.Frame.TargetID
	}
	if event.Frame.Width > 0 && event.Frame.Height > 0 {
		frame.CssWidth, frame.CssHeight = &event.Frame.Width, &event.Frame.Height
		if event.Frame.Ready {
			timestamp := int(event.Frame.Timestamp)
			frame.RtpTimestamp = &timestamp
		}
	}
	if event.Attempt > 0 {
		frame.Attempt = &event.Attempt
	}
	if event.MaxAttempts > 0 {
		frame.MaxAttempts = &event.MaxAttempts
	}
	if detail := webrtcReasonDetail(errors.New(event.Detail)); event.Detail != "" && detail != "" {
		frame.Detail = &detail
	}
	return frame
}
