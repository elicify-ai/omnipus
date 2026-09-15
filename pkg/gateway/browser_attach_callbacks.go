package gateway

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// Callbacks retain the original attachment lifetime even after AttachContext
// returns. Tab snapshots coalesce without waiting for a slow transport.
func browserAttachCallbacks(wc *browserWSConn, attachment context.Context, sessionID, viewerID string) (browser.StatusSink, browser.ControlSink, browser.TabsSink) {
	status := func(message string) {
		wc.sendCriticalScopedGen(sessionErrorStatus(sessionID, message), dropContext(sessionID, viewerID, "status-death"), attachment, nil)
	}
	control := func(other bool) {
		wc.sendCriticalScopedGen(generated.BrowserStatusFrame{
			Type:              string(generated.WsFrameTypeBrowserStatus),
			State:             "idle",
			SessionId:         &sessionID,
			ControlledByOther: &other,
			ControlOnly:       boolPtr(true),
		}, dropContext(sessionID, viewerID, "control-broadcast"), attachment, nil)
	}
	tabs := func(tabs []browser.Tab, active int) {
		wc.sendLatestGen(browserLatestTabs, generated.BrowserTabsFrame{
			Type:        string(generated.WsFrameTypeBrowserTabs),
			SessionId:   &sessionID,
			ActiveIndex: active,
			Tabs:        tabsToBrowserTabsWire(tabs),
		}, attachment)
	}
	return status, control, tabs
}

func (h *BrowserWSHandler) announceWebRTCAvailabilityContext(ctx context.Context, wc *browserWSConn, mgr *browser.BrowserManager, sessionID, viewerID string, cfg *config.Config) {
	reason := webrtcUnavailableReason(cfg, mgr)
	frame := generated.BrowserWebRTCStateFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcState),
		Available: reason == "",
		SessionId: &sessionID,
	}
	if reason != "" {
		frame.Reason = &reason
	} else {
		frame.IceServers = h.iceServersForViewer(cfg, viewerID)
	}
	wc.sendCriticalScopedGen(frame, dropContext(sessionID, viewerID, "webrtc-state:"+reason), ctx, nil)
}
